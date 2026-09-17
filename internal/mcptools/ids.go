// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package mcptools porta el cliente MCP de Kiro para web_search y su
// emulación de SSE — .upstream/kiro/mcp_tools.py (753 líneas), spec §6.14.
package mcptools

import (
	"fmt"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// Generación de identificadores.
//
// mcp_tools.py:56-70 define generate_random_id(length), un alfabeto base62
// (ascii_letters + digits) sobre math/random — SOLO se usa para dos tramos
// de web_search_tooluse_<22>_<ts>_<8> (mcp_tools.py:122,124). El resto de
// funciones de este módulo Python generan IDs directamente vía
// uuid.uuid4().hex (srvtoolu_, mcp_tools.py:126) o uuid.uuid4().hex[:24]
// (msg_, mcp_tools.py:316,710).
//
// El brief de esta tarea pide NO introducir una fuente de aleatoriedad nueva
// en mcptools: el punto de inyección determinista que fases 4/5 ya usan para
// golden tests es utils.NewHexToken (utils/ids.go:43). Así que aquí los tres
// patrones de id se construyen SOLO a partir de ese seam:
//
//   - srvtoolu_<32hex>                     = "srvtoolu_" + utils.NewHexToken()
//   - msg_<24hex>                          = utils.GenerateMessageID() (ya existe, se reusa
//     tal cual en sse.go/websearch.go, sin wrapper aquí)
//   - web_search_tooluse_<22>_<ts ms>_<8>  = utils.NewHexToken()[:22] + "_" + timestamp + "_" + utils.NewHexToken()[:8]
//
// Los dos tramos aleatorios de web_search_tooluse_ pasan de alfabeto base62 a
// hex minúscula: es una divergencia deliberada de FORMA (no de semántica) —
// el brief pide expresamente comparar estos ids por FORMA vía regex, nunca
// por valor, así que el cambio de alfabeto no rompe ningún contrato
// observable (ni el original ni este port comparan el id contra nada
// después de generarlo: viaja en el campo "id" de la petición JSON-RPC y
// nunca se valida en la respuesta).

// NewToolUseID devuelve "srvtoolu_<32 hex>", el tool_use_id que
// CallKiroMCPAPI adjunta a cada resultado exitoso. Puerto de
// mcp_tools.py:126 (f"srvtoolu_{uuid.uuid4().hex[:32]}"; uuid4().hex ya son
// 32 caracteres hex, así que el slice [:32] del original es un no-op).
func NewToolUseID() string {
	return "srvtoolu_" + utils.NewHexToken()
}

// NewWebSearchRequestID devuelve
// "web_search_tooluse_<22hex>_<timestamp ms>_<8hex>", el id de petición
// JSON-RPC que CallKiroMCPAPI manda a Kiro. Puerto de mcp_tools.py:121-125.
// El timestamp usa la hora real (time.Now); no es parte del seam
// determinista porque los tests de forma (regex `[0-9]+`) no necesitan un
// valor fijo, solo un patrón numérico.
func NewWebSearchRequestID() string {
	random22 := utils.NewHexToken()[:22]
	timestampMs := time.Now().UnixMilli()
	random8 := utils.NewHexToken()[:8]
	return fmt.Sprintf("web_search_tooluse_%s_%d_%s", random22, timestampMs, random8)
}
