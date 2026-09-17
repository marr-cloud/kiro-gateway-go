// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package cache

import (
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestGetMaxInputTokensUnknownModelReturnsDefault verifica que un modelo
// desconocido devuelve el default (200000), igual que cache.py:141-142.
func TestGetMaxInputTokensUnknownModelReturnsDefault(t *testing.T) {
	c := New(3600)
	got := c.GetMaxInputTokens("unknown")
	if got != DefaultMaxInputTokens {
		t.Errorf("GetMaxInputTokens(unknown) = %d, want %d", got, DefaultMaxInputTokens)
	}
}

// TestUpdateThenGetMaxInputTokensReturnsCachedLimit verifica que tras Update,
// un modelo con tokenLimits.maxInputTokens devuelve ese valor, no el default.
func TestUpdateThenGetMaxInputTokensReturnsCachedLimit(t *testing.T) {
	c := New(3600)
	c.Update([]map[string]any{
		{
			"modelId":     "claude-sonnet-4",
			"tokenLimits": map[string]any{"maxInputTokens": 500000},
		},
	})

	got := c.GetMaxInputTokens("claude-sonnet-4")
	if got != 500000 {
		t.Errorf("GetMaxInputTokens(claude-sonnet-4) = %d, want 500000", got)
	}
}

// TestGetMaxInputTokensFalsyLimitFallsBackToDefault verifica el chequeo
// "truthy" de Python: maxInputTokens=0 debe caer al default (cache.py:141,
// `.get("maxInputTokens") or DEFAULT_MAX_INPUT_TOKENS`).
func TestGetMaxInputTokensFalsyLimitFallsBackToDefault(t *testing.T) {
	c := New(3600)
	c.Update([]map[string]any{
		{
			"modelId":     "zero-limit-model",
			"tokenLimits": map[string]any{"maxInputTokens": 0},
		},
	})

	got := c.GetMaxInputTokens("zero-limit-model")
	if got != DefaultMaxInputTokens {
		t.Errorf("GetMaxInputTokens(zero-limit-model) = %d, want default %d", got, DefaultMaxInputTokens)
	}
}

// TestGetMaxInputTokensMissingTokenLimitsFallsBackToDefault verifica que un
// modelo sin la clave "tokenLimits" cae al default.
func TestGetMaxInputTokensMissingTokenLimitsFallsBackToDefault(t *testing.T) {
	c := New(3600)
	c.Update([]map[string]any{
		{"modelId": "no-limits-model"},
	})

	got := c.GetMaxInputTokens("no-limits-model")
	if got != DefaultMaxInputTokens {
		t.Errorf("GetMaxInputTokens(no-limits-model) = %d, want default %d", got, DefaultMaxInputTokens)
	}
}

// TestIsValidModelAndGetBeforeAndAfterUpdate verifica que IsValidModel/Get
// reflejan el estado del cache antes y después de Update.
func TestIsValidModelAndGetBeforeAndAfterUpdate(t *testing.T) {
	c := New(3600)

	if c.IsValidModel("claude-sonnet-4") {
		t.Errorf("IsValidModel before Update = true, want false")
	}
	if _, ok := c.Get("claude-sonnet-4"); ok {
		t.Errorf("Get before Update ok = true, want false")
	}
	if !c.IsEmpty() {
		t.Errorf("IsEmpty before Update = false, want true")
	}

	c.Update([]map[string]any{
		{"modelId": "claude-sonnet-4", "modelName": "Claude Sonnet 4"},
	})

	if !c.IsValidModel("claude-sonnet-4") {
		t.Errorf("IsValidModel after Update = false, want true")
	}
	model, ok := c.Get("claude-sonnet-4")
	if !ok {
		t.Fatalf("Get after Update ok = false, want true")
	}
	if model["modelName"] != "Claude Sonnet 4" {
		t.Errorf("Get after Update modelName = %v, want %q", model["modelName"], "Claude Sonnet 4")
	}
	if c.IsEmpty() {
		t.Errorf("IsEmpty after Update = true, want false")
	}
}

// TestUpdateReplacesPreviousContents verifica que Update reemplaza el
// contenido completo del cache (cache.py:77), no lo fusiona.
func TestUpdateReplacesPreviousContents(t *testing.T) {
	c := New(3600)
	c.Update([]map[string]any{{"modelId": "model-a"}})
	c.Update([]map[string]any{{"modelId": "model-b"}})

	if c.IsValidModel("model-a") {
		t.Errorf("IsValidModel(model-a) = true after replacing Update, want false")
	}
	if !c.IsValidModel("model-b") {
		t.Errorf("IsValidModel(model-b) = false after replacing Update, want true")
	}
}

// TestAddHiddenModelMakesModelValidWithDefaultMaxInputTokens verifica la
// forma exacta del dict que cache.py:118-127 construye para modelos ocultos.
func TestAddHiddenModelMakesModelValidWithDefaultMaxInputTokens(t *testing.T) {
	c := New(3600)
	c.AddHiddenModel("claude-3.7-sonnet", "CLAUDE_3_7_SONNET_20250219_V1_0")

	if !c.IsValidModel("claude-3.7-sonnet") {
		t.Errorf("IsValidModel(claude-3.7-sonnet) = false, want true")
	}

	got := c.GetMaxInputTokens("claude-3.7-sonnet")
	if got != DefaultMaxInputTokens {
		t.Errorf("GetMaxInputTokens(claude-3.7-sonnet) = %d, want default %d", got, DefaultMaxInputTokens)
	}

	model, ok := c.Get("claude-3.7-sonnet")
	if !ok {
		t.Fatalf("Get(claude-3.7-sonnet) ok = false, want true")
	}
	if model["modelId"] != "claude-3.7-sonnet" {
		t.Errorf("modelId = %v, want claude-3.7-sonnet", model["modelId"])
	}
	if model["modelName"] != "claude-3.7-sonnet" {
		t.Errorf("modelName = %v, want claude-3.7-sonnet", model["modelName"])
	}
	if model["_internal_id"] != "CLAUDE_3_7_SONNET_20250219_V1_0" {
		t.Errorf("_internal_id = %v, want CLAUDE_3_7_SONNET_20250219_V1_0", model["_internal_id"])
	}
	if model["_is_hidden"] != true {
		t.Errorf("_is_hidden = %v, want true", model["_is_hidden"])
	}
}

// TestAddHiddenModelDoesNotOverwriteExisting verifica que AddHiddenModel es
// un no-op si el modelo ya existe (cache.py:118, `if display_name not in`).
func TestAddHiddenModelDoesNotOverwriteExisting(t *testing.T) {
	c := New(3600)
	c.Update([]map[string]any{
		{"modelId": "claude-3.7-sonnet", "modelName": "real entry"},
	})

	c.AddHiddenModel("claude-3.7-sonnet", "SOME_INTERNAL_ID")

	model, ok := c.Get("claude-3.7-sonnet")
	if !ok {
		t.Fatalf("Get(claude-3.7-sonnet) ok = false, want true")
	}
	if model["modelName"] != "real entry" {
		t.Errorf("AddHiddenModel overwrote existing entry: modelName = %v, want %q", model["modelName"], "real entry")
	}
}

// TestIsStaleBeforeFirstUpdate verifica que un cache nunca actualizado se
// considera stale (cache.py:161-162).
func TestIsStaleBeforeFirstUpdate(t *testing.T) {
	c := New(3600)
	if !c.IsStale() {
		t.Errorf("IsStale before any Update = false, want true")
	}
}

// TestIsStaleAfterUpdateWithinTTL verifica que, justo tras Update, el cache
// no está stale si el TTL configurado no ha transcurrido.
func TestIsStaleAfterUpdateWithinTTL(t *testing.T) {
	c := New(3600)
	c.Update([]map[string]any{{"modelId": "m"}})
	if c.IsStale() {
		t.Errorf("IsStale right after Update (ttl=3600s) = true, want false")
	}
}

// TestIsStaleAfterTTLExpires verifica que, con un TTL muy corto, el cache se
// vuelve stale tras dejarlo transcurrir.
func TestIsStaleAfterTTLExpires(t *testing.T) {
	c := New(0)
	c.Update([]map[string]any{{"modelId": "m"}})
	time.Sleep(5 * time.Millisecond)
	if !c.IsStale() {
		t.Errorf("IsStale with ttl=0 after a short sleep = false, want true")
	}
}

// TestGetAllModelIDsReturnsAllKeys verifica que GetAllModelIDs devuelve
// exactamente los IDs presentes en la cache, incluyendo los añadidos vía
// AddHiddenModel, sin importar el orden (cache.py:165-172; su único
// consumidor, model_resolver.py:386, los envuelve en un set).
func TestGetAllModelIDsReturnsAllKeys(t *testing.T) {
	c := New(3600)
	c.Update([]map[string]any{
		{"modelId": "model-a"},
		{"modelId": "model-b"},
	})
	c.AddHiddenModel("model-c", "INTERNAL_C")

	got := c.GetAllModelIDs()
	want := []string{"model-a", "model-b", "model-c"}

	sort.Strings(got)
	if len(got) != len(want) {
		t.Fatalf("GetAllModelIDs() = %v, want %v (length mismatch)", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("GetAllModelIDs() sorted = %v, want %v", got, want)
			break
		}
	}
}

// TestGetAllModelIDsEmptyCache verifica que una cache vacía devuelve un
// slice vacío, no nil-con-panic ni un slice con basura.
func TestGetAllModelIDsEmptyCache(t *testing.T) {
	c := New(3600)
	got := c.GetAllModelIDs()
	if len(got) != 0 {
		t.Errorf("GetAllModelIDs() on empty cache = %v, want empty", got)
	}
}

// TestConcurrencyTwoPhaseUpdateThenGet ejercita N goroutines llamando Update
// en la fase 1 y N goroutines leyendo en la fase 2, secuencialmente (mismo
// patrón que internal/truncationstate/cache_test.go, ya que este
// repositorio corre los tests sin -race). Todas las goroutines de la fase 1
// actualizan el MISMO modelo con el MISMO límite no-default, para que el
// resultado sea determinista pese a la concurrencia: cualquiera que "gane"
// la carrera deja el cache en el mismo estado observable.
func TestConcurrencyTwoPhaseUpdateThenGet(t *testing.T) {
	c := New(3600)
	const numGoroutines = 10
	const modelID = "shared-model"
	const expectedLimit = 42000

	var wg sync.WaitGroup

	// Fase 1: todas las goroutines actualizan el mismo modelo con el mismo
	// límite no-default.
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.Update([]map[string]any{
				{
					"modelId":     modelID,
					"tokenLimits": map[string]any{"maxInputTokens": expectedLimit},
				},
			})
		}()
	}
	wg.Wait()

	// Fase 2: todas las goroutines leen concurrentemente y deben ver
	// exactamente expectedLimit — nunca el default ni ningún otro valor,
	// ya que la fase 1 ya terminó y todas escribieron el mismo estado.
	var mismatches int32
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !c.IsValidModel(modelID) {
				atomic.AddInt32(&mismatches, 1)
				return
			}
			if got := c.GetMaxInputTokens(modelID); got != expectedLimit {
				atomic.AddInt32(&mismatches, 1)
			}
			if _, ok := c.Get(modelID); !ok {
				atomic.AddInt32(&mismatches, 1)
			}
		}()
	}
	wg.Wait()

	if mismatches != 0 {
		t.Errorf("mismatches = %d, want 0 (every concurrent read should see modelID=%q with limit=%d, not the default %d)", mismatches, modelID, expectedLimit, DefaultMaxInputTokens)
	}
}
