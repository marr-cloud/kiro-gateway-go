// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package convertersanthropic es el port de kiro/converters_anthropic.py
// (jwadow/kiro-gateway, fijado en el commit a5292ca): el adaptador que
// traduce el dialecto de la API Anthropic Messages (/v1/messages) al formato
// unificado que construyó internal/converterscore (Tasks 2-8), y de ahí al
// payload de la API de Kiro vía converterscore.BuildKiroPayload.
//
// Verificado contra los 251 casos golden de testdata/converters_anthropic,
// repartidos en nueve objetivos: anthropic_to_kiro (32, el integrador),
// convert_anthropic_content_to_text (49), convert_anthropic_messages (22),
// convert_anthropic_tools (10), extract_images_from_tool_results (35),
// extract_system_prompt (12), extract_thinking_config_from_anthropic (31),
// extract_tool_results_from_anthropic_content (39) y
// extract_tool_uses_from_anthropic_content (21).
//
// # Desviaciones de la firma del brief, verificadas contra el upstream real
//
// El brief de la fase 3 describía dos firmas que NO sobreviven la
// verificación contra .upstream/kiro/converters_anthropic.py (el mismo
// patrón que ya documentaron los informes de Task 6 y Task 8 para sus
// respectivas tareas):
//
//  1. ConvertAnthropicMessages: el brief proponía
//     "(systemPrompt string, unified []converterscore.UnifiedMessage)" — una
//     tupla con el system prompt incluido. El original
//     (.upstream/kiro/converters_anthropic.py:246-249) declara
//     `def convert_anthropic_messages(messages) -> List[UnifiedMessage]`: NO
//     hay system prompt en el valor de retorno, ni en ningún punto del cuerpo
//     de la función se toca el system prompt — esa extracción vive
//     enteramente en extract_system_prompt, una función DISTINTA que
//     anthropic_to_kiro invoca por separado sobre request.system, no sobre
//     los mensajes. El corpus lo confirma: los 22 casos de
//     testdata/converters_anthropic/convert_anthropic_messages graban
//     "output" como una lista plana de mensajes, nunca una tupla de dos
//     elementos. Esta implementación sigue al upstream: devuelve solo
//     []converterscore.UnifiedMessage.
//
//  2. AnthropicToKiro: el brief proponía "(req
//     *modelsanthropic.AnthropicMessagesRequest, cfg *config.Config)". El
//     original (.upstream/kiro/converters_anthropic.py:429-431) declara
//     `def anthropic_to_kiro(request, conversation_id: str, profile_arn: str)
//     -> dict` — dos parámetros adicionales normales, ni rastro de un objeto
//     de configuración como parámetro. El corpus confirma la forma
//     posicional exacta: los 32 casos de
//     testdata/converters_anthropic/anthropic_to_kiro graban
//     input.args = [request_dict, conversation_id, profile_arn]. Esta
//     implementación sigue al upstream y al patrón que ya fijó Task 8 para
//     BuildKiroPayload: conversationID y profileArn como parámetros
//     explícitos, sin cfg. internal/config no se importa desde este paquete.
package convertersanthropic

import (
	"encoding/json"
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
)

// ==================================================================================================
// Resolución de model ID (kiro.model_resolver)
// ==================================================================================================
//
// anthropic_to_kiro (.upstream/kiro/converters_anthropic.py:481) calcula el
// model_id que manda a Kiro con
// `get_model_id_for_kiro(request.model, HIDDEN_MODELS)`, importado de
// kiro.model_resolver (no de converters_anthropic.py). Esa resolución ahora
// delega en modelresolver.GetModelIDForKiro
// (internal/modelresolver/resolver.go:170), el port literal de
// get_model_id_for_kiro, en vez de mantener una copia local del subconjunto
// normalizeModelName + getModelIDForKiro.
//
// HiddenModels es el equivalente de HIDDEN_MODELS (kiro.config, global de
// módulo que converters_anthropic.py importa con `from kiro.config import
// HIDDEN_MODELS`) con el mismo mecanismo de "global mutable a nivel de
// paquete" que internal/converterscore ya usa para sus siete banderas
// (thinking.go): converters_test.go lo asigna por caso desde input.config
// antes de llamar. Los 251 casos del corpus fijan HIDDEN_MODELS={} en los
// 251 (verificado por grep sobre todo testdata/converters_anthropic): ningún
// caso ejercita una resolución de modelo oculto, así que esta variable no
// tiene cobertura golden más allá de "el mapa vacío no cambia nada" — se
// mantiene de todos modos porque es fiel al mecanismo real y necesaria si
// algún caso futuro sí la ejercitara.
var HiddenModels = map[string]string{}

// ==================================================================================================
// Extractores puros
// ==================================================================================================

// truthy reproduce la verdad/falsedad de Python para un valor ya decodificado
// de JSON a `any` (mismo criterio que converterscore.isTruthy, duplicado
// aquí porque ese helper no está exportado y este paquete no depende de los
// internals de converterscore más allá de su API pública).
func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}

// pyStr aplica str(v) de Python a un valor `any` ya decodificado de JSON,
// reutilizando pyjson.Str vía un roundtrip Marshal. Devuelve "" si v no se
// puede volver a serializar (no debería ocurrir para un valor que ya vino de
// json.Unmarshal).
func pyStr(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return pyjson.Str(raw)
}

// ConvertAnthropicContentToText extrae el texto de un `content` en formato
// Anthropic. Port literal de
// kiro.converters_anthropic.convert_anthropic_content_to_text
// (.upstream/kiro/converters_anthropic.py:47-73), verificado contra los 49
// casos de testdata/converters_anthropic/convert_anthropic_content_to_text.
//
// Formas reconocidas, en el orden que comprueba el original:
//
//   - string: se devuelve tal cual.
//   - []any (lista de bloques): se concatenan (sin separador) los
//     item["text"] de los bloques cuyo item["type"] == "text" (con
//     item.get("text","") si falta la clave); cualquier otro bloque —
//     incluido un elemento que no sea un mapa, p. ej. una cadena suelta
//     dentro de la lista — se ignora en silencio. A diferencia de
//     converterscore.ExtractTextContent (extract_text_content en el
//     original), esta función NO tiene un fallback genérico item["text"]
//     para bloques sin type=="text", ni contribución de elementos-cadena
//     sueltos: son dos funciones del original con lógica similar pero no
//     idéntica, y este port respeta esa diferencia en vez de reutilizar el
//     extractor de converterscore.
//   - cualquier otro valor (nil, número, booleano, mapa suelto): `str(content)
//     if content else ""` — a diferencia de extract_system_prompt (más abajo),
//     aquí SÍ hay una comprobación de verdad: nil, false, 0 y {} vacío
//     devuelven "", el resto pasa por str() (pyStr).
func ConvertAnthropicContentToText(content any) string {
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, itemAny := range v {
			item, ok := itemAny.(map[string]any)
			if !ok {
				continue
			}
			itemType, _ := item["type"].(string)
			if itemType != "text" {
				continue
			}
			if text, ok := item["text"].(string); ok {
				b.WriteString(text)
			}
		}
		return b.String()
	default:
		if !truthy(v) {
			return ""
		}
		return pyStr(v)
	}
}

// ExtractSystemPrompt extrae el texto del system prompt en formato
// Anthropic. Port literal de kiro.converters_anthropic.extract_system_prompt
// (.upstream/kiro/converters_anthropic.py:76-108), verificado contra los 12
// casos de testdata/converters_anthropic/extract_system_prompt.
//
// Formas reconocidas, en el orden que comprueba el original:
//
//   - nil: "".
//   - string: se devuelve tal cual.
//   - []any (lista de bloques, para prompt caching): se concatenan CON "\n"
//     los item["text"] (item.get("text","") si falta la clave) de los
//     bloques con item["type"] == "text"; cache_control se ignora siempre
//     (Kiro no lo soporta). Cualquier bloque que no sea un mapa, o cuyo type
//     no sea "text", se ignora en silencio.
//   - cualquier otro valor: str(system) SIN comprobación de verdad — a
//     diferencia de ConvertAnthropicContentToText, aquí el original no
//     escribe "if system else ”" en la rama final (solo el nil de la
//     primera comprobación devuelve "").
func ExtractSystemPrompt(system any) string {
	if system == nil {
		return ""
	}
	switch v := system.(type) {
	case string:
		return v
	case []any:
		parts := make([]string, 0, len(v))
		for _, itemAny := range v {
			item, ok := itemAny.(map[string]any)
			if !ok {
				continue
			}
			itemType, _ := item["type"].(string)
			if itemType != "text" {
				continue
			}
			text, _ := item["text"].(string)
			parts = append(parts, text)
		}
		return strings.Join(parts, "\n")
	default:
		return pyStr(v)
	}
}

// ExtractToolResultsFromAnthropicContent extrae los bloques tool_result de un
// `content` en formato Anthropic. Port literal de
// kiro.converters_anthropic.extract_tool_results_from_anthropic_content
// (.upstream/kiro/converters_anthropic.py:111-150), verificado contra los 39
// casos de testdata/converters_anthropic/extract_tool_results_from_anthropic_content.
//
// Devuelve una lista vacía (nunca nil) si content no es una lista. Cada
// bloque contribuye solo si item["type"] == "tool_result" Y
// item["tool_use_id"] es "truthy" (no ausente, no ""). El contenido del
// resultado (item["content"], "" si falta la clave) se reduce a texto: si es
// una lista, vía converterscore.ExtractTextContent (equivalente a
// extract_text_content del original, que SÍ es la misma función que importa
// converters_anthropic.py — a diferencia de ConvertAnthropicContentToText,
// que es una función distinta); si no es una cadena, str(...) if ... else ""
// (mismo criterio de verdad que ConvertAnthropicContentToText). El resultado
// vacío cae al placeholder "(empty result)".
func ExtractToolResultsFromAnthropicContent(content any) []map[string]any {
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
		blockType, _ := item["type"].(string)
		toolUseID, hasID := item["tool_use_id"].(string)
		if blockType != "tool_result" || !hasID || toolUseID == "" {
			continue
		}

		resultContent := item["content"]
		var text string
		switch rc := resultContent.(type) {
		case []any:
			text = converterscore.ExtractTextContent(rc)
		case string:
			text = rc
		default:
			if truthy(rc) {
				text = pyStr(rc)
			}
		}
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

// ExtractImagesFromToolResults extrae las imágenes que vienen DENTRO de
// bloques tool_result (p. ej. capturas de pantalla que devuelve una tool MCP
// de navegador). Port literal de
// kiro.converters_anthropic.extract_images_from_tool_results
// (.upstream/kiro/converters_anthropic.py:153-183), verificado contra los 35
// casos de testdata/converters_anthropic/extract_images_from_tool_results.
//
// Devuelve una lista vacía (nunca nil) si content no es una lista. Solo
// procesa bloques con item["type"] == "tool_result" cuyo item["content"] SEA
// A SU VEZ una lista — delega la extracción real a
// converterscore.ExtractImagesFromContent (extract_images_from_content del
// original) sobre esa lista interior. Imágenes fuera de un tool_result
// (bloques "image" al nivel superior del content) se ignoran a propósito:
// esas las cubre converterscore.ExtractImagesFromContent aplicado
// directamente al content del mensaje, no esta función.
func ExtractImagesFromToolResults(content any) []map[string]any {
	images := []map[string]any{}

	items, ok := content.([]any)
	if !ok {
		return images
	}

	for _, itemAny := range items {
		item, ok := itemAny.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := item["type"].(string)
		if blockType != "tool_result" {
			continue
		}
		resultContent, ok := item["content"].([]any)
		if !ok {
			continue
		}
		images = append(images, converterscore.ExtractImagesFromContent(resultContent)...)
	}

	return images
}

// ExtractToolUsesFromAnthropicContent extrae los bloques tool_use de un
// `content` de mensaje assistant en formato Anthropic. Port literal de
// kiro.converters_anthropic.extract_tool_uses_from_anthropic_content
// (.upstream/kiro/converters_anthropic.py:186-227), verificado contra los 21
// casos de testdata/converters_anthropic/extract_tool_uses_from_anthropic_content.
//
// Devuelve una lista vacía (nunca nil) si content no es una lista. Cada
// bloque contribuye solo si item["type"] == "tool_use" Y item["id"] Y
// item["name"] son ambos "truthy" (ni ausentes ni ""). El input
// (item["input"], {} si falta la clave) se copia tal cual a
// function.arguments — el ternario del original
// (`tool_input if isinstance(tool_input, str) else tool_input`) es un no-op
// literal (las dos ramas devuelven tool_input), así que no hay nada que
// replicar más allá de copiar el valor.
func ExtractToolUsesFromAnthropicContent(content any) []map[string]any {
	toolCalls := []map[string]any{}

	items, ok := content.([]any)
	if !ok {
		return toolCalls
	}

	for _, itemAny := range items {
		item, ok := itemAny.(map[string]any)
		if !ok {
			continue
		}
		blockType, _ := item["type"].(string)
		toolID, hasID := item["id"].(string)
		toolName, hasName := item["name"].(string)
		if blockType != "tool_use" || !hasID || toolID == "" || !hasName || toolName == "" {
			continue
		}

		input, hasInput := item["input"]
		if !hasInput {
			input = map[string]any{}
		}

		toolCalls = append(toolCalls, map[string]any{
			"id":   toolID,
			"type": "function",
			"function": map[string]any{
				"name":      toolName,
				"arguments": input,
			},
		})
	}

	return toolCalls
}
