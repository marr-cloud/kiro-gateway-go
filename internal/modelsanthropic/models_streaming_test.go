// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package modelsanthropic_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
)

// TestRoundTripImageSource verifica directamente el aplanado de ImageSource
// (ver el comentario de blocks.go): Marshal debe producir las claves planas
// del wire de Anthropic (type + media_type + data, o type + url), no un
// objeto anidado bajo una clave "Base64ImageSource"/"URLImageSource" — y por
// eso aquí se compara contra los bytes de entrada originales, no solo entre
// sí (a diferencia de TestContentBlockMarshalRoundTrip, que no puede exigir
// igualdad byte a byte con la entrada porque el orden de claves de un
// ContentBlock aplanado no tiene por qué coincidir con el de la fuente).
func TestRoundTripImageSource(t *testing.T) {
	cases := []string{
		`{"type":"base64","media_type":"image/png","data":"AAAA"}`,
		`{"type":"url","url":"https://example.com/x.png"}`,
	}

	for _, raw := range cases {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(raw), &probe); err != nil {
			t.Fatalf("leyendo type de %s: %v", raw, err)
		}

		t.Run(probe.Type, func(t *testing.T) {
			var s1 modelsanthropic.ImageSource
			if err := json.Unmarshal([]byte(raw), &s1); err != nil {
				t.Fatalf("1er unmarshal: %v", err)
			}
			m1, err := json.Marshal(&s1)
			if err != nil {
				t.Fatalf("1er marshal: %v", err)
			}
			if !bytes.Equal(m1, []byte(raw)) {
				t.Errorf("m1 = %s, se esperaba idéntico byte a byte a la entrada %s", m1, raw)
			}

			var s2 modelsanthropic.ImageSource
			if err := json.Unmarshal(m1, &s2); err != nil {
				t.Fatalf("2do unmarshal: %v", err)
			}
			m2, err := json.Marshal(&s2)
			if err != nil {
				t.Fatalf("2do marshal: %v", err)
			}
			if !bytes.Equal(m1, m2) {
				t.Errorf("marshal no es idempotente:\n  m1 = %s\n  m2 = %s", m1, m2)
			}
		})
	}
}

// TestAnthropicMessageContentAsString verifica el field_validator replicado:
// un content que es una cadena JSON se convierte en un único ContentBlock de
// tipo text.
func TestAnthropicMessageContentAsString(t *testing.T) {
	raw := `{"role":"user","content":"What's the weather?"}`
	var m modelsanthropic.AnthropicMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m.Role != "user" {
		t.Errorf("Role = %q, want user", m.Role)
	}
	if len(m.Content) != 1 {
		t.Fatalf("len(Content) = %d, want 1", len(m.Content))
	}
	if m.Content[0].Type != "text" {
		t.Errorf("Content[0].Type = %q, want text", m.Content[0].Type)
	}
	if m.Content[0].Text == nil || *m.Content[0].Text != "What's the weather?" {
		t.Errorf("Content[0].Text = %v, want %q", m.Content[0].Text, "What's the weather?")
	}
}

// TestAnthropicMessageContentAsBlocks verifica que un content que es una
// lista de bloques se decodifica elemento a elemento.
func TestAnthropicMessageContentAsBlocks(t *testing.T) {
	raw := `{"role":"assistant","content":[{"type":"text","text":"a"},{"type":"tool_use","id":"1","name":"f","input":{}}]}`
	var m modelsanthropic.AnthropicMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(m.Content) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(m.Content))
	}
	if m.Content[0].Type != "text" || m.Content[1].Type != "tool_use" {
		t.Errorf("Content types = %q, %q", m.Content[0].Type, m.Content[1].Type)
	}
}

// TestAnthropicMessagesRequestDecode verifica una petición completa a
// /v1/messages, incluidos tools, thinking y tool_choice.
func TestAnthropicMessagesRequestDecode(t *testing.T) {
	body := `{
		"model": "claude-sonnet-4-5",
		"messages": [{"role": "user", "content": "hi"}],
		"max_tokens": 1024,
		"system": "be terse",
		"stream": true,
		"thinking": {"type": "enabled", "budget_tokens": 2000},
		"tools": [{"name": "get_weather", "input_schema": {"type": "object"}}],
		"tool_choice": {"type": "auto"},
		"temperature": 0.5,
		"stop_sequences": ["STOP"]
	}`

	var req modelsanthropic.AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q", req.Model)
	}
	if req.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %d, want 1024", req.MaxTokens)
	}
	if len(req.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(req.Messages))
	}
	if len(req.Tools) != 1 || req.Tools[0].Name != "get_weather" {
		t.Fatalf("Tools = %+v", req.Tools)
	}
	if req.Temperature == nil || *req.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", req.Temperature)
	}
	if len(req.StopSequences) != 1 || req.StopSequences[0] != "STOP" {
		t.Errorf("StopSequences = %v", req.StopSequences)
	}
}

// TestAnthropicMessagesResponseRoundTrip decodifica y vuelve a codificar una
// respuesta no-streaming.
func TestAnthropicMessagesResponseRoundTrip(t *testing.T) {
	body := `{
		"id": "msg_123",
		"type": "message",
		"role": "assistant",
		"content": [{"type": "text", "text": "hi"}],
		"model": "claude-sonnet-4-5",
		"stop_reason": "end_turn",
		"usage": {"input_tokens": 10, "output_tokens": 5}
	}`

	var resp modelsanthropic.AnthropicMessagesResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != "msg_123" {
		t.Errorf("ID = %q", resp.ID)
	}
	if resp.StopReason == nil || *resp.StopReason != "end_turn" {
		t.Errorf("StopReason = %v, want end_turn", resp.StopReason)
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 5 {
		t.Errorf("Usage = %+v", resp.Usage)
	}

	out, err := json.Marshal(&resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundtrip modelsanthropic.AnthropicMessagesResponse
	if err := json.Unmarshal(out, &roundtrip); err != nil {
		t.Fatalf("unmarshal roundtrip: %v", err)
	}
	if roundtrip.ID != resp.ID {
		t.Errorf("roundtrip ID mismatch: %q vs %q", roundtrip.ID, resp.ID)
	}
}

// TestStreamingEventTypes verifica que los tipos de eventos de streaming
// codifican y decodifican sus campos básicos.
func TestStreamingEventTypes(t *testing.T) {
	t.Run("ContentBlockDeltaEvent text_delta", func(t *testing.T) {
		ev := modelsanthropic.ContentBlockDeltaEvent{
			Type:  "content_block_delta",
			Index: 0,
			Delta: modelsanthropic.TextDelta{Type: "text_delta", Text: "hi"},
		}
		out, err := json.Marshal(&ev)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(out, &decoded); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		delta, ok := decoded["delta"].(map[string]any)
		if !ok || delta["text"] != "hi" {
			t.Errorf("delta = %v", decoded["delta"])
		}
	})

	t.Run("ErrorEvent", func(t *testing.T) {
		raw := `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`
		var ev modelsanthropic.ErrorEvent
		if err := json.Unmarshal([]byte(raw), &ev); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if ev.Type != "error" {
			t.Errorf("Type = %q, want error", ev.Type)
		}
		if ev.Error["message"] != "busy" {
			t.Errorf("Error[message] = %v, want busy", ev.Error["message"])
		}
	})

	t.Run("PingEvent and MessageStopEvent", func(t *testing.T) {
		var ping modelsanthropic.PingEvent
		if err := json.Unmarshal([]byte(`{"type":"ping"}`), &ping); err != nil {
			t.Fatalf("unmarshal ping: %v", err)
		}
		if ping.Type != "ping" {
			t.Errorf("ping.Type = %q", ping.Type)
		}
		var stop modelsanthropic.MessageStopEvent
		if err := json.Unmarshal([]byte(`{"type":"message_stop"}`), &stop); err != nil {
			t.Fatalf("unmarshal message_stop: %v", err)
		}
		if stop.Type != "message_stop" {
			t.Errorf("stop.Type = %q", stop.Type)
		}
	})
}

// TestAnthropicErrorResponseRoundTrip verifica el envoltorio de error.
func TestAnthropicErrorResponseRoundTrip(t *testing.T) {
	raw := `{"type":"error","error":{"type":"invalid_request_error","message":"bad request"}}`
	var resp modelsanthropic.AnthropicErrorResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Error.Type != "invalid_request_error" || resp.Error.Message != "bad request" {
		t.Errorf("Error = %+v", resp.Error)
	}
}
