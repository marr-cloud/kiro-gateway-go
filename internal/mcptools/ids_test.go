// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"regexp"
	"testing"
)

// TestNewToolUseIDShape verifica que NewToolUseID produce "srvtoolu_<32 hex>",
// mcp_tools.py:126 (f"srvtoolu_{uuid.uuid4().hex[:32]}"). Comparación por
// FORMA, no por valor: el seam utils.NewHexToken es el punto de inyección
// determinista para fases 4/5, este test solo confirma que mcptools lo usa
// correctamente.
func TestNewToolUseIDShape(t *testing.T) {
	re := regexp.MustCompile(`^srvtoolu_[0-9a-f]{32}$`)
	for i := 0; i < 5; i++ {
		id := NewToolUseID()
		if !re.MatchString(id) {
			t.Fatalf("NewToolUseID() = %q, no matchea %s", id, re.String())
		}
	}
}

// TestNewToolUseIDUnique confirma que llamadas sucesivas no repiten (usan
// crypto/rand real vía utils.NewHexToken por defecto).
func TestNewToolUseIDUnique(t *testing.T) {
	a := NewToolUseID()
	b := NewToolUseID()
	if a == b {
		t.Fatalf("NewToolUseID() devolvió el mismo valor dos veces: %q", a)
	}
}

// TestNewWebSearchRequestIDShape verifica el patrón
// "web_search_tooluse_<22>_<timestamp ms>_<8>" (mcp_tools.py:122-125). El
// brief pide reusar utils.NewHexToken para los dos tramos aleatorios en vez
// del generate_random_id alfanumérico del original (para no introducir una
// fuente de aleatoriedad nueva fuera del seam de test), así que el patrón
// esperado aquí es hex, no base62.
func TestNewWebSearchRequestIDShape(t *testing.T) {
	re := regexp.MustCompile(`^web_search_tooluse_[0-9a-f]{22}_[0-9]+_[0-9a-f]{8}$`)
	id := NewWebSearchRequestID()
	if !re.MatchString(id) {
		t.Fatalf("NewWebSearchRequestID() = %q, no matchea %s", id, re.String())
	}
}
