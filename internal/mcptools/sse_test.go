// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func testResults() map[string]any {
	return map[string]any{
		"results": []any{
			map[string]any{"title": "Go", "url": "https://go.dev", "snippet": strings.Repeat("x", 150)},
		},
		"totalResults": float64(1),
	}
}

// TestGenerateAnthropicWebSearchSSE_AmpersandNotEscaped es el escenario
// real que motiva el fix round 1 (parity #1), no solo un detalle de
// espaciado: las URLs de resultado están llenas de "&" (query strings) y
// los títulos/snippets de "<"/">"/"&". encoding/json.Marshal las escapa por
// defecto a sus secuencias \u00XX (comportamiento HTML-safe); Python
// json.dumps(ensure_ascii=False) — lo que emite format_sse_event en el
// original — las deja literales. Verifica que el SSE Anthropic las emite
// SIN escapar.
func TestGenerateAnthropicWebSearchSSE_AmpersandNotEscaped(t *testing.T) {
	results := map[string]any{
		"results": []any{
			map[string]any{
				"title":   "Go <tutorial> & more",
				"url":     "https://go.dev/search?q=a&b=c",
				"snippet": "Learn <Go> & build things",
			},
		},
	}
	out, err := GenerateAnthropicWebSearchSSE("m", "golang", NewToolUseID(), results, 1)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	s := string(out)

	for _, want := range []string{
		"https://go.dev/search?q=a&b=c",
		"Go <tutorial> & more",
		"Learn <Go> & build things",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("want %q literal (sin escapar) en el SSE, no se encontró.\nSSE:\n%s", want, s)
		}
	}
	for _, mustNotContain := range htmlEscapeSequences() {
		if strings.Contains(s, mustNotContain) {
			t.Errorf("el SSE contiene %q — encoding/json.Marshal escapó HTML donde pyjson.Dumps no debería", mustNotContain)
		}
	}
}

// TestGenerateOpenAIWebSearchSSE_AmpersandNotEscaped: mismo escenario que
// arriba, dialecto OpenAI.
func TestGenerateOpenAIWebSearchSSE_AmpersandNotEscaped(t *testing.T) {
	results := map[string]any{
		"results": []any{
			map[string]any{
				"title":   "Go <tutorial> & more",
				"url":     "https://go.dev/search?q=a&b=c",
				"snippet": "Learn <Go> & build things",
			},
		},
	}
	out, err := GenerateOpenAIWebSearchSSE("m", "golang", NewToolUseID(), results, 1)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	s := string(out)

	if !strings.Contains(s, "Go <tutorial> & more") {
		t.Errorf("want el título literal (sin escapar) en el SSE.\nSSE:\n%s", s)
	}
	for _, mustNotContain := range htmlEscapeSequences() {
		if strings.Contains(s, mustNotContain) {
			t.Errorf("el SSE contiene %q — encoding/json.Marshal escapó HTML donde pyjson.Dumps no debería", mustNotContain)
		}
	}
}

// htmlEscapeSequences construye en runtime (evitando escribir literales
// backslash-u en el fuente, propensos a errores de doble-escapado) las tres
// secuencias que encoding/json.Marshal produciría para &, < y > por su
// modo HTML-safe por defecto — las que el SSE NO debe contener tras el fix.
func htmlEscapeSequences() []string {
	out := make([]string, 0, 3)
	for _, r := range []rune{'&', '<', '>'} {
		out = append(out, fmt.Sprintf(`\u%04x`, r))
	}
	return out
}

// TestGenerateAnthropicWebSearchSSE_EventOrder verifica los 11 eventos en
// orden exacto (mcp_tools.py:291-301) y que tool_use_id/summary viajan en
// el stream.
func TestGenerateAnthropicWebSearchSSE_EventOrder(t *testing.T) {
	toolUseID := NewToolUseID()
	out, err := GenerateAnthropicWebSearchSSE("claude-sonnet-4", "golang", toolUseID, testResults(), 42)
	if err != nil {
		t.Fatalf("GenerateAnthropicWebSearchSSE error: %v", err)
	}

	s := string(out)
	wantOrder := []string{
		"event: message_start",
		"event: content_block_start",
		"event: content_block_delta",
		"event: content_block_stop",
		"event: content_block_start",
		"event: content_block_stop",
		"event: content_block_start",
		"event: content_block_delta", // primer chunk del summary
		"event: content_block_stop",
		"event: message_delta",
		"event: message_stop",
	}
	pos := 0
	for _, want := range wantOrder {
		idx := strings.Index(s[pos:], want)
		if idx == -1 {
			t.Fatalf("no se encontró %q después de la posición %d.\nSSE completo:\n%s", want, pos, s)
		}
		pos += idx + len(want)
	}

	if !strings.Contains(s, toolUseID) {
		t.Errorf("el stream no contiene tool_use_id %q", toolUseID)
	}
	// partial_json es un string JSON embebido en el envelope del evento, así
	// que en el wire format aparece escapado (\"query\": \"golang\"), con
	// espacio tras los dos puntos porque ambos json.dumps (el interno de
	// mcp_tools.py:354 vía pyjson.DumpsASCII y el externo de
	// format_sse_event vía pyjson.Dumps) usan los separadores por defecto
	// de Python, no los compactos de encoding/json.
	if !strings.Contains(s, `\"query\": \"golang\"`) {
		t.Errorf("el stream no contiene el partial_json (escapado) de la query: %s", s)
	}
	if strings.Contains(s, "data: [DONE]") {
		t.Errorf("el dialecto Anthropic no debe llevar [DONE] (eso es OpenAI): %s", s)
	}

	// Cada línea "data: " debe ser JSON válido.
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "data: ") {
			var v any
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &v); err != nil {
				t.Errorf("línea data: no es JSON válido: %q: %v", line, err)
			}
		}
	}
}

// TestGenerateAnthropicWebSearchSSE_SummaryChunksReassemble confirma que
// las deltas de texto, concatenadas, reconstruyen el summary completo
// (mcp_tools.py:398-406).
func TestGenerateAnthropicWebSearchSSE_SummaryChunksReassemble(t *testing.T) {
	results := testResults()
	query := "golang"
	wantSummary := GenerateSearchSummary(query, results)

	out, err := GenerateAnthropicWebSearchSSE("m", query, NewToolUseID(), results, 1)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var reassembled strings.Builder
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var evt struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"delta"`
			Index int `json:"index"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &evt); err != nil {
			continue
		}
		if evt.Type == "content_block_delta" && evt.Index == 2 && evt.Delta.Type == "text_delta" {
			reassembled.WriteString(evt.Delta.Text)
		}
	}
	if reassembled.String() != wantSummary {
		t.Errorf("las deltas reensambladas no coinciden con el summary.\nwant: %q\ngot:  %q", wantSummary, reassembled.String())
	}
}

// TestGenerateOpenAIWebSearchSSE_NoEventPrefixAndDone: formato OpenAI, sin
// "event:", terminado en [DONE] (mcp_tools.py:461-465,526-527).
func TestGenerateOpenAIWebSearchSSE_NoEventPrefixAndDone(t *testing.T) {
	out, err := GenerateOpenAIWebSearchSSE("gpt-test", "golang", NewToolUseID(), testResults(), 10)
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	s := string(out)

	if strings.Contains(s, "event: ") {
		t.Errorf("el dialecto OpenAI no debe llevar líneas 'event: ': %s", s)
	}
	if !strings.HasSuffix(s, "data: [DONE]\n\n") {
		t.Errorf("want sufijo data: [DONE], got: %q", s[max(0, len(s)-40):])
	}
	// Separadores con espacio (pyjson.Dumps, paridad con
	// json.dumps(chunk, ensure_ascii=False) — mcp_tools.py:488,505,524).
	if !strings.Contains(s, `"object": "chat.completion.chunk"`) {
		t.Errorf("falta object=chat.completion.chunk")
	}
	if !strings.Contains(s, `"role": "assistant"`) {
		t.Errorf("falta el primer delta de role")
	}
	if !strings.Contains(s, `"finish_reason": "stop"`) {
		t.Errorf("falta finish_reason=stop")
	}
}

func TestGenerateOpenAIWebSearchSSE_ContentChunksReassemble(t *testing.T) {
	results := testResults()
	wantSummary := GenerateSearchSummary("golang", results)

	out, err := GenerateOpenAIWebSearchSSE("m", "golang", NewToolUseID(), results, 1)
	if err != nil {
		t.Fatalf("error: %v", err)
	}

	var reassembled strings.Builder
	for _, line := range bytes.Split(out, []byte("\n\n")) {
		if !bytes.HasPrefix(line, []byte("data: ")) {
			continue
		}
		payload := bytes.TrimPrefix(line, []byte("data: "))
		if bytes.Equal(payload, []byte("[DONE]")) {
			continue
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(payload, &chunk); err != nil {
			t.Fatalf("chunk no es JSON válido: %s: %v", payload, err)
		}
		if len(chunk.Choices) > 0 {
			reassembled.WriteString(chunk.Choices[0].Delta.Content)
		}
	}
	if reassembled.String() != wantSummary {
		t.Errorf("want %q, got %q", wantSummary, reassembled.String())
	}
}
