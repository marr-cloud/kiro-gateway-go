// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"encoding/json"
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
)

// ExtractTextContent extrae el texto de un `content` en cualquiera de las
// formas que aceptan las APIs soportadas. Port literal de
// kiro.converters_core.extract_text_content.
//
// Formas reconocidas, en el orden que comprueba el original:
//
//   - nil (JSON null): cadena vacía.
//   - string: se devuelve tal cual.
//   - []any (lista de bloques de contenido): se concatenan los fragmentos de
//     texto de cada bloque, saltando en silencio los bloques "image",
//     "image_url" y "tool_reference" (se procesan en otra parte). Un bloque
//     mapa con type == "text" aporta item["text"] (o nada si falta la
//     clave); un bloque mapa sin ese type pero con clave "text" aporta esa
//     clave igualmente; un elemento que es directamente una cadena se
//     concatena tal cual.
//   - cualquier otra cosa (número, booleano, mapa suelto): fallback a
//     str(content). pyjson.Str reproduce las reglas de str() de Python 3
//     sobre el valor vuelto a serializar a JSON.
func ExtractTextContent(content any) string {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case string:
		return v
	case []any:
		var b strings.Builder
		for _, itemAny := range v {
			switch item := itemAny.(type) {
			case map[string]any:
				itemType, _ := item["type"].(string)
				// Los bloques de imagen y tool_reference se procesan en otra
				// parte del pipeline: se saltan en silencio aquí.
				if itemType == "image" || itemType == "image_url" || itemType == "tool_reference" {
					continue
				}
				if itemType == "text" {
					if text, ok := item["text"].(string); ok {
						b.WriteString(text)
					}
					continue
				}
				if text, ok := item["text"]; ok {
					if s, ok := text.(string); ok {
						b.WriteString(s)
					}
				}
			case string:
				b.WriteString(item)
			}
		}
		return b.String()
	default:
		raw, err := json.Marshal(content)
		if err != nil {
			return ""
		}
		return pyjson.Str(raw)
	}
}

// ExtractImagesFromContent extrae las imágenes de un `content` en formato
// unificado. Port literal de kiro.converters_core.extract_images_from_content.
//
// Devuelve una lista vacía (nunca nil) si content no es una lista o no
// contiene imágenes soportadas. Reconoce dos formas:
//
//   - OpenAI: {"type": "image_url", "image_url": {"url": "data:<media>;base64,<data>"}}
//     Solo se soportan URLs de datos (data:); las URLs http(s) se descartan
//     en silencio porque la API de Kiro no puede recuperarlas.
//   - Anthropic: {"type": "image", "source": {"type": "base64", "media_type": ..., "data": ...}}
//     El source.type == "url" también se descarta en silencio por el mismo
//     motivo.
func ExtractImagesFromContent(content any) []map[string]any {
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
		itemType, _ := item["type"].(string)

		switch itemType {
		case "image_url":
			imageURLObj, _ := item["image_url"].(map[string]any)
			url, _ := imageURLObj["url"].(string)
			if !strings.HasPrefix(url, "data:") {
				// Incluye las URLs http(s): Kiro no las soporta y se
				// descartan en silencio (el original solo las registra).
				continue
			}
			header, data, _ := strings.Cut(url, ",")
			if data == "" {
				continue
			}
			mediaPart, _, _ := strings.Cut(header, ";")
			mediaType := strings.ReplaceAll(mediaPart, "data:", "")
			images = append(images, map[string]any{
				"media_type": mediaType,
				"data":       data,
			})

		case "image":
			source, ok := item["source"].(map[string]any)
			if !ok {
				continue
			}
			sourceType, _ := source["type"].(string)
			if sourceType != "base64" {
				// source.type == "url" (u otro): no soportado, se descarta.
				continue
			}
			mediaType, ok := source["media_type"].(string)
			if !ok {
				mediaType = "image/jpeg"
			}
			data, _ := source["data"].(string)
			if data == "" {
				continue
			}
			images = append(images, map[string]any{
				"media_type": mediaType,
				"data":       data,
			})
		}
	}

	return images
}

// ExtractToolResultsFromContent extrae los resultados de herramienta de un
// `content` y los convierte al formato Kiro. Port literal de
// kiro.converters_core.extract_tool_results_from_content.
//
// Devuelve una lista vacía (nunca nil) si content no es una lista, o para
// cualquier bloque cuyo type no sea "tool_result".
func ExtractToolResultsFromContent(content any) []map[string]any {
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

		text := ExtractTextContent(item["content"])
		if text == "" {
			text = "(empty result)"
		}
		toolUseID, _ := item["tool_use_id"].(string)

		results = append(results, map[string]any{
			"content":   []map[string]any{{"text": text}},
			"status":    "success",
			"toolUseId": toolUseID,
		})
	}

	return results
}

// ExtractToolUsesFromMessage extrae los usos de herramienta de un mensaje de
// asistente. Port literal de
// kiro.converters_core.extract_tool_uses_from_message(content, tool_calls),
// cuya firma en el original toma content y tool_calls como dos argumentos
// sueltos; esta versión los agrupa en un UnifiedMessage porque así lo fija
// la interfaz del plan de la fase 3 (solo se leen msg.Content y
// msg.ToolCalls).
//
// Busca en dos sitios, en este orden, y concatena lo que encuentra en cada
// uno (no son excluyentes):
//
//  1. msg.ToolCalls (formato OpenAI, o el formato unificado que produce el
//     adaptador Anthropic): cada entrada aporta name, input y toolUseId.
//     input sale de function.arguments: si arguments es una cadena no vacía,
//     se parsea como JSON; si es una cadena vacía, input es {}; si no es una
//     cadena, se usa tal cual cuando es "truthy" en el sentido de Python
//     (no nil, no false, no numérico cero, no colección vacía) y {} en caso
//     contrario.
//  2. Bloques de msg.Content con type == "tool_use" (formato Anthropic):
//     cada uno aporta name, input y toolUseId directamente de sus claves,
//     con input por defecto {} si falta la clave.
func ExtractToolUsesFromMessage(msg UnifiedMessage) []map[string]any {
	toolUses := []map[string]any{}

	for _, tc := range msg.ToolCalls {
		funcObj, _ := tc["function"].(map[string]any)

		var arguments any = "{}"
		if raw, ok := funcObj["arguments"]; ok {
			arguments = raw
		}

		var inputData any
		switch a := arguments.(type) {
		case string:
			if a == "" {
				inputData = map[string]any{}
			} else {
				var parsed any
				if err := json.Unmarshal([]byte(a), &parsed); err != nil {
					// El original dejaría propagar json.JSONDecodeError; el
					// corpus no ejercita cadenas de argumentos mal formadas,
					// así que no hay caso golden que fije este camino.
					inputData = map[string]any{}
				} else {
					inputData = parsed
				}
			}
		default:
			if isTruthy(a) {
				inputData = a
			} else {
				inputData = map[string]any{}
			}
		}

		name, _ := funcObj["name"].(string)
		id, _ := tc["id"].(string)
		toolUses = append(toolUses, map[string]any{
			"name":      name,
			"input":     inputData,
			"toolUseId": id,
		})
	}

	if items, ok := msg.Content.([]any); ok {
		for _, itemAny := range items {
			item, ok := itemAny.(map[string]any)
			if !ok {
				continue
			}
			if itemType, _ := item["type"].(string); itemType != "tool_use" {
				continue
			}

			name, _ := item["name"].(string)
			input, hasInput := item["input"]
			if !hasInput {
				input = map[string]any{}
			}
			id, _ := item["id"].(string)

			toolUses = append(toolUses, map[string]any{
				"name":      name,
				"input":     input,
				"toolUseId": id,
			})
		}
	}

	return toolUses
}

// isTruthy reproduce la verdad/falsedad de Python para un valor ya
// decodificado de JSON a `any`: None, false, 0/0.0, "" y las colecciones
// vacías son falsos; todo lo demás es verdadero.
func isTruthy(v any) bool {
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
