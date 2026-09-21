// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Logging operativo por-petición de internal/server: un middleware que emite
// una línea slog (method/path/status/latencia/bytes) por cada request servida.
// Es independiente del debug logger basado en ficheros (DEBUG_MODE): este va a
// stdout y su verbosidad la controla LOG_LEVEL (parseado en config, cableado en
// cmd/kiro-gateway). Un logger nil desactiva el middleware por completo, de modo
// que los tests que construyen un Server sin logger no emiten ruido.
package server

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder envuelve el http.ResponseWriter para capturar el código de
// estado final y el número de bytes escritos, sin alterar la respuesta.
//
// Reenvía Flush explícitamente: las rutas de streaming hacen
// `w.(http.Flusher)` y llaman a Flush() entre chunks SSE (routesopenai/
// routesanthropic/stream.go). Si este wrapper NO expusiera Flusher, esa
// aserción de tipo fallaría y el streaming se rompería (o haría panic con un
// flusher nil). Por eso Flush() delega en el ResponseWriter subyacente.
type statusRecorder struct {
	http.ResponseWriter
	status  int
	written int
	wrote   bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wrote {
		r.status = code
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	if !r.wrote {
		r.status = http.StatusOK
		r.wrote = true
	}
	n, err := r.ResponseWriter.Write(b)
	r.written += n
	return n, err
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// logMiddleware registra cada petición atendida por `next`. Se monta por fuera
// de auth y de la recuperación de panic (ver buildHandler), así que el status
// que loguea es el DEFINITIVO: incluye los 401 de auth y los 500 de recover.
// Con logger nil devuelve `next` intacto (sin coste ni ruido).
func logMiddleware(logger *slog.Logger, next http.Handler) http.Handler {
	if logger == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		logger.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", rec.status),
			slog.Int64("dur_ms", time.Since(start).Milliseconds()),
			slog.Int("bytes", rec.written),
		)
	})
}
