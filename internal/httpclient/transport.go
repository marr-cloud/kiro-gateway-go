// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package httpclient

import (
	"crypto/tls"
	"net"
	"net/http"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// Ajustes exactos del pool de conexiones, verificados contra
// .upstream/kiro/http_client.py:79-150 y la tabla de equivalencias httpx →
// net/http del spec §6.9:
//
//	httpx max_connections=100          -> MaxIdleConns
//	httpx max_keepalive_connections=20 -> MaxIdleConnsPerHost
//	httpx keepalive_expiry=30s         -> IdleConnTimeout
//	timeout de conexión TCP            -> net.Dialer.Timeout (DialContext)
//
// httpx nunca activa HTTP/2 (no lo negocia en el ALPN de su cliente por
// defecto), así que ForceAttemptHTTP2 se deja en false Y, ADEMÁS, se fija
// TLSNextProto a un map vacío no-nil: sin esto, net/http intentaría
// igualmente un upgrade a HTTP/2 vía ALPN si el servidor lo ofrece,
// ignorando ForceAttemptHTTP2 (que solo controla si el CLIENTE lo propone).
const (
	connectTimeout      = 30 * time.Second
	idleConnTimeout     = 30 * time.Second
	maxIdleConns        = 100
	maxIdleConnsPerHost = 20
)

// newDialer construye el *net.Dialer con el timeout de conexión de 30 s. Se
// expone como función independiente (en vez de un literal inline en
// buildTransport) para que el test de transporte pueda verificar el valor
// exacto sin tener que forzar un dial real de 30 s.
func newDialer() *net.Dialer {
	return &net.Dialer{Timeout: connectTimeout}
}

// buildTransport construye el *http.Transport que usa Client, con el pool de
// conexiones, HTTP/2 desactivado y el proxy resuelto (VPN_PROXY_URL o las
// variables de entorno estándar). No hay Client.Timeout global en ningún
// punto: cortaría los streams (ver §5.5 del spec y el comentario de
// RequestWithRetry en client.go).
func buildTransport(cfg *config.Config) (*http.Transport, error) {
	dialer := newDialer()

	t := &http.Transport{
		MaxIdleConns:        maxIdleConns,
		MaxIdleConnsPerHost: maxIdleConnsPerHost,
		IdleConnTimeout:     idleConnTimeout,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        map[string]func(string, *tls.Conn) http.RoundTripper{},
		DialContext:         dialer.DialContext,
	}

	if err := configureProxy(t, cfg, dialer); err != nil {
		return nil, err
	}
	return t, nil
}
