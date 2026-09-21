// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package convertersopenai

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

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
