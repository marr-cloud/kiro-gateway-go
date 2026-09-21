// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// --- Token expiration tests ---

func TestIsExpiringSoon(t *testing.T) {
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	t.Run("expires in 601s should not be expiring soon", func(t *testing.T) {
		tokens := Tokens{
			AccessToken: "test-token",
			ExpiresAt:   now.Add(601 * time.Second),
		}
		if tokens.IsExpiringSoon(now, 600*time.Second) {
			t.Error("token expiring in 601s should not be expiring soon")
		}
	})

	t.Run("expires in 599s should be expiring soon", func(t *testing.T) {
		tokens := Tokens{
			AccessToken: "test-token",
			ExpiresAt:   now.Add(599 * time.Second),
		}
		if !tokens.IsExpiringSoon(now, 600*time.Second) {
			t.Error("token expiring in 599s should be expiring soon")
		}
	})

	t.Run("already expired token should be expiring soon", func(t *testing.T) {
		tokens := Tokens{
			AccessToken: "test-token",
			ExpiresAt:   now.Add(-1 * time.Second),
		}
		if !tokens.IsExpiringSoon(now, 600*time.Second) {
			t.Error("already expired token should be expiring soon")
		}
	})

	t.Run("zero expiry time should be expiring soon", func(t *testing.T) {
		tokens := Tokens{
			AccessToken: "test-token",
			ExpiresAt:   time.Time{},
		}
		if !tokens.IsExpiringSoon(now, 600*time.Second) {
			t.Error("zero expiry should be expiring soon")
		}
	})
}

// --- Singleflight coalescing tests ---

func TestRefresh_SingleflightCoalescing(t *testing.T) {
	dir := t.TempDir()
	hitCount := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		resp := oidcRefreshResponse{
			AccessToken:  fmt.Sprintf("new-token-%d", hitCount.Load()),
			RefreshToken: "new-refresh-token",
			ExpiresIn:    nil, // defaults to 3600
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	expiresAt := time.Now().UTC().Add(500 * time.Second) // expiring soon
	path := writeCredsFile(t, dir, map[string]any{
		"accessToken":  "old-token",
		"refreshToken": "rt-123",
		"clientId":     "client-id",
		"clientSecret": "client-secret",
		"expiresAt":    expiresAt.Format(time.RFC3339Nano),
	})

	cfg := loadCfg(t, map[string]string{"KIRO_CREDS_FILE": path})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Override OIDC URL to point to test server
	mgr.oidcURL = func(ssoRegion string) string {
		return server.URL + "/token"
	}

	// Launch 20 goroutines calling Refresh concurrently
	const numGoroutines = 20
	var wg sync.WaitGroup
	results := make(chan string, numGoroutines)
	errs := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := mgr.Refresh(context.Background())
			if err != nil {
				errs <- err
				return
			}
			results <- token
		}()
	}

	wg.Wait()
	close(results)
	close(errs)

	// Check that we got exactly 1 OIDC hit
	if got := hitCount.Load(); got != 1 {
		t.Errorf("expected 1 OIDC hit, got %d", got)
	}

	// Check that all 20 goroutines got the same token
	tokens := make(map[string]bool)
	var errCount int
	for token := range results {
		tokens[token] = true
	}
	for err := range errs {
		if err != nil {
			errCount++
			t.Logf("error from goroutine: %v", err)
		}
	}

	if errCount > 0 {
		t.Errorf("%d goroutines returned errors", errCount)
	}

	if len(tokens) != 1 {
		t.Errorf("expected all goroutines to get the same token; got %d different tokens", len(tokens))
	}

	// Verify that the token was actually updated
	newToken, _ := mgr.AccessToken(context.Background())
	if !strings.HasPrefix(newToken, "new-token-") {
		t.Errorf("AccessToken after refresh should be new-token-*, got %q", newToken)
	}
}

// --- Graceful degradation tests ---

func TestRefresh_GracefulDegradation_FailureButTokenStillValid(t *testing.T) {
	dir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 500 error
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer server.Close()

	// Token expires in 100s (not truly expired, but expiring soon)
	expiresAt := time.Now().UTC().Add(100 * time.Second)
	path := writeCredsFile(t, dir, map[string]any{
		"accessToken":  "old-token-still-valid",
		"refreshToken": "rt-123",
		"clientId":     "client-id",
		"clientSecret": "client-secret",
		"expiresAt":    expiresAt.Format(time.RFC3339Nano),
	})

	cfg := loadCfg(t, map[string]string{"KIRO_CREDS_FILE": path})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	mgr.oidcURL = func(ssoRegion string) string {
		return server.URL + "/token"
	}

	// Refresh should fail but return the old token for JSON mode (non-graceful degradation)
	// Graceful degradation only applies to SQLite+400, so JSON mode should propagate error
	token, err := mgr.Refresh(context.Background())
	if err == nil {
		t.Errorf("Refresh should return an error for non-SQLite mode on server error")
	}
	if token != "" {
		t.Errorf("Refresh should return empty token on error (non-SQLite mode), got %q", token)
	}
}

func TestRefresh_GracefulDegradation_FailureAndTokenExpired(t *testing.T) {
	dir := t.TempDir()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("server error"))
	}))
	defer server.Close()

	// Token is already expired
	expiresAt := time.Now().UTC().Add(-10 * time.Second)
	path := writeCredsFile(t, dir, map[string]any{
		"accessToken":  "old-token-expired",
		"refreshToken": "rt-123",
		"clientId":     "client-id",
		"clientSecret": "client-secret",
		"expiresAt":    expiresAt.Format(time.RFC3339Nano),
	})

	cfg := loadCfg(t, map[string]string{"KIRO_CREDS_FILE": path})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	mgr.oidcURL = func(ssoRegion string) string {
		return server.URL + "/token"
	}

	token, err := mgr.Refresh(context.Background())
	if err == nil {
		t.Error("Refresh should return an error when token is expired and refresh fails")
	}
	if token != "" {
		t.Errorf("Refresh should return empty token, got %q", token)
	}
}

// --- AccessToken gating tests ---

func TestAccessToken_FreshTokenNotRefreshed(t *testing.T) {
	dir := t.TempDir()
	hitCount := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		resp := oidcRefreshResponse{
			AccessToken:  "new-token",
			RefreshToken: "new-refresh-token",
			ExpiresIn:    nil,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Token expires in 700s (not expiring soon, 600s threshold)
	expiresAt := time.Now().UTC().Add(700 * time.Second)
	path := writeCredsFile(t, dir, map[string]any{
		"accessToken":  "fresh-token",
		"refreshToken": "rt-123",
		"clientId":     "client-id",
		"clientSecret": "client-secret",
		"expiresAt":    expiresAt.Format(time.RFC3339Nano),
	})

	cfg := loadCfg(t, map[string]string{"KIRO_CREDS_FILE": path})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	mgr.oidcURL = func(ssoRegion string) string {
		return server.URL + "/token"
	}

	// Call AccessToken twice
	token1, err := mgr.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if token1 != "fresh-token" {
		t.Errorf("AccessToken = %q, want fresh-token", token1)
	}

	token2, err := mgr.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if token2 != "fresh-token" {
		t.Errorf("AccessToken = %q, want fresh-token", token2)
	}

	// Verify no OIDC hits
	if got := hitCount.Load(); got != 0 {
		t.Errorf("expected 0 OIDC hits for fresh token, got %d", got)
	}
}

// --- ForceRefresh tests ---

func TestForceRefresh_BypassesIsExpiringSoonGate(t *testing.T) {
	dir := t.TempDir()
	hitCount := atomic.Int32{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hitCount.Add(1)
		resp := oidcRefreshResponse{
			AccessToken:  "force-refreshed-token",
			RefreshToken: "new-refresh-token",
			ExpiresIn:    nil,
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// Token expires in 700s (not expiring soon, 600s threshold)
	expiresAt := time.Now().UTC().Add(700 * time.Second)
	path := writeCredsFile(t, dir, map[string]any{
		"accessToken":  "fresh-token",
		"refreshToken": "rt-123",
		"clientId":     "client-id",
		"clientSecret": "client-secret",
		"expiresAt":    expiresAt.Format(time.RFC3339Nano),
	})

	cfg := loadCfg(t, map[string]string{"KIRO_CREDS_FILE": path})
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	mgr.oidcURL = func(ssoRegion string) string {
		return server.URL + "/token"
	}

	// ForceRefresh should bypass the gate and hit OIDC even though token is fresh
	token, err := mgr.ForceRefresh(context.Background())
	if err != nil {
		t.Fatalf("ForceRefresh: %v", err)
	}
	if token != "force-refreshed-token" {
		t.Errorf("ForceRefresh = %q, want force-refreshed-token", token)
	}

	// Verify exactly 1 OIDC hit
	if got := hitCount.Load(); got != 1 {
		t.Errorf("expected 1 OIDC hit for ForceRefresh, got %d", got)
	}
}
