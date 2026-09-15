// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestSanitizeJSONSchema valida SanitizeJSONSchema contra los 25 casos
// grabados de kiro.converters_core:sanitize_json_schema.
//
// El original solo elimina dos cosas (ver el docstring de la función en
// .upstream/kiro/converters_core.py:439-491): un `required: []` vacío y
// cualquier `additionalProperties`, recursando en `properties` y en
// cualquier valor anidado que sea dict o list. No toca `$schema`, `$defs`,
// `$ref`, `definitions` ni `default: null` — ninguno de los 25 casos del
// corpus los ejercita, y el código fuente no los menciona.
func TestSanitizeJSONSchema(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_core/sanitize_json_schema")
	if len(cases) != 25 {
		t.Fatalf("se esperaban 25 casos de sanitize_json_schema, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			var schema map[string]any
			if string(raw) != "null" {
				if err := json.Unmarshal(raw, &schema); err != nil {
					t.Fatalf("[%s] decodificando schema de entrada: %v", c.Name, err)
				}
			}
			got := SanitizeJSONSchema(schema)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// TestProcessToolsWithLongDescriptions valida ProcessToolsWithLongDescriptions
// contra los 30 casos grabados de
// kiro.converters_core:process_tools_with_long_descriptions.
//
// El original lee TOOL_DESCRIPTION_MAX_LENGTH de kiro.config; el port lo
// recibe como argumento (ver el plan de fase 3), así que el test lo saca de
// input.config, la misma bandera que el grabador guardó como entrada
// implícita (ver testutil.Config).
//
// La salida grabada es una tupla [processed_tools, system_prompt_addition].
// processed_tools ecoa el/los tool(s) de entrada sin tocar cuando su
// descripción no excede el límite — incluida la descripción tal cual venía,
// que en un solo caso (005ec83f4515da9d) es null porque el Python original
// puede llevar description=None. UnifiedTool.Description es un string Go, no
// *string (ver types.go), así que esa distinción null/"" no sobrevive el
// port: normalizeWantDescriptions neutraliza esa única diferencia antes de
// comparar, sin tocar ningún otro campo.
func TestProcessToolsWithLongDescriptions(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_core/process_tools_with_long_descriptions")
	if len(cases) != 30 {
		t.Fatalf("se esperaban 30 casos de process_tools_with_long_descriptions, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			tools := decodeToolsArg(t, c.Input, c.Name)
			maxLen := decodeMaxLen(t, c.Input, c.Name)

			gotProcessed, gotAddition := ProcessToolsWithLongDescriptions(tools, maxLen)

			var wantTuple [2]json.RawMessage
			if err := json.Unmarshal(c.Output, &wantTuple); err != nil {
				t.Fatalf("[%s] decodificando la tupla de salida esperada: %v", c.Name, err)
			}
			wantProcessed := normalizeWantDescriptions(t, wantTuple[0], c.Name)
			testutil.AssertJSONEqual(t, gotProcessed, wantProcessed, c.Name)

			var wantAddition string
			if err := json.Unmarshal(wantTuple[1], &wantAddition); err != nil {
				t.Fatalf("[%s] decodificando systemPromptAddition esperado: %v", c.Name, err)
			}
			if gotAddition != wantAddition {
				t.Errorf("[%s] systemPromptAddition:\n got:  %q\n want: %q", c.Name, gotAddition, wantAddition)
			}
		})
	}
}

// TestValidateToolNames valida ValidateToolNames contra los 25 casos
// grabados de kiro.converters_core:validate_tool_names.
//
// El original (.upstream/kiro/converters_core.py:560-600) solo rechaza
// nombres de más de 64 caracteres; no valida un patrón de caracteres
// permitidos. Ninguno de los 25 casos grabados dispara la excepción (el más
// largo tiene exactamente 64 caracteres, el límite válido), pero el test
// contempla la rama __exception__ igualmente vía testutil.IsException /
// DecodeException, tal y como exige el brief, para que un corpus futuro con
// casos de fallo pase sin tocar el test.
func TestValidateToolNames(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_core/validate_tool_names")
	if len(cases) != 25 {
		t.Fatalf("se esperaban 25 casos de validate_tool_names, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			tools := decodeToolsArg(t, c.Input, c.Name)
			err := ValidateToolNames(tools)

			if testutil.IsException(c.Output) {
				if err == nil {
					t.Fatalf("[%s] se esperaba un error, ValidateToolNames devolvió nil", c.Name)
				}
				exc, decErr := testutil.DecodeException(c.Output)
				if decErr != nil {
					t.Fatalf("[%s] decodificando la excepción esperada: %v", c.Name, decErr)
				}
				if err.Error() != exc.Str {
					t.Errorf("[%s] mensaje de error:\n got:  %q\n want: %q", c.Name, err.Error(), exc.Str)
				}
				return
			}

			if err != nil {
				t.Fatalf("[%s] se esperaba nil, ValidateToolNames devolvió: %v", c.Name, err)
			}
		})
	}
}

// TestConvertToolsToKiroFormat valida ConvertToolsToKiroFormat contra los 27
// casos grabados de kiro.converters_core:convert_tools_to_kiro_format.
func TestConvertToolsToKiroFormat(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_core/convert_tools_to_kiro_format")
	if len(cases) != 27 {
		t.Fatalf("se esperaban 27 casos de convert_tools_to_kiro_format, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			tools := decodeToolsArg(t, c.Input, c.Name)
			got := ConvertToolsToKiroFormat(tools)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// decodeToolsArg decodifica el argumento `tools` de un caso del corpus,
// aceptando tanto la forma posicional (args[0]) como la forma con nombre
// (kwargs["tools"]) que puede usar el grabador. En el corpus actual de los
// cuatro targets de este fichero siempre llega posicional, pero el brief
// exige contemplar ambas formas.
func decodeToolsArg(tb testing.TB, input json.RawMessage, caseName string) []UnifiedTool {
	tb.Helper()
	var raw json.RawMessage
	if args := testutil.Args(tb, input); len(args) > 0 {
		raw = args[0]
	} else {
		raw = testutil.Kwarg(tb, input, "tools")
	}
	if string(raw) == "null" {
		return nil
	}
	var tools []UnifiedTool
	if err := json.Unmarshal(raw, &tools); err != nil {
		tb.Fatalf("[%s] decodificando tools: %v", caseName, err)
	}
	return tools
}

// decodeMaxLen lee TOOL_DESCRIPTION_MAX_LENGTH de input.config: es la
// bandera que el original lee de kiro.config y que el port recibe como
// argumento explícito (maxLen) en vez de leer configuración global.
func decodeMaxLen(tb testing.TB, input json.RawMessage, caseName string) int {
	tb.Helper()
	cfg := testutil.Config(input)
	raw, ok := cfg["TOOL_DESCRIPTION_MAX_LENGTH"]
	if !ok {
		tb.Fatalf("[%s] falta TOOL_DESCRIPTION_MAX_LENGTH en input.config", caseName)
	}
	var n int
	if err := json.Unmarshal(raw, &n); err != nil {
		tb.Fatalf("[%s] decodificando TOOL_DESCRIPTION_MAX_LENGTH: %v", caseName, err)
	}
	return n
}

// normalizeWantDescriptions neutraliza la única diferencia estructural entre
// el corpus y lo que UnifiedTool.Description (string, no *string) puede
// representar: un description=null del Python original se decodifica en Go
// como "" (json.Unmarshal deja el zero value de string al toparse con
// null), así que se normaliza aquí para la comparación en vez de en
// producción — ver el comentario de UnifiedTool en types.go. Si raw no es
// una lista de tools (p. ej. es el `null` que representa "sin tools"), se
// devuelve tal cual.
func normalizeWantDescriptions(tb testing.TB, raw json.RawMessage, caseName string) json.RawMessage {
	tb.Helper()
	if string(raw) == "null" {
		return raw
	}
	var tools []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &tools); err != nil {
		tb.Fatalf("[%s] decodificando processed_tools esperado: %v", caseName, err)
	}
	for _, tool := range tools {
		if desc, ok := tool["description"]; ok && string(desc) == "null" {
			tool["description"] = json.RawMessage(`""`)
		}
	}
	out, err := json.Marshal(tools)
	if err != nil {
		tb.Fatalf("[%s] recodificando processed_tools esperado: %v", caseName, err)
	}
	return out
}
