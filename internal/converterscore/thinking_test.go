// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// decodeConfigBool lee una bandera booleana de input.config. Las cuatro
// funciones de thinking.go leen banderas de kiro.config como globals de
// módulo (ver el comentario de cabecera de thinking.go); el corpus las graba
// dentro de input.config para cada caso, así que el test las decodifica de
// ahí y las asigna a la variable de paquete correspondiente antes de llamar.
func decodeConfigBool(tb testing.TB, input json.RawMessage, key, caseName string) bool {
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

// decodeConfigInt lee una bandera entera de input.config. Ver decodeConfigBool.
func decodeConfigInt(tb testing.TB, input json.RawMessage, key, caseName string) int {
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

// decodeOutputString decodifica un output de corpus que es una cadena JSON simple.
func decodeOutputString(tb testing.TB, raw json.RawMessage, caseName string) string {
	tb.Helper()
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		tb.Fatalf("case %s: decodificando output string: %v", caseName, err)
	}
	return s
}

// TestGetThinkingSystemPromptAdditionAgainstCorpus valida
// GetThinkingSystemPromptAddition contra los 3 casos grabados de
// kiro.converters_core:get_thinking_system_prompt_addition. La función no
// recibe argumentos (args=[], kwargs={}): todo el caso vive en
// input.config.FAKE_REASONING_ENABLED, que el test vuelca en la variable de
// paquete FakeReasoningEnabled antes de llamar.
func TestGetThinkingSystemPromptAdditionAgainstCorpus(t *testing.T) {
	origEnabled := FakeReasoningEnabled
	t.Cleanup(func() { FakeReasoningEnabled = origEnabled })

	for _, c := range testutil.LoadCorpus(t, "converters_core/get_thinking_system_prompt_addition") {
		t.Run(c.Name, func(t *testing.T) {
			FakeReasoningEnabled = decodeConfigBool(t, c.Input, "FAKE_REASONING_ENABLED", c.Name)

			got := GetThinkingSystemPromptAddition()
			want := decodeOutputString(t, c.Output, c.Name)

			if got != want {
				t.Fatalf("case %s: got %q, want %q", c.Name, got, want)
			}
		})
	}
}

// TestGetTruncationRecoverySystemAdditionAgainstCorpus valida
// GetTruncationRecoverySystemAddition contra los 4 casos grabados de
// kiro.converters_core:get_truncation_recovery_system_addition. Igual que el
// target anterior, depende solo de una bandera de input.config
// (TRUNCATION_RECOVERY) — verificado contra el corpus: el resultado NO
// depende de FAKE_REASONING_ENABLED pese a que ambas banderas aparecen en
// input.config de los cuatro casos.
func TestGetTruncationRecoverySystemAdditionAgainstCorpus(t *testing.T) {
	origEnabled := TruncationRecoveryEnabled
	t.Cleanup(func() { TruncationRecoveryEnabled = origEnabled })

	for _, c := range testutil.LoadCorpus(t, "converters_core/get_truncation_recovery_system_addition") {
		t.Run(c.Name, func(t *testing.T) {
			TruncationRecoveryEnabled = decodeConfigBool(t, c.Input, "TRUNCATION_RECOVERY", c.Name)

			got := GetTruncationRecoverySystemAddition()
			want := decodeOutputString(t, c.Output, c.Name)

			if got != want {
				t.Fatalf("case %s: got %q, want %q", c.Name, got, want)
			}
		})
	}
}

// TestInjectThinkingTagsAgainstCorpus valida InjectThinkingTags contra los 55
// casos grabados de kiro.converters_core:inject_thinking_tags. args[0] es el
// content (string), args[1] es el thinking_config (dict con enabled y
// budget_tokens, decodificado directamente a ThinkingConfig gracias a sus
// tags json). input.config trae las tres banderas de las que depende el
// presupuesto efectivo: FAKE_REASONING_ENABLED, FAKE_REASONING_MAX_TOKENS y
// FAKE_REASONING_BUDGET_CAP.
func TestInjectThinkingTagsAgainstCorpus(t *testing.T) {
	origEnabled := FakeReasoningEnabled
	origMaxTokens := FakeReasoningMaxTokens
	origBudgetCap := FakeReasoningBudgetCap
	t.Cleanup(func() {
		FakeReasoningEnabled = origEnabled
		FakeReasoningMaxTokens = origMaxTokens
		FakeReasoningBudgetCap = origBudgetCap
	})

	for _, c := range testutil.LoadCorpus(t, "converters_core/inject_thinking_tags") {
		t.Run(c.Name, func(t *testing.T) {
			FakeReasoningEnabled = decodeConfigBool(t, c.Input, "FAKE_REASONING_ENABLED", c.Name)
			FakeReasoningMaxTokens = decodeConfigInt(t, c.Input, "FAKE_REASONING_MAX_TOKENS", c.Name)
			FakeReasoningBudgetCap = decodeConfigInt(t, c.Input, "FAKE_REASONING_BUDGET_CAP", c.Name)

			content := decodeOutputString(t, testutil.Arg(t, c.Input, 0), c.Name)

			var cfg ThinkingConfig
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 1), &cfg); err != nil {
				t.Fatalf("case %s: decodificando thinking_config: %v", c.Name, err)
			}

			got := InjectThinkingTags(content, cfg)
			want := decodeOutputString(t, c.Output, c.Name)

			if got != want {
				t.Fatalf("case %s: got %q, want %q", c.Name, got, want)
			}
		})
	}
}

// TestStripAllToolContentAgainstCorpus valida StripAllToolContent contra los
// 44 casos grabados de kiro.converters_core:strip_all_tool_content. La
// salida grabada es la tupla de Python [messages, modified], igual que
// ensure_assistant_before_tool_results en normalize_test.go — reutiliza sus
// helpers decodeMessages/messagesToRaw, del mismo paquete.
//
// A diferencia de merge_adjacent_messages (Task 6), esta función siempre
// construye un UnifiedMessage NUEVO para las entradas que modifica (ver
// strip_all_tool_content, líneas 972-978 del original) en vez de mutar el
// mensaje recibido, así que no sufre el defecto de aliasing del grabador
// documentado en knownCorpusInputAliasingDefects: verificado a mano contra
// dos casos con tool_calls/tool_results (1b4e78aef0c00868, eef15722171ad923)
// y dos casos límite con listas vacías que NO cuentan como contenido de tool,
// bool([]) es False en Python (ac349a82d43c1f2a, 689e7547a6c6e637) — los
// cuatro reproducen el output grabado partiendo literalmente de su input
// grabado, así que los 44 casos se ejecutan sin exclusiones.
func TestStripAllToolContentAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/strip_all_tool_content") {
		t.Run(c.Name, func(t *testing.T) {
			input := decodeMessages(t, testutil.Arg(t, c.Input, 0), c.Name)

			var tuple [2]json.RawMessage
			if err := json.Unmarshal(c.Output, &tuple); err != nil {
				t.Fatalf("case %s: decode output tuple: %v", c.Name, err)
			}
			var wantModified bool
			if err := json.Unmarshal(tuple[1], &wantModified); err != nil {
				t.Fatalf("case %s: decode output[1] (modified): %v", c.Name, err)
			}

			gotMsgs, gotModified := StripAllToolContent(input)

			testutil.AssertJSONEqual(t, messagesToRaw(gotMsgs), tuple[0], c.Name)
			if gotModified != wantModified {
				t.Fatalf("case %s: modified = %v, want %v", c.Name, gotModified, wantModified)
			}
		})
	}
}
