// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package httpclient

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// mockTokenProvider satisface utils.TokenProvider y, opcionalmente,
// forceRefresher (para el camino de 403). accessCalls/refreshCalls dejan que
// los tests verifiquen cuántas veces se llamó a cada método sin necesitar un
// mock framework.
type mockTokenProvider struct {
	mu           sync.Mutex
	token        string
	profileARN   string
	accessCalls  int
	refreshCalls int
}

func (m *mockTokenProvider) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accessCalls++
	return m.token, nil
}

func (m *mockTokenProvider) ProfileARN() string { return m.profileARN }

func (m *mockTokenProvider) ForceRefresh(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refreshCalls++
	m.token = m.token + "-refreshed"
	return m.token, nil
}

func (m *mockTokenProvider) callCounts() (access, refresh int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.accessCalls, m.refreshCalls
}

// fakeClock registra los Sleep() sin bloquear de verdad, para que los tests
// de backoff no tarden segundos reales.
type fakeClock struct {
	mu     sync.Mutex
	sleeps []time.Duration
}

func (f *fakeClock) Now() time.Time { return time.Unix(0, 0) }

func (f *fakeClock) Sleep(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sleeps = append(f.sleeps, d)
}

func (f *fakeClock) recorded() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]time.Duration, len(f.sleeps))
	copy(out, f.sleeps)
	return out
}

func testConfig() *config.Config {
	return &config.Config{
		FirstTokenTimeout:    15,
		FirstTokenMaxRetries: 3,
		StreamingReadTimeout: 300,
	}
}

func newTestClient(t *testing.T, cfg *config.Config, clock Clock) *Client {
	t.Helper()
	c, err := newClientWithClock(cfg, clock)
	if err != nil {
		t.Fatalf("newClientWithClock: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func newPOSTRequest(t *testing.T, url string) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader([]byte(`{"ok":true}`)))
	if err != nil {
		t.Fatalf("http.NewRequest: %v", err)
	}
	return req
}

// TestSimpleOK verifica que una respuesta 200 se devuelve de inmediato, sin
// reintentos ni sleeps.
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

// TestNonRetryableNetworkErrorReturnsImmediately verifica que un error de
// conexión clasificado como no reintentable (aquí, cancelación de contexto)
// no agota reintentos innecesarios.
func TestNonRetryableNetworkErrorReturnsImmediately(t *testing.T) {
	c := newTestClient(t, testConfig(), &fakeClock{})
	tp := &mockTokenProvider{token: "tok"}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := newPOSTRequest(t, "http://127.0.0.1:1/unreachable")
	_, err := c.RequestWithRetry(ctx, req, tp, false)
	if err == nil {
		t.Fatalf("RequestWithRetry: want error for canceled context, got nil")
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
