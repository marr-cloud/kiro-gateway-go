// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"strings"
	"testing"
)

// buildCurrentContent construye un payload de un solo mensaje de usuario
// (sin historial, así que el system prompt va dentro de currentMessage) y
// devuelve el content que se mandaría a Kiro.
func buildCurrentContent(t *testing.T, modelID string, cfg ThinkingConfig) string {
	t.Helper()
	msgs := []UnifiedMessage{{Role: "user", Content: "hi"}}
	got := BuildKiroPayload(msgs, "system prompt", modelID, nil, "id", "", cfg)
	state := got.Payload["conversationState"].(map[string]any)
	current := state["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)
	return current["content"].(string)
}

// withFakeReasoningModels fija la lista de modelos para un test y la
// restaura al terminar, junto con FakeReasoningEnabled.
func withFakeReasoningModels(t *testing.T, ids []string) {
	t.Helper()
	origModels, origEnabled := FakeReasoningModels, FakeReasoningEnabled
	t.Cleanup(func() { FakeReasoningModels, FakeReasoningEnabled = origModels, origEnabled })
	FakeReasoningEnabled = true
	SetFakeReasoningModels(ids)
}

func TestFakeReasoningOnlyForListedModels(t *testing.T) {
	withFakeReasoningModels(t, []string{"claude-sonnet-4-5", "QWEN3-coder-next"})
	enabled := ThinkingConfig{Enabled: true}

	cases := []struct {
		model  string
		inject bool
	}{
		{"claude-sonnet-4.5", true}, // la lista acepta el id con guion y lo normaliza
		{"qwen3-coder-next", true},  // sin distinguir mayúsculas
		{"claude-sonnet-5.5", false},
		{"claude-opus-5", false},
	}
	for _, c := range cases {
		content := buildCurrentContent(t, c.model, enabled)
		hasTags := strings.Contains(content, "<thinking_mode>enabled</thinking_mode>")
		hasAddition := strings.Contains(content, "# Extended Thinking Mode")
		if hasTags != c.inject || hasAddition != c.inject {
			t.Errorf("%s: etiquetas=%v adición=%v, quiero ambas=%v", c.model, hasTags, hasAddition, c.inject)
		}
		if !strings.Contains(content, "system prompt") || !strings.HasSuffix(content, "hi") {
			t.Errorf("%s: se perdió el system prompt o el mensaje: %q", c.model, content)
		}
	}
}

// La adición al system prompt depende solo de que el modelo esté en la
// lista, no de cfg.Enabled: en un modelo de la lista se mantiene el
// comportamiento del original (adición también con thinking desactivado).
func TestFakeReasoningAdditionFollowsListNotRequest(t *testing.T) {
	withFakeReasoningModels(t, []string{"claude-sonnet-4.5"})
	disabled := ThinkingConfig{Enabled: false}

	if c := buildCurrentContent(t, "claude-sonnet-4.5", disabled); !strings.Contains(c, "# Extended Thinking Mode") {
		t.Errorf("modelo listado con thinking desactivado: falta la adición del original")
	}
	if c := buildCurrentContent(t, "claude-opus-5", disabled); strings.Contains(c, "# Extended Thinking Mode") {
		t.Errorf("modelo no listado con thinking desactivado: no debe llevar la adición")
	}
}

func TestFakeReasoningNilListMeansAllModels(t *testing.T) {
	withFakeReasoningModels(t, nil)
	if c := buildCurrentContent(t, "cualquier-modelo", ThinkingConfig{Enabled: true}); !strings.Contains(c, "<thinking_mode>enabled</thinking_mode>") {
		t.Errorf("lista nil debe inyectar en todos los modelos (comportamiento del original)")
	}
}

func TestFakeReasoningGlobalSwitchStillWins(t *testing.T) {
	withFakeReasoningModels(t, []string{"claude-sonnet-4.5"})
	FakeReasoningEnabled = false
	if c := buildCurrentContent(t, "claude-sonnet-4.5", ThinkingConfig{Enabled: true}); strings.Contains(c, "thinking") {
		t.Errorf("FAKE_REASONING=false debe desactivar la inyección aunque el modelo esté listado: %q", c)
	}
}
