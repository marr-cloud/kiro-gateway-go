// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// --- Escenario 1: GET /v1/models con 2 cuentas → union ordenada ---

func TestModels_TwoAccountsUnionSorted(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-a", "tok-b"})

	accs := manager.Accounts()
	accs[0].Models.Models = []string{"claude-sonnet-4", "model-z"}
	accs[1].Models.Models = []string{"model-z", "claude-opus-4"}

	h := New(manager, mustHTTPClient(t, cfg), cfg, truncationstate.New())

	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.Models(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var list modelsopenai.ModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}

	if list.Object != "list" {
		t.Errorf("object = %q, want %q", list.Object, "list")
	}

	var ids []string
	for _, m := range list.Data {
		ids = append(ids, m.ID)
		if m.Object != "model" {
			t.Errorf("model %q: object = %q, want %q", m.ID, m.Object, "model")
		}
		if m.OwnedBy != "anthropic" {
			t.Errorf("model %q: owned_by = %q, want %q (routes_openai.py:151)", m.ID, m.OwnedBy, "anthropic")
		}
	}

	// /v1/models pasa por modelresolver.GetAvailableModels (fase 6a Task 3):
	// añade el alias "auto-kiro" (MODEL_ALIASES) y ordena; las cuentas aquí no
	// incluyen "auto", así que HIDDEN_FROM_LIST no quita nada.
	want := []string{"auto-kiro", "claude-opus-4", "claude-sonnet-4", "model-z"}
	if !equalStrings(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

// --- Escenario 1b: /v1/models oculta "auto" (HIDDEN_FROM_LIST) y muestra el alias ---

func TestModels_HidesAutoShowsAlias(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-a"})
	manager.Accounts()[0].Models.Models = []string{"auto", "claude-sonnet-4"}

	h := New(manager, mustHTTPClient(t, cfg), cfg, truncationstate.New())
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	rec := httptest.NewRecorder()
	h.Models(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var list modelsopenai.ModelList
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}

	var ids []string
	for _, m := range list.Data {
		ids = append(ids, m.ID)
	}
	// "auto" está en HIDDEN_FROM_LIST → oculto; "auto-kiro" (alias) → mostrado.
	want := []string{"auto-kiro", "claude-sonnet-4"}
	if !equalStrings(ids, want) {
		t.Errorf("ids = %v, want %v (auto hidden, auto-kiro shown)", ids, want)
	}
}
