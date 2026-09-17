// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Middleware de internal/server: CORS, autenticación por endpoint y
// recuperación de panic (spec §5.4). Se aplican en ese orden (de más
// externo a más interno) en Server.buildHandler.
package server

import (
	"fmt"
	"net/http"
)

// corsMiddleware añade cabeceras CORS permisivas a toda respuesta y corta
// inmediatamente con 200 cualquier petición OPTIONS (preflight), sin pasar
// por auth ni por el mux.
//
// Simplificación deliberada frente a starlette.middleware.cors.CORSMiddleware
// (vendorizado en .upstream/.venv/Lib/site-packages/starlette/middleware/cors.py,
// que main.py:548-554 monta con allow_origins=["*"], allow_credentials=True,
// allow_methods=["*"], allow_headers=["*"]): el original, cuando
// allow_credentials=True, refleja el Origin real de la petición en vez del
// literal "*" para las respuestas de preflight (cors.py:100-101,
// preflight_explicit_allow_origin) y expande allow_methods=["*"] a la lista
// literal de 7 verbos (cors.py:29-30, ALL_METHODS) en vez de un wildcard —
// además de solo activarse si la petición trae cabecera Origin (cors.py:85-87)
// y de exigir Access-Control-Request-Method para tratar un OPTIONS como
// preflight real (cors.py:89-90). El brief de esta tarea (§5.4, punto 1)
// pide expresamente el comportamiento simple: "*" literal en orígenes,
// métodos y cabeceras, credentials:true, y OPTIONS→200 sin condiciones — eso
// es lo que implementa esta función, incondicionalmente y en toda petición
// (no solo las que traen Origin).
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Allow-Methods", "*")
		h.Set("Access-Control-Allow-Headers", "*")
		h.Set("Access-Control-Allow-Credentials", "true")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// dialect identifica qué formato de error (y qué chequeo de API key) exige
// un endpoint, según el path. Port de la tabla de dependencias de
// routes_openai.py (verify_api_key, Authorization: Bearer únicamente) y
// routes_anthropic.py:73-117 (verify_anthropic_api_key, x-api-key O Bearer).
type dialect int

const (
	dialectPublic dialect = iota
	dialectOpenAI
	dialectAnthropic
)

// classify decide el dialect de un path. Los paths fuera de las 6 rutas
// conocidas (incluida cualquier 404 futura) caen en dialectPublic: sin auth,
// y el 500 de panic recovery (poco probable en esas rutas) usa el dialecto
// OpenAI como fallback genérico — ver writeErrorForPath.
func classify(path string) dialect {
	switch path {
	case "/v1/models", "/v1/chat/completions":
		return dialectOpenAI
	case "/v1/messages", "/v1/messages/count_tokens":
		return dialectAnthropic
	default:
		return dialectPublic
	}
}

// authMiddleware verifica la API key según el dialecto del endpoint (spec
// §5.4, punto 2). La cabecera anthropic-version se acepta y nunca se valida
// (spec §7.1): este middleware, y el resto del paquete, no la lee en ningún
// punto — es una ausencia deliberada de código, no un olvido.
func authMiddleware(apiKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch classify(r.URL.Path) {
		case dialectOpenAI:
			if !validBearer(r, apiKey) {
				writeOpenAIError(w, http.StatusUnauthorized, "Invalid or missing API Key")
				return
			}
		case dialectAnthropic:
			if !validAnthropicAuth(r, apiKey) {
				writeAnthropicError(w, http.StatusUnauthorized, "authentication_error",
					"Invalid or missing API key. Use x-api-key header or Authorization: Bearer.")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// validBearer replica verify_api_key (routes_openai.py:68-86): exige
// exactamente "Bearer {PROXY_API_KEY}" en Authorization.
func validBearer(r *http.Request, apiKey string) bool {
	return r.Header.Get("Authorization") == "Bearer "+apiKey
}

// validAnthropicAuth replica verify_anthropic_api_key (routes_anthropic.py:73-112):
// x-api-key primero, Authorization: Bearer como fallback.
//
// El guard `key != ""` en la rama x-api-key replica el chequeo de truthiness
// del original (`if x_api_key and x_api_key == PROXY_API_KEY`,
// routes_anthropic.py:94-96): sin él, una config con PROXY_API_KEY vacío (una
// mala configuración plausible del operador: `PROXY_API_KEY=` en .env) dejaría
// pasar una petición SIN cabecera x-api-key, porque `Header.Get` devuelve "" y
// "" == "" sería true. validBearer no necesita el guard: compara contra el
// literal no vacío "Bearer "+apiKey, así que una cabecera ausente ("") nunca
// coincide aunque apiKey sea "".
func validAnthropicAuth(r *http.Request, apiKey string) bool {
	if key := r.Header.Get("x-api-key"); key != "" && key == apiKey {
		return true
	}
	return r.Header.Get("Authorization") == "Bearer "+apiKey
}

// recoverMiddleware captura los panic() deliberados de converterscore/
// convertersopenai (fase 3, tres en total: payload.go:238,259 y
// convertersopenai/converters.go:522 — equivalentes a ValueError sin hueco
// en la firma Go, ver el comentario de cabecera de routesopenai/handler.go y
// routesanthropic/handler.go) y cualquier otro panic no anticipado, y
// devuelve 500 en el dialecto de error correspondiente al path — spec §5.4,
// punto 3.
func recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				message := fmt.Sprintf("%v", rec)
				if classify(r.URL.Path) == dialectAnthropic {
					writeAnthropicError(w, http.StatusInternalServerError, "api_error", message)
				} else {
					writeOpenAIError(w, http.StatusInternalServerError, message)
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// openAIErrorEnvelope es el sobre de error OpenAI del spec (§7.1):
// {"error":{"message","type":"kiro_api_error","code"}}. Duplicado a
// propósito de routesopenai.openAIErrorEnvelope: ese tipo (y
// writeOpenAIError) no están exportados — el brief de esta tarea pide
// explícitamente NO exportarlos y que el servidor escriba su propio JSON de
// error (ver task-11-brief.md, sección Middleware punto 3).
type openAIErrorEnvelope struct {
	Error openAIErrorDetail `json:"error"`
}

type openAIErrorDetail struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    int    `json:"code"`
}

func writeOpenAIError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, openAIErrorEnvelope{
		Error: openAIErrorDetail{Message: message, Type: "kiro_api_error", Code: status},
	})
}

// anthropicErrorEnvelope es el sobre de error Anthropic del spec (§7.1):
// {"type":"error","error":{"type","message"}}. Duplicado a propósito de
// routesanthropic.anthropicError, misma razón que openAIErrorEnvelope.
type anthropicErrorEnvelope struct {
	Type  string             `json:"type"`
	Error anthropicErrorBody `json:"error"`
}

type anthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func writeAnthropicError(w http.ResponseWriter, status int, errType, message string) {
	writeJSON(w, status, anthropicErrorEnvelope{
		Type:  "error",
		Error: anthropicErrorBody{Type: errType, Message: message},
	})
}
