// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/mcptools"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
)

// webSearchToolDescription y webSearchToolInputSchema son los literales
// EXACTOS que routes_anthropic.py:270-277 usa para construir la tool
// sintética de Path B (auto-inject / emulación MCP).
const webSearchToolDescription = "Search the web for current information. Use when you need up-to-date data from the internet."

var webSearchToolInputSchema = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query"}},"required":["query"]}`)

// injectWebSearchTool implementa Path B — WebSearch Support: Auto-Injection
// (MCP Tool Emulation), routes_anthropic.py:255-280: si WEB_SEARCH_ENABLED
// está activo y la petición no trae ya una tool llamada "web_search" (por
// nombre, no por type — routes_anthropic.py:261-264), se le añade una
// sintética con la descripción/schema literales del original. Corre siempre,
// independientemente de si Path A también aplica (mismo orden que el
// original: B se evalúa antes que A).
func (h *Handler) injectWebSearchTool(req *modelsanthropic.AnthropicMessagesRequest) {
	if !h.cfg.WebSearchEnabled {
		return
	}
	for _, tool := range req.Tools {
		if tool.Name == "web_search" {
			return
		}
	}
	desc := webSearchToolDescription
	req.Tools = append(req.Tools, modelsanthropic.AnthropicTool{
		Name:        "web_search",
		Description: &desc,
		InputSchema: webSearchToolInputSchema,
	})
}

// hasNativeWebSearchTool detecta la tool server-side nativa de Anthropic
// (routes_anthropic.py:288-291): cualquier tool cuyo "type" empiece por
// "web_search" (p.ej. "web_search_20250305"). Distinta del chequeo de Path B,
// que mira "name", no "type".
func hasNativeWebSearchTool(tools []modelsanthropic.AnthropicTool) bool {
	for _, tool := range tools {
		if tool.Type != nil && strings.HasPrefix(*tool.Type, "web_search") {
			return true
		}
	}
	return false
}

// handleNativeWebSearch implementa Path A — WebSearch Support: Native
// Anthropic (Early Return), routes_anthropic.py:282-310: si req trae una
// tool nativa web_search, intercepta ANTES del bucle de failover, resuelve la
// query vía la API MCP con la PRIMERA cuenta disponible (get_first_account,
// SIN failover — routes_anthropic.py:294) y escribe la respuesta directamente
// en w. Funciona SIEMPRE, sin importar WEB_SEARCH_ENABLED (routes_anthropic.py:287).
//
// Devuelve true si la petición quedó resuelta — el llamador (Messages) NO
// debe seguir con failoverMessages.
func (h *Handler) handleNativeWebSearch(ctx context.Context, w http.ResponseWriter, req *modelsanthropic.AnthropicMessagesRequest) bool {
	if !hasNativeWebSearchTool(req.Tools) {
		return false
	}

	acc := h.accounts.GetFirstAccount()
	if acc == nil || acc.Auth == nil {
		// routes_anthropic.py:295-306.
		writeAnthropicError(w, http.StatusServiceUnavailable, "api_error", "No initialized accounts available")
		return true
	}

	messages := make([]json.RawMessage, len(req.Messages))
	for i, msg := range req.Messages {
		b, err := json.Marshal(msg)
		if err != nil {
			// Defensivo: AnthropicMessage no tiene MarshalJSON personalizado que
			// pueda fallar en la práctica; sin equivalente en el original (un
			// modelo Pydantic ya validado no falla al reserializarse).
			writeAnthropicError(w, http.StatusInternalServerError, "api_error", "failed to encode message: "+err.Error())
			return true
		}
		messages[i] = b
	}

	outcome := mcptools.HandleNativeWebSearch(ctx, mcptools.NativeWebSearchRequest{
		Messages: messages,
		Model:    req.Model,
		Stream:   req.Stream,
	}, h.mcpHost(acc), acc.Auth, "anthropic")

	writeWebSearchOutcome(w, outcome)
	return true
}

// writeWebSearchOutcome vuelca un mcptools.NativeWebSearchOutcome en w:
// streaming → SSE con las mismas cabeceras que serveStreaming
// (mcp_tools.py:670-674: Cache-Control no-cache, Connection keep-alive);
// no-streaming (éxito o error, mcp_tools.py:613-640 y :753) → JSON único con
// el StatusCode que HandleNativeWebSearch decidió.
func writeWebSearchOutcome(w http.ResponseWriter, outcome mcptools.NativeWebSearchOutcome) {
	if outcome.Streaming {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(outcome.StatusCode)
		_, _ = w.Write(outcome.Body)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(outcome.StatusCode)
	_, _ = w.Write(outcome.Body)
}
