// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingopenai

import (
	"bytes"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
)

// TestBracketToolCallsWiredIntoFinish cubre la detección de tool calls estilo
// "[Called fn with args: {...}]" en el contenido acumulado, que Finish debe
// fusionar con los tool calls del stream y deduplicar
// (streaming_openai.py:276-279). Ningún fixture del corpus de Task 7 ejercita
// esta ruta (grep de `[Called` en los 40 fixtures = 0), así que se prueba a
// mano: sin este cableado, el texto bracket saldría como contenido literal con
// finish_reason "stop", no como un delta tool_calls con finish_reason
// "tool_calls" (era el gap encontrado en la review whole-branch de fase 5).
func TestBracketToolCallsWiredIntoFinish(t *testing.T) {
	f := New("claude-sonnet-4", AsReasoningContent)

	var buf bytes.Buffer
	content := `[Called get_weather with args: {"city": "Bogota"}]`
	if err := f.Handle(streamingcore.KiroEvent{Kind: "content", Content: content}, &buf); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := f.Finish(&buf); err != nil {
		t.Fatalf("Finish: %v", err)
	}

	var sawToolCall bool
	var toolName, finishReason string
	for _, data := range parseOutputChunks(buf.String()) {
		if data == "[DONE]" {
			continue
		}
		chunk := parseChunk(t, data)
		choices := getListField(chunk, "choices")
		if len(choices) == 0 {
			continue
		}
		choice, ok := choices[0].(map[string]any)
		if !ok {
			continue
		}
		delta := getMapField(choice, "delta")
		if tcs := getListField(delta, "tool_calls"); len(tcs) > 0 {
			if tc, ok := tcs[0].(map[string]any); ok {
				sawToolCall = true
				toolName = getStringField(getMapField(tc, "function"), "name")
			}
		}
		if fr, ok := choice["finish_reason"].(string); ok && fr != "" {
			finishReason = fr
		}
	}

	if !sawToolCall {
		t.Fatalf("no tool_calls chunk emitted for a bracket-style tool call; output=%s", buf.String())
	}
	if toolName != "get_weather" {
		t.Errorf("tool call name = %q, want get_weather", toolName)
	}
	if finishReason != "tool_calls" {
		t.Errorf("finish_reason = %q, want tool_calls", finishReason)
	}
}
