// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package convertersanthropic

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelcaps"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
)

func registerSonnet55(t *testing.T) {
	t.Helper()
	t.Cleanup(modelcaps.Reset)
	modelcaps.Reset()
	modelcaps.Set("claude-sonnet-5.5", modelcaps.Caps{
		ThinkingTypes: []string{"adaptive"},
		EffortPath:    "output_config",
		EffortLevels:  []string{"low", "medium", "high", "xhigh", "max"},
	})
}

func toKiro(t *testing.T, body string) map[string]any {
	t.Helper()
	var req modelsanthropic.AnthropicMessagesRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	return AnthropicToKiro(&req, "conv", "").Payload
}

func currentContent(payload map[string]any) string {
	cs := payload["conversationState"].(map[string]any)
	return cs["currentMessage"].(map[string]any)["userInputMessage"].(map[string]any)["content"].(string)
}

// Con un modelo de razonamiento nativo, thinking y output_config.effort de
// Claude Code van a additionalModelRequestFields y no hay fake reasoning
// (DIFFERENCES §18).
func TestAnthropicToKiroNativeThinking(t *testing.T) {
	registerSonnet55(t)
	payload := toKiro(t, `{"model":"claude-sonnet-5-5","max_tokens":100,
		"thinking":{"type":"adaptive"},"output_config":{"effort":"xhigh"},
		"messages":[{"role":"user","content":"hola"}]}`)

	want := map[string]any{
		"thinking":      map[string]any{"type": "adaptive"},
		"output_config": map[string]any{"effort": "xhigh"},
	}
	if got := payload["additionalModelRequestFields"]; !reflect.DeepEqual(got, want) {
		t.Errorf("additionalModelRequestFields = %#v; quiero %#v", got, want)
	}
	if c := currentContent(payload); strings.Contains(c, "thinking") {
		t.Errorf("un modelo nativo no debe llevar fake reasoning: %q", c)
	}
}

func TestAnthropicToKiroWithoutCapsKeepsPayload(t *testing.T) {
	registerSonnet55(t)
	payload := toKiro(t, `{"model":"claude-sonnet-4-5","max_tokens":100,
		"thinking":{"type":"enabled","budget_tokens":2000},
		"messages":[{"role":"user","content":"hola"}]}`)

	if _, ok := payload["additionalModelRequestFields"]; ok {
		t.Errorf("un modelo sin capacidades nativas no debe llevar additionalModelRequestFields")
	}
}

func historyReasoning(payload map[string]any) []any {
	cs := payload["conversationState"].(map[string]any)
	var out []any
	for _, h := range cs["history"].([]map[string]any) {
		if a, ok := h["assistantResponseMessage"].(map[string]any); ok {
			out = append(out, a["reasoningContent"])
		}
	}
	return out
}

const toolsJSON = `"tools":[{"name":"Read","description":"lee","input_schema":{"type":"object"}}]`

// El razonamiento firmado del turno actual (desde el último mensaje del
// usuario que no es un tool_result) vuelve a Kiro en reasoningContent, como
// hace el IDE; los turnos anteriores y las firmas inventadas del fake
// reasoning no (DIFFERENCES §18).
func TestAnthropicToKiroHistoryReasoning(t *testing.T) {
	registerSonnet55(t)
	payload := toKiro(t, `{"model":"claude-sonnet-5.5","max_tokens":100,`+toolsJSON+`,"messages":[
		{"role":"user","content":"a"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"viejo","signature":"S0"},{"type":"text","text":"ok"}]},
		{"role":"user","content":"b"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"pienso","signature":"S1"},{"type":"tool_use","id":"t1","name":"Read","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"x"},{"type":"text","text":"<system-reminder>"}]},
		{"role":"assistant","content":[{"type":"redacted_thinking","data":"AAEC"},{"type":"tool_use","id":"t2","name":"Read","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t2","content":"y"}]}]}`)

	got := historyReasoning(payload)
	want := []any{
		nil,
		map[string]any{"reasoningText": map[string]any{"text": "pienso", "signature": "S1"}},
		map[string]any{"redactedContent": "AAEC"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("reasoningContent por turno = %#v\nquiero %#v", got, want)
	}
}

func TestAnthropicToKiroHistoryReasoningSkipsFakeAndNonNative(t *testing.T) {
	registerSonnet55(t)
	msgs := `"messages":[
		{"role":"user","content":"a"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"x","signature":"sig_0123456789abcdef"},{"type":"tool_use","id":"t1","name":"Read","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"x"}]}]}`
	for _, model := range []string{"claude-sonnet-5.5", "claude-sonnet-4.5"} {
		got := historyReasoning(toKiro(t, `{"model":"`+model+`","max_tokens":100,`+toolsJSON+`,`+msgs))
		if !reflect.DeepEqual(got, []any{nil}) {
			t.Errorf("%s: reasoningContent = %#v, quiero ninguno", model, got)
		}
	}
	// Sin capacidades nativas tampoco se manda una firma real.
	got := historyReasoning(toKiro(t, `{"model":"claude-sonnet-4.5","max_tokens":100,`+toolsJSON+`,"messages":[
		{"role":"user","content":"a"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"x","signature":"REAL"},{"type":"tool_use","id":"t1","name":"Read","input":{}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"x"}]}]}`))
	if !reflect.DeepEqual(got, []any{nil}) {
		t.Errorf("modelo sin thinking nativo: reasoningContent = %#v, quiero ninguno", got)
	}
}
