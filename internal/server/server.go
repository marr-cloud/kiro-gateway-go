// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package server es el equivalente Go del `lifespan` + registro de rutas de
// FastAPI en .upstream/main.py:319-573: monta el mux HTTP, aplica el
// middleware de CORS/debug-logger/auth/panic-recovery (middleware.go, spec
// §5.4; debugmiddleware montado en buildHandler, Task 5 de la fase 6a) y
// expone los dos endpoints de estado (§7.1). Task 11, la última de la fase
// 5: es el paquete que ata routesopenai (Task 9), routesanthropic (Task 10),
// config (fase 2) y accountmanager/httpclient (fase 4) en un *http.Server
// servible.
//
// # Desviaciones documentadas frente al brief/upstream
//
//  1. version.String() → version.Version(). El brief de esta tarea cita
//     `internal/version.String()`, pero ese paquete (fase 1, ver
//     internal/version/version.go) solo expone `Version()`. No hay
//     `String()` en ningún sitio del repo (verificado por grep). Se usa
//     `version.Version()`, la función real.
//  2. Campos extra de "/" y "/health" (cuenta activa, uptime, modo). NO
//     existen en el upstream real (.upstream/kiro/routes_openai.py:93-120
//     solo devuelve status/message/version y status/timestamp/version) — es
//     una extensión deliberada del spec de este port, no una discrepancia
//     brief-vs-upstream: docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md:742-743
//     dice explícitamente "añade cuenta activa, uptime y modo". `mode` es el
//     literal fijo "ACCOUNT_SYSTEM": este port no implementa el modo legacy
//     de una sola cuenta sin accountmanager que main.py:383-451 sí soporta
//     (ACCOUNT_SYSTEM=false), así que el único modo posible es ese.
//  3. Timestamp de "/health" en RFC3339Nano vía time.Now().UTC(), no el
//     isoformat() de Python (que produce "+00:00" en vez de "Z" y
//     microsegundos de ancho fijo). Mismo layout que ya usa el resto del
//     repo para timestamps (internal/auth/json_source.go:137,
//     internal/accountmanager/state.go:116) — consistencia interna sobre
//     imitar un detalle de formato de Python que ningún cliente parsea.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/debugmiddleware"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
	"github.com/marr-cloud/kiro-gateway-go/internal/routesanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/routesopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/version"
)

// serverMode es el literal fijo del campo "mode" de "/" y "/health" (ver
// desviación 2 del comentario de cabecera).
const serverMode = "ACCOUNT_SYSTEM"

// Server envuelve un *http.Server ya cableado con el mux, el middleware y
// los cuatro endpoints de la API, más los dos de estado.
type Server struct {
	cfg      *config.Config
	accounts *accountmanager.Manager
	http     *http.Server

	mu        sync.RWMutex
	startedAt time.Time

	// Seams de test: los cuatro endpoints de la API montados como campos no
	// exportados, resueltos por el mux en cada petición (no capturados por
	// valor al registrar las rutas) — mismo patrón que
	// routesopenai.Handler.apiURL / routesanthropic.Handler.apiURL (ver esos
	// ficheros): permite que server_test.go (mismo paquete) sustituya un
	// stub en las rutas que golpearían Kiro real por una respuesta
	// httptest.Server 200 fija, sin exponer nada al exterior del paquete y
	// sin tocar red real en ningún test (ver el brief, "For routes that
	// would hit Kiro, you can mount a stub handler").
	openaiModels          http.HandlerFunc
	openaiChatCompletions http.HandlerFunc
	anthropicMessages     http.HandlerFunc
	anthropicCountTokens  http.HandlerFunc
}

// New construye un Server listo para Start. accounts y client deben estar ya
// inicializados por el llamador (LoadCredentials/Initialize corridos) — New
// no hace I/O por sí mismo, igual que routesopenai.New/routesanthropic.New.
func New(cfg *config.Config, accounts *accountmanager.Manager, client *httpclient.Client) *Server {
	s := &Server{cfg: cfg, accounts: accounts}

	openaiHandler := routesopenai.New(accounts, client, cfg)
	anthropicHandler := routesanthropic.New(accounts, client, cfg)

	s.openaiModels = openaiHandler.Models
	s.openaiChatCompletions = openaiHandler.ChatCompletions
	s.anthropicMessages = anthropicHandler.Messages
	s.anthropicCountTokens = anthropicHandler.CountTokens

	s.http = &http.Server{
		Addr:    fmt.Sprintf("%s:%d", cfg.ServerHost, cfg.ServerPort),
		Handler: s.buildHandler(),
	}

	return s
}

// buildHandler registra el mux (endpoints de estado + los cuatro de la API,
// cada uno indirecto vía los campos de Server para que el seam de test
// funcione) y aplica el middleware en el orden del spec §5.4: CORS
// (más externo) → debug logger → auth → panic recovery (más interno, junto
// al mux).
//
// debugmiddleware.New se inserta ENTRE auth y CORS (es decir, la petición lo
// atraviesa ANTES de auth Y ANTES de recoverMiddleware — queda fuera de
// ambos) — ruling del controlador de Task 5 (fase 6a): debug_middleware.py
// es un middleware GLOBAL en el original (Starlette BaseHTTPMiddleware
// montado sobre toda la app, :53) que se auto-limita por ruta mirando
// request.url.path (LOGGED_ENDPOINTS, :47-50, :82-83) y corre ANTES de la
// validación del cuerpo — de ahí que en Python capture también los cuerpos
// que luego fallan con 422. Montarlo antes que authMiddleware replica esa
// misma propiedad "antes de validar" para el caso 401 también, no solo 422.
func (s *Server) buildHandler() http.Handler {
	mux := http.NewServeMux()

	// "GET /{$}" es el patrón de coincidencia EXACTA de "/" en el
	// ServeMux mejorado de Go 1.22+: "GET /" a secas sería un patrón de
	// subárbol que capturaría cualquier ruta sin match explícito (incluidas
	// las 404 que sí queremos).
	mux.HandleFunc("GET /{$}", s.handleRoot)
	mux.HandleFunc("GET /health", s.handleHealth)
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, r *http.Request) { s.openaiModels(w, r) })
	mux.HandleFunc("POST /v1/chat/completions", func(w http.ResponseWriter, r *http.Request) { s.openaiChatCompletions(w, r) })
	mux.HandleFunc("POST /v1/messages", func(w http.ResponseWriter, r *http.Request) { s.anthropicMessages(w, r) })
	mux.HandleFunc("POST /v1/messages/count_tokens", func(w http.ResponseWriter, r *http.Request) { s.anthropicCountTokens(w, r) })

	var h http.Handler = mux
	h = recoverMiddleware(h)
	h = authMiddleware(s.cfg.ProxyAPIKey, h)
	h = debugmiddleware.New(s.cfg)(h)
	h = corsMiddleware(h)
	return h
}

// rootResponse es el JSON de GET / (§7.1): mantiene status/message/version
// de routes_openai.py:101-105 y añade cuenta activa/uptime/modo (desviación
// 2 del comentario de cabecera).
type rootResponse struct {
	Status        string  `json:"status"`
	Message       string  `json:"message"`
	Version       string  `json:"version"`
	ActiveAccount *string `json:"active_account"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	Mode          string  `json:"mode"`
}

// healthResponse es el JSON de GET /health (§7.1): mantiene
// status/timestamp/version de routes_openai.py:116-120 y añade los mismos
// campos extra que rootResponse.
type healthResponse struct {
	Status        string  `json:"status"`
	Timestamp     string  `json:"timestamp"`
	Version       string  `json:"version"`
	ActiveAccount *string `json:"active_account"`
	UptimeSeconds float64 `json:"uptime_seconds"`
	Mode          string  `json:"mode"`
}

func (s *Server) handleRoot(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, rootResponse{
		Status:        "ok",
		Message:       "Kiro Gateway is running",
		Version:       version.Version(),
		ActiveAccount: s.activeAccountID(),
		UptimeSeconds: s.uptimeSeconds(),
		Mode:          serverMode,
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:        "healthy",
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
		Version:       version.Version(),
		ActiveAccount: s.activeAccountID(),
		UptimeSeconds: s.uptimeSeconds(),
		Mode:          serverMode,
	})
}

// activeAccountID devuelve el ID de accounts.GetFirstAccount(), o nil si no
// hay ninguna cuenta cargada (o accounts es nil, defensivo para tests que
// construyen un Server sin Manager).
func (s *Server) activeAccountID() *string {
	if s.accounts == nil {
		return nil
	}
	acc := s.accounts.GetFirstAccount()
	if acc == nil {
		return nil
	}
	id := acc.ID
	return &id
}

// uptimeSeconds devuelve los segundos transcurridos desde Start, o 0 si el
// servidor todavía no arrancó (startedAt en su cero — no debería pasar en
// producción, pero es el valor seguro).
func (s *Server) uptimeSeconds() float64 {
	s.mu.RLock()
	started := s.startedAt
	s.mu.RUnlock()

	if started.IsZero() {
		return 0
	}
	return time.Since(started).Seconds()
}

// Start marca el instante de arranque (base de uptimeSeconds) y bloquea
// sirviendo hasta que ctx se cancela o ListenAndServe falla por sí solo.
// El cierre ordenado no lo hace Start: main.go es responsable de invocar
// Shutdown (con su propio timeout, típicamente en una goroutine que observa
// ctx.Done() en paralelo a esta llamada) — Start solo deja de bloquear
// cuando eso ocurre, sin intentar cerrar nada por su cuenta.
func (s *Server) Start(ctx context.Context) error {
	s.mu.Lock()
	s.startedAt = time.Now()
	s.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.http.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		return nil
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

// Shutdown cierra el servidor de forma ordenada, respetando el deadline de
// ctx. Port del cierre de app.state.http_client en main.py:528-533, pero
// para el propio servidor HTTP en vez del cliente hacia Kiro.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}

// writeJSON serializa v como JSON con el status dado. Igual que
// routesopenai.writeJSON/routesanthropic.writeJSON (duplicado a propósito:
// son funciones no exportadas de otro paquete, ver middleware.go).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
