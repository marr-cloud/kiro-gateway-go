// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// TestLoadModelsOverride cubre los tres casos: fichero ausente (nil), válido
// (lista) y malformado (error). El path vacío también es nil.
func TestLoadModelsOverride(t *testing.T) {
	if got, err := loadModelsOverride(""); got != nil || err != nil {
		t.Errorf("path vacío: got %v, %v; want nil, nil", got, err)
	}

	absent := filepath.Join(t.TempDir(), "nope.json")
	if got, err := loadModelsOverride(absent); got != nil || err != nil {
		t.Errorf("ausente: got %v, %v; want nil, nil", got, err)
	}

	valid := filepath.Join(t.TempDir(), "models.json")
	if err := os.WriteFile(valid, []byte(`["claude-sonnet-5","gpt-5.6-sol"]`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := loadModelsOverride(valid)
	if err != nil {
		t.Fatalf("válido: %v", err)
	}
	if len(got) != 2 || got[0] != "claude-sonnet-5" || got[1] != "gpt-5.6-sol" {
		t.Errorf("válido: got %v", got)
	}

	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{not an array`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadModelsOverride(bad); err == nil {
		t.Error("malformado: se esperaba error, got nil")
	}
}

// TestModelsOverrideShortCircuits verifica que, con un override cargado,
// refreshAccountModels usa esa lista (no la estática fallbackModels) sin tocar
// el endpoint ni la auth de la cuenta.
func TestModelsOverrideShortCircuits(t *testing.T) {
	cfg := &config.Config{AccountCacheTTL: 3600}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	m.modelsOverride = []string{"claude-sonnet-5", "gpt-5.6-sol"}

	m.mu.Lock()
	m.accounts = append(m.accounts, &Account{ID: "acc1", Enabled: true})
	m.mu.Unlock()

	if err := m.refreshAccountModels(context.Background(), "acc1"); err != nil {
		t.Fatalf("refreshAccountModels: %v", err)
	}

	got := m.GetAllAvailableModels()
	// GetAllAvailableModels ordena: "claude-sonnet-5" < "gpt-5.6-sol".
	want := []string{"claude-sonnet-5", "gpt-5.6-sol"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("índice %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// No debe filtrarse ningún modelo de la lista estática.
	for _, mdl := range got {
		if mdl == "qwen3-coder-next" {
			t.Error("se filtró un modelo de fallbackModels; el override debía ser autoritativo")
		}
	}
}
