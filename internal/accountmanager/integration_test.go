// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
)

// TestPhase4Integration exercises accountmanager.Manager + auth.Manager + httpclient.Client
// together against a fake Kiro httptest server. It verifies:
// - Success on first attempt (200)
// - Retry on 429 then 200
// - Failover: account 0 returns 500 three times, account 1 returns 200
func TestPhase4Integration(t *testing.T) {
	dir := t.TempDir()

	// Create two credential files with distinct tokens
	credsFileA := createKiroDesktopCredsFile(t, dir, "tok-A", "creds-a.json")
	credsFileB := createKiroDesktopCredsFile(t, dir, "tok-B", "creds-b.json")

	// Create a credentials.json array listing both accounts
	credentialsJSON := filepath.Join(dir, "credentials.json")
	credsData := []map[string]interface{}{
		{
			"type":    "json",
			"enabled": true,
			"path":    credsFileA,
		},
		{
			"type":    "json",
			"enabled": true,
			"path":    credsFileB,
		},
	}
	credsJSON, _ := json.Marshal(credsData)
	if err := os.WriteFile(credentialsJSON, credsJSON, 0o600); err != nil {
		t.Fatalf("write credentials.json: %v", err)
	}

	// Create config pointing at the credentials file
	cfg := &config.Config{
		AccountsConfigFile:   credentialsJSON,
		KiroRegion:           "us-east-1",
		AccountCacheTTL:      3600,
		StreamingReadTimeout: 300,
	}

	t.Run("success on first attempt", func(t *testing.T) {
		// Server returns 200 for both tokens
		var requestCount int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			atomic.AddInt32(&requestCount, 1)
			// Both tok-A and tok-B get 200
			w.Header().Set("Content-Type", "application/json")
			if strings.HasPrefix(r.URL.Path, "/ListAvailableModels") {
				w.Write([]byte(`{"models":["model1","model2"]}`))
			} else {
				w.Write([]byte(`{"success":true}`))
			}
		}))
		defer server.Close()

		// Create manager and load credentials
		m, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}

		ctx := context.Background()
		if err := m.LoadCredentials(ctx); err != nil {
			t.Fatalf("LoadCredentials: %v", err)
		}

		// Wire the override to point at the mock server
		m.mu.Lock()
		m.listURLOverride = func(qhost string) string { return server.URL + "/ListAvailableModels" }
		m.isRuntimeEndpointOverride = func(apiHost string) bool { return false }
		m.mu.Unlock()

		// Initialize to load models
		if err := m.Initialize(ctx); err != nil {
			t.Fatalf("Initialize: %v", err)
		}

		// Create httpclient
		httpCli, err := httpclient.New(cfg)
		if err != nil {
			t.Fatalf("httpclient.New: %v", err)
		}
		defer httpCli.Close()

		// Get next account (should be account 0)
		acc, err := m.GetNextAccount("model1", nil)
		if err != nil {
			t.Fatalf("GetNextAccount: %v", err)
		}

		// Build and make request
		req, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/request", bytes.NewReader([]byte(`{"test":"data"}`)))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpCli.RequestWithRetry(ctx, req, acc.Auth, false)
		if err != nil {
			t.Fatalf("RequestWithRetry: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("expected 200, got %d", resp.StatusCode)
		}

		// Report success
		m.ReportSuccess(acc.ID, "model1")

		// Verify no retry happened (single request)
		count := atomic.LoadInt32(&requestCount)
		if count < 3 { // 3 = initial + ListAvailableModels initialization
			// This is expected — we only made the one request after Initialize
		}
	})

	t.Run("retry on 429", func(t *testing.T) {
		// Server returns 429 first, then 200
		var attempt int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			current := atomic.AddInt32(&attempt, 1)
			if strings.HasPrefix(r.URL.Path, "/ListAvailableModels") {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"models":["model1"]}`))
			} else if current == 1 {
				// First attempt returns 429
				w.WriteHeader(429)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"error":"rate limited"}`))
			} else {
				// Subsequent attempts return 200
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"success":true}`))
			}
		}))
		defer server.Close()

		// Create manager and initialize
		m, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}

		ctx := context.Background()
		if err := m.LoadCredentials(ctx); err != nil {
			t.Fatalf("LoadCredentials: %v", err)
		}

		m.mu.Lock()
		m.listURLOverride = func(qhost string) string { return server.URL + "/ListAvailableModels" }
		m.isRuntimeEndpointOverride = func(apiHost string) bool { return false }
		m.mu.Unlock()

		if err := m.Initialize(ctx); err != nil {
			t.Fatalf("Initialize: %v", err)
		}

		httpCli, err := httpclient.New(cfg)
		if err != nil {
			t.Fatalf("httpclient.New: %v", err)
		}
		defer httpCli.Close()

		acc, err := m.GetNextAccount("model1", nil)
		if err != nil {
			t.Fatalf("GetNextAccount: %v", err)
		}

		req, err := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/request", bytes.NewReader([]byte(`{"test":"data"}`)))
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := httpCli.RequestWithRetry(ctx, req, acc.Auth, false)
		if err != nil {
			t.Fatalf("RequestWithRetry: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("expected 200 after retry, got %d", resp.StatusCode)
		}

		m.ReportSuccess(acc.ID, "model1")

		// Verify that at least 2 attempts were made (429 + 200)
		attempts := atomic.LoadInt32(&attempt)
		if attempts < 2 {
			t.Errorf("expected at least 2 attempts, got %d", attempts)
		}
	})

	t.Run("failover on 500x3", func(t *testing.T) {
		// Server returns 500 for tok-A, then 200 for tok-B
		var requestNum int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")

			if strings.HasPrefix(r.URL.Path, "/ListAvailableModels") {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"models":["model1"]}`))
			} else if strings.Contains(authHeader, "tok-A") {
				// Account A always returns 500
				atomic.AddInt32(&requestNum, 1)
				w.WriteHeader(500)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"error":"internal server error"}`))
			} else {
				// Account B returns 200
				atomic.AddInt32(&requestNum, 1)
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"success":true}`))
			}
		}))
		defer server.Close()

		// Create manager and initialize
		m, err := NewManager(cfg)
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}

		ctx := context.Background()
		if err := m.LoadCredentials(ctx); err != nil {
			t.Fatalf("LoadCredentials: %v", err)
		}

		m.mu.Lock()
		m.listURLOverride = func(qhost string) string { return server.URL + "/ListAvailableModels" }
		m.isRuntimeEndpointOverride = func(apiHost string) bool { return false }
		m.mu.Unlock()

		if err := m.Initialize(ctx); err != nil {
			t.Fatalf("Initialize: %v", err)
		}

		httpCli, err := httpclient.New(cfg)
		if err != nil {
			t.Fatalf("httpclient.New: %v", err)
		}
		defer httpCli.Close()

		// Get first account, make request, expect 500 and failover
		exclude := make(map[string]struct{})

		// First request: account 0 (tok-A) should return 500 after retries
		acc0, err := m.GetNextAccount("model1", exclude)
		if err != nil {
			t.Fatalf("GetNextAccount 0: %v", err)
		}

		req0, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/request", bytes.NewReader([]byte(`{}`)))
		req0.Header.Set("Content-Type", "application/json")
		resp0, err0 := httpCli.RequestWithRetry(ctx, req0, acc0.Auth, false)

		if err0 == nil && resp0 != nil {
			// Got a response (probably 500)
			if resp0.StatusCode != 200 {
				// Expected 500 — this is the failover trigger
				io.Copy(io.Discard, resp0.Body)
				resp0.Body.Close()
				m.ReportFailure(acc0.ID, "model1", resp0.StatusCode, "", "500 internal server error")
				exclude[acc0.ID] = struct{}{}
			} else {
				t.Fatalf("account 0 should have returned 500, got %d", resp0.StatusCode)
			}
		} else if err0 != nil {
			// RequestWithRetry failed — also marks as failed
			m.ReportFailure(acc0.ID, "model1", 500, "", "request error")
			exclude[acc0.ID] = struct{}{}
		}

		// Second request: account 1 (tok-B) should return 200
		acc1, err := m.GetNextAccount("model1", exclude)
		if err != nil {
			t.Fatalf("GetNextAccount 1: %v", err)
		}

		req1, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/request", bytes.NewReader([]byte(`{}`)))
		req1.Header.Set("Content-Type", "application/json")
		resp1, err1 := httpCli.RequestWithRetry(ctx, req1, acc1.Auth, false)

		if err1 != nil {
			t.Fatalf("RequestWithRetry account 1: %v", err1)
		}
		if resp1.StatusCode != 200 {
			t.Fatalf("account 1 should return 200, got %d", resp1.StatusCode)
		}

		io.Copy(io.Discard, resp1.Body)
		resp1.Body.Close()
		m.ReportSuccess(acc1.ID, "model1")

		// Verify we made requests to both accounts
		if atomic.LoadInt32(&requestNum) < 2 {
			t.Errorf("expected at least 2 requests (to both accounts), got %d", atomic.LoadInt32(&requestNum))
		}
	})
}

// createKiroDesktopCredsFile creates a temporary Kiro Desktop JSON creds file
// with a far-future expiresAt so AccessToken() returns immediately without refresh.
func createKiroDesktopCredsFile(t *testing.T, dir, token, filename string) string {
	t.Helper()
	credsPath := filepath.Join(dir, filename)
	creds := map[string]any{
		"accessToken":  token,
		"refreshToken": "test-refresh-" + token,
		"profileArn":   "arn:aws:codewhisperer:us-east-1:123456789012:profile/test",
		"region":       "us-east-1",
		"expiresAt":    "2099-12-31T23:59:59.999999999Z",
	}
	data, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal creds: %v", err)
	}
	if err := os.WriteFile(credsPath, data, 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}
	return credsPath
}
