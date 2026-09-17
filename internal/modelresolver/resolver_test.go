// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package modelresolver

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/cache"
	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// ==================================================================================================
// NormalizeModelName — corpus (testdata/model_resolver/normalize_model_name, 55 casos)
// ==================================================================================================

func TestNormalizeModelNameAgainstCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "model_resolver/normalize_model_name")
	if len(cases) != 40 {
		t.Fatalf("se esperaban 40 casos de normalize_model_name, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var input string
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &input); err != nil {
				t.Fatalf("case %s: decode input: %v", c.Name, err)
			}
			var want string
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decode want: %v", c.Name, err)
			}
			got := NormalizeModelName(input)
			if got != want {
				t.Fatalf("case %s: NormalizeModelName(%q) = %q, want %q", c.Name, input, got, want)
			}
		})
	}
}

// ==================================================================================================
// ToRuntimeModelID — corpus (testdata/model_resolver/to_runtime_model_id, 15 casos)
// ==================================================================================================

func TestToRuntimeModelIDAgainstCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "model_resolver/to_runtime_model_id")
	if len(cases) != 15 {
		t.Fatalf("se esperaban 15 casos de to_runtime_model_id, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var input string
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &input); err != nil {
				t.Fatalf("case %s: decode input: %v", c.Name, err)
			}
			var want string
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decode want: %v", c.Name, err)
			}
			got := ToRuntimeModelID(input)
			if got != want {
				t.Fatalf("case %s: ToRuntimeModelID(%q) = %q, want %q", c.Name, input, got, want)
			}
		})
	}
}

// ==================================================================================================
// ExtractModelFamily — corpus (testdata/model_resolver/extract_model_family, 14 casos)
//
// La salida grabada es un str o `null` (Optional[str] en Python); el port devuelve
// (string, bool) al estilo Go, así que null se traduce a ok=false.
// ==================================================================================================

func TestExtractModelFamilyAgainstCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "model_resolver/extract_model_family")
	if len(cases) != 14 {
		t.Fatalf("se esperaban 14 casos de extract_model_family, hay %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var input string
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &input); err != nil {
				t.Fatalf("case %s: decode input: %v", c.Name, err)
			}
			var want *string
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decode want: %v", c.Name, err)
			}
			gotFamily, gotOK := ExtractModelFamily(input)
			if want == nil {
				if gotOK {
					t.Fatalf("case %s: ExtractModelFamily(%q) = (%q, true), want (_, false)", c.Name, input, gotFamily)
				}
				return
			}
			if !gotOK || gotFamily != *want {
				t.Fatalf("case %s: ExtractModelFamily(%q) = (%q, %v), want (%q, true)", c.Name, input, gotFamily, gotOK, *want)
			}
		})
	}
}

// ==================================================================================================
// GetModelIDForKiro — hand-written, derivados de los ejemplos del docstring
// original (.upstream/kiro/model_resolver.py:209-215).
// ==================================================================================================

func TestGetModelIDForKiroNormalizesWhenNotHidden(t *testing.T) {
	got := GetModelIDForKiro("claude-haiku-4-5-20251001", map[string]string{})
	want := "claude-haiku-4.5"
	if got != want {
		t.Fatalf("GetModelIDForKiro() = %q, want %q", got, want)
	}
}

func TestGetModelIDForKiroResolvesHiddenModelFromDotFormat(t *testing.T) {
	hidden := map[string]string{"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0"}
	got := GetModelIDForKiro("claude-3.7-sonnet", hidden)
	want := "CLAUDE_3_7_SONNET_20250219_V1_0"
	if got != want {
		t.Fatalf("GetModelIDForKiro() = %q, want %q", got, want)
	}
}

// El cliente puede mandar el formato legacy con guiones (claude-3-7-sonnet);
// get_model_id_for_kiro normaliza ANTES de mirar hidden_models, así que debe
// resolver al mismo id interno que la forma con puntos.
func TestGetModelIDForKiroNormalizesLegacyDashFormatBeforeHiddenLookup(t *testing.T) {
	hidden := map[string]string{"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0"}
	got := GetModelIDForKiro("claude-3-7-sonnet", hidden)
	want := "CLAUDE_3_7_SONNET_20250219_V1_0"
	if got != want {
		t.Fatalf("GetModelIDForKiro() = %q, want %q", got, want)
	}
}

// ==================================================================================================
// ModelResolver.Resolve — cuatro capas (model_resolver.py:301-368), hand-written:
// sin corpus grabado para resolve/get_available_models.
// ==================================================================================================

func TestResolveAliasThenCacheHit(t *testing.T) {
	c := cache.New(3600)
	c.Update([]map[string]any{{"modelId": "auto"}})
	r := NewModelResolver(c, nil, map[string]string{"auto-kiro": "auto"}, nil)

	got := r.Resolve("auto-kiro")

	want := ModelResolution{RuntimeModelID: "auto", Normalized: "auto", Source: "cache", IsVerified: true}
	if got != want {
		t.Fatalf("Resolve(%q) = %+v, want %+v", "auto-kiro", got, want)
	}
}

func TestResolveNormalizesThenCacheHit(t *testing.T) {
	c := cache.New(3600)
	c.Update([]map[string]any{{"modelId": "claude-sonnet-4.5"}})
	r := NewModelResolver(c, nil, nil, nil)

	got := r.Resolve("claude-sonnet-4-5-20250929")

	want := ModelResolution{RuntimeModelID: "claude-sonnet-4.5", Normalized: "claude-sonnet-4.5", Source: "cache", IsVerified: true}
	if got != want {
		t.Fatalf("Resolve() = %+v, want %+v", got, want)
	}
}

func TestResolveFallsBackToHiddenModel(t *testing.T) {
	c := cache.New(3600) // vacío: nunca hay hit en cache
	hidden := map[string]string{"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0"}
	r := NewModelResolver(c, hidden, nil, nil)

	got := r.Resolve("claude-3-7-sonnet")

	want := ModelResolution{
		RuntimeModelID: "CLAUDE_3_7_SONNET_20250219_V1_0",
		Normalized:     "claude-3.7-sonnet",
		Source:         "hidden",
		IsVerified:     true,
	}
	if got != want {
		t.Fatalf("Resolve() = %+v, want %+v", got, want)
	}
}

func TestResolveUnknownModelPassesThroughUnverified(t *testing.T) {
	c := cache.New(3600)
	r := NewModelResolver(c, nil, nil, nil)

	got := r.Resolve("gpt-5-turbo")

	want := ModelResolution{
		RuntimeModelID: "gpt-5-turbo",
		Normalized:     "gpt-5-turbo",
		Source:         "passthrough",
		IsVerified:     false,
	}
	if got != want {
		t.Fatalf("Resolve() = %+v, want %+v", got, want)
	}
}

// ==================================================================================================
// ModelResolver.GetAvailableModels (model_resolver.py:370-397), hand-written.
// ==================================================================================================

func TestGetAvailableModelsFiltersHiddenFromListButKeepsAlias(t *testing.T) {
	c := cache.New(3600)
	c.Update([]map[string]any{{"modelId": "auto"}, {"modelId": "claude-sonnet-4.5"}})
	r := NewModelResolver(c, nil, map[string]string{"auto-kiro": "auto"}, []string{"auto"})

	got := r.GetAvailableModels()

	want := []string{"auto-kiro", "claude-sonnet-4.5"}
	if len(got) != len(want) {
		t.Fatalf("GetAvailableModels() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("GetAvailableModels() = %v, want %v", got, want)
		}
	}
}

func TestGetAvailableModelsIncludesHiddenModelDisplayNames(t *testing.T) {
	c := cache.New(3600)
	c.Update([]map[string]any{{"modelId": "claude-sonnet-4.5"}})
	r := NewModelResolver(c, map[string]string{"claude-3.7-sonnet": "CLAUDE_3_7_SONNET_20250219_V1_0"}, nil, nil)

	got := r.GetAvailableModels()

	want := []string{"claude-3.7-sonnet", "claude-sonnet-4.5"}
	if len(got) != len(want) {
		t.Fatalf("GetAvailableModels() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("GetAvailableModels() = %v, want %v", got, want)
		}
	}
}

// ==================================================================================================
// Catálogo estático (§6.12) — FallbackModels/Aliases/HiddenFromList
// ==================================================================================================

func TestFallbackModelsCatalogMatchesConfigPy(t *testing.T) {
	want := []string{
		"auto",
		"claude-sonnet-4",
		"claude-sonnet-4.5",
		"claude-sonnet-4.6",
		"claude-haiku-4.5",
		"claude-opus-4.5",
		"claude-opus-4.6",
		"claude-opus-4.7",
		"deepseek-3.2",
		"glm-5",
		"minimax-m2.1",
		"minimax-m2.5",
		"qwen3-coder-next",
	}
	if len(FallbackModels) != len(want) {
		t.Fatalf("FallbackModels tiene %d elementos, quiero %d", len(FallbackModels), len(want))
	}
	for i := range want {
		if FallbackModels[i] != want[i] {
			t.Fatalf("FallbackModels[%d] = %q, quiero %q", i, FallbackModels[i], want[i])
		}
	}
}

func TestAliasesCatalogHasAutoKiroDefault(t *testing.T) {
	if len(Aliases) != 1 || Aliases["auto-kiro"] != "auto" {
		t.Fatalf("Aliases = %v, quiero {\"auto-kiro\":\"auto\"}", Aliases)
	}
}

func TestHiddenFromListCatalogHidesAuto(t *testing.T) {
	if len(HiddenFromList) != 1 || HiddenFromList[0] != "auto" {
		t.Fatalf("HiddenFromList = %v, quiero [\"auto\"]", HiddenFromList)
	}
}
