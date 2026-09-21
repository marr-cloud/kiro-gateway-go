// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package httpclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mockTokenProvider satisface utils.TokenProvider y, opcionalmente,
// forceRefresher (para el camino de 403). accessCalls/refreshCalls dejan que
// los tests verifiquen cuántas veces se llamó a cada método sin necesitar un
// mock framework.

func TestSimpleOK(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	clock := &fakeClock{}
	c := newTestClient(t, testConfig(), clock)
	tp := &mockTokenProvider{token: "tok"}

	resp, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, false)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
	if len(clock.recorded()) != 0 {
		t.Fatalf("sleeps = %v, want none", clock.recorded())
	}
}

// Test403TriggersRefreshAndRetries verifica que un 403 fuerza el refresco del
// token (vía la interfaz opcional ForceRefresh) y repite la petición.
func Test403TriggersRefreshAndRetries(t *testing.T) {
	var hits int
	var lastAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		lastAuth = r.Header.Get("Authorization")
		if hits == 1 {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	clock := &fakeClock{}
	c := newTestClient(t, testConfig(), clock)
	tp := &mockTokenProvider{token: "tok"}

	resp, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, false)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2", hits)
	}
	_, refreshCalls := tp.callCounts()
	if refreshCalls != 1 {
		t.Fatalf("refreshCalls = %d, want 1", refreshCalls)
	}
	if lastAuth != "Bearer tok-refreshed" {
		t.Fatalf("second attempt Authorization = %q, want refreshed token", lastAuth)
	}
	// El 403 no debe consumir backoff.
	if len(clock.recorded()) != 0 {
		t.Fatalf("sleeps = %v, want none for 403 path", clock.recorded())
	}
}

// TestExhausted403NonStreamReturns502 verifica que, cuando TODOS los
// intentos devuelven 403 (nunca se llega a un 200 ni a un 429/5xx que deje
// last_response), RequestWithRetry con stream=false cae en el
// HTTPException genérico de http_client.py:331-342 con 502 — el código para
// no-streaming.
func TestExhausted403NonStreamReturns502(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	clock := &fakeClock{}
	c := newTestClient(t, testConfig(), clock)
	tp := &mockTokenProvider{token: "tok"}

	_, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, false)
	if err == nil {
		t.Fatalf("RequestWithRetry: want error after exhausting all-403 attempts, got nil")
	}
	var reqErr *RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("err = %v (%T), want *RequestError", err, err)
	}
	if reqErr.Info.SuggestedHTTPCode != 502 {
		t.Fatalf("SuggestedHTTPCode = %d, want 502 (non-streaming)", reqErr.Info.SuggestedHTTPCode)
	}
	if hits != maxRetries {
		t.Fatalf("hits = %d, want %d (maxRetries)", hits, maxRetries)
	}
	if _, refreshCalls := tp.callCounts(); refreshCalls != maxRetries {
		t.Fatalf("refreshCalls = %d, want %d (one per 403)", refreshCalls, maxRetries)
	}
}

// TestExhausted403StreamReturns504 replica
// TestExhausted403NonStreamReturns502 con stream=true: el original devuelve
// 504 en ese caso (http_client.py:333-337), no 502. El presupuesto de
// intentos también cambia a cfg.FirstTokenMaxRetries.
func TestExhausted403StreamReturns504(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	cfg := testConfig()
	cfg.FirstTokenMaxRetries = 2

	clock := &fakeClock{}
	c := newTestClient(t, cfg, clock)
	tp := &mockTokenProvider{token: "tok"}

	_, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, true)
	if err == nil {
		t.Fatalf("RequestWithRetry: want error after exhausting all-403 attempts, got nil")
	}
	var reqErr *RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("err = %v (%T), want *RequestError", err, err)
	}
	if reqErr.Info.SuggestedHTTPCode != 504 {
		t.Fatalf("SuggestedHTTPCode = %d, want 504 (streaming)", reqErr.Info.SuggestedHTTPCode)
	}
	if hits != cfg.FirstTokenMaxRetries {
		t.Fatalf("hits = %d, want %d (FirstTokenMaxRetries)", hits, cfg.FirstTokenMaxRetries)
	}
}

// Test429Backoff verifica el backoff exponencial 1s, 2s ante 429 repetidos,
// con reloj falso para no dormir de verdad.
func Test429Backoff(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	clock := &fakeClock{}
	c := newTestClient(t, testConfig(), clock)
	tp := &mockTokenProvider{token: "tok"}

	resp, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, false)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if hits != 3 {
		t.Fatalf("hits = %d, want 3", hits)
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second}
	got := clock.recorded()
	if len(got) != len(want) {
		t.Fatalf("sleeps = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sleeps[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// Test429ExhaustedReturnsLastResponse verifica que, agotados los 3 intentos,
// se devuelve la última respuesta 429 tal cual (sin error), como el original.
func Test429ExhaustedReturnsLastResponse(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte("rate limited"))
	}))
	defer srv.Close()

	clock := &fakeClock{}
	c := newTestClient(t, testConfig(), clock)
	tp := &mockTokenProvider{token: "tok"}

	resp, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, false)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
	if hits != maxRetries {
		t.Fatalf("hits = %d, want %d (maxRetries)", hits, maxRetries)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "rate limited" {
		t.Fatalf("body = %q, want the last 429 body preserved", body)
	}
}

// TestExhaustedRetriesSleepsExpectedTimes fija el patrón exacto de esperas
// cuando los 3 intentos agotan con 429: el original (http_client.py:247-261)
// duerme SIN condición en cada iteración, incluso la última — a diferencia
// de las ramas de excepción, que sí comprueban si queda otro intento antes
// de dormir. baseRetryDelay×2^attempt para attempt=0,1,2 da 1s+2s+4s=7s.
func TestExhaustedRetriesSleepsExpectedTimes(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	clock := &fakeClock{}
	c := newTestClient(t, testConfig(), clock)
	tp := &mockTokenProvider{token: "tok"}

	resp, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, false)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if hits != maxRetries {
		t.Fatalf("hits = %d, want %d (maxRetries)", hits, maxRetries)
	}

	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second}
	got := clock.recorded()
	if len(got) != len(want) {
		t.Fatalf("sleeps = %v, want %v (one per attempt, including the last)", got, want)
	}
	var total time.Duration
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sleeps[%d] = %v, want %v", i, got[i], want[i])
		}
		total += got[i]
	}
	if total != 7*time.Second {
		t.Fatalf("cumulative sleep = %v, want 7s (1s+2s+4s)", total)
	}
}

// Test5xxBackoff replica Test429Backoff para 500.
func Test5xxBackoff(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if hits < 2 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	clock := &fakeClock{}
	c := newTestClient(t, testConfig(), clock)
	tp := &mockTokenProvider{token: "tok"}

	resp, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, false)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	got := clock.recorded()
	if len(got) != 1 || got[0] != 1*time.Second {
		t.Fatalf("sleeps = %v, want [1s]", got)
	}
}

// TestOtherStatusReturnedImmediately verifica que un 400 (u otro código que no
// sea 403/429/5xx) se devuelve sin reintento.
func TestOtherStatusReturnedImmediately(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	clock := &fakeClock{}
	c := newTestClient(t, testConfig(), clock)
	tp := &mockTokenProvider{token: "tok"}

	resp, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, false)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	if hits != 1 {
		t.Fatalf("hits = %d, want 1", hits)
	}
}

// TestHeadersMatchGetKiroHeaders verifica que la petición final lleva
// exactamente las cabeceras que produce utils.GetKiroHeaders.
