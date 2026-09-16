// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package parsers

import (
	"encoding/json"
	"regexp"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestFindMatchingBrace valida findMatchingBrace contra los 39 casos
// grabados de kiro.parsers:find_matching_brace
// (.upstream/kiro/parsers.py:39-89).
func TestFindMatchingBrace(t *testing.T) {
	cases := testutil.LoadCorpus(t, "parsers/find_matching_brace")
	if len(cases) != 39 {
		t.Fatalf("se esperaban 39 casos de find_matching_brace, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var text string
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &text); err != nil {
				t.Fatalf("case %s: decodificando text: %v", c.Name, err)
			}
			var start int
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 1), &start); err != nil {
				t.Fatalf("case %s: decodificando start: %v", c.Name, err)
			}

			got := findMatchingBrace([]byte(text), start)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// TestDeduplicateToolCalls valida DeduplicateToolCalls contra los 19 casos
// grabados de kiro.parsers:deduplicate_tool_calls
// (.upstream/kiro/parsers.py:151-208).
func TestDeduplicateToolCalls(t *testing.T) {
	cases := testutil.LoadCorpus(t, "parsers/deduplicate_tool_calls")
	if len(cases) != 19 {
		t.Fatalf("se esperaban 19 casos de deduplicate_tool_calls, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var toolCalls []map[string]any
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &toolCalls); err != nil {
				t.Fatalf("case %s: decodificando tool_calls: %v", c.Name, err)
			}

			got := DeduplicateToolCalls(toolCalls)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// toolCallIDPattern es la forma de un id de tool call generado:
// "call_" + 8 dígitos hexadecimales en minúscula (utils.GenerateToolCallID).
var toolCallIDPattern = regexp.MustCompile(`^call_[0-9a-f]{8}$`)

// TestParseBracketToolCalls valida ParseBracketToolCalls contra los 11 casos
// grabados de kiro.parsers:parse_bracket_tool_calls
// (.upstream/kiro/parsers.py:92-148).
//
// El campo "id" de cada tool call grabado (p.ej. "call_00000004") sale de
// generate_tool_call_id() con un CONTADOR GLOBAL DE SESIÓN que no se
// reinicia entre casos ni entre objetivos del corpus (docs/CORPUS.md §"El
// corpus solo es reproducible con la suite completa y en el orden
// canónico", líneas 44-48): el valor exacto depende de cuántas veces se
// llamó al generador en TODO el resto de la sesión de grabación (2032 casos,
// 64 objetivos), no solo en este fixture. No hay forma de reproducir ese
// contador desde un fixture aislado, así que — igual que docs/CORPUS.md ya
// documenta para streaming_openai/streaming_anthropic (ids chatcmpl-<hex>,
// msg_<hex>) y para la rama uuid4 de generate_conversation_id — se comprueba
// la FORMA del id ("call_" + 8 hex), no el valor exacto. name, arguments y
// type sí se comparan byte a byte.
func TestParseBracketToolCalls(t *testing.T) {
	cases := testutil.LoadCorpus(t, "parsers/parse_bracket_tool_calls")
	if len(cases) != 11 {
		t.Fatalf("se esperaban 11 casos de parse_bracket_tool_calls, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var text string
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &text); err != nil {
				t.Fatalf("case %s: decodificando response_text: %v", c.Name, err)
			}

			var want []map[string]any
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decodificando output: %v", c.Name, err)
			}

			got := ParseBracketToolCalls(text)
			if len(got) != len(want) {
				t.Fatalf("case %s: got %d tool calls, want %d\n got=%#v\nwant=%#v", c.Name, len(got), len(want), got, want)
			}

			for i := range want {
				wantFn, _ := want[i]["function"].(map[string]any)
				gotFn, _ := got[i]["function"].(map[string]any)

				if gotFn["name"] != wantFn["name"] {
					t.Errorf("case %s[%d]: function.name got %v, want %v", c.Name, i, gotFn["name"], wantFn["name"])
				}
				if gotFn["arguments"] != wantFn["arguments"] {
					t.Errorf("case %s[%d]: function.arguments got %v, want %v", c.Name, i, gotFn["arguments"], wantFn["arguments"])
				}
				if got[i]["type"] != want[i]["type"] {
					t.Errorf("case %s[%d]: type got %v, want %v", c.Name, i, got[i]["type"], want[i]["type"])
				}

				gotID, _ := got[i]["id"].(string)
				if !toolCallIDPattern.MatchString(gotID) {
					t.Errorf("case %s[%d]: id %q no tiene forma call_<8 hex>", c.Name, i, gotID)
				}
			}
		})
	}
}

// TestAwsEventStreamParser valida Parser contra los 22 casos "sequence"
// grabados de kiro.parsers:AwsEventStreamParser
// (.upstream/kiro/parsers.py:211-569): cada caso es una secuencia de
// llamadas a __init__/feed/get_tool_calls/reset sobre la misma instancia.
//
// El paso "__init__" trae config.TRUNCATION_RECOVERY, pero esa bandera solo
// cambia el TEXTO de un log dentro de _finalize_tool_call
// (.upstream/kiro/parsers.py:434-441) — nunca los datos devueltos ni el
// estado del tool call. Se ignora a propósito: no hace falta leerla para
// reproducir ningún caso, y este paquete no depende de os.Getenv ni de
// ninguna config externa.
func TestAwsEventStreamParser(t *testing.T) {
	cases := testutil.LoadCorpus(t, "parsers/AwsEventStreamParser")
	if len(cases) != 22 {
		t.Fatalf("se esperaban 22 casos de AwsEventStreamParser, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			p := NewParser()

			for i, step := range c.Steps {
				switch step.Method {
				case "__init__":
					// p ya se creó arriba con NewParser(); ver comentario de
					// función sobre por qué config.TRUNCATION_RECOVERY se
					// ignora.
				case "feed":
					chunk, err := testutil.DecodeBytes(testutil.Arg(t, step.Input, 0))
					if err != nil {
						t.Fatalf("case %s paso %d: decodificando chunk: %v", c.Name, i, err)
					}
					got := p.Feed(chunk)
					assertFeedEventsEqual(t, got, step.Output, c.Name, i)
				case "get_tool_calls":
					got := p.GetToolCalls()
					testutil.AssertJSONEqual(t, got, step.Output, c.Name)
				case "reset":
					p.Reset()
				default:
					t.Fatalf("case %s paso %d: método de secuencia desconocido %q", c.Name, i, step.Method)
				}
			}
		})
	}
}

// assertFeedEventsEqual reconstruye la forma {"type": ..., "data": ...} que
// devuelve AwsEventStreamParser.feed en el original a partir de los Event
// que devuelve Parser.Feed, y la compara contra lo grabado en el corpus.
// Value siempre es el objeto JSON completo (ver el comentario de Event en
// parser.go); aquí se extrae solo el campo que el original exponía como
// "data" para cada tipo.
func assertFeedEventsEqual(t *testing.T, got []Event, want json.RawMessage, caseName string, stepIdx int) {
	t.Helper()
	shaped := make([]map[string]any, len(got))
	for i, ev := range got {
		var data any
		switch ev.Kind {
		case "content":
			data = ev.Value["content"]
		case "usage":
			data = ev.Value["usage"]
		case "context_usage":
			data = ev.Value["contextUsagePercentage"]
		default:
			t.Fatalf("case %s paso %d: Feed produjo un Kind inesperado %q", caseName, stepIdx, ev.Kind)
		}
		shaped[i] = map[string]any{"type": ev.Kind, "data": data}
	}
	testutil.AssertJSONEqual(t, shaped, want, caseName)
}

// TestDiagnoseJSONTruncation cubre diagnoseJSONTruncation con 11 cadenas que
// ejercitan cada rama (vacía, solo espacios, llave/corchete sin cerrar,
// desbalanceo de llaves/corchetes, comilla sin cerrar con y sin escape, y
// JSON malformado sin ninguna de esas señales). Ninguno de los 22 casos de
// AwsEventStreamParser ejercita la rama de error de _finalize_tool_call
// (todos los tool calls del corpus tienen argumentos válidos), así que este
// target no tiene fixtures propias en testdata/ — los valores esperados
// salen de ejecutar el método real _diagnose_json_truncation del upstream
// (commit a5292ca, .upstream/kiro/parsers.py:464-548) directamente contra
// estas 11 cadenas, con loguru/kiro.utils/kiro.config sustituidos por stubs
// mínimos para poder importar el módulo de forma aislada.
func TestDiagnoseJSONTruncation(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  truncationInfo
	}{
		{"empty", "", truncationInfo{false, "empty string", 0}},
		{"whitespace_only", "   ", truncationInfo{false, "empty string", 3}},
		{"missing_closing_brace", `{"a": 1`, truncationInfo{true, "missing 1 closing brace(s)", 7}},
		{"missing_closing_bracket", `[1, 2, 3`, truncationInfo{true, "missing 1 closing bracket(s)", 8}},
		{"extra_closing_brace", `{"a": 1}}`, truncationInfo{true, "unbalanced braces (1 open, 2 close)", 9}},
		{"unbalanced_brackets_inside", `{"a": [1, 2}`, truncationInfo{true, "unbalanced brackets (1 open, 0 close)", 12}},
		{"unclosed_string", `{"a": "hello`, truncationInfo{true, "missing 1 closing brace(s)", 12}},
		{"unclosed_string_with_escaped_quote", `{"a": "he\"llo"`, truncationInfo{true, "missing 1 closing brace(s)", 15}},
		{"not_json", "not json at all", truncationInfo{false, "malformed JSON", 15}},
		{"nested_missing_brace", `{"a": 1, "b": {"c": 2}`, truncationInfo{true, "unbalanced braces (2 open, 1 close)", 22}},
		{"unclosed_string_trailing_escape", `{"a": "va\lue"`, truncationInfo{true, "missing 1 closing brace(s)", 14}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := diagnoseJSONTruncation(c.input)
			if got != c.want {
				t.Errorf("diagnoseJSONTruncation(%q) = %+v, want %+v", c.input, got, c.want)
			}
		})
	}
}
