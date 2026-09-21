// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package convertersopenai es el port de kiro/converters_openai.py
// (jwadow/kiro-gateway, fijado en el commit a5292ca): el adaptador que
// traduce el dialecto de la API OpenAI Chat Completions (/v1/chat/completions)
// al formato unificado que construyó internal/converterscore (Tasks 2-8), y
// de ahí al payload de la API de Kiro vía converterscore.BuildKiroPayload.
//
// Verificado contra los 141 casos golden de testdata/converters_openai,
// repartidos en cinco objetivos: build_kiro_payload (38, el integrador),
// convert_openai_messages_to_unified (31), convert_openai_tools_to_unified
// (25), extract_thinking_config_from_openai (37) y reasoning_effort_to_budget
// (10).
//
// # Desviaciones de la firma del brief, verificadas contra el upstream real
//
// El brief de la fase 3 describía dos formas que NO sobreviven la
// verificación contra .upstream/kiro/converters_openai.py — la misma familia
// de correcciones que ya documentaron los informes de Task 6, 8 y 9 para sus
// respectivas tareas:
//
//  1. BuildKiroPayload: el brief proponía "(req
//     *modelsopenai.ChatCompletionRequest, cfg *config.Config)". El original
//     (.upstream/kiro/converters_openai.py:393-397) declara
//     `def build_kiro_payload(request_data, conversation_id: str, profile_arn:
//     str) -> dict` — dos parámetros adicionales normales, ni rastro de un
//     objeto de configuración. El corpus confirma la forma posicional exacta:
//     los 38 casos de testdata/converters_openai/build_kiro_payload graban
//     input.args = [request_dict, conversation_id, profile_arn]. Esta
//     implementación sigue al upstream y al patrón que ya fijaron Task 8
//     (converterscore.BuildKiroPayload) y Task 9 (AnthropicToKiro):
//     conversationID y profileArn como parámetros explícitos, sin cfg.
//     internal/config no se importa desde este paquete.
//
//  2. ReasoningEffortToBudget: el brief describía una tabla de cuatro
//     entradas (low=0.2, medium=0.5, high=0.8, none=0, con un "default =
//     medium" para cualquier otro valor). El original
//     (.upstream/kiro/converters_openai.py:320-328) indexa un dict de SEIS
//     entradas directamente con `percent[effort]`, sin `.get()` ni fallback:
//     none=0.0, minimal=0.10, low=0.20, medium=0.50, high=0.80, xhigh=0.95.
//     No hay ningún "default = medium" en el original — una clave fuera de
//     esas seis lanzaría KeyError sin capturar. El corpus (10 casos) solo
//     ejercita las seis claves válidas, así que el comportamiento ante una
//     clave desconocida no tiene cobertura golden. Ver el comentario de
//     ReasoningEffortToBudget para la decisión de manejo de ese caso.
package convertersopenai

import (
	"encoding/json"
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
)

// ==================================================================================================
// Resolución de model ID (kiro.model_resolver)
// ==================================================================================================
//
// build_kiro_payload (.upstream/kiro/converters_openai.py:423) calcula el
// model_id que manda a Kiro con `get_model_id_for_kiro(request_data.model,
// HIDDEN_MODELS)`, importado de kiro.model_resolver (no de
// converters_openai.py) — exactamente la misma función que usa el adaptador
// Anthropic (Task 9). Esa resolución ahora delega en
// modelresolver.GetModelIDForKiro (internal/modelresolver/resolver.go:170),
// el port literal de get_model_id_for_kiro, en vez de mantener una copia
// local del subconjunto normalizeModelName + getModelIDForKiro.
//
// HiddenModels es el equivalente de HIDDEN_MODELS (kiro.config, global de
// módulo que converters_openai.py importa con `from kiro.config import
// HIDDEN_MODELS`). Los 38 casos de testdata/converters_openai/build_kiro_payload
// fijan HIDDEN_MODELS={} (verificado por grep sobre todo el corpus): ningún
// caso ejercita una resolución de modelo oculto, así que esta variable no
// tiene cobertura golden más allá de "el mapa vacío no cambia nada" — se
// mantiene de todos modos por fidelidad al mecanismo real.
var HiddenModels = map[string]string{}

// ==================================================================================================
// Extractores puros de mensajes
// ==================================================================================================

// decodeOpenAIContent decodifica el `content` crudo de un ChatMessage a
// `any` genérico, para poder pasarlo a los extractores de converterscore.
// Ausente (raw vacío, campo con omitempty que no llegó) o JSON null
// decodifican los dos a nil — igual que el Optional[...] = None del original
// para ambos casos (Pydantic no distingue "ausente" de "None" para un campo
// Optional sin otro default).
func decodeOpenAIContent(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}

// extractToolResultsFromOpenAI extrae los bloques tool_result de un
// `content` de mensaje user en formato OpenAI. Port literal de
// kiro.converters_openai._extract_tool_results_from_openai
// (.upstream/kiro/converters_openai.py:55-76).
//
// Devuelve una lista vacía (nunca nil) si content no es una lista. Cada
// bloque contribuye solo si item["type"] == "tool_result" (item mapa). El
// tool_use_id se copia tal cual, con "" si falta la clave o no es cadena. El
// contenido del resultado (item["content"], "" si falta la clave) se reduce
// a texto vía converterscore.ExtractTextContent; el resultado vacío cae al
// placeholder "(empty result)".
func extractToolResultsFromOpenAI(content any) []map[string]any {
	results := []map[string]any{}

	items, ok := content.([]any)
	if !ok {
		return results
	}

	for _, itemAny := range items {
		item, ok := itemAny.(map[string]any)
		if !ok {
			continue
		}
		if itemType, _ := item["type"].(string); itemType != "tool_result" {
			continue
		}

		toolUseID, _ := item["tool_use_id"].(string)
		text := converterscore.ExtractTextContent(item["content"])
		if text == "" {
			text = "(empty result)"
		}

		results = append(results, map[string]any{
			"type":        "tool_result",
			"tool_use_id": toolUseID,
			"content":     text,
		})
	}

	return results
}

// extractToolCallsFromOpenAI extrae los tool_calls de un mensaje assistant
// en formato OpenAI. Port literal de
// kiro.converters_openai._extract_tool_calls_from_openai
// (.upstream/kiro/converters_openai.py:114-138).
//
// Devuelve una lista vacía (nunca nil) si msg.ToolCalls está vacío. Cada
// entrada de msg.ToolCalls es JSON crudo (modelsopenai.ChatMessage.ToolCalls
// es []json.RawMessage): se decodifica a un mapa suelto, y se descarta en
// silencio si no decodifica a un objeto JSON — mismo criterio que
// `isinstance(tc, dict)` en el original. id y function.name se copian con
// "" por defecto; function.arguments se copia con el literal de cadena "{}"
// por defecto (el default real de `tc.get("function", {}).get("arguments",
// "{}")`, aplicable tanto si falta "function" como si falta "arguments"
// dentro de él).
func extractToolCallsFromOpenAI(msg modelsopenai.ChatMessage) []map[string]any {
	toolCalls := []map[string]any{}

	for _, raw := range msg.ToolCalls {
		var tc map[string]any
		if err := json.Unmarshal(raw, &tc); err != nil || tc == nil {
			continue
		}

		funcObj, _ := tc["function"].(map[string]any)

		var arguments any = "{}"
		if v, ok := funcObj["arguments"]; ok {
			arguments = v
		}
		name, _ := funcObj["name"].(string)
		id, _ := tc["id"].(string)

		toolCalls = append(toolCalls, map[string]any{
			"id":   id,
			"type": "function",
			"function": map[string]any{
				"name":      name,
				"arguments": arguments,
			},
		})
	}

	return toolCalls
}

// ConvertOpenAIMessagesToUnified convierte una lista de mensajes en formato
// OpenAI Chat Completions al formato unificado, extrayendo por el camino el
// system prompt. Port literal de
// kiro.converters_openai.convert_openai_messages_to_unified
// (.upstream/kiro/converters_openai.py:141-249), verificado contra los 31
// casos de testdata/converters_openai/convert_openai_messages_to_unified.
//
// # Firma
//
// Devuelve (systemPrompt string, unified []converterscore.UnifiedMessage) —
// una tupla, a diferencia de convertersanthropic.ConvertAnthropicMessages
// (Task 9, que NO devuelve el system prompt: en Anthropic el system prompt
// llega por un campo aparte de la request, no dentro de la lista de
// mensajes). El corpus lo confirma: los 31 casos de
// testdata/converters_openai/convert_openai_messages_to_unified graban
// "output" como una lista JSON de dos elementos [system_prompt, messages].
//
// # Algoritmo (verificado línea a línea contra el original)
//
//  1. Primera pasada: cada mensaje con role=="system" aporta
//     ExtractTextContent(content)+"\n" al system prompt acumulado (se
//     concatenan varios mensajes system, en orden); el resto pasa a
//     non_system_messages sin tocar. El resultado final se recorta con
//     strings.TrimSpace (el .strip() de Python).
//  2. Segunda pasada sobre non_system_messages, con un buffer
//     pendingToolResults/pendingToolImages:
//     - role=="tool": NO se procesa como mensaje propio. Se acumula un
//     tool_result (tool_use_id=msg.ToolCallID o "", content=texto extraído
//     o "(empty result)" si vacío) y, si el content del mensaje tool es una
//     lista, las imágenes que contenga (p.ej. capturas de pantalla de una
//     tool MCP como browsermcp) vía converterscore.ExtractImagesFromContent.
//     - cualquier otro role: si hay tool_results pendientes, se vuelcan
//     PRIMERO como un mensaje "user" sintético (content="",
//     tool_results=pendientes, images=pendientes si las hay), y el buffer se
//     limpia. Luego se procesa el mensaje normal: content siempre sale de
//     ExtractTextContent; para role=="assistant", tool_calls sale de
//     extractToolCallsFromOpenAI (nil si vacío); para role=="user",
//     tool_results sale de extractToolResultsFromOpenAI sobre el content
//     decodificado (nil si vacío) e images de
//     converterscore.ExtractImagesFromContent sobre el mismo content (nil si
//     vacío); cualquier otro role dejando ambos en nil, igual que el
//     if/elif del original.
//  3. Al final del bucle, si quedan tool_results pendientes sin volcar
//     (mensajes tool al final de la lista), se vuelcan con el mismo mensaje
//     "user" sintético.
func ConvertOpenAIMessagesToUnified(msgs []modelsopenai.ChatMessage) (string, []converterscore.UnifiedMessage) {
	var systemPromptBuilder strings.Builder
	nonSystem := make([]modelsopenai.ChatMessage, 0, len(msgs))

	for _, msg := range msgs {
		if msg.Role == "system" {
			systemPromptBuilder.WriteString(converterscore.ExtractTextContent(decodeOpenAIContent(msg.Content)))
			systemPromptBuilder.WriteString("\n")
		} else {
			nonSystem = append(nonSystem, msg)
		}
	}
	systemPrompt := strings.TrimSpace(systemPromptBuilder.String())

	processed := make([]converterscore.UnifiedMessage, 0, len(nonSystem))
	var pendingToolResults []map[string]any
	var pendingToolImages []map[string]any

	flushPending := func() {
		if len(pendingToolResults) == 0 {
			return
		}
		trCopy := append([]map[string]any(nil), pendingToolResults...)
		var imgCopy []map[string]any
		if len(pendingToolImages) > 0 {
			imgCopy = append([]map[string]any(nil), pendingToolImages...)
		}
		processed = append(processed, converterscore.UnifiedMessage{
			Role:        "user",
			Content:     "",
			ToolResults: trCopy,
			Images:      imgCopy,
		})
		pendingToolResults = nil
		pendingToolImages = nil
	}

	for _, msg := range nonSystem {
		if msg.Role == "tool" {
			content := decodeOpenAIContent(msg.Content)

			toolCallID := ""
			if msg.ToolCallID != nil {
				toolCallID = *msg.ToolCallID
			}
			text := converterscore.ExtractTextContent(content)
			if text == "" {
				text = "(empty result)"
			}
			pendingToolResults = append(pendingToolResults, map[string]any{
				"type":        "tool_result",
				"tool_use_id": toolCallID,
				"content":     text,
			})

			if imgs := converterscore.ExtractImagesFromContent(content); len(imgs) > 0 {
				pendingToolImages = append(pendingToolImages, imgs...)
			}
			continue
		}

		flushPending()

		content := decodeOpenAIContent(msg.Content)
		textContent := converterscore.ExtractTextContent(content)

		var toolCalls, toolResults, images []map[string]any
		switch msg.Role {
		case "assistant":
			if tc := extractToolCallsFromOpenAI(msg); len(tc) > 0 {
				toolCalls = tc
			}
		case "user":
			if tr := extractToolResultsFromOpenAI(content); len(tr) > 0 {
				toolResults = tr
			}
			if imgs := converterscore.ExtractImagesFromContent(content); len(imgs) > 0 {
				images = imgs
			}
		}

		processed = append(processed, converterscore.UnifiedMessage{
			Role:        msg.Role,
			Content:     textContent,
			ToolCalls:   toolCalls,
			ToolResults: toolResults,
			Images:      images,
		})
	}

	flushPending()

	return systemPrompt, processed
}
