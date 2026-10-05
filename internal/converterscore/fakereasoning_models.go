// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelcaps"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelresolver"
)

// FakeReasoningModels limita el fake reasoning a estos model IDs
// (normalizados con modelresolver.NormalizeModelName y en minúsculas). nil
// significa "todos los modelos", el comportamiento del original. Divergencia
// intencional (DIFFERENCES §16): algunos modelos con razonamiento nativo
// (claude-sonnet-5.5, claude-opus-5) cortan la respuesta con
// REASONING_EXTRACTION cuando reciben la inyección, y otros la ignoran. Se
// asigna con SetFakeReasoningModels desde internal/config.
var FakeReasoningModels map[string]struct{}

// SetFakeReasoningModels fija FakeReasoningModels a partir de los ids de
// FAKE_REASONING_MODELS. ids nil restaura "todos los modelos".
func SetFakeReasoningModels(ids []string) {
	if ids == nil {
		FakeReasoningModels = nil
		return
	}
	set := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		if key := fakeReasoningModelKey(id); key != "" {
			set[key] = struct{}{}
		}
	}
	FakeReasoningModels = set
}

// fakeReasoningAppliesTo indica si el payload para modelID lleva la
// inyección de fake reasoning (adición al system prompt y etiquetas en el
// mensaje actual). FakeReasoningEnabled sigue siendo el interruptor global.
func fakeReasoningAppliesTo(modelID string) bool {
	if !FakeReasoningEnabled {
		return false
	}
	// Los modelos con razonamiento nativo usan el oficial (DIFFERENCES §18).
	if _, native := modelcaps.Get(modelID); native {
		return false
	}
	if FakeReasoningModels == nil {
		return true
	}
	_, ok := FakeReasoningModels[fakeReasoningModelKey(modelID)]
	return ok
}

// AddNativeReasoningFields añade a payload los additionalModelRequestFields
// del razonamiento nativo de modelID, si el modelo lo admite y req pide algo
// que su esquema acepta. Los manda el IDE de Kiro en el nivel superior de
// GenerateAssistantResponse (DIFFERENCES §18).
func AddNativeReasoningFields(payload map[string]any, modelID string, req modelcaps.Request) {
	caps, ok := modelcaps.Get(modelID)
	if !ok {
		return
	}
	if fields := modelcaps.RequestFields(caps, req); fields != nil {
		payload["additionalModelRequestFields"] = fields
	}
}

func fakeReasoningModelKey(id string) string {
	return strings.ToLower(modelresolver.NormalizeModelName(strings.TrimSpace(id)))
}
