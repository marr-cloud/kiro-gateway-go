// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	"golang.org/x/net/proxy"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// Resolución de proxy. El original NO hace esto en http_client.py: lo hace
// main.py (líneas 181-205), que ANTES de crear ningún cliente httpx muta
// os.environ (HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/NO_PROXY) a partir de
// VPN_PROXY_URL, y deja que httpx recoja esas variables solo por su
// trust_env=True por defecto. Verificado además contra
// .upstream/tests/unit/test_vpn_proxy.py (los 6 casos parametrizados de
// test_vpn_proxy_environment_setup + test_no_proxy_list_merging +
// test_proxy_does_not_affect_local_connections).
//
// Puerto Go: en vez de mutar variables de entorno del proceso (un global
// mutable que el resto del port evita a propósito — ver la restricción
// "Nada de globals mutables nuevos" del plan de fase 4), se calcula
// directamente la función de proxy del *http.Transport a partir de la MISMA
// entrada (VPN_PROXY_URL de config + HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/
// NO_PROXY del entorno), preservando el comportamiento observable:
//
//   - VPN_PROXY_URL no vacía: se normaliza (añade "http://" si falta
//     esquema) y se usa como proxy único para TODO tráfico (igual que
//     main.py fija HTTP_PROXY=HTTPS_PROXY=ALL_PROXY al mismo valor). Se
//     añaden "127.0.0.1" y "localhost" a la lista de bypass (además de lo
//     que ya trajera NO_PROXY).
//   - VPN_PROXY_URL vacía: no se toca nada extra. Se usa la primera variable
//     no vacía entre HTTPS_PROXY, HTTP_PROXY, ALL_PROXY (mayúsculas o
//     minúsculas) tal cual, y NO_PROXY se respeta sin inyectar nada — ver
//     test_empty_vpn_proxy_url_does_not_set_variables, que verifica
//     explícitamente que sin VPN_PROXY_URL no se toca NO_PROXY.
//
// Simplificación deliberada frente al original: httpx (con socksio)
// distingue el proxy por esquema de la URL destino (HTTP_PROXY para
// http://, HTTPS_PROXY para https://, ALL_PROXY como fallback de ambos).
// Aquí se resuelve UN único valor de proxy con esa misma prioridad y se
// aplica a todo el tráfico del Transport, porque el uso real (VPN_PROXY_URL)
// fija los tres a idéntico valor y nunca depende del esquema del destino.
func resolveProxyURL(cfg *config.Config) (*url.URL, error) {
	raw := cfg.VPNProxyURL
	if raw == "" {
		raw = firstNonEmpty(
			os.Getenv("HTTPS_PROXY"), os.Getenv("https_proxy"),
			os.Getenv("HTTP_PROXY"), os.Getenv("http_proxy"),
			os.Getenv("ALL_PROXY"), os.Getenv("all_proxy"),
		)
	}
	if raw == "" {
		return nil, nil
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	return url.Parse(raw)
}

// noProxyHosts calcula la lista de hosts que deben saltarse el proxy. Los
// defaults 127.0.0.1/localhost SOLO se inyectan cuando VPN_PROXY_URL está
// configurada — replica main.py línea a línea (ver el comentario de
// resolveProxyURL) y el test upstream test_empty_vpn_proxy_url_does_not_set_variables.
func noProxyHosts(cfg *config.Config) []string {
	raw := firstNonEmpty(os.Getenv("NO_PROXY"), os.Getenv("no_proxy"))
	var hosts []string
	for _, h := range strings.Split(raw, ",") {
		h = strings.TrimSpace(h)
		if h != "" {
			hosts = append(hosts, h)
		}
	}
	if cfg.VPNProxyURL != "" {
		hosts = append(hosts, "127.0.0.1", "localhost")
	}
	return hosts
}

// shouldBypass decide si host (forma "host" o "host:puerto") debe saltarse
// el proxy según noProxy. Compara por sufijo de dominio además de coincidencia
// exacta, tal como NO_PROXY se interpreta convencionalmente (curl/requests):
// "example.com" en la lista también excluye "api.example.com".
func shouldBypass(host string, noProxy []string) bool {
	h := host
	if i := strings.LastIndex(h, ":"); i >= 0 {
		h = h[:i]
	}
	h = strings.ToLower(h)
	for _, np := range noProxy {
		np = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(np), "."))
		if np == "" {
			continue
		}
		if h == np || strings.HasSuffix(h, "."+np) {
			return true
		}
	}
	return false
}

// configureProxy aplica el proxy resuelto al *http.Transport: SOCKS5 se
// desvía por DialContext (golang.org/x/net/proxy no lo soporta vía
// Transport.Proxy, que solo entiende HTTP/HTTPS); cualquier otro esquema usa
// Transport.Proxy con el filtro de bypass. Sin proxy configurado, no toca
// nada y el Transport queda con el DialContext directo de dialer.
func configureProxy(t *http.Transport, cfg *config.Config, dialer *net.Dialer) error {
	proxyURL, err := resolveProxyURL(cfg)
	if err != nil {
		return fmt.Errorf("proxy inválido (VPN_PROXY_URL o entorno): %w", err)
	}
	if proxyURL == nil {
		return nil
	}
	noProxy := noProxyHosts(cfg)

	if isSOCKS5(proxyURL) {
		socksDialer, err := newSOCKS5Dialer(proxyURL, dialer)
		if err != nil {
			return fmt.Errorf("configurando proxy SOCKS5: %w", err)
		}
		direct := dialer.DialContext
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			if shouldBypass(addr, noProxy) {
				return direct(ctx, network, addr)
			}
			return dialViaSOCKS5(ctx, socksDialer, network, addr)
		}
		return nil
	}

	t.Proxy = func(req *http.Request) (*url.URL, error) {
		if shouldBypass(req.URL.Host, noProxy) {
			return nil, nil
		}
		return proxyURL, nil
	}
	return nil
}

func isSOCKS5(u *url.URL) bool {
	switch u.Scheme {
	case "socks5", "socks5h":
		return true
	default:
		return false
	}
}

func newSOCKS5Dialer(u *url.URL, forward *net.Dialer) (proxy.Dialer, error) {
	var auth *proxy.Auth
	if u.User != nil {
		pw, _ := u.User.Password()
		auth = &proxy.Auth{User: u.User.Username(), Password: pw}
	}
	return proxy.SOCKS5("tcp", u.Host, auth, forward)
}

// dialViaSOCKS5 usa DialContext si el Dialer de golang.org/x/net/proxy lo
// implementa (proxy.ContextDialer); si no, cae a Dial sin propagar ctx —
// golang.org/x/net/proxy.SOCKS5 lo implementa desde hace años, pero el
// fallback evita un panic si esto cambiase.
func dialViaSOCKS5(ctx context.Context, d proxy.Dialer, network, addr string) (net.Conn, error) {
	if cd, ok := d.(proxy.ContextDialer); ok {
		return cd.DialContext(ctx, network, addr)
	}
	return d.Dial(network, addr)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
