// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// --- Fixtures/helpers compartidos por handler_test.go ---
//
// Mismo patrón que internal/routesopenai/helpers_test.go: cuentas respaldadas
// por ficheros de credenciales JSON con un access token estático y expiración
// lejana (auth.Manager.AccessToken devuelve sin red), porque el slice interno
// de accountmanager.Manager solo se puebla vía LoadCredentials.

func createCredsFile(t *testing.T, dir, filename, token string) string {
	t.Helper()
	path := filepath.Join(dir, filename)
	creds := map[string]any{
		"accessToken":  token,
		"refreshToken": "refresh-" + token,
		"profileArn":   "arn:aws:codewhisperer:us-east-1:123456789012:profile/test",
		"region":       "us-east-1",
		"expiresAt":    "2099-12-31T23:59:59.999999999Z",
	}
	data, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal creds: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write creds file: %v", err)
	}
	return path
}

// newTestManager crea un *accountmanager.Manager con len(tokens) cuentas.
func newTestManager(t *testing.T, cfg *config.Config, tokens []string) *accountmanager.Manager {
	t.Helper()
	dir := t.TempDir()

	type entry struct {
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
		Path    string `json:"path"`
	}
	var entries []entry
	for i, tok := range tokens {
		p := createCredsFile(t, dir, filenameFor(i), tok)
		entries = append(entries, entry{Type: "json", Enabled: true, Path: p})
	}

	credentialsPath := filepath.Join(dir, "credentials.json")
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal credentials.json: %v", err)
	}
	if err := os.WriteFile(credentialsPath, data, 0o600); err != nil {
		t.Fatalf("write credentials.json: %v", err)
	}

	cfg.KiroCredsFile = credentialsPath
	if cfg.KiroRegion == "" {
		cfg.KiroRegion = "us-east-1"
	}

	m, err := accountmanager.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.LoadCredentials(t.Context()); err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if got := len(m.Accounts()); got != len(tokens) {
		t.Fatalf("LoadCredentials loaded %d accounts, want %d", got, len(tokens))
	}
	return m
}

func filenameFor(i int) string {
	return "creds-" + string(rune('a'+i)) + ".json"
}

// testConfig devuelve un *config.Config con timeouts pequeños pero positivos
// (ver el comentario homónimo en internal/routesopenai/helpers_test.go).
func testConfig() *config.Config {
	return &config.Config{
		FirstTokenTimeout:              5,
		FirstTokenMaxRetries:           1,
		StreamingReadTimeout:           30,
		AccountCacheTTL:                3600,
		FakeReasoningInitialBufferSize: 20,
	}
}

// newTestHandler construye un Handler cuyo apiURL apunta al httptest.Server
// dado (mismo seam de test que routesopenai), con una *truncationstate.State
// nueva y vacía por defecto — los tests que necesiten pre-poblarla (Task 8a)
// construyen el Handler directamente con New en su lugar.
func newTestHandler(t *testing.T, manager *accountmanager.Manager, cfg *config.Config, serverURL string) *Handler {
	t.Helper()
	h := New(manager, mustHTTPClient(t, cfg), cfg, truncationstate.New())
	h.apiURL = func(acc *accountmanager.Account) string {
		return serverURL + "/generateAssistantResponse"
	}
	return h
}

func mustHTTPClient(t *testing.T, cfg *config.Config) *httpclient.Client {
	t.Helper()
	c, err := httpclient.New(cfg)
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	return c
}

// kiroContentBody construye un cuerpo de respuesta Kiro "crudo" — dialect-
// agnóstico, igual que en routesopenai — concatenando literales JSON con los
// prefijos que internal/parsers.Parser reconoce, más un evento usage y uno
// contextUsagePercentage al final.
func kiroContentBody(parts ...string) []byte {
	var buf bytes.Buffer
	for _, p := range parts {
		b, _ := json.Marshal(map[string]string{"content": p})
		buf.Write(b)
	}
	usage, _ := json.Marshal(map[string]any{
		"usage": map[string]any{
			"input_tokens": 10, "output_tokens": 5,
			"cache_read_tokens": 0, "cache_creation_tokens": 0,
		},
	})
	buf.Write(usage)
	ctxUsage, _ := json.Marshal(map[string]any{"contextUsagePercentage": 12.5})
	buf.Write(ctxUsage)
	return buf.Bytes()
}

// newMessagesRequest construye una *http.Request para POST /v1/messages con un
// AnthropicMessagesRequest mínimo (content como cadena, que
// AnthropicMessage.UnmarshalJSON convierte en un bloque text).
func newMessagesRequest(t *testing.T, model string, stream bool) *http.Request {
	t.Helper()
	payload := map[string]any{
		"model":      model,
		"max_tokens": 100,
		"stream":     stream,
		"messages": []map[string]any{
			{"role": "user", "content": "Hi there"},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}
