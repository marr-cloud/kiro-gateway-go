// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package modelsanthropic_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestAnthropicMessageRoundTrip decodifica el arg 0 ([]AnthropicMessage
// serializado) de cada caso golden de
// kiro.converters_anthropic:convert_anthropic_messages y comprueba que
// json.Unmarshal no falla. Cubre tanto content como cadena como content como
// lista de bloques (text, tool_use, tool_result, image), que es justo la
// polimorfía que AnthropicMessage.UnmarshalJSON y ContentBlock.UnmarshalJSON
// tienen que resolver.
func TestAnthropicMessageRoundTrip(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_anthropic/convert_anthropic_messages") {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			var msgs []modelsanthropic.AnthropicMessage
			if err := json.Unmarshal(raw, &msgs); err != nil {
				t.Fatalf("case %s: unmarshal: %v", c.Name, err)
			}
		})
	}
}

// TestContentBlockDiscrimination verifica, para cada valor de "type" que
// ContentBlock puede recibir, que los campos correspondientes se capturan y
// que Raw preserva los bytes JSON originales byte a byte.
func TestContentBlockDiscrimination(t *testing.T) {
	cases := []struct {
		name  string
		json  string
		check func(t *testing.T, b modelsanthropic.ContentBlock)
	}{
		{
			name: "text",
			json: `{"type":"text","text":"hello"}`,
			check: func(t *testing.T, b modelsanthropic.ContentBlock) {
				if b.Type != "text" {
					t.Errorf("Type = %q, want text", b.Type)
				}
				if b.Text == nil || *b.Text != "hello" {
					t.Errorf("Text = %v, want hello", b.Text)
				}
			},
		},
		{
			name: "thinking",
			json: `{"type":"thinking","thinking":"let me think","signature":"sig123"}`,
			check: func(t *testing.T, b modelsanthropic.ContentBlock) {
				if b.Type != "thinking" {
					t.Errorf("Type = %q, want thinking", b.Type)
				}
				if b.Thinking == nil || *b.Thinking != "let me think" {
					t.Errorf("Thinking = %v, want %q", b.Thinking, "let me think")
				}
				if b.Signature == nil || *b.Signature != "sig123" {
					t.Errorf("Signature = %v, want sig123", b.Signature)
				}
			},
		},
		{
			name: "tool_use",
			json: `{"type":"tool_use","id":"call_1","name":"get_weather","input":{"location":"Moscow"}}`,
			check: func(t *testing.T, b modelsanthropic.ContentBlock) {
				if b.Type != "tool_use" {
					t.Errorf("Type = %q, want tool_use", b.Type)
				}
				if b.ID == nil || *b.ID != "call_1" {
					t.Errorf("ID = %v, want call_1", b.ID)
				}
				if b.Name == nil || *b.Name != "get_weather" {
					t.Errorf("Name = %v, want get_weather", b.Name)
				}
				if string(b.Input) != `{"location":"Moscow"}` {
					t.Errorf("Input = %s, want %s", b.Input, `{"location":"Moscow"}`)
				}
			},
		},
		{
			name: "tool_result",
			json: `{"type":"tool_result","tool_use_id":"call_1","content":"Weather: Sunny","is_error":false}`,
			check: func(t *testing.T, b modelsanthropic.ContentBlock) {
				if b.Type != "tool_result" {
					t.Errorf("Type = %q, want tool_result", b.Type)
				}
				if b.ToolUseID == nil || *b.ToolUseID != "call_1" {
					t.Errorf("ToolUseID = %v, want call_1", b.ToolUseID)
				}
				if string(b.Content) != `"Weather: Sunny"` {
					t.Errorf("Content = %s, want %s", b.Content, `"Weather: Sunny"`)
				}
				if b.IsError == nil || *b.IsError != false {
					t.Errorf("IsError = %v, want false", b.IsError)
				}
			},
		},
		{
			name: "image",
			json: `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}`,
			check: func(t *testing.T, b modelsanthropic.ContentBlock) {
				if b.Type != "image" {
					t.Errorf("Type = %q, want image", b.Type)
				}
				if b.Source == nil {
					t.Fatalf("Source = nil, want non-nil")
				}
				if b.Source.Type != "base64" {
					t.Errorf("Source.Type = %q, want base64", b.Source.Type)
				}
				if b.Source.Base64 == nil || b.Source.Base64.MediaType != "image/png" || b.Source.Base64.Data != "AAAA" {
					t.Errorf("Source.Base64 = %+v", b.Source.Base64)
				}
			},
		},
		{
			name: "tool_reference",
			json: `{"type":"tool_reference","tool_name":"Read"}`,
			check: func(t *testing.T, b modelsanthropic.ContentBlock) {
				if b.Type != "tool_reference" {
					t.Errorf("Type = %q, want tool_reference", b.Type)
				}
				// tool_name se consolida en el campo Name, el mismo que usa
				// tool_use para el nombre de la herramienta: la interfaz del
				// task 1 no reserva un campo ToolName aparte.
				if b.Name == nil || *b.Name != "Read" {
					t.Errorf("Name = %v, want Read", b.Name)
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b modelsanthropic.ContentBlock
			if err := json.Unmarshal([]byte(tc.json), &b); err != nil {
				t.Fatalf("case %s: unmarshal: %v", tc.name, err)
			}
			tc.check(t, b)
			if !bytes.Equal(b.Raw, []byte(tc.json)) {
				t.Errorf("case %s: Raw = %s, want %s", tc.name, b.Raw, tc.json)
			}
		})
	}
}

// TestContentBlockUnknownFields comprueba que campos JSON desconocidos no
// hacen fallar la decodificación (extra="allow" del original) y que Raw los
// conserva para la reemisión.
func TestContentBlockUnknownFields(t *testing.T) {
	raw := `{"type":"tool_result","tool_use_id":"call_1","content":"ok","cache_control":{"type":"ephemeral"}}`
	var b modelsanthropic.ContentBlock
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !bytes.Equal(b.Raw, []byte(raw)) {
		t.Errorf("Raw = %s, want %s", b.Raw, raw)
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
