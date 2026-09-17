// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

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

// --- Escenario 7: fallo de transporte en la cuenta 0 → failover + breaker armado ---

// TestChatCompletions_TransportErrorFailsOverAndArmsBreaker cubre
// handleTransportError (failover.go), que hasta este fix round no tenía
// ningún test propio en routesopenai. La cuenta A apunta a un
// httptest.Server ya cerrado — cualquier intento de conexión se rechaza
// (ECONNREFUSED), así que httpclient.RequestWithRetry agota sus intentos y
// devuelve un *httpclient.RequestError (un fallo de TRANSPORTE, distinto de
// una respuesta HTTP no-2xx real de Kiro). Verifica dos cosas: (a) el
// cliente sigue recibiendo la respuesta de la cuenta B, sana (el failover
// avanzó); y (b) el circuit breaker de la cuenta A quedó armado
// (ConsecutiveFailures incrementado vía accountmanager.ReportFailureAs con
// accounterrors.Recoverable forzado — ver el fix round 1 de esta tarea).
func TestChatCompletions_TransportErrorFailsOverAndArmsBreaker(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-a", "tok-b"})
	accs := manager.Accounts()
	badAccountID := accs[0].ID

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("Hello", " world"))
	}))
	defer healthy.Close()

	// unreachable se cierra inmediatamente: cualquier intento de conexión a
	// su dirección se rechaza, produciendo un fallo de transporte real, no
	// simulado.
	unreachable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unreachableURL := unreachable.URL
	unreachable.Close()

	h := New(manager, mustHTTPClient(t, cfg), cfg, truncationstate.New())
	h.apiURL = func(acc *accountmanager.Account) string {
		if acc.ID == badAccountID {
			return unreachableURL + "/generateAssistantResponse"
		}
		return healthy.URL + "/generateAssistantResponse"
	}

	req := newChatRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (failover to the healthy account); body=%s", rec.Code, rec.Body.String())
	}

	var resp chatCompletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if resp.Choices[0].Message.Content != "Hello world" {
		t.Errorf("message.content = %q, want the healthy account's response", resp.Choices[0].Message.Content)
	}

	badAcc := accs[0]
	if badAcc.Stats.ConsecutiveFailures == 0 {
		t.Error("transport failure on account A did not arm the circuit breaker (ConsecutiveFailures still 0)")
	}
	if badAcc.Stats.LastFailure.IsZero() {
		t.Error("transport failure on account A did not set LastFailure")
	}
}
