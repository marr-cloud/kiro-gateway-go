// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/tokenizer"
)

// --- Escenario 1: streaming — secuencia de eventos SSE Anthropic ---

func TestMessages_StreamingSSEBytes(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("Hello", " world"))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newMessagesRequest(t, "claude-sonnet-4", true)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/event-stream; charset=utf-8")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}

	datas := splitSSEData(rec.Body.Bytes())
	if len(datas) == 0 {
		t.Fatalf("no SSE events produced; body=%s", rec.Body.String())
	}

	var types []string
	var fullText string
	sawMessageDelta := false
	for _, data := range datas {
		var ev sseEventIn
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("decode event %s: %v", data, err)
		}
		types = append(types, ev.Type)
		if ev.Type == "content_block_delta" {
			var d deltaIn
			_ = json.Unmarshal(ev.Delta, &d)
			fullText += d.Text
		}
		if ev.Type == "message_delta" {
			sawMessageDelta = true
		}
	}

	if types[0] != "message_start" {
		t.Errorf("first event = %q, want message_start", types[0])
	}
	if types[len(types)-1] != "message_stop" {
		t.Errorf("last event = %q, want message_stop", types[len(types)-1])
	}
	if !sawMessageDelta {
		t.Error("no message_delta event emitted")
	}
	if fullText != "Hello world" {
		t.Errorf("accumulated text = %q, want %q", fullText, "Hello world")
	}
}

// --- Escenario 2: no-streaming — respuesta Anthropic message única ---

type nonStreamResp struct {
	Type    string `json:"type"`
	Role    string `json:"role"`
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	StopReason string `json:"stop_reason"`
	Usage      struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func TestMessages_NonStreamingSingleJSON(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("Hello", " world"))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newMessagesRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp nonStreamResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}

	if resp.Type != "message" {
		t.Errorf("type = %q, want message", resp.Type)
	}
	if resp.Role != "assistant" {
		t.Errorf("role = %q, want assistant", resp.Role)
	}
	var text string
	for _, b := range resp.Content {
		if b.Type == "text" {
			text += b.Text
		}
	}
	if text != "Hello world" {
		t.Errorf("text content = %q, want %q", text, "Hello world")
	}
	if resp.StopReason == "" {
		t.Error("stop_reason is empty")
	}
	if resp.Usage.OutputTokens == 0 {
		t.Error("usage.output_tokens is 0")
	}
	// input_tokens debe venir del override por context_usage (kiroContentBody
	// manda contextUsagePercentage: 12.5 → 12.5% de 200000 = 25000, menos
	// output_tokens), NO de la estimación pre-petición del message_start
	// (streaming_anthropic.py:809-816). Esto es lo que devuelve
	// collect_anthropic_response en el caso común.
	wantInput := 25000 - resp.Usage.OutputTokens
	if resp.Usage.InputTokens != wantInput {
		t.Errorf("usage.input_tokens = %d, want %d (context-usage override: int(12.5/100*200000) - output_tokens)", resp.Usage.InputTokens, wantInput)
	}
}

// --- Escenario 3: failover — cuenta 0 recuperable, cuenta 1 éxito ---

func TestMessages_FailoverRecoverableThenSuccess(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-a", "tok-b"})

	var attemptsA, attemptsB atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch {
		case strings.Contains(auth, "tok-a"):
			attemptsA.Add(1)
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

	req := newMessagesRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if attemptsA.Load() == 0 {
		t.Error("account A (tok-a) was never tried")
	}
	if attemptsB.Load() == 0 {
		t.Error("account B (tok-b) was never tried")
	}

	var resp nonStreamResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	var text string
	for _, b := range resp.Content {
		text += b.Text
	}
	if text != "Hello world" {
		t.Errorf("text = %q, want account B's response", text)
	}
}

// --- Escenario 4: multi-cuenta agotada → 503 en dialecto Anthropic ---

func TestMessages_MultiAccountExhausted503(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-a", "tok-b"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusPaymentRequired)
		w.Write([]byte(`{"message":"insufficient credits","reason":"NO_CREDITS"}`))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newMessagesRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}

	var errResp anthropicError
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error: %v; body=%s", err, rec.Body.String())
	}
	if errResp.Type != "error" {
		t.Errorf("type = %q, want error", errResp.Type)
	}
	if errResp.Error.Type != "api_error" {
		t.Errorf("error.type = %q, want api_error", errResp.Error.Type)
	}
	if errResp.Error.Message == "" {
		t.Error("error.message is empty")
	}
}

// --- Escenario 5: cuenta única, error Fatal → error real de Kiro, no 503 ---

func TestMessages_SingleAccountFatalNotServiceUnavailable(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"Context too large","reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD"}`))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newMessagesRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (real Kiro status, not 503); body=%s", rec.Code, rec.Body.String())
	}

	var errResp anthropicError
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error: %v; body=%s", err, rec.Body.String())
	}
	if errResp.Error.Type != "api_error" {
		t.Errorf("error.type = %q, want api_error", errResp.Error.Type)
	}
	const wantMessage = "Model context limit reached. Conversation size exceeds model capacity."
	if errResp.Error.Message != wantMessage {
		t.Errorf("error.message = %q, want %q (kiroerrors literal)", errResp.Error.Message, wantMessage)
	}
}

// --- Escenario 6: fallo de transporte → failover + breaker armado ---

func TestMessages_TransportErrorFailsOverAndArmsBreaker(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-a", "tok-b"})
	accs := manager.Accounts()
	badAccountID := accs[0].ID

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("Hello", " world"))
	}))
	defer healthy.Close()

	// unreachable se cierra de inmediato: conexión rechazada real (ECONNREFUSED).
	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unreachableURL := unreachable.URL
	unreachable.Close()

	h := New(manager, mustHTTPClient(t, cfg), cfg)
	h.apiURL = func(acc *accountmanager.Account) string {
		if acc.ID == badAccountID {
			return unreachableURL + "/generateAssistantResponse"
		}
		return healthy.URL + "/generateAssistantResponse"
	}

	req := newMessagesRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (failover to healthy account); body=%s", rec.Code, rec.Body.String())
	}
	var resp nonStreamResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	var text string
	for _, b := range resp.Content {
		text += b.Text
	}
	if text != "Hello world" {
		t.Errorf("text = %q, want healthy account's response", text)
	}

	badAcc := accs[0]
	if badAcc.Stats.ConsecutiveFailures == 0 {
		t.Error("transport failure did not arm the circuit breaker (ConsecutiveFailures still 0)")
	}
	if badAcc.Stats.LastFailure.IsZero() {
		t.Error("transport failure did not set LastFailure")
	}
}

// --- Escenario 7: /v1/messages/count_tokens local ---

func TestCountTokens_Local(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok"})
	h := New(manager, mustHTTPClient(t, cfg), cfg)

	bodyMap := map[string]any{
		"model": "claude-sonnet-4",
		"messages": []map[string]any{
			{"role": "user", "content": "The quick brown fox jumps over the lazy dog several times today."},
			{"role": "assistant", "content": "A classic pangram often used to test fonts, keyboards, and encoders."},
		},
	}
	body, _ := json.Marshal(bodyMap)

	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	h.CountTokens(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var resp struct {
		InputTokens int `json:"input_tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}

	// Cómputo independiente: la MISMA suma de tres sub-conteos corregidos
	// (apply_claude_correction=True) que el endpoint hace, validando el
	// cableado (marshaling, corrección aplicada, suma de los tres).
	var parsed modelsanthropic.AnthropicCountTokensRequest
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("reparse body: %v", err)
	}
	want := tokenizer.CountMessageTokens(marshalMessages(parsed.Messages), true) +
		tokenizer.CountToolsTokens(marshalTools(parsed.Tools), true) +
		tokenizer.CountSystemTokens(parsed.System, true)
	if resp.InputTokens != want {
		t.Errorf("input_tokens = %d, want %d (three corrected sub-counts summed)", resp.InputTokens, want)
	}
	if resp.InputTokens == 0 {
		t.Fatal("input_tokens is 0")
	}

	// La corrección Claude (1.15) debe estar aplicada: el total corregido es
	// mayor que el conteo de mensajes SIN corregir para esta entrada.
	uncorrected := tokenizer.CountMessageTokens(marshalMessages(parsed.Messages), false)
	if resp.InputTokens <= uncorrected {
		t.Errorf("input_tokens %d not greater than uncorrected message count %d — apply_claude_correction=True not wired?", resp.InputTokens, uncorrected)
	}
}
