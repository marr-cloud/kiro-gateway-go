// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package truncationstate

// ToolRecord y ContentRecord son el contrato compartido que el lado SAVE
// (Task 8b, tras cerrar un stream truncado) escribe vía SetTool/SetContent y
// que el lado READ/inject (Task 8a, routesanthropic/routesopenai, al recibir
// la SIGUIENTE petición) lee vía GetTool/GetContent, type-asserting el `any`
// devuelto a estos tipos. Viven aquí (no en un paquete de rutas) porque
// routesanthropic y routesopenai comparten UNA sola *State (server.go) y
// ambos necesitan el mismo contrato para interoperar entre sí y con el save
// side.
//
// Minimalismo deliberado: solo los campos que el lado READ consume
// realmente. dataclasses.ToolTruncationInfo/ContentTruncationInfo
// (truncation_state.py:39-68) también llevan tool_call_id (redundante: ya es
// la clave del mapa) y timestamp (sin uso en el inject — la cache no tiene
// TTL, truncation_state.py:74-75); ninguno de los dos se replica aquí.

// ToolRecord es la información de una llamada a tool truncada, espejo de
// ToolTruncationInfo (truncation_state.py:39-53) sin tool_call_id (clave del
// mapa, ver SetTool/GetTool en cache.go) ni timestamp (no consumido por el
// inject).
type ToolRecord struct {
	// ToolName es el nombre de la tool truncada — kiro.truncation_state.ToolTruncationInfo.tool_name.
	ToolName string
	// TruncationInfo es la información diagnóstica del parser — kiro.truncation_state.ToolTruncationInfo.truncation_info,
	// pasada tal cual a truncationrecovery.GenerateTruncationToolResult (claves esperadas: "size_bytes", "reason").
	TruncationInfo map[string]any
}

// ContentRecord es la información de contenido truncado (salida no-tool),
// espejo de ContentTruncationInfo (truncation_state.py:56-68) sin
// content_preview (solo para debugging en el original, sin uso en el
// inject) ni timestamp. MessageHash se conserva únicamente para logging en
// el lado inject — su EXISTENCIA en la cache (GetContent ok=true) es lo
// único que importa para decidir si se inyecta el aviso de recovery.
type ContentRecord struct {
	// MessageHash es el hash SHA256 (16 hex) del contenido truncado —
	// kiro.truncation_state.ContentTruncationInfo.message_hash. Coincide con
	// el hash que hashContentKey ya computa internamente en SetContent/
	// GetContent (cache.go); se conserva aquí solo para que el lado inject
	// pueda citarlo en logs, igual que routes_anthropic.py:242/routes_openai.py:227.
	MessageHash string
}
