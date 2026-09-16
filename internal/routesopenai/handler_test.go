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
	"strings"
	"sync/atomic"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
)

// --- Helpers de test: cuentas respaldadas por ficheros de credenciales JSON ---
//
// Sigue el mismo patrón que internal/accountmanager/integration_test.go's
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
	client, err := httpclient.New(cfg)
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	h := New(manager, client, cfg)
	h.apiURL = func(acc *accountmanager.Account) string {
		return serverURL + "/generateAssistantResponse"
	}
	return h
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

// --- Escenario 1: GET /v1/models con 2 cuentas → union ordenada ---

func TestModels_TwoAccountsUnionSorted(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-a", "tok-b"})

	accs := manager.Accounts()
	accs[0].Models.Models = []string{"claude-sonnet-4", "model-z"}
	accs[1].Models.Models = []string{"model-z", "claude-opus-4"}

	h := New(manager, mustHTTPClient(t, cfg), cfg)

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

	want := []string{"claude-opus-4", "claude-sonnet-4", "model-z"}
	if !equalStrings(ids, want) {
		t.Errorf("ids = %v, want %v", ids, want)
	}
}

func mustHTTPClient(t *testing.T, cfg *config.Config) *httpclient.Client {
	t.Helper()
	c, err := httpclient.New(cfg)
	if err != nil {
		t.Fatalf("httpclient.New: %v", err)
	}
	return c
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

// --- Escenario 2: streaming — bytes SSE finales ---

func TestChatCompletions_StreamingSSEBytes(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("Hello", " world"))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newChatRequest(t, "claude-sonnet-4", true)
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream; charset=utf-8")
	}

	events := splitSSEData(rec.Body.Bytes())
	if len(events) == 0 {
		t.Fatalf("no SSE events produced; body=%s", rec.Body.String())
	}
	if string(events[len(events)-1]) != "[DONE]" {
		t.Fatalf("last event = %q, want [DONE]", events[len(events)-1])
	}

	var fullContent string
	var sawFinalUsage bool
	var finishReason string
	for _, data := range events[:len(events)-1] {
		var chunk sseChunkIn
		if err := json.Unmarshal(data, &chunk); err != nil {
			t.Fatalf("decode chunk %s: %v", data, err)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		fullContent += chunk.Choices[0].Delta.Content
		if chunk.Choices[0].FinishReason != nil {
			finishReason = *chunk.Choices[0].FinishReason
		}
		if len(chunk.Usage) > 0 {
			sawFinalUsage = true
		}
	}

	if fullContent != "Hello world" {
		t.Errorf("accumulated content = %q, want %q", fullContent, "Hello world")
	}
	if finishReason != "stop" {
		t.Errorf("finish_reason = %q, want %q", finishReason, "stop")
	}
	if !sawFinalUsage {
		t.Error("no chunk carried a usage object")
	}
}

// --- Escenario 3: no-streaming — respuesta JSON única ---

func TestChatCompletions_NonStreamingSingleJSON(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("Hello", " world"))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newChatRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp chatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}

	if resp.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", resp.Object)
	}
	if len(resp.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(resp.Choices))
	}
	if resp.Choices[0].Message.Content != "Hello world" {
		t.Errorf("message.content = %q, want %q", resp.Choices[0].Message.Content, "Hello world")
	}
	if resp.Choices[0].Message.Role != "assistant" {
		t.Errorf("message.role = %q, want assistant", resp.Choices[0].Message.Role)
	}
	if resp.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", resp.Choices[0].FinishReason)
	}
	if len(resp.Usage) == 0 {
		t.Error("usage missing from non-streaming response")
	}
}

// --- Escenario 4: failover — cuenta 0 recuperable, cuenta 1 éxito ---

func TestChatCompletions_FailoverRecoverableThenSuccess(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-a", "tok-b"})

	var attemptsA, attemptsB atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch {
		case strings.Contains(auth, "tok-a"):
			attemptsA.Add(1)
			// 402: Recoverable sin importar el reason (accounterrors.Classify
			// trata 402/403/429 como Recoverable incondicionalmente).
			w.WriteHeader(http.StatusPaymentRequired)
			w.Write([]byte(`{"message":"insufficient credits","reason":"NO_CREDITS"}`))
		case strings.Contains(auth, "tok-b"):
			attemptsB.Add(1)
			w.WriteHeader(http.StatusOK)
			w.Write(kiroContentBody("Hello", " world"))
		default:
			t.Errorf("unexpected Authorization header: %q", auth)
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newChatRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if attemptsA.Load() == 0 {
		t.Error("account A (tok-a) was never tried")
	}
	if attemptsB.Load() == 0 {
		t.Error("account B (tok-b) was never tried")
	}

	var resp chatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if resp.Choices[0].Message.Content != "Hello world" {
		t.Errorf("message.content = %q, want the response from account B", resp.Choices[0].Message.Content)
	}
}

// --- Escenario 5: multi-cuenta agotada → 503 en dialecto OpenAI ---

func TestChatCompletions_MultiAccountExhausted503(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-a", "tok-b"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Las dos cuentas fallan siempre de forma recuperable.
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"message":"insufficient credits","reason":"NO_CREDITS"}`))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newChatRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}

	var envelope openAIErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rec.Body.String())
	}
	if envelope.Error.Type != "kiro_api_error" {
		t.Errorf("error.type = %q, want kiro_api_error", envelope.Error.Type)
	}
	if envelope.Error.Code != http.StatusServiceUnavailable {
		t.Errorf("error.code = %d, want 503", envelope.Error.Code)
	}
	if envelope.Error.Message == "" {
		t.Error("error.message is empty")
	}
}

// --- Escenario 6: cuenta única, error Fatal → error real de Kiro, no 503 ---

func TestChatCompletions_SingleAccountFatalNotServiceUnavailable(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"Context too large","reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD"}`))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newChatRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (the real Kiro status, not 503); body=%s", rec.Code, rec.Body.String())
	}

	var envelope openAIErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error envelope: %v; body=%s", err, rec.Body.String())
	}
	if envelope.Error.Code != http.StatusBadRequest {
		t.Errorf("error.code = %d, want 400", envelope.Error.Code)
	}
	const wantMessage = "Model context limit reached. Conversation size exceeds model capacity."
	if envelope.Error.Message != wantMessage {
		t.Errorf("error.message = %q, want %q (kiroerrors literal for CONTENT_LENGTH_EXCEEDS_THRESHOLD)", envelope.Error.Message, wantMessage)
	}
}
