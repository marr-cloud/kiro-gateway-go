// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import (
	"encoding/json"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
)

// webSearchToolDescription y webSearchToolInputSchema son los literales
// EXACTOS que routes_openai.py:257-269 usa para construir la tool sintética
// de Path B (auto-inject / emulación MCP). Ver
// internal/routesanthropic/websearch.go para el mismo mecanismo en el
// dialecto Anthropic (con la única diferencia real: el chequeo de duplicado
// mira tool.type=="function" && tool.function.name=="web_search", no un
// campo "name" plano — routes_openai.py:246-250).
const webSearchToolDescription = "Search the web for current information. Use when you need up-to-date data from the internet."

var webSearchToolInputSchema = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"Search query"}},"required":["query"]}`)

// injectWebSearchTool implementa Path B — WebSearch Support: Auto-Injection
// (MCP Tool Emulation), routes_openai.py:236-272: si WEB_SEARCH_ENABLED está
// activo y la petición no trae ya una tool function llamada "web_search", se
// le añade una sintética con la descripción/schema literales del original.
//
// Path A ausente en el original. routes_openai.py:55 importa
// handle_native_web_search pero NUNCA lo invoca en ningún punto del fichero
// (verificado con una búsqueda exhaustiva del símbolo sobre todo
// routes_openai.py: la única aparición es el import). A diferencia de
// routes_anthropic.py:286-310 (que sí recorre request_data.tools buscando un
// tool.type que empiece por "web_search" antes del bucle de failover), esta
// ruta no tiene ningún camino de detección de tool nativa equivalente — es
// import muerto en el original. Este port replica esa ausencia: NO existe un
// handleNativeWebSearch en este paquete, y ChatCompletions (failover.go)
// nunca hace un early return por web_search.
func (h *Handler) injectWebSearchTool(req *modelsopenai.ChatCompletionRequest) {
	if !h.cfg.WebSearchEnabled {
		return
	}
	for _, tool := range req.Tools {
		if tool.Type == "function" && tool.Function != nil && tool.Function.Name == "web_search" {
			return
		}
	}
	desc := webSearchToolDescription
	req.Tools = append(req.Tools, modelsopenai.Tool{
		Type: "function",
		Function: &modelsopenai.ToolFunction{
			Name:        "web_search",
			Description: &desc,
			Parameters:  webSearchToolInputSchema,
		},
	})
}
