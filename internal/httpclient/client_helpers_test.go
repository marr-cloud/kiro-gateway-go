// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package httpclient

import (
	"bytes"
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

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

// fakeClock registra los Sleep()/After() sin bloquear de verdad, para que
// los tests de backoff no tarden segundos reales. Ambos métodos comparten el
// mismo registro `sleeps`: da igual qué camino del retry loop se ejerza
// (Sleep con guarda para errores de red/primer-token, After sin guarda para
// 429/5xx), un test que llama a recorded() ve todas las esperas por igual.
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

// After registra la duración pedida y devuelve un canal ya disparado: los
// tests con fakeClock nunca esperan de verdad. Como el canal ya trae un
// valor al devolverse, un select{ case <-ctx.Done(): ...; case <-After(d): }
// con un ctx TODAVÍA no cancelado en el momento de la llamada siempre toma
// la rama de After (ctx.Done() no está listo todavía); ningún test de este
// paquete llama a waitForRetry con un ctx ya cancelado de antemano.
func (f *fakeClock) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	f.sleeps = append(f.sleeps, d)
	f.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- time.Now()
	return ch
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
