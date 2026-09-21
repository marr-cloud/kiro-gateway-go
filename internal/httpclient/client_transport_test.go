// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package httpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHeadersMatchGetKiroHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, testConfig(), &fakeClock{})
	tp := &mockTokenProvider{token: "tok", profileARN: "arn:aws:test"}

	resp, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, false)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	defer resp.Body.Close()

	wantHeaders := []struct{ name, value string }{
		{"Authorization", "Bearer tok"},
		{"Content-Type", "application/x-amz-json-1.0"},
		{"X-Amz-Target", "AmazonCodeWhispererStreamingService.GenerateAssistantResponse"},
		{"X-Amzn-Codewhisperer-Optout", "true"},
		{"X-Amzn-Kiro-Agent-Mode", "vibe"},
	}
	for _, wh := range wantHeaders {
		if v := got.Get(wh.name); v != wh.value {
			t.Errorf("header %s = %q, want %q", wh.name, v, wh.value)
		}
	}
	for _, name := range []string{"User-Agent", "X-Amz-User-Agent", "Amz-Sdk-Invocation-Id", "Amz-Sdk-Request"} {
		if got.Get(name) == "" {
			t.Errorf("header %s missing, want non-empty", name)
		}
	}
}

// TestConnectionCloseOnlyOnStreaming verifica que Connection: close solo se
// añade cuando stream=true.
func TestConnectionCloseOnlyOnStreaming(t *testing.T) {
	var streamConn, plainConn string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream" {
			streamConn = r.Header.Get("Connection")
		} else {
			plainConn = r.Header.Get("Connection")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("body"))
	}))
	defer srv.Close()

	c := newTestClient(t, testConfig(), &fakeClock{})
	tp := &mockTokenProvider{token: "tok"}

	respPlain, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL+"/plain"), tp, false)
	if err != nil {
		t.Fatalf("plain RequestWithRetry: %v", err)
	}
	io.Copy(io.Discard, respPlain.Body)
	respPlain.Body.Close()

	respStream, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL+"/stream"), tp, true)
	if err != nil {
		t.Fatalf("stream RequestWithRetry: %v", err)
	}
	io.Copy(io.Discard, respStream.Body)
	respStream.Body.Close()

	if plainConn != "" {
		t.Errorf("plain Connection header = %q, want empty", plainConn)
	}
	if streamConn != "close" {
		t.Errorf("stream Connection header = %q, want close", streamConn)
	}
}

// TestFirstTokenTimeoutRetriesWholeRequest verifica que un servidor que
// retrasa el primer byte más allá de FirstTokenTimeout hace que se reintente
// la petición completa, hasta FirstTokenMaxRetries veces, y luego falla.
func TestFirstTokenTimeoutRetriesWholeRequest(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		// Cabeceras al instante (como Kiro aceptando la conexión), pero el
		// primer byte del body llega tarde (como el modelo "pensando").
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	cfg := testConfig()
	cfg.FirstTokenTimeout = 0.03 // 30ms, muy por debajo de los 150ms del servidor
	cfg.FirstTokenMaxRetries = 2

	clock := &fakeClock{}
	c := newTestClient(t, cfg, clock)
	tp := &mockTokenProvider{token: "tok"}

	_, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, true)
	if err == nil {
		t.Fatalf("RequestWithRetry: want error after exhausting FirstTokenMaxRetries, got nil")
	}
	if hits != cfg.FirstTokenMaxRetries {
		t.Fatalf("hits = %d, want %d (FirstTokenMaxRetries)", hits, cfg.FirstTokenMaxRetries)
	}
	var reqErr *RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("err = %v (%T), want *RequestError", err, err)
	}
}

// TestFirstTokenTimeoutRecoversOnSecondAttempt verifica que si el primer
// intento se retrasa pero el segundo responde a tiempo, la petición se
// recupera y el primer chunk leído no se pierde.
func TestFirstTokenTimeoutRecoversOnSecondAttempt(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		if hits == 1 {
			time.Sleep(150 * time.Millisecond)
		}
		_, _ = w.Write([]byte("chunk-data"))
	}))
	defer srv.Close()

	cfg := testConfig()
	cfg.FirstTokenTimeout = 0.03
	cfg.FirstTokenMaxRetries = 3

	clock := &fakeClock{}
	c := newTestClient(t, cfg, clock)
	tp := &mockTokenProvider{token: "tok"}

	resp, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, true)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(body) != "chunk-data" {
		t.Fatalf("body = %q, want %q (first chunk must not be lost)", body, "chunk-data")
	}
	if hits != 2 {
		t.Fatalf("hits = %d, want 2", hits)
	}
}

// TestStreamingReadTimeoutAppliesBetweenChunks verifica que, tras el primer
// chunk, una pausa mayor que StreamingReadTimeout hace que Read() devuelva un
// error en vez de bloquear indefinidamente.
func TestStreamingReadTimeoutAppliesBetweenChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("first"))
		if ok {
			flusher.Flush()
		}
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte("second"))
	}))
	defer srv.Close()

	cfg := testConfig()
	cfg.FirstTokenTimeout = 1
	cfg.StreamingReadTimeout = 0.03 // 30ms
	cfg.FirstTokenMaxRetries = 1

	c := newTestClient(t, cfg, &fakeClock{})
	tp := &mockTokenProvider{token: "tok"}

	resp, err := c.RequestWithRetry(context.Background(), newPOSTRequest(t, srv.URL), tp, true)
	if err != nil {
		t.Fatalf("RequestWithRetry: %v", err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 64)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("first Read: %v", err)
	}
	if string(buf[:n]) != "first" {
		t.Fatalf("first Read = %q, want %q", buf[:n], "first")
	}

	_, err = resp.Body.Read(buf)
	if err == nil {
		t.Fatalf("second Read: want timeout error, got nil")
	}
}

// roundTripFunc adapta una función a http.RoundTripper, para poder inyectar
// errores de transporte sintéticos sin un servidor real.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// TestNonRetryableCertificateErrorStopsAfterOneAttempt verifica que un error
// de transporte clasificado como NO reintentable no consume ningún
// reintento. Antes usaba context.Canceled como el error "no reintentable",
// pero networkerrors.Classify traduce context.Canceled a kindOther, cuyo
// caso por defecto en classifyDesc pone IsRetryable=true (ver
// internal/networkerrors/classify.go) — esa premisa era falsa y la única
// aserción (err == nil) no lo notaba, porque pasaba igual tras 1 intento que
// tras maxRetries con sleeps de por medio.
//
// El caso real no-reintentable más simple de classify.go es un error TLS/
// certificado (rama SSL de classifyConnectError, IsRetryable=false, 502).
// *tls.CertificateVerificationError es justo el tipo que
// networkerrors.translate() reconoce explícitamente. Se inyecta vía un
// http.RoundTripper falso (sin red real) y se cuenta cuántas veces se llama,
// para verificar que el retry loop corta al primer intento.
func TestNonRetryableCertificateErrorStopsAfterOneAttempt(t *testing.T) {
	var calls int
	rt := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		return nil, &tls.CertificateVerificationError{Err: errors.New("bad certificate")}
	})

	clock := &fakeClock{}
	c := &Client{
		cfg:        testConfig(),
		httpClient: &http.Client{Transport: rt},
		clock:      clock,
	}
	tp := &mockTokenProvider{token: "tok"}

	req := newPOSTRequest(t, "https://kiro.invalid/x")
	_, err := c.RequestWithRetry(context.Background(), req, tp, false)
	if err == nil {
		t.Fatalf("RequestWithRetry: want error for a non-retryable TLS error, got nil")
	}
	var reqErr *RequestError
	if !errors.As(err, &reqErr) {
		t.Fatalf("err = %v (%T), want *RequestError", err, err)
	}
	if reqErr.Info.IsRetryable {
		t.Fatalf("Info.IsRetryable = true, want false (certificate errors are not retryable)")
	}
	if calls != 1 {
		t.Fatalf("RoundTrip calls = %d, want 1 (a non-retryable error must not retry)", calls)
	}
	if got := clock.recorded(); len(got) != 0 {
		t.Fatalf("sleeps = %v, want none (no retry means no backoff wait)", got)
	}
}

// TestTransportSettings verifica los ajustes exactos del Transport contra
// .upstream/kiro/http_client.py:79-150 (ver §6.9 del spec).
func TestTransportSettings(t *testing.T) {
	c := newTestClient(t, testConfig(), &fakeClock{})

	tr := c.transport
	if tr.MaxIdleConns != 100 {
		t.Errorf("MaxIdleConns = %d, want 100", tr.MaxIdleConns)
	}
	if tr.MaxIdleConnsPerHost != 20 {
		t.Errorf("MaxIdleConnsPerHost = %d, want 20", tr.MaxIdleConnsPerHost)
	}
	if tr.IdleConnTimeout != 30*time.Second {
		t.Errorf("IdleConnTimeout = %v, want 30s", tr.IdleConnTimeout)
	}
	if tr.ForceAttemptHTTP2 {
		t.Errorf("ForceAttemptHTTP2 = true, want false")
	}
	if tr.TLSNextProto == nil || len(tr.TLSNextProto) != 0 {
		t.Errorf("TLSNextProto = %v, want a non-nil empty map (disables HTTP/2 upgrade)", tr.TLSNextProto)
	}
	if tr.DialContext == nil {
		t.Errorf("DialContext is nil, want 30s-timeout dialer")
	}
	if c.httpClient.Timeout != 0 {
		t.Errorf("httpClient.Timeout = %v, want 0 (no global timeout, it would cut streams)", c.httpClient.Timeout)
	}
	if d := newDialer(); d.Timeout != connectTimeout {
		t.Errorf("dialer timeout = %v, want %v", d.Timeout, connectTimeout)
	}
}

// TestClose verifica que Close() no falla y es idempotente.
func TestClose(t *testing.T) {
	c, err := newClientWithClock(testConfig(), &fakeClock{})
	if err != nil {
		t.Fatalf("newClientWithClock: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}
