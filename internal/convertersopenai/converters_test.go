// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package convertersopenai

import (
	"encoding/json"
	"strconv"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// ==================================================================================================
// Helpers de decodificación compartidos por los tests de este fichero
// ==================================================================================================

// messagesToRaw convierte una lista de UnifiedMessage a la forma
// []map[string]any que usa el corpus para sus mensajes: los cinco campos
// SIEMPRE presentes (role, content, tool_calls, tool_results, images), con
// null explícito en vez de ausencia cuando un slice está vacío. Mismo helper
// que convertersanthropic/converters_test.go (duplicado por la misma razón:
// converterscore.UnifiedMessage lleva omitempty en los tres slices
// opcionales, así que un Marshal directo OMITIRÍA la clave en vez de escribir
// null).
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
// null). Mismo patrón que convertersanthropic/converters_test.go.
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

// decodeConfigBoolLocal / decodeConfigIntLocal replican
// converterscore.decodeConfigBool/decodeConfigInt (thinking_test.go), no
// exportadas desde ese paquete: este test las necesita para poblar las siete
// banderas de converterscore antes de llamar a BuildKiroPayload.
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

// argOrKwarg devuelve el argumento posicional en la posición index si
// existe, o el kwarg de nombre kwargName en su defecto. build_kiro_payload
// tiene un caso del corpus (2216bceb8056483d) grabado con la forma con
// nombre (request_data=..., conversation_id=..., profile_arn=...) en vez de
// la posicional que usan los otros 37 — el original acepta ambas formas
// (build_kiro_payload(request_data, conversation_id, profile_arn), llamable
// posicional o con nombre) y pytest graba la que use cada test exacto.
func argOrKwarg(tb testing.TB, input json.RawMessage, index int, kwargName string) json.RawMessage {
	tb.Helper()
	args := testutil.Args(tb, input)
	if index < len(args) {
		return args[index]
	}
	return testutil.Kwarg(tb, input, kwargName)
}

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

// ==================================================================================================
// ConvertOpenAIMessagesToUnified
// ==================================================================================================

// TestConvertOpenAIMessagesToUnified valida ConvertOpenAIMessagesToUnified
// contra los 31 casos grabados de
// kiro.converters_openai:convert_openai_messages_to_unified.
//
// A diferencia de convertersanthropic.ConvertAnthropicMessages, esta función
// SÍ devuelve una tupla (system_prompt, unified_messages) — el corpus graba
// "output" como una lista JSON de dos elementos: [system_prompt, messages].
// Ver .upstream/kiro/converters_openai.py:141-154.
func TestConvertOpenAIMessagesToUnified(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_openai/convert_openai_messages_to_unified")
	if len(cases) != 31 {
		t.Fatalf("se esperaban 31 casos de convert_openai_messages_to_unified, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var msgs []modelsopenai.ChatMessage
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &msgs); err != nil {
				t.Fatalf("case %s: decodificando messages: %v", c.Name, err)
			}

			var wantTuple [2]json.RawMessage
			if err := json.Unmarshal(c.Output, &wantTuple); err != nil {
				t.Fatalf("case %s: decodificando output esperado: %v", c.Name, err)
			}
			var wantSystemPrompt string
			if err := json.Unmarshal(wantTuple[0], &wantSystemPrompt); err != nil {
				t.Fatalf("case %s: decodificando system_prompt esperado: %v", c.Name, err)
			}

			gotSystemPrompt, gotMessages := ConvertOpenAIMessagesToUnified(msgs)

			if gotSystemPrompt != wantSystemPrompt {
				t.Errorf("case %s: systemPrompt = %q, want %q", c.Name, gotSystemPrompt, wantSystemPrompt)
			}
			testutil.AssertJSONEqual(t, messagesToRaw(gotMessages), wantTuple[1], c.Name)
		})
	}
}

// ==================================================================================================
// ConvertOpenAIToolsToUnified
// ==================================================================================================

// TestConvertOpenAIToolsToUnified valida ConvertOpenAIToolsToUnified contra
// los 25 casos grabados de kiro.converters_openai:convert_openai_tools_to_unified.
//
// args[0] es null o una lista de tools con la forma exacta de
// modelsopenai.Tool (tanto el formato estándar {"type","function"} como el
// formato plano estilo Cursor {"name","description","input_schema"}).
func TestConvertOpenAIToolsToUnified(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_openai/convert_openai_tools_to_unified")
	if len(cases) != 25 {
		t.Fatalf("se esperaban 25 casos de convert_openai_tools_to_unified, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)

			var tools []modelsopenai.Tool
			if string(raw) != "null" {
				if err := json.Unmarshal(raw, &tools); err != nil {
					t.Fatalf("case %s: decodificando tools: %v", c.Name, err)
				}
			}

			got := ConvertOpenAIToolsToUnified(tools)
			want := normalizeWantToolDescriptions(t, c.Output, c.Name)
			testutil.AssertJSONEqual(t, got, want, c.Name)
		})
	}
}

// ==================================================================================================
// ReasoningEffortToBudget
// ==================================================================================================

// TestReasoningEffortToBudget valida ReasoningEffortToBudget contra los 10
// casos grabados de kiro.converters_openai:reasoning_effort_to_budget.
// args = [max_tokens, effort]. La tabla real (verificada contra
// .upstream/kiro/converters_openai.py:320-327) es none=0, minimal=0.10,
// low=0.20, medium=0.50, high=0.80, xhigh=0.95 — más fina que la tabla de
// cuatro entradas que describía el brief (sin minimal/xhigh, y sin el
// "default = medium" para claves desconocidas: el original indexa el dict
// directamente sin .get(), así que una clave fuera de esas seis lanzaría
// KeyError; ningún caso del corpus la ejercita).
func TestReasoningEffortToBudget(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_openai/reasoning_effort_to_budget")
	if len(cases) != 10 {
		t.Fatalf("se esperaban 10 casos de reasoning_effort_to_budget, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var maxTokens int
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &maxTokens); err != nil {
				t.Fatalf("case %s: decodificando max_tokens: %v", c.Name, err)
			}
			var effort string
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 1), &effort); err != nil {
				t.Fatalf("case %s: decodificando effort: %v", c.Name, err)
			}
			var want int
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decodificando output esperado: %v", c.Name, err)
			}

			got := ReasoningEffortToBudget(maxTokens, effort)
			if got != want {
				t.Errorf("case %s: ReasoningEffortToBudget(%d, %q) = %d, want %d", c.Name, maxTokens, effort, got, want)
			}
		})
	}
}

// ==================================================================================================
// ExtractThinkingConfigFromOpenAI
// ==================================================================================================

// TestExtractThinkingConfigFromOpenAI valida ExtractThinkingConfigFromOpenAI
// contra los 37 casos grabados de
// kiro.converters_openai:extract_thinking_config_from_openai.
//
// Esta función NO lee ningún package var de converterscore (a diferencia de
// build_kiro_payload): opera solo sobre los campos del propio request
// (reasoning_effort, max_tokens, max_completion_tokens), así que no hace
// falta poblar las siete banderas antes de llamarla.
func TestExtractThinkingConfigFromOpenAI(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_openai/extract_thinking_config_from_openai")
	if len(cases) != 37 {
		t.Fatalf("se esperaban 37 casos de extract_thinking_config_from_openai, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var req modelsopenai.ChatCompletionRequest
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &req); err != nil {
				t.Fatalf("case %s: decodificando request: %v", c.Name, err)
			}

			var want converterscore.ThinkingConfig
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decodificando output esperado: %v", c.Name, err)
			}

			got := ExtractThinkingConfigFromOpenAI(&req)

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
// BuildKiroPayload (integrador)
// ==================================================================================================

// knownDefectiveBuildKiroPayloadCases es el único caso de
// testdata/converters_openai/build_kiro_payload cuyo "input.config" grabado
// NO refleja la configuración realmente activa durante la llamada real que
// produjo su "output" — la misma familia de defecto de grabación que ya
// documentaron Task 9 (knownDefectiveAnthropicToKiroCases,
// convertersanthropic/converters_test.go) para anthropic_to_kiro.
//
// # Prueba
//
// .upstream/tests/unit/test_converters_openai.py:776-794
// (test_uses_continue_for_empty_content) llama a
//
//	with patch('kiro.converters_core.FAKE_REASONING_ENABLED', False):
//	    with patch('kiro.config.TRUNCATION_RECOVERY', False):
//	        result = build_kiro_payload(request, "conv-123", "")
//
// con request = ChatCompletionRequest(model="claude-sonnet-4-5",
// messages=[ChatMessage(role="user", content="")]) — exactamente el input
// grabado en e618c63b76f87ba9.json (mensaje único, content="", conv-123,
// profile_arn=""). El grabador (tools/corpus/recorder.py:_flag_snapshot) lee
// cada bandera de CONFIG_INPUTS así: "si el módulo del target la tiene como
// global propia, usa esa; si no, cae a kiro.config". El target de este caso
// es build_kiro_payload, cuyo módulo es kiro.converters_openai — y ese módulo
// NUNCA importa FAKE_REASONING_ENABLED a su propio namespace (su único import
// de kiro.config es HIDDEN_MODELS; ver la cabecera de
// .upstream/kiro/converters_openai.py:36-38). Por eso _flag_snapshot cae
// SIEMPRE a leer kiro.config.FAKE_REASONING_ENABLED para este target — nunca
// kiro.converters_core.FAKE_REASONING_ENABLED, la copia que de verdad
// consultan get_thinking_system_prompt_addition() e inject_thinking_tags()
// (ambas en converters_core.py, que SÍ importa el nombre a su propio
// namespace). kiro.config.FAKE_REASONING_ENABLED por defecto es True (sin
// FAKE_REASONING en el entorno — .upstream/kiro/config.py:432-434), así que
// el config grabado para este caso dice {"FAKE_REASONING_ENABLED": true,
// "TRUNCATION_RECOVERY": false, ...} — TRUNCATION_RECOVERY sí se capturó bien
// (se parchea directamente sobre kiro.config, el módulo que _flag_snapshot sí
// lee para esa bandera), pero FAKE_REASONING_ENABLED describe una
// configuración que nunca estuvo activa: la llamada real corrió con
// converters_core.FAKE_REASONING_ENABLED=False parcheado.
//
// El "output" grabado de e618c63b76f87ba9 es
// {"conversationState":{"chatTriggerType":"MANUAL","conversationId":"conv-123","currentMessage":{"userInputMessage":{"content":"(empty placeholder)","modelId":"claude-sonnet-4.5","origin":"AI_EDITOR"}}}}
// — sin userInputMessageContext, sin ninguna etiqueta de thinking: el
// resultado bajo FAKE_REASONING_ENABLED=False. Un port fiel de
// build_kiro_payload que aplica el config grabado (FAKE_REASONING_ENABLED=true)
// produciría en su lugar el contenido con las etiquetas de thinking
// inyectadas — el comportamiento CORRECTO del upstream real bajo esa
// configuración, solo que no es la que realmente rigió esta grabación.
var knownDefectiveBuildKiroPayloadCases = map[string]string{
	"e618c63b76f87ba9": "test_uses_continue_for_empty_content (línea 776): parchea converters_core.FAKE_REASONING_ENABLED=False, no kiro.config; _flag_snapshot graba FAKE_REASONING_ENABLED=true igualmente (default de kiro.config sin parchear)",
}

// TestBuildKiroPayload valida BuildKiroPayload contra los 38 casos grabados
// de kiro.converters_openai:build_kiro_payload, con la excepción documentada
// de knownDefectiveBuildKiroPayloadCases.
//
// input.args = [request_dict, conversation_id, profile_arn] (forma
// posicional, verificada contra el corpus). "output" es directamente el dict
// de payload devuelto por build_kiro_payload (el original hace `return
// result.payload`, ver .upstream/kiro/converters_openai.py:446).
//
// Los 38 casos fijan HIDDEN_MODELS={} (verificado por grep sobre todo el
// corpus de converters_openai): esta suite igualmente asigna HiddenModels
// desde input.config en cada caso, por si un corpus futuro sí lo ejercitara.
func TestBuildKiroPayload(t *testing.T) {
	cases := testutil.LoadCorpus(t, "converters_openai/build_kiro_payload")
	if len(cases) != 38 {
		t.Fatalf("se esperaban 38 casos de build_kiro_payload, hay %d", len(cases))
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
		if reason, known := knownDefectiveBuildKiroPayloadCases[c.Name]; known {
			skipped++
			t.Run(c.Name, func(t *testing.T) {
				t.Skipf("case %s: corpus defectuoso (config grabado no refleja el activo en la llamada real, ver knownDefectiveBuildKiroPayloadCases): %s", c.Name, reason)
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

			var req modelsopenai.ChatCompletionRequest
			if err := json.Unmarshal(argOrKwarg(t, c.Input, 0, "request_data"), &req); err != nil {
				t.Fatalf("case %s: decodificando request: %v", c.Name, err)
			}
			var conversationID string
			if err := json.Unmarshal(argOrKwarg(t, c.Input, 1, "conversation_id"), &conversationID); err != nil {
				t.Fatalf("case %s: decodificando conversation_id: %v", c.Name, err)
			}
			var profileArn string
			if err := json.Unmarshal(argOrKwarg(t, c.Input, 2, "profile_arn"), &profileArn); err != nil {
				t.Fatalf("case %s: decodificando profile_arn: %v", c.Name, err)
			}

			got := BuildKiroPayload(&req, conversationID, profileArn)
			testutil.AssertJSONEqual(t, got.Payload, c.Output, c.Name)
		})
	}

	if skipped != len(knownDefectiveBuildKiroPayloadCases) {
		t.Fatalf("se esperaban %d casos defectuosos documentados, se saltaron %d — knownDefectiveBuildKiroPayloadCases y el corpus se han desincronizado", len(knownDefectiveBuildKiroPayloadCases), skipped)
	}
}
