// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package convertersanthropic

import (
	"encoding/json"
	"strconv"
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

// ==================================================================================================
// ExtractThinkingConfigFromAnthropic
// ==================================================================================================

// TestExtractThinkingConfigFromAnthropic valida
// ExtractThinkingConfigFromAnthropic contra los 31 casos grabados de
// kiro.converters_anthropic:extract_thinking_config_from_anthropic.
//
// La comparación NO usa testutil.AssertJSONEqual sobre el marshal de got:
// converterscore.ThinkingConfig.BudgetTokens lleva `json:"budget_tokens,
// omitempty"` (Task 7), así que json.Marshal OMITIRÍA la clave por completo
// cuando BudgetTokens es nil, mientras que el corpus siempre graba
// "budget_tokens": null explícito — el mismo problema estructural que
// messagesToRaw/normalizeWantToolDescriptions resuelven para sus propios
// targets, pero aquí basta con comparar los dos campos a mano porque
// ThinkingConfig solo tiene dos.
func TestExtractThinkingConfigFromAnthropic(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_anthropic/extract_thinking_config_from_anthropic")
	if len(cases) != 31 {
		t.Fatalf("se esperaban 31 casos de extract_thinking_config_from_anthropic, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var req modelsanthropic.AnthropicMessagesRequest
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &req); err != nil {
				t.Fatalf("case %s: decodificando request: %v", c.Name, err)
			}

			var want converterscore.ThinkingConfig
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decodificando output esperado: %v", c.Name, err)
			}

			got := ExtractThinkingConfigFromAnthropic(&req)

			if got.Enabled != want.Enabled {
				t.Errorf("case %s: Enabled = %v, want %v", c.Name, got.Enabled, want.Enabled)
			}
			gotBudget, wantBudget := "nil", "nil"
			if got.BudgetTokens != nil {
				gotBudget = strconv.Itoa(*got.BudgetTokens)
			}
			if want.BudgetTokens != nil {
				wantBudget = strconv.Itoa(*want.BudgetTokens)
			}
			if gotBudget != wantBudget {
				t.Errorf("case %s: BudgetTokens = %v, want %v", c.Name, gotBudget, wantBudget)
			}
		})
	}
}

// ==================================================================================================
// AnthropicToKiro (integrador)
// ==================================================================================================

// knownDefectiveAnthropicToKiroCases son los 5 casos de
// testdata/converters_anthropic/anthropic_to_kiro cuyo "input.config" grabado
// NO refleja la configuración realmente activa durante la llamada real que
// produjo su "output" — la misma familia de defecto de grabación que ya
// documentaron Task 6 (knownCorpusInputAliasingDefects,
// converterscore/normalize_test.go) y Task 8
// (knownDefectiveBuildKiroPayloadCases, converterscore/payload_test.go), pero
// con un mecanismo distinto: no es aliasing de argumentos mutados in-place,
// es que el propio grabador lee la bandera de configuración del módulo
// equivocado para este target concreto.
//
// # Prueba (reproducible, ver la instrucción del controller de "no saltar sin
// # prueba")
//
// Se ejecutó tools/corpus/recorder.py:_wrap_function/_flag_snapshot (líneas
// 483-500 y 552-568) contra el upstream real fijado en el commit a5292ca
// (.upstream/.venv), aplicando cada input.config grabado con setattr tanto
// sobre kiro.config como sobre kiro.converters_core (la misma disciplina que
// usa el propio _flag_snapshot: lee del módulo del target si la bandera está
// ahí, si no cae a kiro.config), reconstruyendo un AnthropicMessagesRequest
// fresco desde cada input.args[0] grabado y llamando a
// kiro.converters_anthropic.anthropic_to_kiro(request, conversation_id,
// profile_arn) tal cual. Resultado sobre los 32 casos: 27 coinciden byte a
// byte con su "output" grabado; exactamente estos 5 no — y lo que SÍ
// reproducen esos 5 es el resultado que ya produce esta implementación (con
// las etiquetas de thinking inyectadas), no el "output" grabado (sin ellas).
// Guion de reproducción y su salida completa archivados en el informe de esta
// tarea (task-9-report.md).
//
// # Causa raíz (mecanismo, no solo síntoma)
//
// _flag_snapshot(module, module_name) (recorder.py:483-500) lee cada bandera
// de CONFIG_INPUTS así: "si el módulo del target la tiene como global propia,
// usa esa; si no, cae a kiro.config". El target de estos 5 casos es
// anthropic_to_kiro, cuyo módulo es kiro.converters_anthropic — y ese módulo
// NUNCA importa FAKE_REASONING_ENABLED a su propio namespace (su único import
// de kiro.config es HIDDEN_MODELS; ver la cabecera de
// .upstream/kiro/converters_anthropic.py). Por eso _flag_snapshot cae
// SIEMPRE a leer kiro.config.FAKE_REASONING_ENABLED para este target — nunca
// kiro.converters_core.FAKE_REASONING_ENABLED, que es la copia que de verdad
// consultan get_thinking_system_prompt_addition() e inject_thinking_tags()
// (ambas viven en converters_core.py, que SÍ importa el nombre a su propio
// namespace con `from kiro.config import FAKE_REASONING_ENABLED` — ver el
// comentario de cabecera de converterscore/thinking.go, ya verificado en
// Task 7).
//
// Los 5 tests de origen en .upstream/tests/unit/test_converters_anthropic.py
// (p. ej. TestAnthropicToKiro.test_builds_simple_payload, línea 1447 —
// exactamente el request de 33a4bdf77cd1edcd: model claude-sonnet-4-5,
// mensaje único "Hello!", conv-123, arn:aws:test) llaman a anthropic_to_kiro
// dentro de `with patch("kiro.converters_core.FAKE_REASONING_ENABLED",
// False):` — parchean el módulo converters_core directamente, NUNCA
// kiro.config. Mientras el parche está activo, el comportamiento real es "sin
// thinking" (correcto, coincide con el "output" grabado), pero
// kiro.config.FAKE_REASONING_ENABLED permanece en su valor real sin parchear
// (True) durante toda la llamada — y ESO es lo que _flag_snapshot graba como
// "config": {"FAKE_REASONING_ENABLED": true, ...}, describiendo una
// configuración que nunca estuvo activa para esa llamada concreta.
//
// Consecuencia práctica: un port fiel de anthropic_to_kiro (el que pide la
// tarea) no puede reproducir el "output" de estos 5 casos partiendo de su
// "input.config" grabado, porque ese config nunca fue el que realmente rigió
// la llamada. Aplicar fielmente FAKE_REASONING_ENABLED=true (lo que dice el
// config grabado) es precisamente lo que hace esta implementación, y
// reproduce el comportamiento CORRECTO del upstream real bajo esa
// configuración — solo que no es la configuración bajo la que se grabaron
// estos 5 casos concretos.
var knownDefectiveAnthropicToKiroCases = map[string]string{
	"33a4bdf77cd1edcd": "test_builds_simple_payload (línea 1447): parchea converters_core.FAKE_REASONING_ENABLED=False, no kiro.config; _flag_snapshot graba FAKE_REASONING_ENABLED=true igualmente",
	"54dd684afca762f7": "mismo defecto: config grabado dice FAKE_REASONING_ENABLED=true, la llamada real corrió con converters_core.FAKE_REASONING_ENABLED=False parcheado",
	"5f3439336717932d": "mismo defecto (request con tools + tool_result, currentContent=placeholder): ni la adición de system prompt ni la inyección de tags aparecen en el output grabado pese a FAKE_REASONING_ENABLED=true en config",
	"65b172de47bd9399": "mismo defecto (request con system prompt propio): el output grabado antepone solo la adición de truncation recovery al system prompt, nunca la de thinking",
	"e321e58164827dd4": "mismo defecto (conversación de 3 mensajes): ni el history ni currentMessage llevan la adición o inyección de thinking en el output grabado",
}

// TestAnthropicToKiro valida AnthropicToKiro contra los 32 casos grabados de
// kiro.converters_anthropic:anthropic_to_kiro, con la excepción documentada
// de knownDefectiveAnthropicToKiroCases (ver su comentario para la prueba
// completa).
//
// input.args = [request_dict, conversation_id, profile_arn] (forma
// posicional, verificada contra el corpus — ver el comentario de cabecera de
// converters.go). "output" es directamente el dict de payload (NO envuelto
// en {"payload":...,"tool_documentation":...} como el corpus de
// converters_core/build_kiro_payload): el original devuelve result.payload,
// no el KiroPayloadResult completo.
//
// Los 251 casos de todo testdata/converters_anthropic fijan HIDDEN_MODELS={}
// (verificado por grep sobre el corpus completo): esta suite igualmente
// asigna HiddenModels desde input.config en cada caso, por si un corpus
// futuro sí lo ejercitara, siguiendo el mismo patrón de las siete banderas de
// converterscore (Task 7/8) más esta octava propia del adaptador Anthropic.
func TestAnthropicToKiro(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_anthropic/anthropic_to_kiro")
	if len(cases) != 32 {
		t.Fatalf("se esperaban 32 casos de anthropic_to_kiro, hay %d", len(cases))
	}

	origHidden := HiddenModels
	t.Cleanup(func() { HiddenModels = origHidden })

	origEnabled := converterscore.FakeReasoningEnabled
	origMaxTokens := converterscore.FakeReasoningMaxTokens
	origBudgetCap := converterscore.FakeReasoningBudgetCap
	origTruncation := converterscore.TruncationRecoveryEnabled
	origToolMaxLen := converterscore.ToolDescriptionMaxLength
	origAutoTrim := converterscore.AutoTrimPayload
	origMaxBytes := converterscore.KiroMaxPayloadBytes
	t.Cleanup(func() {
		converterscore.FakeReasoningEnabled = origEnabled
		converterscore.FakeReasoningMaxTokens = origMaxTokens
		converterscore.FakeReasoningBudgetCap = origBudgetCap
		converterscore.TruncationRecoveryEnabled = origTruncation
		converterscore.ToolDescriptionMaxLength = origToolMaxLen
		converterscore.AutoTrimPayload = origAutoTrim
		converterscore.KiroMaxPayloadBytes = origMaxBytes
	})

	skipped := 0
	for _, c := range cases {
		if reason, known := knownDefectiveAnthropicToKiroCases[c.Name]; known {
			skipped++
			t.Run(c.Name, func(t *testing.T) {
				t.Skipf("case %s: corpus defectuoso (config grabado no refleja el activo en la llamada real, ver knownDefectiveAnthropicToKiroCases): %s", c.Name, reason)
			})
			continue
		}

		t.Run(c.Name, func(t *testing.T) {
			HiddenModels = decodeConfigStringMap(t, c.Input, "HIDDEN_MODELS", c.Name)
			converterscore.FakeReasoningEnabled = decodeConfigBoolLocal(t, c.Input, "FAKE_REASONING_ENABLED", c.Name)
			converterscore.FakeReasoningMaxTokens = decodeConfigIntLocal(t, c.Input, "FAKE_REASONING_MAX_TOKENS", c.Name)
			converterscore.FakeReasoningBudgetCap = decodeConfigIntLocal(t, c.Input, "FAKE_REASONING_BUDGET_CAP", c.Name)
			converterscore.TruncationRecoveryEnabled = decodeConfigBoolLocal(t, c.Input, "TRUNCATION_RECOVERY", c.Name)
			converterscore.ToolDescriptionMaxLength = decodeConfigIntLocal(t, c.Input, "TOOL_DESCRIPTION_MAX_LENGTH", c.Name)
			converterscore.AutoTrimPayload = decodeConfigBoolLocal(t, c.Input, "AUTO_TRIM_PAYLOAD", c.Name)
			converterscore.KiroMaxPayloadBytes = decodeConfigIntLocal(t, c.Input, "KIRO_MAX_PAYLOAD_BYTES", c.Name)

			var req modelsanthropic.AnthropicMessagesRequest
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &req); err != nil {
				t.Fatalf("case %s: decodificando request: %v", c.Name, err)
			}
			var conversationID string
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 1), &conversationID); err != nil {
				t.Fatalf("case %s: decodificando conversation_id: %v", c.Name, err)
			}
			var profileArn string
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 2), &profileArn); err != nil {
				t.Fatalf("case %s: decodificando profile_arn: %v", c.Name, err)
			}

			got := AnthropicToKiro(&req, conversationID, profileArn)
			testutil.AssertJSONEqual(t, got.Payload, c.Output, c.Name)
		})
	}

	if skipped != len(knownDefectiveAnthropicToKiroCases) {
		t.Fatalf("se esperaban %d casos defectuosos documentados, se saltaron %d — knownDefectiveAnthropicToKiroCases y el corpus se han desincronizado", len(knownDefectiveAnthropicToKiroCases), skipped)
	}
}

// decodeConfigBoolLocal / decodeConfigIntLocal replican
// converterscore.decodeConfigBool/decodeConfigInt (thinking_test.go), no
// exportadas desde ese paquete: este test las necesita para poblar las siete
// banderas de converterscore antes de llamar a AnthropicToKiro.
func decodeConfigBoolLocal(tb testing.TB, input json.RawMessage, key, caseName string) bool {
	tb.Helper()
	cfg := testutil.Config(input)
	raw, ok := cfg[key]
	if !ok {
		tb.Fatalf("case %s: falta %s en input.config", caseName, key)
	}
	var v bool
	if err := json.Unmarshal(raw, &v); err != nil {
		tb.Fatalf("case %s: decodificando %s: %v", caseName, key, err)
	}
	return v
}

func decodeConfigIntLocal(tb testing.TB, input json.RawMessage, key, caseName string) int {
	tb.Helper()
	cfg := testutil.Config(input)
	raw, ok := cfg[key]
	if !ok {
		tb.Fatalf("case %s: falta %s en input.config", caseName, key)
	}
	var v int
	if err := json.Unmarshal(raw, &v); err != nil {
		tb.Fatalf("case %s: decodificando %s: %v", caseName, key, err)
	}
	return v
}
