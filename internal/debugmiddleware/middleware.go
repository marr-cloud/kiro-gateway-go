// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package debugmiddleware es el port de .upstream/kiro/debug_middleware.py
// (§6.13): un middleware GLOBAL que se auto-limita por ruta a las dos rutas
// de generación (LOGGED_ENDPOINTS, :47-50) y, cuando DEBUG_MODE no es "off",
// prepara el *debuglogger.DebugLogger de la petición ANTES de que el handler
// downstream valide el cuerpo — así un 422 también queda registrado
// (:70-114, y el comentario de cabecera del propio fichero original,
// líneas 20-35).
//
// Ruling del controlador de esta tarea: el middleware se monta GLOBAL en
// internal/server.Server.buildHandler (no como wrapper por-ruta del mux),
// exactamente igual que el BaseHTTPMiddleware de Starlette del original, que
// también es global y se auto-limita mirando request.url.path (:82-83).
package debugmiddleware

import (
	"bytes"
	"context"
	"io"
	"net/http"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/debuglogger"
)

// loggerCtxKey: clave de context no exportada bajo la que New inyecta el
// *debuglogger.DebugLogger de la petición. Los handlers/formatters lo
// recuperan con FromContext.
type loggerCtxKey struct{}

// FromContext devuelve el *debuglogger.DebugLogger instalado por el
// middleware, o nil si la petición no pasó por él (rutas fuera de
// LOGGED_ENDPOINTS, o context ajeno a este middleware). Nunca hace panic:
// una aserción de tipo fallida (o ausencia de valor) se trata igual, como
// "no hay logger".
func FromContext(ctx context.Context) *debuglogger.DebugLogger {
	logger, _ := ctx.Value(loggerCtxKey{}).(*debuglogger.DebugLogger)
	return logger
}

// New construye el middleware. Construye UN *debuglogger.DebugLogger
// compartido dentro del closure devuelto (single-flight, replicando el
// singleton de proceso `debug_logger` del original — ver
// .upstream/kiro/debug_logger.py:56-60 — pero closure-captured en vez de
// global mutable de paquete, como exige el brief de esta fase). cfg solo se
// lee aquí, en construcción: el middleware no vuelve a tocar *config.Config
// por petición.
func New(cfg *config.Config) func(http.Handler) http.Handler {
	mode := debuglogger.Mode(cfg.DebugMode)
	logger := debuglogger.New(mode, cfg.DebugDir)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !inScope(r) {
				next.ServeHTTP(w, r)
				return
			}

			ctx := context.WithValue(r.Context(), loggerCtxKey{}, logger)

			if mode != debuglogger.ModeOff {
				ctx = logger.PrepareNewRequest(ctx)

				// Leer el cuerpo completo y volver a envolver r.Body: el
				// original puede llamar a request.body() aquí y el handler
				// downstream lo vuelve a leer sin problema porque Starlette
				// cachea el resultado (:100-105). net/http.Request.Body es
				// un stream de un solo uso, así que hay que reponerlo con
				// un io.NopCloser fresco para que el handler real (y, más
				// adelante, la validación) puedan leerlo de nuevo.
				body, _ := io.ReadAll(r.Body)
				r.Body = io.NopCloser(bytes.NewReader(body))

				// `if body:` del original (:104) — solo loguea si hay
				// contenido.
				if len(body) > 0 {
					logger.LogRequestBody(body)
				}
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// inScope replica el auto-scoping de dispatch (:81-87): LOGGED_ENDPOINTS
// solo tiene las 2 rutas de generación, y ambas son POST en el mux real de
// internal/server (routes_openai.py/routes_anthropic.py no registran otro
// verbo en esos paths) — exigir POST aquí documenta esa restricción en vez
// de depender implícitamente del mux para no procesar, p.ej., un GET a la
// misma ruta si algún día se monta.
func inScope(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	switch r.URL.Path {
	case "/v1/chat/completions", "/v1/messages":
		return true
	default:
		return false
	}
}
