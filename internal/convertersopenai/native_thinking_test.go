// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package convertersopenai

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelcaps"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
)

// reasoning_effort de OpenAI va al campo de esfuerzo del modelo y activa el
// thinking adaptive (Kiro lo deja apagado por defecto); "none" lo desactiva
// (DIFFERENCES §18).
func TestBuildKiroPayloadReasoningEffort(t *testing.T) {
	t.Cleanup(modelcaps.Reset)
	modelcaps.Reset()
	modelcaps.Set("gpt-5.6-sol", modelcaps.Caps{EffortPath: "reasoning", EffortLevels: []string{"none", "low", "medium", "high"}})
	modelcaps.Set("claude-opus-4.8", modelcaps.Caps{ThinkingTypes: []string{"adaptive", "disabled"}, EffortPath: "output_config", EffortLevels: []string{"low", "medium", "high"}})

	type obj = map[string]any
	cases := []struct {
		model, effort string
		want          any
	}{
		{"gpt-5.6-sol", "medium", obj{"reasoning": obj{"effort": "medium"}}},
		{"gpt-5.6-sol", "none", obj{"reasoning": obj{"effort": "none"}}},
		{"claude-opus-4.8", "high", obj{"thinking": obj{"type": "adaptive"}, "output_config": obj{"effort": "high"}}},
		{"claude-opus-4.8", "none", obj{"thinking": obj{"type": "disabled"}}},
		{"claude-opus-4.8", "", nil},
	}
	for _, c := range cases {
		body := `{"model":"` + c.model + `","messages":[{"role":"user","content":"hola"}]`
		if c.effort != "" {
			body += `,"reasoning_effort":"` + c.effort + `"`
		}
		var req modelsopenai.ChatCompletionRequest
		if err := json.Unmarshal([]byte(body+"}"), &req); err != nil {
			t.Fatal(err)
		}
		got, ok := BuildKiroPayload(&req, "conv", "").Payload["additionalModelRequestFields"]
		if !ok {
			got = nil
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s effort=%q: %#v; quiero %#v", c.model, c.effort, got, c.want)
		}
	}
}
