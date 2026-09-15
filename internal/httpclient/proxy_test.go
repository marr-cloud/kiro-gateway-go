// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package httpclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestResolveProxyURLPrependsScheme verifica que VPN_PROXY_URL sin esquema se
// interpreta como http://, y que un esquema explícito se respeta.
func TestResolveProxyURLPrependsScheme(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"sin esquema", "proxy.example.com:8080", "http://proxy.example.com:8080"},
		{"con esquema http", "http://proxy.example.com:8080", "http://proxy.example.com:8080"},
		{"con esquema socks5", "socks5://proxy.example.com:1080", "socks5://proxy.example.com:1080"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := testConfig()
			cfg.VPNProxyURL = c.raw
			u, err := resolveProxyURL(cfg)
			if err != nil {
				t.Fatalf("resolveProxyURL(%q): %v", c.raw, err)
			}
			if u == nil {
				t.Fatalf("resolveProxyURL(%q) = nil, want %q", c.raw, c.want)
			}
			if got := u.String(); got != c.want {
				t.Errorf("resolveProxyURL(%q) = %q, want %q", c.raw, got, c.want)
			}
		})
	}
}

// TestResolveProxyURLFallsBackToEnv verifica que, sin VPN_PROXY_URL, se
// respetan HTTPS_PROXY/HTTP_PROXY/ALL_PROXY del entorno.
func TestResolveProxyURLFallsBackToEnv(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "http://from-env:3128")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("NO_PROXY", "")

	cfg := testConfig()
	u, err := resolveProxyURL(cfg)
	if err != nil {
		t.Fatalf("resolveProxyURL: %v", err)
	}
	if u == nil || u.String() != "http://from-env:3128" {
		t.Fatalf("resolveProxyURL = %v, want http://from-env:3128", u)
	}
}

// TestResolveProxyURLNoneConfigured verifica que sin VPN_PROXY_URL ni env
// vars de proxy, no hay proxy (nil, nil).
func TestResolveProxyURLNoneConfigured(t *testing.T) {
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("ALL_PROXY", "")
	t.Setenv("NO_PROXY", "")

	cfg := testConfig()
	u, err := resolveProxyURL(cfg)
	if err != nil {
		t.Fatalf("resolveProxyURL: %v", err)
	}
	if u != nil {
		t.Fatalf("resolveProxyURL = %v, want nil", u)
	}
}

// TestNoProxyHostsIncludesDefaultsWhenVPNProxySet verifica que 127.0.0.1 y
// localhost se añaden a la lista de bypass SOLO cuando VPN_PROXY_URL está
// configurado, replicando main.py:187-205 (test_vpn_proxy_environment_setup
// del upstream: los defaults son un efecto secundario de fijar VPN_PROXY_URL,
// no de tener NO_PROXY a secas).
func TestNoProxyHostsIncludesDefaultsWhenVPNProxySet(t *testing.T) {
	t.Setenv("NO_PROXY", "internal.example.com, .corp.example.com")

	cfg := testConfig()
	cfg.VPNProxyURL = "http://proxy.example.com:8080"
	hosts := noProxyHosts(cfg)
	// noProxyHosts conserva las entradas de NO_PROXY tal cual (incluido un
	// posible "." de prefijo); shouldBypass es quien normaliza ese prefijo
	// al comparar (ver TestShouldBypass).
	want := map[string]bool{"127.0.0.1": false, "localhost": false, "internal.example.com": false, ".corp.example.com": false}
	for _, h := range hosts {
		if _, ok := want[h]; ok {
			want[h] = true
		}
	}
	for h, found := range want {
		if !found {
			t.Errorf("noProxyHosts() = %v, missing %q", hosts, h)
		}
	}
}

// TestNoProxyHostsNoDefaultsWithoutVPNProxyURL verifica que, sin
// VPN_PROXY_URL, NO_PROXY se respeta tal cual sin inyectar 127.0.0.1/
// localhost (upstream: test_empty_vpn_proxy_url_does_not_set_variables).
func TestNoProxyHostsNoDefaultsWithoutVPNProxyURL(t *testing.T) {
	t.Setenv("NO_PROXY", "internal.example.com")

	cfg := testConfig() // VPNProxyURL == ""
	hosts := noProxyHosts(cfg)
	for _, h := range hosts {
		if h == "127.0.0.1" || h == "localhost" {
			t.Errorf("noProxyHosts() = %v, must not inject %q when VPN_PROXY_URL is empty", hosts, h)
		}
	}
}

// TestShouldBypass cubre coincidencia exacta y de subdominio.
func TestShouldBypass(t *testing.T) {
	noProxy := []string{"127.0.0.1", "localhost", "corp.example.com"}
	cases := []struct {
		host string
		want bool
	}{
		{"127.0.0.1:8080", true},
		{"localhost:9000", true},
		{"corp.example.com", true},
		{"api.corp.example.com", true},
		{"kiro.dev", false},
		{"evilcorp.example.com", false},
	}
	for _, c := range cases {
		if got := shouldBypass(c.host, noProxy); got != c.want {
			t.Errorf("shouldBypass(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

// TestClientRoutesThroughHTTPProxy comprueba, de punta a punta, que cuando
// VPN_PROXY_URL apunta a un httptest.Server, las peticiones a un host que NO
// resuelve DNS igualmente llegan, porque pasan por el proxy.
func TestClientRoutesThroughHTTPProxy(t *testing.T) {
	var gotHost string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("via-proxy"))
	}))
	defer proxy.Close()

	cfg := testConfig()
	cfg.VPNProxyURL = proxy.URL

	c := newTestClient(t, cfg, &fakeClock{})
	tp := &mockTokenProvider{token: "tok"}

	req := newPOSTRequest(t, "http://fake-kiro.invalid/GenerateAssistantResponse")
	resp, err := c.RequestWithRetry(context.Background(), req, tp, false)
	if err != nil {
		t.Fatalf("RequestWithRetry through proxy: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gotHost != "fake-kiro.invalid" {
		t.Fatalf("proxy saw Host = %q, want fake-kiro.invalid (request must reach the proxy verbatim)", gotHost)
	}
}

// TestClientBypassesProxyForLocalhost comprueba que, con un proxy configurado
// pero inalcanzable, una petición a 127.0.0.1 igual se completa porque
// 127.0.0.1 siempre se añade a NO_PROXY.
func TestClientBypassesProxyForLocalhost(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	cfg := testConfig()
	// Puerto que nadie escucha: si la petición pasara por el proxy, fallaría.
	cfg.VPNProxyURL = "http://127.0.0.1:1"

	c := newTestClient(t, cfg, &fakeClock{})
	tp := &mockTokenProvider{token: "tok"}

	req := newPOSTRequest(t, target.URL)
	resp, err := c.RequestWithRetry(context.Background(), req, tp, false)
	if err != nil {
		t.Fatalf("RequestWithRetry to 127.0.0.1 with broken proxy configured: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

// TestClientSOCKS5ProxyIsUsedForDialing comprueba, a nivel de Transport, que
// un VPN_PROXY_URL socks5:// desvía el DialContext (no usa Transport.Proxy
// HTTP): al apuntar el proxy SOCKS5 a un puerto cerrado, marcar el dial de un
// host NO excluido por NO_PROXY debe fallar, porque intenta pasar por el
// proxy roto en vez de conectar directo.
func TestClientSOCKS5ProxyIsUsedForDialing(t *testing.T) {
	cfg := testConfig()
	cfg.VPNProxyURL = "socks5://127.0.0.1:1" // puerto cerrado

	c := newTestClient(t, cfg, &fakeClock{})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// example.com no está en la lista de bypass (que solo contiene
	// 127.0.0.1/localhost), así que este dial debe intentar el SOCKS5 roto.
	_, err := c.transport.DialContext(ctx, "tcp", "example.com:80")
	if err == nil {
		t.Fatalf("DialContext: want error dialing through the broken SOCKS5 proxy, got nil")
	}
}
