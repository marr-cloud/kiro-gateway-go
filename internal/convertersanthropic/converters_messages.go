// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package convertersanthropic

import (
	"encoding/json"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelresolver"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
)

// ==================================================================================================
// Conversión de mensajes y tools
// ==================================================================================================

// contentBlocksToAny convierte los bloques de contenido ya tipados de un
// AnthropicMessage ([]modelsanthropic.ContentBlock) a la forma []any de
// mapas sueltos que esperan los extractores de este fichero y
// converterscore.ExtractImagesFromContent — la misma forma en la que llega
// `content` cuando el corpus llama a esos extractores directamente con JSON
// genérico decodificado (ver el comentario de cabecera del paquete sobre por
// qué modelsanthropic.AnthropicMessage.Content nunca es una cadena cruda).
//
// El roundtrip Marshal/Unmarshal por bloque reconstruye exactamente el mapa
// JSON que produciría decodificar el mismo bloque directamente a `any`: cada
// ContentBlock ya lleva sus tags json como claves de wire reales (ver
// blocks.go), y Marshal omite los campos no presentes (omitempty) igual que
// el original nunca los añadió.
func contentBlocksToAny(blocks []modelsanthropic.ContentBlock) []any {
	out := make([]any, 0, len(blocks))
	for _, block := range blocks {
		raw, err := json.Marshal(block)
		if err != nil {
			continue
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			continue
		}
		out = append(out, v)
	}
	return out
}

// ConvertAnthropicMessages convierte una lista de mensajes en formato
// Anthropic al formato unificado. Port literal de
// kiro.converters_anthropic.convert_anthropic_messages
// (.upstream/kiro/converters_anthropic.py:230-294), verificado contra los 22
// casos de testdata/converters_anthropic/convert_anthropic_messages.
//
// # Firma
//
// Devuelve solo []converterscore.UnifiedMessage — SIN system prompt. Ver el
// comentario de cabecera del paquete para la corrección sobre la firma que
// proponía el brief (una tupla con systemPrompt que el original no produce).
//
// # Comportamiento por role
//
//   - "assistant": tool_calls sale de ExtractToolUsesFromAnthropicContent
//     sobre content; tool_results e images quedan nil.
//   - "user": tool_results sale de ExtractToolResultsFromAnthropicContent
//     sobre content. images combina, EN ESTE ORDEN, las imágenes de
//     converterscore.ExtractImagesFromContent(content) (bloques "image" al
//     nivel superior) seguidas de las de ExtractImagesFromToolResults(content)
//     (imágenes anidadas dentro de un tool_result, p. ej. capturas de
//     pantalla de una tool MCP). Si ambas fuentes están vacías, images queda
//     nil (no un slice vacío) — igual que tool_calls/tool_results, refleja
//     el "images if images else None" del original.
//   - cualquier otro role (no debería aparecer en un AnthropicMessage válido,
//     Role solo admite "user"/"assistant" por la spec de la API): ni
//     tool_calls ni tool_results ni images se calculan, igual que el
//     original (las dos ramas if/elif no cubren nada más).
//
// content siempre se calcula (ConvertAnthropicContentToText), sea cual sea
// el role.
func ConvertAnthropicMessages(msgs []modelsanthropic.AnthropicMessage) []converterscore.UnifiedMessage {
	unified := make([]converterscore.UnifiedMessage, 0, len(msgs))

	for _, msg := range msgs {
		content := contentBlocksToAny(msg.Content)
		textContent := ConvertAnthropicContentToText(content)

		var toolCalls, toolResults, images []map[string]any

		switch msg.Role {
		case "assistant":
			if tc := ExtractToolUsesFromAnthropicContent(content); len(tc) > 0 {
				toolCalls = tc
			}
		case "user":
			if tr := ExtractToolResultsFromAnthropicContent(content); len(tr) > 0 {
				toolResults = tr
			}

			imgs := converterscore.ExtractImagesFromContent(content)
			if trImgs := ExtractImagesFromToolResults(content); len(trImgs) > 0 {
				imgs = append(imgs, trImgs...)
			}
			if len(imgs) > 0 {
				images = imgs
			}
		}

		unified = append(unified, converterscore.UnifiedMessage{
			Role:        msg.Role,
			Content:     textContent,
			ToolCalls:   toolCalls,
			ToolResults: toolResults,
			Images:      images,
		})
	}

	return unified
}

// ConvertAnthropicTools convierte una lista de tools en formato Anthropic al
// formato unificado. Port literal de
// kiro.converters_anthropic.convert_anthropic_tools
// (.upstream/kiro/converters_anthropic.py:297-326), verificado contra los 10
// casos de testdata/converters_anthropic/convert_anthropic_tools.
//
// tools vacío o nil → nil (el "if not tools: return None" del original: una
// lista vacía y None son indistinguibles para el bool() de Python, así que
// ambos casos colapsan a la misma rama).
//
// Por tool: Name se copia tal cual. Description es *string en
// modelsanthropic.AnthropicTool (Optional[str] en el original) y string en
// converterscore.UnifiedTool (Task 4): un puntero nil se aplana a "" — la
// misma pérdida de la distinción None/"" que ya documentó Task 4 para
// ProcessToolsWithLongDescriptions (ver su comentario en tools_test.go);
// converters_test.go normaliza a mano el único caso del corpus donde
// importa (78d1cb1e1bbecde5). InputSchema NO se defaultea a un mapa vacío
// cuando falta: tool.InputSchema es json.RawMessage vacío tanto si la clave
// input_schema no llegó como si llegó con valor null (Unmarshal sobre un mapa
// deja el mapa en nil en ambos casos), lo que reproduce fielmente
// input_schema: Optional[Dict[str, Any]] = None del dataclass AnthropicTool
// original — el llamador real de este port (AnthropicToKiro) siempre pasa
// instancias tipadas de AnthropicTool, nunca dicts sueltos, así que la rama
// `isinstance(tool, dict)` del original (con su default `tool.get(
// "input_schema", {})`) no aplica aquí: la rama que sí aplica es el `else`
// (acceso a atributo, sin default). Ninguno de los 10 casos del corpus trae
// input_schema ausente o null, así que esta rama no tiene cobertura golden
// directa — se documenta por fidelidad al original, no porque el corpus la
// ejercite.
func ConvertAnthropicTools(tools []modelsanthropic.AnthropicTool) []converterscore.UnifiedTool {
	if len(tools) == 0 {
		return nil
	}

	unified := make([]converterscore.UnifiedTool, 0, len(tools))
	for _, tool := range tools {
		description := ""
		if tool.Description != nil {
			description = *tool.Description
		}

		var inputSchema map[string]any
		if len(tool.InputSchema) > 0 {
			_ = json.Unmarshal(tool.InputSchema, &inputSchema)
		}

		unified = append(unified, converterscore.UnifiedTool{
			Name:        tool.Name,
			Description: description,
			InputSchema: inputSchema,
		})
	}

	return unified
}

// ==================================================================================================
// Thinking config
// ==================================================================================================

// ExtractThinkingConfigFromAnthropic extrae la configuración de thinking del
// parámetro `thinking` de una AnthropicMessagesRequest. Port literal de
// kiro.converters_anthropic.extract_thinking_config_from_anthropic
// (.upstream/kiro/converters_anthropic.py:329-378), verificado contra los 31
// casos de testdata/converters_anthropic/extract_thinking_config_from_anthropic.
//
// req.Thinking es json.RawMessage (Task 1: Union[Dict[str,Any], None,
// Dict[str,Any]|otro en el original). Reglas, en el orden que comprueba el
// original:
//
//   - Ausente, null, o no decodifica a un objeto JSON (el "not
//     isinstance(request.thinking, dict)" del original): {enabled: true,
//     budget_tokens: nil} — el default.
//   - {"type": "disabled", ...}: {enabled: false, budget_tokens: nil}.
//   - {"type": "enabled", ...}: {enabled: true, budget_tokens:
//     thinking["budget_tokens"]} — budget_tokens se copia TAL CUAL sin
//     comprobación de verdad (el "if budget: logger.debug(...)" del original
//     solo condiciona el log, no el valor devuelto — hasta un budget_tokens
//     de 0 explícito se propagaría, aunque el corpus no ejercita ese caso).
//     Ausente o null → nil (mismo resultado que dict.get(...) devolviendo
//     None).
//   - Cualquier otro "type" (incluido ausente): default, igual que el primer
//     punto.
func ExtractThinkingConfigFromAnthropic(req *modelsanthropic.AnthropicMessagesRequest) converterscore.ThinkingConfig {
	defaultCfg := converterscore.ThinkingConfig{Enabled: true, BudgetTokens: nil}

	if len(req.Thinking) == 0 {
		return defaultCfg
	}

	var thinking map[string]any
	if err := json.Unmarshal(req.Thinking, &thinking); err != nil || thinking == nil {
		return defaultCfg
	}

	thinkingType, _ := thinking["type"].(string)

	switch thinkingType {
	case "disabled":
		return converterscore.ThinkingConfig{Enabled: false, BudgetTokens: nil}
	case "enabled":
		var budget *int
		if raw, ok := thinking["budget_tokens"]; ok {
			if bf, ok := raw.(float64); ok {
				bi := int(bf)
				budget = &bi
			}
		}
		return converterscore.ThinkingConfig{Enabled: true, BudgetTokens: budget}
	default:
		return defaultCfg
	}
}

// ==================================================================================================
// Integrador
// ==================================================================================================

// AnthropicToKiro convierte una AnthropicMessagesRequest al payload de la
// API de Kiro. Port literal de kiro.converters_anthropic.anthropic_to_kiro
// (.upstream/kiro/converters_anthropic.py:429-486), verificado contra los 32
// casos de testdata/converters_anthropic/anthropic_to_kiro.
//
// # Firma
//
// TRES parámetros: req, conversationID, profileArn — NO un *config.Config.
// Ver el comentario de cabecera del paquete para la corrección sobre la
// firma que proponía el brief.
//
// # Orden de la cadena (verificado línea a línea contra el original)
//
//  1. ConvertAnthropicMessages(req.Messages).
//  2. ConvertAnthropicTools(req.Tools).
//  3. ExtractSystemPrompt sobre req.System decodificado a `any` (nil si
//     System está ausente o es el literal JSON null) — a diferencia de
//     ConvertAnthropicMessages, el system prompt SIEMPRE sale de
//     ExtractSystemPrompt sobre request.system, nunca de los mensajes.
//  4. modelID = modelresolver.GetModelIDForKiro(req.Model, HiddenModels) —
//     ver el comentario de cabecera de la sección "Resolución de model ID"
//     más arriba.
//  5. ExtractThinkingConfigFromAnthropic(req).
//  6. converterscore.BuildKiroPayload(unifiedMessages, systemPrompt, modelID,
//     unifiedTools, conversationID, profileArn, thinkingCfg).Payload — el
//     original devuelve result.payload, NO el KiroPayloadResult completo (a
//     diferencia de BuildKiroPayload, que si expone ToolDocumentation): el
//     corpus lo confirma, "output" en los 32 casos de anthropic_to_kiro es
//     directamente el dict de payload, nunca envuelto en
//     {"payload":...,"tool_documentation":...} como sí lo está el corpus de
//     converters_core/build_kiro_payload.
//
// Esta función SÍ devuelve converterscore.KiroPayloadResult completo (con
// ToolDocumentation), no solo el payload: es la firma que fija la interfaz
// del plan de fase 3 y la que permite a un llamador de fase 4/5 acceder a
// ambos campos sin tener que reconstruir el payload; el corpus solo compara
// el campo Payload (ver converters_test.go), así que ToolDocumentation queda
// sin cobertura golden directa aquí — hereda la de
// converterscore.BuildKiroPayload (Task 8).
func AnthropicToKiro(req *modelsanthropic.AnthropicMessagesRequest, conversationID string, profileArn string) converterscore.KiroPayloadResult {
	unifiedMessages := ConvertAnthropicMessages(req.Messages)
	unifiedTools := ConvertAnthropicTools(req.Tools)

	var systemAny any
	if len(req.System) > 0 {
		_ = json.Unmarshal(req.System, &systemAny)
	}
	systemPrompt := ExtractSystemPrompt(systemAny)

	modelID := modelresolver.GetModelIDForKiro(req.Model, HiddenModels)

	thinkingCfg := ExtractThinkingConfigFromAnthropic(req)

	return converterscore.BuildKiroPayload(
		unifiedMessages,
		systemPrompt,
		modelID,
		unifiedTools,
		conversationID,
		profileArn,
		thinkingCfg,
	)
}
