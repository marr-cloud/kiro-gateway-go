// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"encoding/json"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
	"github.com/marr-cloud/kiro-gateway-go/internal/sse"
	"github.com/marr-cloud/kiro-gateway-go/internal/tokenizer"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// outputTokenCount cuenta los tokens del summary SIN la corrección Claude
// (mcp_tools.py:320,474: count_tokens(summary, apply_claude_correction=False)
// — es una respuesta de la API MCP, no generación del modelo, así que no
// aplica el factor 1.15 que corrige el undercount de tiktoken sobre texto
// de Claude).
func outputTokenCount(summary string) int {
	return tokenizer.CountTokens(summary, false)
}

// nowUnix es time.Now().Unix() aislado en una función para que quede un
// único sitio que tocar si algún día se necesita inyectar un reloj de test
// (hoy no hace falta: el timestamp de "created" no se compara en ningún
// test, solo se necesita que exista, mismo criterio que NewWebSearchRequestID).
func nowUnix() int64 {
	return time.Now().Unix()
}

// Por qué internal/pyjson aquí (fix round 1, parity #1).
//
// format_sse_event(event_type, data) del original (.upstream/kiro/streaming_anthropic.py:70-85)
// serializa SIEMPRE con `json.dumps(data, ensure_ascii=False)`: separadores
// `", "`/`": "` (con espacio) y SIN escapar `&`/`<`/`>` a entidades HTML.
// encoding/json.Marshal de Go diverge en ambos frentes — sin espacio tras
// los dos puntos, y con `&`/`<`/`>` escapados a sus secuencias \u00XX (modo
// HTML-safe por defecto). Para web_search en concreto eso es una divergencia
// de bytes real y frecuente, no un caso de esquina: las URLs de resultado
// están llenas de `&`, y títulos/snippets de `<`/`>`/`&`.
// Cada emisor de SSE del port (streaminganthropic/payloads.go,
// streaminganthropic/finish.go, streamingopenai) ya resuelve esto pasando
// el payload por internal/pyjson.Dumps antes de enmarcarlo — este archivo
// sigue el mismo patrón (writeAnthropicEvent/writeOpenAIChunk en
// sseopenai.go) para quedar consistente con el resto del port, aunque no
// haya corpus grabado que compare estos eventos byte a byte (MAPPING.md,
// filas streaming_anthropic.py/routes_anthropic.py: la intercepción
// web_search nunca se ejerció por ningún fixture).
//
// Una excepción deliberada: el "partial_json" del evento
// content_block_delta (mcp_tools.py:354) es el resultado de
// `json.dumps({"query": query})` SIN `ensure_ascii=False` — el default real
// de Python (ensure_ascii=True). Por eso esa construcción concreta usa
// pyjson.DumpsASCII, no pyjson.Dumps (ver el comentario de esa línea más
// abajo) — pyjson.Dumps documenta expresamente que es solo para los call
// sites que pasan ensure_ascii=False de forma explícita.

// searchResultContentBlock es el elemento de "content" de un bloque
// web_search_tool_result, mcp_tools.py:366-373,713-721 (encrypted_content
// es el snippet crudo — el original no cifra nada, es literal el nombre del
// campo del protocolo Anthropic real).
type searchResultContentBlock struct {
	Type             string  `json:"type"`
	Title            string  `json:"title"`
	URL              string  `json:"url"`
	EncryptedContent string  `json:"encrypted_content"`
	PageAge          *string `json:"page_age"`
}

func buildSearchResultContent(results map[string]any) []searchResultContentBlock {
	items := extractSearchResults(results)
	blocks := make([]searchResultContentBlock, 0, len(items))
	for _, item := range items {
		blocks = append(blocks, searchResultContentBlock{
			Type:             "web_search_result",
			Title:            item.Title,
			URL:              item.URL,
			EncryptedContent: item.Snippet,
			PageAge:          nil,
		})
	}
	return blocks
}

// --- Formato Anthropic (mcp_tools.py:281-424) ---

type anthropicSSEUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type anthropicMessageStartMessage struct {
	ID         string            `json:"id"`
	Type       string            `json:"type"`
	Role       string            `json:"role"`
	Model      string            `json:"model"`
	Content    []any             `json:"content"`
	StopReason *string           `json:"stop_reason"`
	Usage      anthropicSSEUsage `json:"usage"`
}

type anthropicMessageStartData struct {
	Type    string                       `json:"type"`
	Message anthropicMessageStartMessage `json:"message"`
}

type serverToolUseBlock struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

type contentBlockStart struct {
	Type         string `json:"type"`
	Index        int    `json:"index"`
	ContentBlock any    `json:"content_block"`
}

type inputJSONDelta struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}

type textDelta struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type contentBlockDelta struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	Delta any    `json:"delta"`
}

type contentBlockStop struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

type webSearchToolResultBlock struct {
	Type      string                     `json:"type"`
	ToolUseID string                     `json:"tool_use_id"`
	Content   []searchResultContentBlock `json:"content"`
}

type textContentBlockShape struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type anthropicMessageDeltaDelta struct {
	StopReason   string  `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
}

type anthropicMessageDeltaUsage struct {
	OutputTokens int `json:"output_tokens"`
}

type anthropicMessageDelta struct {
	Type  string                     `json:"type"`
	Delta anthropicMessageDeltaDelta `json:"delta"`
	Usage anthropicMessageDeltaUsage `json:"usage"`
}

type anthropicMessageStop struct {
	Type string `json:"type"`
}

// writeAnthropicEvent serializa payload, lo reformatea con
// internal/pyjson.Dumps (paridad de separadores/escapado con Python
// json.dumps(..., ensure_ascii=False)) y lo emite framed vía
// internal/sse.FormatEvent(eventType, ...) — el mismo patrón que
// streaminganthropic/payloads.go::writeEvent, equivalente Go de
// format_sse_event(event_type, data) del original.
func writeAnthropicEvent(buf *[]byte, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	formatted, err := pyjson.Dumps(raw)
	if err != nil {
		return err
	}
	*buf = append(*buf, sse.FormatEvent(eventType, []byte(formatted))...)
	return nil
}

// GenerateAnthropicWebSearchSSE emite los 11 eventos SSE en formato
// Anthropic que emulan la ejecución server-side de web_search. Puerto de
// mcp_tools.py:281-424 (generate_anthropic_web_search_sse).
//
// A diferencia del generador async del original (que yield-ea a un
// StreamingResponse en vivo), esta función devuelve el stream COMPLETO ya
// enmarcado: cada evento se deriva de datos que ya están enteros en memoria
// (results, el summary ya generado) — no hay ninguna fuente incremental
// real (a diferencia del pipeline de streaming_core.py que sí consume un
// AWS event-stream chunk a chunk) cuya latencia justifique escribir a un
// io.Writer con flush entre eventos. Task 7 escribe estos bytes tal cual al
// ResponseWriter.
func GenerateAnthropicWebSearchSSE(model, query, toolUseID string, results map[string]any, inputTokens int) ([]byte, error) {
	messageID := utils.GenerateMessageID()
	summary := GenerateSearchSummary(query, results)
	outputTokens := outputTokenCount(summary)

	var out []byte

	if err := writeAnthropicEvent(&out, "message_start", anthropicMessageStartData{
		Type: "message_start",
		Message: anthropicMessageStartMessage{
			ID:         messageID,
			Type:       "message",
			Role:       "assistant",
			Model:      model,
			Content:    []any{},
			StopReason: nil,
			Usage:      anthropicSSEUsage{InputTokens: inputTokens, OutputTokens: 0},
		},
	}); err != nil {
		return nil, err
	}

	if err := writeAnthropicEvent(&out, "content_block_start", contentBlockStart{
		Type:  "content_block_start",
		Index: 0,
		ContentBlock: serverToolUseBlock{
			Type:  "server_tool_use",
			ID:    toolUseID,
			Name:  "web_search",
			Input: map[string]any{},
		},
	}); err != nil {
		return nil, err
	}

	// mcp_tools.py:354: json.dumps({"query": query}) SIN ensure_ascii=False
	// → default real de Python (ensure_ascii=True) → pyjson.DumpsASCII, no
	// pyjson.Dumps (ver el comentario de cabecera del archivo).
	queryRaw, err := json.Marshal(map[string]string{"query": query})
	if err != nil {
		return nil, err
	}
	queryJSON, err := pyjson.DumpsASCII(queryRaw)
	if err != nil {
		return nil, err
	}
	if err := writeAnthropicEvent(&out, "content_block_delta", contentBlockDelta{
		Type:  "content_block_delta",
		Index: 0,
		Delta: inputJSONDelta{Type: "input_json_delta", PartialJSON: queryJSON},
	}); err != nil {
		return nil, err
	}

	if err := writeAnthropicEvent(&out, "content_block_stop", contentBlockStop{Type: "content_block_stop", Index: 0}); err != nil {
		return nil, err
	}

	if err := writeAnthropicEvent(&out, "content_block_start", contentBlockStart{
		Type:  "content_block_start",
		Index: 1,
		ContentBlock: webSearchToolResultBlock{
			Type:      "web_search_tool_result",
			ToolUseID: toolUseID,
			Content:   buildSearchResultContent(results),
		},
	}); err != nil {
		return nil, err
	}

	if err := writeAnthropicEvent(&out, "content_block_stop", contentBlockStop{Type: "content_block_stop", Index: 1}); err != nil {
		return nil, err
	}

	if err := writeAnthropicEvent(&out, "content_block_start", contentBlockStart{
		Type:         "content_block_start",
		Index:        2,
		ContentBlock: textContentBlockShape{Type: "text", Text: ""},
	}); err != nil {
		return nil, err
	}

	for _, chunk := range chunkRunes(summary, 100) {
		if err := writeAnthropicEvent(&out, "content_block_delta", contentBlockDelta{
			Type:  "content_block_delta",
			Index: 2,
			Delta: textDelta{Type: "text_delta", Text: chunk},
		}); err != nil {
			return nil, err
		}
	}

	if err := writeAnthropicEvent(&out, "content_block_stop", contentBlockStop{Type: "content_block_stop", Index: 2}); err != nil {
		return nil, err
	}

	if err := writeAnthropicEvent(&out, "message_delta", anthropicMessageDelta{
		Type:  "message_delta",
		Delta: anthropicMessageDeltaDelta{StopReason: "end_turn", StopSequence: nil},
		Usage: anthropicMessageDeltaUsage{OutputTokens: outputTokens},
	}); err != nil {
		return nil, err
	}

	if err := writeAnthropicEvent(&out, "message_stop", anthropicMessageStop{Type: "message_stop"}); err != nil {
		return nil, err
	}

	return out, nil
}

// chunkRunes trocea s en fragmentos de como máximo size runas (code points),
// nunca bytes: Python 3 trocea "for i in range(0, len(summary), chunk_size):
// summary[i:i+chunk_size]" (mcp_tools.py:400-401,492-493) sobre code points,
// y un snippet/título con UTF-8 multibyte partiría un carácter a la mitad si
// se troceara por bytes. Usado por los dos generadores SSE de este archivo
// para las deltas de texto (chunk_size=100, mcp_tools.py:399,491).
func chunkRunes(s string, size int) []string {
	r := []rune(s)
	if len(r) == 0 {
		return nil
	}
	chunks := make([]string, 0, (len(r)+size-1)/size)
	for i := 0; i < len(r); i += size {
		end := i + size
		if end > len(r) {
			end = len(r)
		}
		chunks = append(chunks, string(r[i:end]))
	}
	return chunks
}
