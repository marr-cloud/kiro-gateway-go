// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/auth"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

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

// TestInitialize_ParallelismCapped tests that Initialize respects concurrency limit
func TestInitialize_ParallelismCapped(t *testing.T) {
	cfg := &config.Config{
		KiroRegion:      "us-east-1",
		RefreshToken:    "test-token",
		AccountCacheTTL: 3600,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	const numAccounts = 20
	var running int32
	var maxRunning int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cur := atomic.AddInt32(&running, 1)
		defer atomic.AddInt32(&running, -1)

		// Track maximum concurrency
		for {
			m := atomic.LoadInt32(&maxRunning)
			if cur > m && atomic.CompareAndSwapInt32(&maxRunning, m, cur) {
				break
			}
			if cur <= m {
				break
			}
		}

		time.Sleep(50 * time.Millisecond) // Hold long enough to overlap goroutines

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"models": []string{"model"},
		})
	}))
	defer server.Close()

	// Create 20 accounts
	m.mu.Lock()
	for i := 0; i < numAccounts; i++ {
		authMgr, err := auth.NewManager(cfg)
		if err != nil {
			t.Fatalf("NewManager auth failed: %v", err)
		}

		m.accounts = append(m.accounts,
			&Account{
				ID:      "acc",
				Enabled: true,
				Auth:    authMgr,
				Models: ModelAccountList{
					LoadedAt: time.Time{},   // Zero time triggers refresh check
					TTL:      1 * time.Hour, // Non-zero TTL for proper expiry check
				},
			},
		)
	}
	m.isRuntimeEndpointOverride = func(_ string) bool { return false }
	m.listURLOverride = func(_ string) string { return server.URL + "/ListAvailableModels" }
	m.mu.Unlock()

	// Initialize all accounts in parallel
	err = m.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Verify concurrency was capped
	maxConcur := atomic.LoadInt32(&maxRunning)
	if maxConcur > int32(initConcurrency) {
		t.Errorf("Max concurrent goroutines (%d) exceeded limit (%d)", maxConcur, initConcurrency)
	}
}

// TestRefreshAccountModels_OldEndpointDynamicFetch verifies the old-endpoint
// path: force isRuntimeEndpoint=false, point at an httptest server, and check
// that refreshAccountModels parses the response, updates the account, and sent
// the required upstream query parameters (origin=AI_EDITOR + profileArn).
func TestRefreshAccountModels_OldEndpointDynamicFetch(t *testing.T) {
	var hits int32
	var gotOrigin, gotProfileArn, gotMethod string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		gotOrigin = r.URL.Query().Get("origin")
		gotProfileArn = r.URL.Query().Get("profileArn")
		gotMethod = r.Method
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":["A","B","C"]}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		KiroRegion:      "us-east-1",
		AccountCacheTTL: 3600,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	authMgr := newAuthManagerWithFreshToken(t, "tok-old-endpoint")
	m.mu.Lock()
	m.accounts = append(m.accounts, &Account{
		ID:         "acc-old",
		Enabled:    true,
		Auth:       authMgr,
		ProfileARN: "arn:aws:codewhisperer:us-east-1:123456789012:profile/test",
		Models:     ModelAccountList{},
	})
	m.isRuntimeEndpointOverride = func(_ string) bool { return false }
	m.listURLOverride = func(_ string) string { return server.URL + "/ListAvailableModels" }
	m.mu.Unlock()

	if err := m.refreshAccountModels(context.Background(), "acc-old"); err != nil {
		t.Fatalf("refreshAccountModels: %v", err)
	}

	if got := atomic.LoadInt32(&hits); got != 1 {
		t.Errorf("expected 1 hit, got %d", got)
	}
	if gotMethod != "GET" {
		t.Errorf("expected GET, got %s", gotMethod)
	}
	if gotOrigin != "AI_EDITOR" {
		t.Errorf("expected origin=AI_EDITOR, got %q", gotOrigin)
	}
	if gotProfileArn != "arn:aws:codewhisperer:us-east-1:123456789012:profile/test" {
		t.Errorf("expected profileArn set for KIRO_DESKTOP account, got %q", gotProfileArn)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	got := m.accounts[0].Models.Models
	want := []string{"A", "B", "C"}
	if len(got) != len(want) {
		t.Fatalf("models: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("model[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
	if m.accounts[0].Models.LoadedAt.IsZero() {
		t.Errorf("LoadedAt should be non-zero after successful fetch")
	}
}

// TestRefreshAccountModels_OldEndpointFallbackOn5xx verifies that when the
// old-endpoint HTTP fetch fails (5xx across all retries) the account silently
// falls back to fallbackModels — matching upstream account_manager.py:534-541.
func TestRefreshAccountModels_OldEndpointFallbackOn5xx(t *testing.T) {
	var hits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &config.Config{
		KiroRegion:      "us-east-1",
		AccountCacheTTL: 3600,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	authMgr := newAuthManagerWithFreshToken(t, "tok-5xx")
	m.mu.Lock()
	m.accounts = append(m.accounts, &Account{
		ID:      "acc-5xx",
		Enabled: true,
		Auth:    authMgr,
		Models:  ModelAccountList{},
	})
	m.isRuntimeEndpointOverride = func(_ string) bool { return false }
	m.listURLOverride = func(_ string) string { return server.URL + "/ListAvailableModels" }
	m.mu.Unlock()

	if err := m.refreshAccountModels(context.Background(), "acc-5xx"); err != nil {
		t.Fatalf("refreshAccountModels should not propagate 5xx errors, got: %v", err)
	}

	got := atomic.LoadInt32(&hits)
	if got < 2 {
		t.Errorf("expected retries to fire (>=2 hits), got %d — RequestWithRetry may be bypassed", got)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	models := m.accounts[0].Models.Models
	if len(models) != len(fallbackModels) {
		t.Fatalf("expected fallback list of %d models after 5xx exhaustion, got %d", len(fallbackModels), len(models))
	}
	if models[0] != fallbackModels[0] || models[len(models)-1] != fallbackModels[len(fallbackModels)-1] {
		t.Errorf("fallback list content mismatch: got first=%q last=%q, want first=%q last=%q",
			models[0], models[len(models)-1], fallbackModels[0], fallbackModels[len(fallbackModels)-1])
	}
	if m.accounts[0].Models.LoadedAt.IsZero() {
		t.Errorf("LoadedAt should be set even on fallback")
	}
}

// TestInitialize_ExpiredTTLRefreshes proves that refreshAccountModels fires
// when the account's Models.LoadedAt+TTL has passed, and successfully updates
// the account from the mock server (the correct-path replacement for the test
// deleted in fix round 1).
func TestInitialize_ExpiredTTLRefreshes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":["fresh-model"]}`))
	}))
	defer server.Close()

	cfg := &config.Config{
		KiroRegion:      "us-east-1",
		AccountCacheTTL: 3600,
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	authMgr := newAuthManagerWithFreshToken(t, "tok-ttl")
	staleLoadedAt := time.Now().Add(-2 * time.Hour)
	m.mu.Lock()
	m.accounts = append(m.accounts, &Account{
		ID:      "acc-ttl",
		Enabled: true,
		Auth:    authMgr,
		Models: ModelAccountList{
			Models:   []string{"stale"},
			LoadedAt: staleLoadedAt,
			TTL:      1 * time.Hour,
		},
	})
	m.isRuntimeEndpointOverride = func(_ string) bool { return false }
	m.listURLOverride = func(_ string) string { return server.URL + "/ListAvailableModels" }
	m.mu.Unlock()

	if err := m.refreshAccountModels(context.Background(), "acc-ttl"); err != nil {
		t.Fatalf("refreshAccountModels: %v", err)
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	models := m.accounts[0].Models.Models
	if len(models) != 1 || models[0] != "fresh-model" {
		t.Errorf("expected refreshed list [fresh-model], got %v", models)
	}
	if !m.accounts[0].Models.LoadedAt.After(staleLoadedAt) {
		t.Errorf("LoadedAt should have advanced past staleLoadedAt; got %v vs %v",
			m.accounts[0].Models.LoadedAt, staleLoadedAt)
	}
}
