// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/auth"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// TestGetAllAvailableModels returns union of models from all enabled accounts
func TestGetAllAvailableModels(t *testing.T) {
	cfg := &config.Config{
		AccountCacheTTL: 3600,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	m.mu.Lock()
	m.accounts = append(m.accounts,
		&Account{
			ID:      "acc1",
			Enabled: true,
			Models: ModelAccountList{
				Models: []string{"a", "b", "c"},
			},
		},
		&Account{
			ID:      "acc2",
			Enabled: true,
			Models: ModelAccountList{
				Models: []string{"b", "c", "d"},
			},
		},
		&Account{
			ID:      "acc3",
			Enabled: false,
			Models: ModelAccountList{
				Models: []string{"e", "f"},
			},
		},
	)
	m.mu.Unlock()

	models := m.GetAllAvailableModels()

	// Should be sorted union (excluding disabled acc3)
	expected := []string{"a", "b", "c", "d"}
	if len(models) != len(expected) {
		t.Errorf("Expected %d models, got %d: %v", len(expected), len(models), models)
	}
	for i, model := range models {
		if model != expected[i] {
			t.Errorf("Model mismatch at %d: expected %s, got %s", i, expected[i], model)
		}
	}
}

// TestGetAllAvailableModels_SkipsDisabled tests that disabled accounts are excluded
func TestGetAllAvailableModels_SkipsDisabled(t *testing.T) {
	cfg := &config.Config{
		AccountCacheTTL: 3600,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	m.mu.Lock()
	m.accounts = append(m.accounts,
		&Account{
			ID:      "enabled",
			Enabled: true,
			Models: ModelAccountList{
				Models: []string{"a", "b"},
			},
		},
		&Account{
			ID:      "disabled",
			Enabled: false,
			Models: ModelAccountList{
				Models: []string{"c", "d"},
			},
		},
	)
	m.mu.Unlock()

	models := m.GetAllAvailableModels()

	// Should only have models from enabled account
	expected := []string{"a", "b"}
	if len(models) != len(expected) {
		t.Errorf("Expected %d models, got %d", len(expected), len(models))
	}
	for i, model := range models {
		if model != expected[i] {
			t.Errorf("Model mismatch at %d: expected %s, got %s", i, expected[i], model)
		}
	}
}

// TestGetFirstAccount returns first account
func TestGetFirstAccount(t *testing.T) {
	cfg := &config.Config{
		AccountCacheTTL: 3600,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	m.mu.Lock()
	m.accounts = append(m.accounts,
		&Account{ID: "first"},
		&Account{ID: "second"},
	)
	m.mu.Unlock()

	acc := m.GetFirstAccount()
	if acc == nil {
		t.Fatalf("GetFirstAccount returned nil")
	}
	if acc.ID != "first" {
		t.Errorf("Expected first account, got %s", acc.ID)
	}
}

// TestGetFirstAccount_ReturnsNilEmpty returns nil when no accounts
func TestGetFirstAccount_ReturnsNilEmpty(t *testing.T) {
	cfg := &config.Config{
		AccountCacheTTL: 3600,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	acc := m.GetFirstAccount()
	if acc != nil {
		t.Errorf("GetFirstAccount should return nil for empty accounts, got %v", acc)
	}
}

// TestRefreshAccountModels_NotExpiredNoOp tests that non-expired TTL doesn't refresh
func TestRefreshAccountModels_NotExpiredNoOp(t *testing.T) {
	cfg := &config.Config{
		KiroRegion:      "us-east-1",
		RefreshToken:    "test-token",
		AccountCacheTTL: 3600,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	authMgr, err := auth.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager auth failed: %v", err)
	}

	initialLoadedAt := time.Now().Add(-10 * time.Minute)
	m.mu.Lock()
	m.accounts = append(m.accounts,
		&Account{
			ID:      "account1",
			Enabled: true,
			Auth:    authMgr,
			Models: ModelAccountList{
				Models:   []string{"a", "b"},
				LoadedAt: initialLoadedAt,
				TTL:      1 * time.Hour,
			},
		},
	)
	m.mu.Unlock()

	err = m.refreshAccountModels(context.Background(), "account1")
	if err != nil {
		t.Fatalf("refreshAccountModels failed: %v", err)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// LoadedAt should not change
	if m.accounts[0].Models.LoadedAt != initialLoadedAt {
		t.Errorf("LoadedAt changed unexpectedly")
	}
}

// TestFallbackModels verifies the static fallback models list
func TestFallbackModels(t *testing.T) {
	expected := []string{
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

	if len(fallbackModels) != len(expected) {
		t.Errorf("Fallback models length mismatch: expected %d, got %d", len(expected), len(fallbackModels))
	}

	for i, model := range fallbackModels {
		if model != expected[i] {
			t.Errorf("Fallback model mismatch at %d: expected %s, got %s", i, expected[i], model)
		}
	}
}

// TestRefreshAccountModels_RuntimeEndpoint verifies runtime endpoints use static models
func TestRefreshAccountModels_RuntimeEndpoint(t *testing.T) {
	cfg := &config.Config{
		KiroRegion:      "us-east-1",
		RefreshToken:    "test-token",
		AccountCacheTTL: 3600,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	authMgr, err := auth.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager auth failed: %v", err)
	}

	m.mu.Lock()
	m.accounts = append(m.accounts,
		&Account{
			ID:      "runtime-account",
			Enabled: true,
			Auth:    authMgr,
			Models:  ModelAccountList{},
		},
	)
	m.mu.Unlock()

	// Call refreshAccountModels - should detect runtime endpoint and use static models
	err = m.refreshAccountModels(context.Background(), "runtime-account")
	if err != nil {
		t.Fatalf("refreshAccountModels failed: %v", err)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	// Should have fallback models (runtime endpoint doesn't make HTTP calls)
	if len(m.accounts[0].Models.Models) != len(fallbackModels) {
		t.Errorf("Expected %d models, got %d", len(fallbackModels), len(m.accounts[0].Models.Models))
	}
	if m.accounts[0].Models.LoadedAt.IsZero() {
		t.Errorf("LoadedAt should not be zero")
	}
	if m.accounts[0].Models.TTL == 0 {
		t.Errorf("TTL should not be zero")
	}
}
