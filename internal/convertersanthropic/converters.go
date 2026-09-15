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
	"regexp"
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
)

// ==================================================================================================
// Resolución de model ID (kiro.model_resolver, subconjunto)
// ==================================================================================================
//
// anthropic_to_kiro (.upstream/kiro/converters_anthropic.py:481) calcula el
// model_id que manda a Kiro con
// `get_model_id_for_kiro(request.model, HIDDEN_MODELS)`, importado de
// kiro.model_resolver (no de converters_anthropic.py). docs/MAPPING.md
// asigna ese módulo completo a un paquete propio, internal/modelresolver,
// que todavía no existe — es una tarea futura del plan de fase 3, fuera del
// alcance de esta (Task 9 solo puede tocar internal/convertersanthropic/* y
// docs/MAPPING.md). Crear ese paquete aquí violaría esa restricción.
//
// La propia documentación de get_model_id_for_kiro en el original la
// describe como "a simple helper for converters that don't have access to
// the full ModelResolver" — exactamente la situación de este adaptador. Este
// fichero porta ESE subconjunto mínimo (normalize_model_name +
// get_model_id_for_kiro, sin la clase ModelResolver completa, sin
// extract_model_family, sin caché dinámica ni alias) como funciones no
// exportadas, para no bloquear anthropic_to_kiro en un paquete que Task 9 no
// puede crear. Cuando internal/modelresolver exista, la tarea que lo cree
// debería sustituir este subconjunto por una llamada real a ese paquete.
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

// Los cinco patrones de kiro.model_resolver.normalize_model_name
// (.upstream/kiro/model_resolver.py:93-175), copiados literalmente.
var (
	modelSuffixPattern    = regexp.MustCompile(`(?i)\[\d+[mk]\]$`)
	standardModelPattern  = regexp.MustCompile(`^(claude-(?:haiku|sonnet|opus)-\d+)-(\d{1,2})(?:-(?:\d{8}|latest|\d+))?$`)
	noMinorModelPattern   = regexp.MustCompile(`^(claude-(?:haiku|sonnet|opus)-\d+)(?:-\d{8})?$`)
	legacyModelPattern    = regexp.MustCompile(`^(claude)-(\d+)-(\d+)-(haiku|sonnet|opus)(?:-(?:\d{8}|latest|\d+))?$`)
	dotWithDateModelRegex = regexp.MustCompile(`^(claude-(?:\d+\.\d+-)?(?:haiku|sonnet|opus)(?:-\d+\.\d+)?)-\d{8}$`)
	invertedSuffixPattern = regexp.MustCompile(`^claude-(\d+)\.(\d+)-(haiku|sonnet|opus)-(.+)$`)
)

// normalizeModelName normaliza un nombre de modelo externo al formato que
// espera Kiro. Port literal de kiro.model_resolver.normalize_model_name
// (.upstream/kiro/model_resolver.py:93-175): los cinco patrones se prueban
// en orden y el primero que hace match decide el resultado; sin match,
// devuelve name tal cual (pass-through, preservando mayúsculas).
func normalizeModelName(name string) string {
	if name == "" {
		return name
	}

	// Sufijo de ventana de contexto (p.ej. "[1m]", "[200k]"): indicador del
	// cliente, no parte del model ID.
	name = modelSuffixPattern.ReplaceAllString(name, "")
	nameLower := strings.ToLower(name)

	if m := standardModelPattern.FindStringSubmatch(nameLower); m != nil {
		return m[1] + "." + m[2]
	}
	if m := noMinorModelPattern.FindStringSubmatch(nameLower); m != nil {
		return m[1]
	}
	if m := legacyModelPattern.FindStringSubmatch(nameLower); m != nil {
		return m[1] + "-" + m[2] + "." + m[3] + "-" + m[4]
	}
	if m := dotWithDateModelRegex.FindStringSubmatch(nameLower); m != nil {
		return m[1]
	}
	if m := invertedSuffixPattern.FindStringSubmatch(nameLower); m != nil {
		return "claude-" + m[3] + "-" + m[1] + "." + m[2]
	}

	return name
}

// getModelIDForKiro resuelve el model ID que se manda a Kiro. Port literal
// de kiro.model_resolver.get_model_id_for_kiro
// (.upstream/kiro/model_resolver.py:178-203): normaliza el nombre y
// comprueba hiddenModels; to_runtime_model_id (línea 55-66 del original) es
// un pass-through puro, así que no aporta nada que replicar aparte.
func getModelIDForKiro(modelName string, hiddenModels map[string]string) string {
	normalized := normalizeModelName(modelName)
	if internal, ok := hiddenModels[normalized]; ok {
		return internal
	}
	return normalized
}

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
//  4. modelID = getModelIDForKiro(req.Model, HiddenModels) — ver el
//     comentario de cabecera de la sección "Resolución de model ID" más
//     arriba sobre el alcance de este subconjunto.
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

	modelID := getModelIDForKiro(req.Model, HiddenModels)

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
