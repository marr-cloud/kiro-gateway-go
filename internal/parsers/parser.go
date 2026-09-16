// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package parsers extrae eventos JSON del stream binario de AWS Event
// Stream que devuelve Kiro, deduplica contenido repetido, agrega los
// fragmentos de un tool call y diagnostica truncamientos. Port de
// kiro.parsers (.upstream/kiro/parsers.py, 568 líneas).
package parsers

import (
	"bytes"
	"encoding/json"
	"reflect"
)

// Event es un fragmento de stream ya parseado. Kind es "content", "usage" o
// "context_usage" para los eventos que produce Feed, o "tool_call" para los
// que produce Finish (ver su comentario). Raw es una copia de los bytes JSON
// originales — no un slice del buffer interno, para que mutaciones
// posteriores del buffer no lo corrompan (D5). Value es el objeto JSON
// completo ya decodificado (siempre un objeto, porque los 7 prefijos
// reconocidos empiezan todos por `{"`); para "content" el campo relevante es
// Value["content"], para "usage" es Value["usage"], para "context_usage" es
// Value["contextUsagePercentage"].
type Event struct {
	Kind  string
	Raw   []byte
	Value map[string]any
}

// eventPattern empareja un prefijo JSON literal con el tipo de evento que
// dispara. Port de AwsEventStreamParser.EVENT_PATTERNS
// (.upstream/kiro/parsers.py:241-249). Los 7 prefijos son byte-exactos al
// original; un error de transcripción aquí rompe todo lo que dependa de
// este paquete.
type eventPattern struct {
	prefix string
	kind   string
}

var eventPatterns = []eventPattern{
	{`{"content":`, "content"},
	{`{"name":`, "tool_start"},
	{`{"input":`, "tool_input"},
	{`{"stop":`, "tool_stop"},
	{`{"followupPrompt":`, "followup"},
	{`{"usage":`, "usage"},
	{`{"contextUsagePercentage":`, "context_usage"},
}

// Parser es el equivalente Go de AwsEventStreamParser
// (.upstream/kiro/parsers.py:211-569).
type Parser struct {
	buffer          bytes.Buffer
	lastContent     any          // Optional[str] en el original, pero en la práctica puede llevar cualquier tipo JSON (ver processContent).
	currentToolCall *toolCallAcc // Optional[Dict] en el original.
	toolCalls       []map[string]any

	// lastTruncation/hasTruncation no existen en el original como atributos
	// del parser: allí el diagnóstico de truncamiento se guarda dentro del
	// propio dict del tool call finalizado (_truncation_detected /
	// _truncation_info, ver finalizeToolCall). Se añaden aquí solo para
	// poder ofrecer TruncationDiagnosis() con la firma que pide la Task 1;
	// guardan el último diagnóstico truncado visto por cualquier tool call
	// finalizado. Adición Go-only, documentada en el informe.
	lastTruncation truncationInfo
	hasTruncation  bool
}

// NewParser crea un Parser vacío. Port de AwsEventStreamParser.__init__
// (.upstream/kiro/parsers.py:251-256).
func NewParser() *Parser {
	return &Parser{}
}

// Feed añade chunk al buffer interno y devuelve los eventos completos que
// pudieron extraerse. Port literal de AwsEventStreamParser.feed
// (.upstream/kiro/parsers.py:258-306).
//
// Detalle bug-a-bug crítico: el original decodifica cada chunk de forma
// INDEPENDIENTE con chunk.decode('utf-8', errors='ignore') antes de
// concatenarlo al buffer. Si un carácter multibyte queda partido entre dos
// chunks, las dos mitades de bytes inválidos se descartan por separado y el
// carácter se corrompe — no se reensamblan runas entre llamadas a Feed.
// bytes.ToValidUTF8(chunk, nil) replica esto exactamente: valida chunk en
// aislamiento, sin ver el estado ya acumulado en p.buffer.
func (p *Parser) Feed(chunk []byte) []Event {
	p.buffer.Write(bytes.ToValidUTF8(chunk, nil))

	var events []Event

	for {
		bufBytes := p.buffer.Bytes()

		earliestPos := -1
		earliestKind := ""
		for _, pat := range eventPatterns {
			idx := bytes.Index(bufBytes, []byte(pat.prefix))
			if idx != -1 && (earliestPos == -1 || idx < earliestPos) {
				earliestPos = idx
				earliestKind = pat.kind
			}
		}

		if earliestPos == -1 {
			break
		}

		jsonEnd := findMatchingBrace(bufBytes, earliestPos)
		if jsonEnd == -1 {
			// JSON incompleto: espera a que lleguen más datos. El buffer NO
			// se toca, igual que el original.
			break
		}

		rawCopy := append([]byte(nil), bufBytes[earliestPos:jsonEnd+1]...)
		// El original recorta self.buffer ANTES del try/except de
		// json.loads: el texto consumido (incluida la basura anterior al
		// prefijo) se descarta tanto si el parseo tiene éxito como si no.
		p.buffer.Next(jsonEnd + 1)

		var value map[string]any
		if err := json.Unmarshal(rawCopy, &value); err != nil {
			// json.JSONDecodeError en el original: logger.warning y sigue el
			// bucle sin producir evento.
			continue
		}

		switch earliestKind {
		case "content":
			if ev, ok := p.processContent(value, rawCopy); ok {
				events = append(events, ev)
			}
		case "tool_start":
			var fieldsRaw map[string]json.RawMessage
			_ = json.Unmarshal(rawCopy, &fieldsRaw)
			p.processToolStart(value, fieldsRaw)
		case "tool_input":
			var fieldsRaw map[string]json.RawMessage
			_ = json.Unmarshal(rawCopy, &fieldsRaw)
			p.processToolInput(value, fieldsRaw)
		case "tool_stop":
			p.processToolStop(value)
		case "usage":
			events = append(events, Event{Kind: "usage", Raw: rawCopy, Value: value})
		case "context_usage":
			events = append(events, Event{Kind: "context_usage", Raw: rawCopy, Value: value})
		case "followup":
			// _process_event no tiene rama 'followup': cuando
			// {"followupPrompt": es el prefijo MÁS TEMPRANO (no solo una
			// clave adicional dentro de un objeto {"content":...}), el
			// original cae al `return None` final sin mutar ningún estado.
			// Se consume el JSON del buffer y no pasa nada más — a
			// propósito, no es un caso omitido.
		}
	}

	return events
}

// processContent procesa un evento cuyo prefijo más temprano fue
// `{"content":`. Port de _process_content_event
// (.upstream/kiro/parsers.py:334-348).
func (p *Parser) processContent(value map[string]any, raw []byte) (Event, bool) {
	content := value["content"]

	if isTruthy(value["followupPrompt"]) {
		return Event{}, false
	}

	if reflect.DeepEqual(content, p.lastContent) {
		return Event{}, false
	}

	p.lastContent = content
	return Event{Kind: "content", Raw: raw, Value: value}, true
}

// Reset limpia el estado del parser para reutilizar la misma instancia en un
// stream nuevo. Port literal de reset (.upstream/kiro/parsers.py:564-569).
func (p *Parser) Reset() {
	p.buffer.Reset()
	p.lastContent = nil
	p.currentToolCall = nil
	p.toolCalls = nil
	p.lastTruncation = truncationInfo{}
	p.hasTruncation = false
}

// TruncationDiagnosis devuelve el último diagnóstico de truncamiento visto
// al finalizar un tool call, si lo hay. kind es el "reason" del diagnóstico
// (p.ej. "missing 2 closing brace(s)", "unclosed string literal"); ok es
// false si ningún tool call finalizado hasta ahora se diagnosticó como
// truncado. Sin equivalente directo en el original, que guarda el
// diagnóstico dentro del propio dict del tool call
// (_truncation_detected/_truncation_info, ver finalizeToolCall) en vez de
// como un atributo único del parser — ver el comentario de
// lastTruncation/hasTruncation en la definición de Parser.
func (p *Parser) TruncationDiagnosis() (kind string, ok bool) {
	if !p.hasTruncation {
		return "", false
	}
	return p.lastTruncation.reason, true
}
