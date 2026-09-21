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
