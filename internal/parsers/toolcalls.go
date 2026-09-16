// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package parsers

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// toolCallAcc es el tool call en construcción. Port de
// AwsEventStreamParser.current_tool_call (.upstream/kiro/parsers.py:255),
// que en el original es un único Optional[Dict] — nunca un mapa de varios
// tool calls concurrentes por id. _process_tool_start_event finaliza
// explícitamente el tool call anterior antes de empezar uno nuevo
// (.upstream/kiro/parsers.py:353-354), así que solo hace falta un puntero,
// no un map[string]*toolCallAcc.
type toolCallAcc struct {
	id        any // data.get('toolUseId', <id generado>) — casi siempre string, pero se preserva el tipo tal cual.
	name      any // data.get('name', '')
	arguments strings.Builder
}

// processToolStart procesa un evento cuyo prefijo más temprano fue
// `{"name":`. Port de _process_tool_start_event
// (.upstream/kiro/parsers.py:350-380). Nunca devuelve un Event — el
// original siempre `return None` aquí; el tool call se acumula en estado
// interno y solo se expone vía GetToolCalls/Finish.
func (p *Parser) processToolStart(value map[string]any, fieldsRaw map[string]json.RawMessage) {
	if p.currentToolCall != nil {
		p.finalizeToolCall()
	}

	inputStr := toolInputArgString(fieldsRaw)

	// data.get('toolUseId', generate_tool_call_id()) evalúa el default de
	// forma ansiosa: Python llama a generate_tool_call_id() SIEMPRE, exista
	// o no la clave, y solo descarta el resultado si toolUseId está
	// presente. Se replica el efecto secundario (consumir un id del
	// generador) aunque el corpus de AwsEventStreamParser nunca lo
	// necesite: los 22 fixtures siempre traen toolUseId explícito.
	generated := utils.GenerateToolCallID()
	id, hasID := value["toolUseId"]
	if !hasID {
		id = generated
	}

	name, hasName := value["name"]
	if !hasName {
		name = ""
	}

	acc := &toolCallAcc{id: id, name: name}
	acc.arguments.WriteString(inputStr)
	p.currentToolCall = acc

	if isTruthy(value["stop"]) {
		p.finalizeToolCall()
	}
}

// processToolInput procesa un evento cuyo prefijo más temprano fue
// `{"input":`. Port de _process_tool_input_event
// (.upstream/kiro/parsers.py:382-395).
func (p *Parser) processToolInput(value map[string]any, fieldsRaw map[string]json.RawMessage) {
	if p.currentToolCall == nil {
		return
	}
	p.currentToolCall.arguments.WriteString(toolInputArgString(fieldsRaw))
}

// processToolStop procesa un evento cuyo prefijo más temprano fue
// `{"stop":`. Port de _process_tool_stop_event
// (.upstream/kiro/parsers.py:397-401).
func (p *Parser) processToolStop(value map[string]any) {
	if p.currentToolCall != nil && isTruthy(value["stop"]) {
		p.finalizeToolCall()
	}
}

// toolInputArgString normaliza el campo "input" de un evento tool_start o
// tool_input a la cadena que se concatena en los argumentos acumulados.
// Port de la lógica duplicada en _process_tool_start_event
// (.upstream/kiro/parsers.py:356-366) y _process_tool_input_event
// (.upstream/kiro/parsers.py:386-393):
//
//	if isinstance(input_data, dict):
//	    input_str = json.dumps(input_data) if input_data else ''
//	else:
//	    input_str = str(input_data) if input_data else ''
//
// Usa los bytes crudos del campo "input" (no el mapa ya decodificado) para
// que pyjson.DumpsASCII/pyjson.Str reproduzcan el formato exacto de
// json.dumps/str de Python — un remarshal vía encoding/json ordenaría las
// claves alfabéticamente en vez de conservar el orden de aparición.
// json.dumps(input_data) en el original no pasa ensure_ascii (default
// True), así que es DumpsASCII, no Dumps, el que reproduce su escapado —
// Dumps es para los call sites que SÍ pasan ensure_ascii=False (el
// tokenizer). str(input_data), en cambio, nunca escapa nada: pyjson.Str es
// correcto tal cual.
func toolInputArgString(fields map[string]json.RawMessage) string {
	raw, ok := fields["input"]
	if !ok {
		return ""
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return ""
	}

	if m, isMap := v.(map[string]any); isMap {
		if len(m) == 0 {
			return ""
		}
		s, err := pyjson.DumpsASCII(raw)
		if err != nil {
			return ""
		}
		return s
	}

	if !isTruthy(v) {
		return ""
	}
	return pyjson.Str(raw)
}

// finalizeToolCall cierra el tool call en construcción: intenta parsear sus
// argumentos acumulados como JSON y reformatearlos con las reglas de
// json.dumps de Python (sin ensure_ascii=False — el original no lo pasa
// aquí tampoco, así que es DumpsASCII); si falla, diagnostica truncamiento y
// dispone de "{}" como argumentos. Port de _finalize_tool_call
// (.upstream/kiro/parsers.py:403-462).
//
// El original distingue isinstance(args, str) / dict / otro, pero arguments
// SIEMPRE es una cadena en este port: se construye por concatenación desde
// toolInputArgString, que siempre devuelve string. Las ramas dict/otro del
// original son código muerto en la práctica (nunca alcanzable dado cómo se
// construye current_tool_call) y no se traducen.
func (p *Parser) finalizeToolCall() {
	acc := p.currentToolCall
	if acc == nil {
		return
	}
	p.currentToolCall = nil

	argsStr := acc.arguments.String()
	finalArgs := "{}"
	var trunc *truncationInfo

	if strings.TrimSpace(argsStr) != "" {
		if reformatted, err := pyjson.DumpsASCII(json.RawMessage(argsStr)); err == nil {
			finalArgs = reformatted
		} else {
			info := diagnoseJSONTruncation(argsStr)
			if info.isTruncated {
				trunc = &info
			}
		}
	}

	tc := map[string]any{
		"id":   acc.id,
		"type": "function",
		"function": map[string]any{
			"name":      acc.name,
			"arguments": finalArgs,
		},
	}

	if trunc != nil {
		tc["_truncation_detected"] = true
		tc["_truncation_info"] = map[string]any{
			"is_truncated": trunc.isTruncated,
			"reason":       trunc.reason,
			"size_bytes":   trunc.sizeBytes,
		}
		p.lastTruncation = *trunc
		p.hasTruncation = true
	}

	p.toolCalls = append(p.toolCalls, tc)
}

// GetToolCalls finaliza el tool call pendiente (si lo hay) y devuelve todos
// los tool calls acumulados, deduplicados. Port literal de get_tool_calls
// (.upstream/kiro/parsers.py:550-562). A diferencia de Finish, mantiene la
// forma de dict cruda (id/type/function) en vez de envolverla en Event, y no
// muta p.toolCalls — igual que el original, que solo lee self.tool_calls sin
// reasignarlo, así que llamar dos veces vuelve a deduplicar la misma lista
// completa.
func (p *Parser) GetToolCalls() []map[string]any {
	if p.currentToolCall != nil {
		p.finalizeToolCall()
	}
	return DeduplicateToolCalls(p.toolCalls)
}

// Finish finaliza el tool call pendiente y devuelve los tool calls
// acumulados y deduplicados como eventos de Kind "tool_call". El original no
// tiene un método equivalente: sus llamadores invocan get_tool_calls()
// directamente al final del stream. "tool_call" es una adición de este
// port — Value lleva el dict id/type/function tal cual lo produce
// GetToolCalls; Raw queda nil porque un tool call agregado no corresponde a
// un único rango contiguo del buffer (puede construirse a partir de varios
// chunks/eventos).
func (p *Parser) Finish() []Event {
	toolCalls := p.GetToolCalls()
	events := make([]Event, 0, len(toolCalls))
	for _, tc := range toolCalls {
		events = append(events, Event{Kind: "tool_call", Value: tc})
	}
	return events
}

// bracketToolCallPattern reconoce el formato de texto
// "[Called nombre_func with args: {...}]" que algunos modelos usan en vez
// de tool calls estructurados. Port de la constante `pattern` dentro de
// parse_bracket_tool_calls (.upstream/kiro/parsers.py:115).
//
// El \w de Python (sin re.ASCII) matchea letras Unicode además de
// [0-9A-Za-z_]; el \w de RE2/Go es solo ASCII. Ningún caso del corpus
// parse_bracket_tool_calls usa un nombre de función no-ASCII — divergencia
// documentada, no ejercitada.
var bracketToolCallPattern = regexp.MustCompile(`(?i)\[Called\s+(\w+)\s+with\s+args:\s*`)

// ParseBracketToolCalls extrae tool calls en formato de texto
// "[Called func_name with args: {...}]". Port literal de
// parse_bracket_tool_calls (.upstream/kiro/parsers.py:92-148).
func ParseBracketToolCalls(responseText string) []map[string]any {
	toolCalls := []map[string]any{}

	if responseText == "" || !strings.Contains(responseText, "[Called") {
		return toolCalls
	}

	matches := bracketToolCallPattern.FindAllStringSubmatchIndex(responseText, -1)
	for _, m := range matches {
		funcName := responseText[m[2]:m[3]]
		argsStart := m[1] // match.end(): posición justo tras el prefijo completo, \s* final incluido.

		relJSONStart := strings.IndexByte(responseText[argsStart:], '{')
		if relJSONStart == -1 {
			continue
		}
		jsonStart := argsStart + relJSONStart

		jsonEnd := findMatchingBrace([]byte(responseText), jsonStart)
		if jsonEnd == -1 {
			continue
		}

		jsonStr := responseText[jsonStart : jsonEnd+1]
		// json.dumps(args) en el original (parsers.py:142) tampoco pasa
		// ensure_ascii=False, así que DumpsASCII, no Dumps.
		argsFormatted, err := pyjson.DumpsASCII(json.RawMessage(jsonStr))
		if err != nil {
			// json.JSONDecodeError en el original: logger.warning y se
			// descarta este match, pero se sigue con los siguientes.
			continue
		}

		toolCalls = append(toolCalls, map[string]any{
			"id":   utils.GenerateToolCallID(),
			"type": "function",
			"function": map[string]any{
				"name":      funcName,
				"arguments": argsFormatted,
			},
		})
	}

	return toolCalls
}
