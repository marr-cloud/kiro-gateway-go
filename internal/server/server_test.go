// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
	"github.com/marr-cloud/kiro-gateway-go/internal/version"
)

const testAPIKey = "test-proxy-key-123"

// --- Fixtures/helpers ---
//
// Igual patrón que internal/routesopenai/helpers_test.go's newTestManager:
// un credentials.json real con un access token estático y expiración lejana,
// para que accountmanager.Manager tenga al menos una cuenta sin tocar red
// (no se llama a Initialize — accountmanager/init_test.go ya cubre esa
// ruta, y aquí solo hace falta que GetFirstAccount() no sea nil).

func newTestManager(t *testing.T) *accountmanager.Manager {
	t.Helper()
	dir := t.TempDir()

	credsPath := filepath.Join(dir, "creds-a.json")
	creds := map[string]any{
		"accessToken":  "tok-abc",
		"refreshToken": "refresh-tok-abc",
		"profileArn":   "arn:aws:codewhisperer:us-east-1:123456789012:profile/test",
		"region":       "us-east-1",
		"expiresAt":    "2099-12-31T23:59:59.999999999Z",
	}
	data, err := json.Marshal(creds)
	if err != nil {
		t.Fatalf("marshal creds: %v", err)
	}
	if err := os.WriteFile(credsPath, data, 0o600); err != nil {
		t.Fatalf("write creds file: %v", err)
	}

	type entry struct {
		Type    string `json:"type"`
		Enabled bool   `json:"enabled"`
		Path    string `json:"path"`
	}
	entries := []entry{{Type: "json", Enabled: true, Path: credsPath}}
	credentialsPath := filepath.Join(dir, "credentials.json")
	entriesData, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal credentials.json: %v", err)
	}
	if err := os.WriteFile(credentialsPath, entriesData, 0o600); err != nil {
		t.Fatalf("write credentials.json: %v", err)
	}

	cfg := testConfig()
	cfg.AccountsConfigFile = credentialsPath

	m, err := accountmanager.NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.LoadCredentials(t.Context()); err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if got := len(m.Accounts()); got != 1 {
		t.Fatalf("LoadCredentials loaded %d accounts, want 1", got)
	}
	return m
}

// testConfig devuelve un *config.Config con la API key de prueba y timeouts
// pequeños-pero-positivos (mismo comentario que routesopenai/helpers_test.go:
// FirstTokenTimeout/StreamingReadTimeout en 0 rompería httpclient.readFirstChunk).
func testConfig() *config.Config {
	return &config.Config{
		ProxyAPIKey:                    testAPIKey,
		ServerHost:                     "127.0.0.1",
		ServerPort:                     0,
		KiroRegion:                     "us-east-1",
		FirstTokenTimeout:              5,
		FirstTokenMaxRetries:           1,
		StreamingReadTimeout:           30,
		AccountCacheTTL:                3600,
		FakeReasoningInitialBufferSize: 20,
	}
}

// newTestServer construye un *Server con un Manager de una cuenta (para que
// "/" y "/health" tengan cuenta activa que reportar) y startedAt ya fijado
// (Start() en sí bloquea en ListenAndServe, así que los tests fijan el campo
// directamente — mismo paquete, seam de test documentado en server.go).
func newTestServer(t *testing.T) *Server {
	t.Helper()
	cfg := testConfig()
	manager := newTestManager(t)
	// newTestManager ya fijó KiroCredsFile en SU PROPIO *cfg (interno);
	// aquí reconstruimos un *Client con la misma config base para el Server.
	client, err := httpclient.New(cfg)
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	s := New(cfg, manager, client, nil)
	s.startedAt = time.Now().Add(-5 * time.Second)
	return s
}

// doRequest ejecuta una petición directamente contra s.http.Handler (el
// http.Handler ya envuelto en CORS/auth/recovery), sin abrir ningún listener
// real — ningún test de este fichero toca red.
func doRequest(s *Server, method, path string, headers map[string]string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	s.http.Handler.ServeHTTP(rec, req)
	return rec
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode JSON response %q: %v", rec.Body.String(), err)
	}
	return body
}

// --- GET / y GET /health, sin auth ---

func TestRoot_NoAuth_ReturnsStatus(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/", nil, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	if body["status"] != "ok" {
		t.Errorf("status field = %v, want ok", body["status"])
	}
	if body["message"] != "Kiro Gateway is running" {
		t.Errorf("message field = %v", body["message"])
	}
	if body["version"] != version.Version() {
		t.Errorf("version field = %v, want %s", body["version"], version.Version())
	}
	if body["mode"] != serverMode {
		t.Errorf("mode field = %v, want %s", body["mode"], serverMode)
	}
	if body["active_account"] == nil {
		t.Errorf("active_account field = nil, want the loaded account's ID")
	}
	uptime, ok := body["uptime_seconds"].(float64)
	if !ok || uptime <= 0 {
		t.Errorf("uptime_seconds = %v, want a positive number", body["uptime_seconds"])
	}
}

func TestHealth_NoAuth_ReturnsStatus(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/health", nil, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	if body["status"] != "healthy" {
		t.Errorf("status field = %v, want healthy", body["status"])
	}
	if body["version"] != version.Version() {
		t.Errorf("version field = %v, want %s", body["version"], version.Version())
	}
	ts, _ := body["timestamp"].(string)
	if _, err := time.Parse(time.RFC3339Nano, ts); err != nil {
		t.Errorf("timestamp %q not RFC3339: %v", ts, err)
	}
	if body["active_account"] == nil {
		t.Errorf("active_account field = nil, want the loaded account's ID")
	}
}

// --- /v1/models: auth Bearer solamente ---

func TestModels_NoAuth_Returns401(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/v1/models", nil, nil)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected OpenAI error envelope, got %s", rec.Body.String())
	}
	if errObj["type"] != "kiro_api_error" {
		t.Errorf("error.type = %v, want kiro_api_error", errObj["type"])
	}
}

func TestModels_Bearer_Returns200(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/v1/models",
		map[string]string{"Authorization": "Bearer " + testAPIKey}, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	if body["object"] != "list" {
		t.Errorf("object field = %v, want list", body["object"])
	}
}

// --- POST /v1/messages: x-api-key O Bearer ---
//
// El Handler real de routesanthropic haría una petición HTTP real a Kiro
// (apiURL no es alcanzable desde este paquete: es un campo no exportado de
// routesanthropic.Handler, ver el comentario de Server.anthropicMessages).
// Por eso estos tests sustituyen el handler por un stub 200 fijo: lo que se
// está probando aquí es el middleware de auth, no el failover — ese ya lo
// cubre internal/routesanthropic/handler_test.go.

func TestMessages_Auth(t *testing.T) {
	cases := []struct {
		name       string
		headers    map[string]string
		wantStatus int
	}{
		{"x-api-key válido", map[string]string{"x-api-key": testAPIKey}, http.StatusOK},
		{"Bearer válido", map[string]string{"Authorization": "Bearer " + testAPIKey}, http.StatusOK},
		{"sin auth", nil, http.StatusUnauthorized},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newTestServer(t)
			s.anthropicMessages = func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
			}

			rec := doRequest(s, http.MethodPost, "/v1/messages", c.headers, []byte(`{}`))
			if rec.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, c.wantStatus, rec.Body.String())
			}
			if c.wantStatus == http.StatusUnauthorized {
				body := decodeJSON(t, rec)
				if body["type"] != "error" {
					t.Errorf("expected Anthropic error envelope, got %s", rec.Body.String())
				}
			}
		})
	}
}

// TestMessages_EmptyConfiguredKey_NoHeader_Returns401 es la regresión del
// bypass de auth encontrado en la review de Task 11: con PROXY_API_KEY vacío
// (mala config plausible del operador, p.ej. `PROXY_API_KEY=` en .env), una
// petición SIN cabecera x-api-key NO debe autenticarse. Antes del fix,
// Header.Get("x-api-key")=="" (cabecera ausente) coincidía con la key vacía y
// dejaba pasar; el guard `key != ""` de validAnthropicAuth lo rechaza,
// replicando el `if x_api_key and ...` de verify_anthropic_api_key
// (routes_anthropic.py:94-96). El handler se sustituye por un stub 200: si el
// auth (erróneamente) pasara, el test vería 200 en vez del 401 esperado.
func TestMessages_EmptyConfiguredKey_NoHeader_Returns401(t *testing.T) {
	cfg := testConfig()
	cfg.ProxyAPIKey = ""
	manager := newTestManager(t)
	client, err := httpclient.New(cfg)
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	s := New(cfg, manager, client, nil)
	s.startedAt = time.Now()
	s.anthropicMessages = func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // si auth pasara (bug), veríamos esto
	}

	rec := doRequest(s, http.MethodPost, "/v1/messages", nil, []byte(`{}`))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("empty PROXY_API_KEY + no x-api-key: status = %d, want 401 (sin bypass de auth); body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	if body["type"] != "error" {
		t.Errorf("expected Anthropic error envelope, got %s", rec.Body.String())
	}
}

// --- Panic recovery: 500 en el dialecto correcto según el path ---

func TestPanicRecovery_OpenAIDialect(t *testing.T) {
	s := newTestServer(t)
	s.openaiChatCompletions = func(w http.ResponseWriter, r *http.Request) {
		panic("boom openai")
	}

	rec := doRequest(s, http.MethodPost, "/v1/chat/completions",
		map[string]string{"Authorization": "Bearer " + testAPIKey}, []byte(`{}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected OpenAI error envelope, got %s", rec.Body.String())
	}
	if errObj["type"] != "kiro_api_error" {
		t.Errorf("error.type = %v, want kiro_api_error", errObj["type"])
	}
	if errObj["message"] != "boom openai" {
		t.Errorf("error.message = %v, want boom openai", errObj["message"])
	}
}

func TestPanicRecovery_AnthropicDialect(t *testing.T) {
	s := newTestServer(t)
	s.anthropicMessages = func(w http.ResponseWriter, r *http.Request) {
		panic("boom anthropic")
	}

	rec := doRequest(s, http.MethodPost, "/v1/messages",
		map[string]string{"x-api-key": testAPIKey}, []byte(`{}`))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	body := decodeJSON(t, rec)
	if body["type"] != "error" {
		t.Errorf("type field = %v, want error", body["type"])
	}
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected Anthropic error envelope, got %s", rec.Body.String())
	}
	if errObj["type"] != "api_error" {
		t.Errorf("error.type = %v, want api_error", errObj["type"])
	}
	if errObj["message"] != "boom anthropic" {
		t.Errorf("error.message = %v, want boom anthropic", errObj["message"])
	}
}

// --- CORS preflight ---

func TestCORSPreflight_Options(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodOptions, "/v1/chat/completions", nil, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	for header, want := range map[string]string{
		"Access-Control-Allow-Origin":      "*",
		"Access-Control-Allow-Methods":     "*",
		"Access-Control-Allow-Headers":     "*",
		"Access-Control-Allow-Credentials": "true",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}
