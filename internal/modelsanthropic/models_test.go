// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package modelsanthropic_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
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
				if b.Source.Base64ImageSource == nil || b.Source.MediaType != "image/png" || b.Source.Data != "AAAA" {
					t.Errorf("Source.Base64ImageSource = %+v", b.Source.Base64ImageSource)
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
				// tool_name tiene su propio campo Go, separado de Name: ver el
				// comentario de ContentBlock en blocks.go (fix round 1) sobre
				// por qué no se consolidan en un único campo.
				if b.ToolName == nil || *b.ToolName != "Read" {
					t.Errorf("ToolName = %v, want Read", b.ToolName)
				}
				if b.Name != nil {
					t.Errorf("Name = %v, want nil (tool_reference no lleva \"name\")", *b.Name)
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

// walkAssertLowercaseKeys recorre un valor JSON ya decodificado a any y falla
// el test si encuentra una clave de objeto que no sea enteramente minúscula
// (señal de que un campo Go sin tag json se filtró tal cual, p.ej. "Type" en
// vez de "type") o que sea literalmente "Raw" (el campo de preservación de
// bytes de ContentBlock/ImageSource, que nunca debe aparecer en la salida).
func walkAssertLowercaseKeys(t *testing.T, v any, path string) {
	t.Helper()
	switch val := v.(type) {
	case map[string]any:
		for k, sub := range val {
			if k != strings.ToLower(k) {
				t.Errorf("%s: clave JSON con mayúsculas %q (un campo Go se filtró sin tag json)", path, k)
			}
			if k == "Raw" {
				t.Errorf("%s: clave \"Raw\" presente en la salida: el buffer de preservación no debe serializarse nunca", path)
			}
			walkAssertLowercaseKeys(t, sub, path+"."+k)
		}
	case []any:
		for i, sub := range val {
			walkAssertLowercaseKeys(t, sub, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

// TestContentBlockMarshalRoundTrip es la prueba del fix del hallazgo
// importante de la ronda 1 de revisión: internal/modelsanthropic/blocks.go
// no llevaba tags json en ContentBlock ni en ImageSource, así que
// json.Marshal producía claves con el nombre Go capitalizado (Type, Text,
// ...) y además reemitía el bloque entero duplicado bajo una clave "Raw".
// Como AnthropicMessage.Content y AnthropicMessagesResponse.Content son
// []ContentBlock, cualquier código que serialice una respuesta Anthropic
// (la fase de streaming/converters que viene después de este task) habría
// heredado ese bug.
//
// Para cada uno de los 6 valores de "type" que ContentBlock discrimina,
// decodifica, serializa, vuelve a decodificar el resultado y serializa otra
// vez: los dos Marshal deben producir bytes IDÉNTICOS (el struct aplanado es
// la única fuente de verdad; no hay nada que perder o ganar en la segunda
// vuelta), y ninguna clave de la salida debe llevar mayúsculas ni
// llamarse "Raw". El caso tool_reference es el que de verdad ejercita el fix:
// antes de este fix round, su "tool_name" se consolidaba en el mismo campo
// Go que tool_use usa para "name", así que una reemisión habría escrito la
// clave equivocada.
func TestContentBlockMarshalRoundTrip(t *testing.T) {
	cases := []string{
		`{"type":"text","text":"hello"}`,
		`{"type":"thinking","thinking":"let me think","signature":"sig123"}`,
		`{"type":"tool_use","id":"call_1","name":"get_weather","input":{"location":"Moscow"}}`,
		`{"type":"tool_result","tool_use_id":"call_1","content":"Weather: Sunny","is_error":false}`,
		`{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AAAA"}}`,
		`{"type":"tool_reference","tool_name":"Read"}`,
	}

	for _, raw := range cases {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(raw), &probe); err != nil {
			t.Fatalf("leyendo type de %s: %v", raw, err)
		}

		t.Run(probe.Type, func(t *testing.T) {
			var b1 modelsanthropic.ContentBlock
			if err := json.Unmarshal([]byte(raw), &b1); err != nil {
				t.Fatalf("1er unmarshal: %v", err)
			}
			m1, err := json.Marshal(&b1)
			if err != nil {
				t.Fatalf("1er marshal: %v", err)
			}

			var b2 modelsanthropic.ContentBlock
			if err := json.Unmarshal(m1, &b2); err != nil {
				t.Fatalf("2do unmarshal (de %s): %v", m1, err)
			}
			m2, err := json.Marshal(&b2)
			if err != nil {
				t.Fatalf("2do marshal: %v", err)
			}

			if !bytes.Equal(m1, m2) {
				t.Errorf("marshal no es idempotente:\n  m1 = %s\n  m2 = %s", m1, m2)
			}

			var asAny any
			if err := json.Unmarshal(m1, &asAny); err != nil {
				t.Fatalf("unmarshal de m1 a any: %v", err)
			}
			walkAssertLowercaseKeys(t, asAny, "m1")

			// Comprobación explícita de la clave correcta para el caso que
			// motivó el fix: tool_reference debe reemitir "tool_name", nunca
			// "name" (y viceversa para tool_use, que no debe reemitir
			// "tool_name").
			obj, ok := asAny.(map[string]any)
			if !ok {
				t.Fatalf("m1 no decodifica a un objeto JSON: %s", m1)
			}
			switch probe.Type {
			case "tool_reference":
				if _, present := obj["tool_name"]; !present {
					t.Errorf("m1 = %s: falta la clave \"tool_name\"", m1)
				}
				if _, present := obj["name"]; present {
					t.Errorf("m1 = %s: no debería llevar la clave \"name\"", m1)
				}
			case "tool_use":
				if _, present := obj["name"]; !present {
					t.Errorf("m1 = %s: falta la clave \"name\"", m1)
				}
				if _, present := obj["tool_name"]; present {
					t.Errorf("m1 = %s: no debería llevar la clave \"tool_name\"", m1)
				}
			}
		})
	}
}

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
