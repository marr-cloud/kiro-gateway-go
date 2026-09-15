// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package converterscore es el port de kiro/converters_core.py
// (jwadow/kiro-gateway, fijado en el commit a5292ca): la lógica compartida
// que usan los adaptadores OpenAI y Anthropic para pasar de sus formatos
// respectivos a un formato unificado y de ahí al payload de la API de Kiro.
//
// Esta fase (Task 2) traduce las cuatro dataclasses del original y los
// cuatro extractores puros — extract_text_content, extract_images_from_content,
// extract_tool_results_from_content y extract_tool_uses_from_message — cuya
// paridad se verifica contra los 339 casos golden de testdata/converters_core.
package converterscore

// ThinkingConfig es la configuración unificada de "thinking" (razonamiento
// falso) que construyen los adaptadores específicos de cada API y que la capa
// core usa para decidir si inyecta las etiquetas de thinking.
//
// Corresponde a kiro.converters_core.ThinkingConfig. BudgetTokens es un
// puntero porque None (sin presupuesto explícito, usar el default de
// configuración) y 0 (presupuesto explícito de cero) son valores distintos en
// el original.
type ThinkingConfig struct {
	Enabled      bool
	BudgetTokens *int
}

// UnifiedMessage es el formato de mensaje unificado, independiente de API,
// que sirve de representación canónica antes de convertir al payload de
// Kiro. Corresponde a kiro.converters_core.UnifiedMessage.
//
// Content acepta cualquier forma que traiga el mensaje ya decodificado de
// JSON: una cadena, una lista de bloques de contenido, o nil. Images sigue el
// formato unificado que describe extract_images_from_content:
// [{"media_type": "image/jpeg", "data": "base64..."}].
type UnifiedMessage struct {
	Role        string
	Content     any
	ToolCalls   []map[string]any
	ToolResults []map[string]any
	Images      []map[string]any
}

// UnifiedTool es el formato de herramienta unificado, independiente de API.
// Corresponde a kiro.converters_core.UnifiedTool.
type UnifiedTool struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// KiroPayloadResult es el resultado de construir el payload de Kiro: el
// payload en sí más la documentación de herramientas cuya descripción excedía
// el límite y se movió al system prompt. Corresponde a
// kiro.converters_core.KiroPayloadResult.
type KiroPayloadResult struct {
	Payload           map[string]any
	ToolDocumentation string
}
