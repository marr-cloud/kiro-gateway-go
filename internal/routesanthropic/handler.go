// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package routesanthropic implementa los endpoints HTTP compatibles con la API
// Anthropic Messages (`POST /v1/messages`, `POST /v1/messages/count_tokens`).
// Port de .upstream/kiro/routes_anthropic.py:1-959, verificado contra el
// original y contra el documento de diseño
// (docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md, §7.1).
//
// Es el gemelo de internal/routesopenai para el dialecto Anthropic: comparte
// exactamente la misma forma de bucle de failover, manejo de errores de
// transporte y bucle explícito D4 de streaming; solo difieren el builder de
// payload (convertersanthropic.AnthropicToKiro), el formatter
// (streaminganthropic con block indexing), el sobre de error y el hecho de que
// el system prompt llega en un campo separado de la petición (no dentro de la
// lista de mensajes como en OpenAI). Ver internal/routesopenai/handler.go para
// los detalles compartidos.
//
// Este paquete NO valida la API key (`x-api-key` / `Authorization: Bearer`):
// eso lo hace el middleware de internal/server (Task 11), que monta
// Handler.Messages y Handler.CountTokens detrás del chequeo
// (verify_anthropic_api_key, routes_anthropic.py:73-117). Tampoco captura
// panics de converterscore (fase 3, tres panic() deliberados) — se propagan al
// middleware de recuperación de Task 11.
//
// # Desviaciones documentadas frente al brief/upstream
//
//  1. Sobre de error unificado Anthropic. Todas las respuestas de error de
//     /v1/messages usan el dialecto Anthropic
//     `{"type":"error","error":{"type":<t>,"message":<m>}}`
//     (routes_anthropic.py:297-365,390-398,556-565,617-653): `api_error` para
//     agotamiento de cuentas / errores de Kiro / internos, `invalid_request_error`
//     para cuerpos mal formados (routes_anthropic.py:390-398).
//  2. Kiro siempre en modo streaming. Igual que routesopenai (punto 3 de su
//     cabecera): `RequestWithRetry(..., true)` incondicional; req.Stream solo
//     decide SSE vs JSON único hacia el cliente.
//  3. Fallos de transporte siempre Recoverable, vía
//     accountmanager.Manager.ReportFailureAs con accounterrors.Recoverable
//     forzado (routes_anthropic.py trata network errors 502/504 como
//     RECOVERABLE explícito, igual que routes_openai.py:512-517). Ver el
//     comentario de ReportFailureAs en internal/accountmanager/failover.go.
//  4. Error a mitad de stream: NO se emite el evento `event: error` que
//     routes_anthropic.py:465-469 produce cuando el generador falla ya
//     empezado el stream. Igual que routesopenai.serveStreaming, este port
//     descarta el error de drivePipeline una vez arrancado el SSE: en ese
//     punto el fallo probable es la desconexión del cliente (w.Write falla) y
//     no hay forma fiable de distinguirlo de un error de lectura de Kiro sin
//     más fontanería, ni nada útil que devolver por una conexión ya rota. Los
//     errores PRE-stream (cuerpo inválido, cuentas agotadas, error Fatal de
//     Kiro) sí emiten el JSON de error Anthropic correcto.
//  5. web_search (Path A / Path B, routes_anthropic.py:262-310,354-468) NO se
//     porta: intercepta tools server-side de Anthropic llamando a una API MCP
//     de red real (kiro.mcp_tools), fuera del alcance de una capa de rutas SSE
//     — misma decisión que Task 8 tomó para el formatter (ver docs/MAPPING.md,
//     fila streaming_anthropic.py). Queda para quien porte mcp_tools.py.
package routesanthropic

import (
	"encoding/json"
	"net/http"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
	"github.com/marr-cloud/kiro-gateway-go/internal/streaminganthropic"
)

// Handler implementa /v1/messages y /v1/messages/count_tokens.
type Handler struct {
	accounts *accountmanager.Manager
	client   *httpclient.Client
	cfg      *config.Config

	// apiURL construye la URL de destino en Kiro para una cuenta dada. Por
	// defecto, acc.Auth.APIHost()+"/generateAssistantResponse" (producción,
	// routes_anthropic.py:409-419). Mismo seam no exportado que
	// routesopenai.Handler.apiURL para que handler_test.go (mismo paquete)
	// pueda apuntar a su httptest.Server; inaccesible desde fuera del paquete.
	apiURL func(acc *accountmanager.Account) string
}

// New construye un Handler. accounts y client deben estar ya inicializados —
// New no hace I/O por sí mismo.
func New(accounts *accountmanager.Manager, client *httpclient.Client, cfg *config.Config) *Handler {
	return &Handler{
		accounts: accounts,
		client:   client,
		cfg:      cfg,
		apiURL: func(acc *accountmanager.Account) string {
			return acc.Auth.APIHost() + "/generateAssistantResponse"
		},
	}
}

// thinkingHandling traduce FAKE_REASONING_HANDLING (cfg) al enum tipado que
// streaminganthropic.New espera. Los valores válidos de config son
// {as_reasoning_content, remove, pass, strip_tags} (config.go / config.py:463-467);
// streaming_anthropic.py:265-324 solo distingue "as_reasoning_content" (bloque
// thinking nativo) de un else que hace strip — su rama "include_as_text" es
// código muerto porque ese valor nunca pasa la validación de config. remove/
// pass/strip_tags ya resuelven el contenido de thinking en el
// streamingcore.Pipeline (a través de thinkingparser) ANTES de que llegue al
// formatter, así que a este nivel no traen eventos "thinking" y mapear a Strip
// (el else de upstream) es fiel. Difiere de routesopenai, cuyo else es
// AsContent (fold a contenido) porque streaming_openai.py:163 sí incluye el
// thinking como texto en su rama else, no lo descarta.
func (h *Handler) thinkingHandling() streaminganthropic.ThinkingHandling {
	if h.cfg.FakeReasoningHandling == string(streaminganthropic.AsReasoningContent) {
		return streaminganthropic.AsReasoningContent
	}
	return streaminganthropic.Strip
}

// writeJSON serializa v como JSON con el status dado.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// anthropicError es el sobre de error del dialecto Anthropic
// (modelsanthropic.AnthropicErrorResponse):
// {"type":"error","error":{"type":<errType>,"message":<message>}}.
type anthropicError struct {
	Type  string             `json:"type"`
	Error anthropicErrorBody `json:"error"`
}

type anthropicErrorBody struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// writeAnthropicError escribe un error en el dialecto Anthropic. errType es
// "api_error" para la mayoría de casos, "invalid_request_error" para cuerpos
// mal formados (routes_anthropic.py:390-398).
func writeAnthropicError(w http.ResponseWriter, status int, errType, message string) {
	writeJSON(w, status, anthropicError{
		Type:  "error",
		Error: anthropicErrorBody{Type: errType, Message: message},
	})
}
