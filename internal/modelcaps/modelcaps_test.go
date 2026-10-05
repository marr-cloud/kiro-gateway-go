// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package modelcaps

import (
	"encoding/json"
	"reflect"
	"testing"
)

// Esquemas reales de ListAvailableModels (2026-10-05), recortados.
const (
	schemaSonnet55 = `{"type":"object","properties":{"thinking":{"type":"object","properties":{"type":{"type":"string","enum":["adaptive","between_tools"]},"display":{"type":"string","enum":["summarized","omitted"]}},"required":["type"]},"output_config":{"type":"object","properties":{"effort":{"type":"string","enum":["low","medium","high","xhigh","max"],"default":"high"}}},"max_tokens":{"type":"integer"}}}`
	schemaOpus46   = `{"type":"object","properties":{"thinking":{"type":"object","properties":{"type":{"type":"string","enum":["adaptive","disabled"]}}},"output_config":{"type":"object","properties":{"effort":{"type":"string","enum":["low","medium","high","max"],"default":"high"}}}}}`
	schemaGPT      = `{"type":"object","properties":{"reasoning":{"type":"object","properties":{"effort":{"type":"string","enum":["none","low","medium","high","xhigh","max"],"default":"high"}}}}}`
)

func TestParseSchema(t *testing.T) {
	cases := []struct {
		name   string
		schema string
		want   Caps
		ok     bool
	}{
		{"sonnet-5.5", schemaSonnet55, Caps{ThinkingTypes: []string{"adaptive", "between_tools"}, ThinkingDisplays: []string{"summarized", "omitted"}, EffortPath: "output_config", EffortLevels: []string{"low", "medium", "high", "xhigh", "max"}}, true},
		{"gpt", schemaGPT, Caps{EffortPath: "reasoning", EffortLevels: []string{"none", "low", "medium", "high", "xhigh", "max"}}, true},
		{"sin esquema", ``, Caps{}, false},
		{"esquema sin razonamiento", `{"type":"object","properties":{"max_tokens":{"type":"integer"}}}`, Caps{}, false},
	}
	for _, c := range cases {
		got, ok := ParseSchema(json.RawMessage(c.schema))
		if ok != c.ok || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: ParseSchema = %+v, %v; quiero %+v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

func mustCaps(t *testing.T, schema string) Caps {
	t.Helper()
	c, ok := ParseSchema(json.RawMessage(schema))
	if !ok {
		t.Fatalf("esquema sin capacidades: %s", schema)
	}
	return c
}

func TestRequestFields(t *testing.T) {
	sonnet55, opus46, gpt := mustCaps(t, schemaSonnet55), mustCaps(t, schemaOpus46), mustCaps(t, schemaGPT)
	type obj = map[string]any
	cases := []struct {
		name string
		caps Caps
		req  Request
		want map[string]any
	}{
		{"adaptive + effort pasan tal cual", sonnet55, Request{Thinking: "adaptive", Effort: "xhigh"},
			obj{"thinking": obj{"type": "adaptive"}, "output_config": obj{"effort": "xhigh"}}},
		{"enabled con presupuesto se convierte en adaptive", opus46, Request{Thinking: "enabled"},
			obj{"thinking": obj{"type": "adaptive"}}},
		{"display soportado pasa", sonnet55, Request{Thinking: "adaptive", Display: "summarized"},
			obj{"thinking": obj{"type": "adaptive", "display": "summarized"}}},
		{"display no soportado se omite", opus46, Request{Thinking: "adaptive", Display: "summarized"},
			obj{"thinking": obj{"type": "adaptive"}}},
		{"disabled donde no se puede desactivar: se omite thinking", sonnet55, Request{Thinking: "disabled", Effort: "low"},
			obj{"output_config": obj{"effort": "low"}}},
		{"disabled limita xhigh/max al nivel más alto restante", opus46, Request{Thinking: "disabled", Effort: "max"},
			obj{"thinking": obj{"type": "disabled"}, "output_config": obj{"effort": "high"}}},
		{"effort no soportado se omite", opus46, Request{Effort: "xhigh"}, nil},
		{"sin nada no se manda nada", sonnet55, Request{}, nil},
		{"gpt: effort va en reasoning", gpt, Request{Thinking: "adaptive", Effort: "medium"},
			obj{"reasoning": obj{"effort": "medium"}}},
		{"gpt: disabled es effort none", gpt, Request{Thinking: "disabled"},
			obj{"reasoning": obj{"effort": "none"}}},
	}
	for _, c := range cases {
		got := RequestFields(c.caps, c.req)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: RequestFields = %#v; quiero %#v", c.name, got, c.want)
		}
	}
}

func TestRegistryNormalizesIDs(t *testing.T) {
	t.Cleanup(Reset)
	Reset()
	Set("claude-sonnet-4.6", mustCaps(t, schemaOpus46))

	for _, id := range []string{"claude-sonnet-4.6", "claude-sonnet-4-6", "CLAUDE-SONNET-4-6-20260101"} {
		if _, ok := Get(id); !ok {
			t.Errorf("Get(%q) no encontró el modelo", id)
		}
	}
	if _, ok := Get("claude-sonnet-4.5"); ok {
		t.Errorf("Get(claude-sonnet-4.5) no debería existir")
	}
}

// El respaldo por refusal se busca con el id normalizado, como Get.
func TestRefusalFallback(t *testing.T) {
	t.Cleanup(Reset)
	Reset()
	SetRefusalFallback("claude-sonnet-5.5", "claude-sonnet-5")

	for _, id := range []string{"claude-sonnet-5.5", "claude-sonnet-5-5", "CLAUDE-SONNET-5.5"} {
		if got := RefusalFallback(id); got != "claude-sonnet-5" {
			t.Errorf("RefusalFallback(%q) = %q, quiero claude-sonnet-5", id, got)
		}
	}
	if got := RefusalFallback("claude-haiku-4.5"); got != "" {
		t.Errorf("modelo sin respaldo: %q, quiero vacío", got)
	}
	Reset()
	if got := RefusalFallback("claude-sonnet-5.5"); got != "" {
		t.Errorf("Reset no vació los respaldos: %q", got)
	}
}
