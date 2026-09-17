// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

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
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// --- Fixtures/helpers compartidos por handler_test.go ---
//
// Cuentas respaldadas por ficheros de credenciales JSON. Sigue el mismo
// patrón que internal/accountmanager/integration_test.go's
// createKiroDesktopCredsFile: un token estático con expiresAt en el futuro
// lejano hace que auth.Manager.AccessToken(ctx) devuelva de inmediato sin
// red. Como los campos de accountmanager.Account son públicos pero el slice
// interno de accountmanager.Manager no lo es, la única forma pública de
// poblar un Manager con cuentas reales es LoadCredentials leyendo un
// credentials.json real — no hay forma de inyectar *Account a mano desde
// fuera del paquete.

// createCredsFile escribe un fichero de credenciales estilo Kiro Desktop con
// un access token estático y expiración lejana.
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

// newTestManager crea un *accountmanager.Manager con len(tokens) cuentas,
// cada una respaldada por un fichero de credenciales JSON con el token
// dado. No llama a Initialize (no hace falta: Enabled ya lo fija
// LoadCredentials a partir de credentials.json, y GetNextAccount no
// consulta Models para elegir cuenta — solo GetAllAvailableModels lo lee).
// Las cuentas quedan en m.Accounts() en el mismo orden que tokens.
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

// testConfig devuelve un *config.Config con timeouts pequeños pero
// positivos: un FirstTokenTimeout/StreamingReadTimeout en 0 haría que
// httpclient.readFirstChunk(rc, 0) compitiera casi siempre contra
// time.After(0) y fallase por timeout incluso contra un httptest.Server
// local en memoria (ver internal/httpclient/stream.go).
func testConfig() *config.Config {
	return &config.Config{
		FirstTokenTimeout:              5,
		FirstTokenMaxRetries:           1,
		StreamingReadTimeout:           30,
		AccountCacheTTL:                3600,
		FakeReasoningInitialBufferSize: 20,
	}
}

// newTestHandler construye un Handler cuyo apiURL apunta siempre al
// httptest.Server dado, sobreescribiendo el seam de test descrito en el
// comentario de Handler.apiURL (handler.go): auth.Manager.APIHost() nunca
// puede apuntar a un servidor local, así que el propio paquete se da un
// override no exportado.
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

// kiroContentBody construye un cuerpo de respuesta Kiro "crudo" concatenando
// literales JSON con los prefijos que internal/parsers.Parser reconoce
// directamente (parser.go: eventPatterns) — sin ningún framing binario AWS
// event-stream real, igual que el propio parser hace (D4.2 del spec:
// "parser oportunista del stream", replicado tal cual del original).
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

// newChatRequest construye una *http.Request para POST /v1/chat/completions
// con un ChatCompletionRequest mínimo válido.
func newChatRequest(t *testing.T, model string, stream bool) *http.Request {
	t.Helper()
	payload := modelsopenai.ChatCompletionRequest{
		Model:    model,
		Stream:   stream,
		Messages: []modelsopenai.ChatMessage{{Role: "user", Content: json.RawMessage(`"Hi there"`)}},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
