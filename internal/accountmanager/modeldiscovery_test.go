// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelcaps"
)

// fakeManagementServer devuelve las páginas dadas y valida la forma de la
// petición (método, X-Amz-Target, Bearer, origin=KIRO_CLI).
func fakeManagementServer(t *testing.T, pages []map[string]any) *httptest.Server {
	t.Helper()
	i := 0
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("método = %s, quiero POST", r.Method)
		}
		if got := r.Header.Get("X-Amz-Target"); got != "AmazonCodeWhispererService.ListAvailableModels" {
			t.Errorf("X-Amz-Target = %q", got)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("Authorization sin Bearer: %q", r.Header.Get("Authorization"))
		}
		if got := r.URL.Query().Get("origin"); got != "KIRO_CLI" {
			t.Errorf("origin = %q, quiero KIRO_CLI", got)
		}
		page := pages[i]
		if i < len(pages)-1 {
			i++
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	}))
}

func newRuntimeManager(t *testing.T, override func(region string) string) (*Manager, *Account) {
	t.Helper()
	m, err := NewManager(&config.Config{AccountCacheTTL: 3600})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.managementURLOverride = override
	m.isRuntimeEndpointOverride = func(string) bool { return true }
	acc := &Account{
		ID:         "acc1",
		Enabled:    true,
		ProfileARN: "arn:aws:codewhisperer:us-east-1:123456789012:profile/test",
		Auth:       newAuthManagerWithFreshToken(t, "tok-1"),
	}
	m.mu.Lock()
	m.accounts = append(m.accounts, acc)
	m.mu.Unlock()
	return m, acc
}

func TestListModelsFromManagement(t *testing.T) {
	srv := fakeManagementServer(t, []map[string]any{{
		"models":       []map[string]any{{"modelId": "claude-sonnet-5"}, {"modelId": "gpt-5.6-sol"}},
		"defaultModel": map[string]any{"modelId": "auto"},
		"nextToken":    "",
	}})
	defer srv.Close()

	m, acc := newRuntimeManager(t, func(string) string { return srv.URL + "/" })
	ids, err := m.listModelsFromManagement(context.Background(), acc)
	if err != nil {
		t.Fatalf("listModelsFromManagement: %v", err)
	}
	if len(ids) != 2 || ids[0] != "claude-sonnet-5" || ids[1] != "gpt-5.6-sol" {
		t.Errorf("ids = %v", ids)
	}
}

func TestListModelsFromManagementPaginates(t *testing.T) {
	srv := fakeManagementServer(t, []map[string]any{
		{"models": []map[string]any{{"modelId": "a"}}, "nextToken": "PAGE2"},
		{"models": []map[string]any{{"modelId": "b"}}, "nextToken": ""},
	})
	defer srv.Close()

	m, acc := newRuntimeManager(t, func(string) string { return srv.URL + "/" })
	ids, err := m.listModelsFromManagement(context.Background(), acc)
	if err != nil {
		t.Fatalf("listModelsFromManagement: %v", err)
	}
	if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
		t.Errorf("paginación: ids = %v", ids)
	}
}

func TestRuntimeBranchUsesDynamicDiscovery(t *testing.T) {
	srv := fakeManagementServer(t, []map[string]any{{
		"models":    []map[string]any{{"modelId": "claude-opus-5"}, {"modelId": "claude-sonnet-5"}},
		"nextToken": "",
	}})
	defer srv.Close()

	m, _ := newRuntimeManager(t, func(string) string { return srv.URL + "/" })
	if err := m.refreshAccountModels(context.Background(), "acc1"); err != nil {
		t.Fatalf("refreshAccountModels: %v", err)
	}
	got := m.GetAllAvailableModels()
	// GetAllAvailableModels ordena: "claude-opus-5" < "claude-sonnet-5".
	if len(got) != 2 || got[0] != "claude-opus-5" || got[1] != "claude-sonnet-5" {
		t.Errorf("la rama runtime no usó el descubrimiento dinámico: %v", got)
	}
}

func TestRuntimeBranchFallsBackOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	m, _ := newRuntimeManager(t, func(string) string { return srv.URL + "/" })
	if err := m.refreshAccountModels(context.Background(), "acc1"); err != nil {
		t.Fatalf("refreshAccountModels: %v", err)
	}
	got := m.GetAllAvailableModels()
	// En error, cae a la lista estática (que incluye qwen3-coder-next).
	found := false
	for _, s := range got {
		if s == "qwen3-coder-next" {
			found = true
		}
	}
	if !found {
		t.Errorf("no cayó al fallback estático tras el 500: %v", got)
	}
}

// El discovery registra en modelcaps el razonamiento nativo que declara cada
// modelo (DIFFERENCES §18); los modelos sin esquema no se registran.
func TestListModelsFromManagementRegistersCaps(t *testing.T) {
	t.Cleanup(modelcaps.Reset)
	modelcaps.Reset()
	schema := json.RawMessage(`{"properties":{"thinking":{"properties":{"type":{"enum":["adaptive","disabled"]}}},"output_config":{"properties":{"effort":{"enum":["low","high"]}}}}}`)
	srv := fakeManagementServer(t, []map[string]any{{
		"models": []map[string]any{
			{"modelId": "claude-sonnet-5", "additionalModelRequestFieldsSchema": schema},
			{"modelId": "claude-sonnet-4.5"},
		},
	}})
	defer srv.Close()

	m, acc := newRuntimeManager(t, func(string) string { return srv.URL + "/" })
	if _, err := m.listModelsFromManagement(context.Background(), acc); err != nil {
		t.Fatalf("listModelsFromManagement: %v", err)
	}

	caps, ok := modelcaps.Get("claude-sonnet-5")
	if !ok || caps.EffortPath != "output_config" || len(caps.ThinkingTypes) != 2 {
		t.Errorf("caps de claude-sonnet-5 = %+v, %v", caps, ok)
	}
	if _, ok := modelcaps.Get("claude-sonnet-4.5"); ok {
		t.Errorf("claude-sonnet-4.5 no tiene esquema y no debe registrarse")
	}
}

// El discovery registra el respaldo por refusal que declara Kiro
// (refusalFallbackModels), con la forma real de la respuesta.
func TestListModelsFromManagementRegistersRefusalFallback(t *testing.T) {
	t.Cleanup(modelcaps.Reset)
	modelcaps.Reset()
	raw, err := os.ReadFile("testdata/list_available_models.json")
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(raw)
	}))
	defer srv.Close()

	m, acc := newRuntimeManager(t, func(string) string { return srv.URL + "/" })
	ids, err := m.listModelsFromManagement(context.Background(), acc)
	if err != nil {
		t.Fatalf("listModelsFromManagement: %v", err)
	}
	if len(ids) != 3 {
		t.Fatalf("ids = %v, quiero 3", ids)
	}
	for model, want := range map[string]string{
		"claude-sonnet-5.5": "claude-sonnet-5",
		"claude-sonnet-5-5": "claude-sonnet-5", // id en forma de Claude Code
		"claude-opus-5":     "claude-opus-4.8",
		"claude-haiku-4.5":  "",
	} {
		if got := modelcaps.RefusalFallback(model); got != want {
			t.Errorf("RefusalFallback(%q) = %q, quiero %q", model, got, want)
		}
	}
	caps, ok := modelcaps.Get("claude-sonnet-5.5")
	if !ok || len(caps.ThinkingTypes) != 2 || caps.ThinkingTypes[1] != "between_tools" {
		t.Errorf("caps de claude-sonnet-5.5 = %+v, %v", caps, ok)
	}
}
