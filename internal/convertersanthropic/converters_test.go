// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package convertersanthropic

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// ==================================================================================================
// Helpers de decodificación compartidos por los tests de este fichero
// ==================================================================================================

// decodeOutputString decodifica un output de corpus que es una cadena JSON
// simple.
func decodeOutputString(tb testing.TB, raw json.RawMessage, caseName string) string {
	tb.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		tb.Fatalf("case %s: decodificando output string: %v", caseName, err)
	}
	return s
}

// decodeAny decodifica un argumento de corpus a `any` genérico (el mismo
// mecanismo que usaría json.Unmarshal en el propio código de producción
// cuando decodifica un valor sin forma fija, p. ej. request.system).
func decodeAny(tb testing.TB, raw json.RawMessage, caseName string) any {
	tb.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		tb.Fatalf("case %s: decodificando any: %v", caseName, err)
	}
	return v
}

// decodeConfigStringMap lee una bandera de tipo mapa string->string de
// input.config (HIDDEN_MODELS). Ver decodeConfigBool/decodeConfigInt en
// internal/converterscore/thinking_test.go para el mismo patrón sobre bool e
// int.
func decodeConfigStringMap(tb testing.TB, input json.RawMessage, key, caseName string) map[string]string {
	tb.Helper()
	cfg := testutil.Config(input)
	raw, ok := cfg[key]
	if !ok {
		tb.Fatalf("case %s: falta %s en input.config", caseName, key)
	}
	m := map[string]string{}
	if err := json.Unmarshal(raw, &m); err != nil {
		tb.Fatalf("case %s: decodificando %s: %v", caseName, key, err)
	}
	return m
}

// messagesToRaw convierte una lista de UnifiedMessage a la forma
// []map[string]any que usa el corpus para sus mensajes: los cinco campos
// SIEMPRE presentes (role, content, tool_calls, tool_results, images), con
// null explícito en vez de ausencia cuando un slice está vacío. No se
// reutiliza json.Marshal(msgs) directamente porque UnifiedMessage lleva
// omitempty en los tres slices opcionales (converterscore/types.go): con eso
// Marshal OMITIRÍA la clave en vez de escribir null, y el corpus siempre
// escribe la clave. Mismo helper que converterscore/normalize_test.go
// (messagesToRaw), duplicado aquí porque ese no está exportado y este
// paquete no puede depender de los internals de converterscore.
func messagesToRaw(msgs []converterscore.UnifiedMessage) []map[string]any {
	out := make([]map[string]any, len(msgs))
	for i, m := range msgs {
		out[i] = map[string]any{
			"role":         m.Role,
			"content":      m.Content,
			"tool_calls":   m.ToolCalls,
			"tool_results": m.ToolResults,
			"images":       m.Images,
		}
	}
	return out
}

// normalizeWantToolDescriptions neutraliza la única diferencia estructural
// entre el corpus y lo que UnifiedTool.Description (string, no *string)
// puede representar: un description=null del Python original se decodifica
// en Go como "" (json.Unmarshal deja el zero value de string al toparse con
// null). Mismo patrón que converterscore/tools_test.go
// (normalizeWantDescriptions), duplicado aquí por la misma razón que
// messagesToRaw. Si raw es el literal "null" (sin tools), se devuelve tal
// cual.
func normalizeWantToolDescriptions(tb testing.TB, raw json.RawMessage, caseName string) json.RawMessage {
	tb.Helper()
	if string(raw) == "null" {
		return raw
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		tb.Fatalf("case %s: decodificando tools esperadas: %v", caseName, err)
	}
	for _, tool := range tools {
		if desc, ok := tool["description"]; ok && string(desc) == "null" {
			tool["description"] = json.RawMessage(`""`)
		}
	}
	out, err := json.Marshal(tools)
	if err != nil {
		tb.Fatalf("case %s: recodificando tools esperadas: %v", caseName, err)
	}
	return out
}

// ==================================================================================================
// ConvertAnthropicContentToText
// ==================================================================================================

// TestConvertAnthropicContentToText valida ConvertAnthropicContentToText
// contra los 49 casos grabados de
// kiro.converters_anthropic:convert_anthropic_content_to_text. args[0] es
// `content` decodificado a `any` genérico: cadena, lista de bloques, null, o
// un escalar suelto (los dos casos límite del corpus: null y el entero 42).
func TestConvertAnthropicContentToText(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_anthropic/convert_anthropic_content_to_text")
	if len(cases) != 49 {
		t.Fatalf("se esperaban 49 casos de convert_anthropic_content_to_text, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			content := decodeAny(t, testutil.Arg(t, c.Input, 0), c.Name)

			got := ConvertAnthropicContentToText(content)
			want := decodeOutputString(t, c.Output, c.Name)

			if got != want {
				t.Errorf("case %s: got %q, want %q", c.Name, got, want)
			}
		})
	}
}

// ==================================================================================================
// ExtractSystemPrompt
// ==================================================================================================

// TestExtractSystemPrompt valida ExtractSystemPrompt contra los 12 casos
// grabados de kiro.converters_anthropic:extract_system_prompt.
func TestExtractSystemPrompt(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_anthropic/extract_system_prompt")
	if len(cases) != 12 {
		t.Fatalf("se esperaban 12 casos de extract_system_prompt, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			system := decodeAny(t, testutil.Arg(t, c.Input, 0), c.Name)

			got := ExtractSystemPrompt(system)
			want := decodeOutputString(t, c.Output, c.Name)

			if got != want {
				t.Errorf("case %s: got %q, want %q", c.Name, got, want)
			}
		})
	}
}

// ==================================================================================================
// ExtractToolResultsFromAnthropicContent
// ==================================================================================================

// TestExtractToolResultsFromAnthropicContent valida
// ExtractToolResultsFromAnthropicContent contra los 39 casos grabados de
// kiro.converters_anthropic:extract_tool_results_from_anthropic_content.
func TestExtractToolResultsFromAnthropicContent(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_anthropic/extract_tool_results_from_anthropic_content")
	if len(cases) != 39 {
		t.Fatalf("se esperaban 39 casos de extract_tool_results_from_anthropic_content, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			content := decodeAny(t, testutil.Arg(t, c.Input, 0), c.Name)

			got := ExtractToolResultsFromAnthropicContent(content)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// ==================================================================================================
// ExtractImagesFromToolResults
// ==================================================================================================

// TestExtractImagesFromToolResults valida ExtractImagesFromToolResults
// contra los 35 casos grabados de
// kiro.converters_anthropic:extract_images_from_tool_results.
func TestExtractImagesFromToolResults(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_anthropic/extract_images_from_tool_results")
	if len(cases) != 35 {
		t.Fatalf("se esperaban 35 casos de extract_images_from_tool_results, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			content := decodeAny(t, testutil.Arg(t, c.Input, 0), c.Name)

			got := ExtractImagesFromToolResults(content)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// ==================================================================================================
// ExtractToolUsesFromAnthropicContent
// ==================================================================================================

// TestExtractToolUsesFromAnthropicContent valida
// ExtractToolUsesFromAnthropicContent contra los 21 casos grabados de
// kiro.converters_anthropic:extract_tool_uses_from_anthropic_content.
func TestExtractToolUsesFromAnthropicContent(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_anthropic/extract_tool_uses_from_anthropic_content")
	if len(cases) != 21 {
		t.Fatalf("se esperaban 21 casos de extract_tool_uses_from_anthropic_content, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			content := decodeAny(t, testutil.Arg(t, c.Input, 0), c.Name)

			got := ExtractToolUsesFromAnthropicContent(content)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// ==================================================================================================
// ConvertAnthropicMessages
// ==================================================================================================

// TestConvertAnthropicMessages valida ConvertAnthropicMessages contra los 22
// casos grabados de kiro.converters_anthropic:convert_anthropic_messages.
//
// args[0] es una lista de mensajes con la forma exacta del wire Anthropic
// (role + content, donde content puede ser cadena o lista de bloques):
// modelsanthropic.AnthropicMessage.UnmarshalJSON decodifica ambas formas
// directamente, así que no hace falta un decodificador propio.
//
// La salida grabada es una lista plana de UnifiedMessage (NO una tupla con
// system prompt — ver el comentario de cabecera de converters.go sobre la
// corrección de firma respecto al brief).
func TestConvertAnthropicMessages(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_anthropic/convert_anthropic_messages")
	if len(cases) != 22 {
		t.Fatalf("se esperaban 22 casos de convert_anthropic_messages, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var msgs []modelsanthropic.AnthropicMessage
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &msgs); err != nil {
				t.Fatalf("case %s: decodificando messages: %v", c.Name, err)
			}

			got := ConvertAnthropicMessages(msgs)
			testutil.AssertJSONEqual(t, messagesToRaw(got), c.Output, c.Name)
		})
	}
}

// ==================================================================================================
// ConvertAnthropicTools
// ==================================================================================================

// TestConvertAnthropicTools valida ConvertAnthropicTools contra los 10 casos
// grabados de kiro.converters_anthropic:convert_anthropic_tools.
//
// args[0] es null o una lista de tools con la forma exacta de
// modelsanthropic.AnthropicTool (incluidos sus campos null explícitos, p.
// ej. "type": null, "max_uses": null): se decodifica directamente a
// []modelsanthropic.AnthropicTool.
func TestConvertAnthropicTools(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_anthropic/convert_anthropic_tools")
	if len(cases) != 10 {
		t.Fatalf("se esperaban 10 casos de convert_anthropic_tools, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)

			var tools []modelsanthropic.AnthropicTool
			if string(raw) != "null" {
				if err := json.Unmarshal(raw, &tools); err != nil {
					t.Fatalf("case %s: decodificando tools: %v", c.Name, err)
				}
			}

			got := ConvertAnthropicTools(tools)
			want := normalizeWantToolDescriptions(t, c.Output, c.Name)
			testutil.AssertJSONEqual(t, got, want, c.Name)
		})
	}
}
