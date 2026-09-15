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
	"fmt"
	"regexp"
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
)

// ==================================================================================================
// Resolución de model ID (kiro.model_resolver, subconjunto)
// ==================================================================================================
//
// build_kiro_payload (.upstream/kiro/converters_openai.py:423) calcula el
// model_id que manda a Kiro con `get_model_id_for_kiro(request_data.model,
// HIDDEN_MODELS)`, importado de kiro.model_resolver (no de
// converters_openai.py) — exactamente la misma función que usa el adaptador
// Anthropic (Task 9). internal/modelresolver (docs/MAPPING.md) todavía no
// existe: es una tarea futura del plan de fase 3, fuera del alcance de esta
// (Task 10 solo puede tocar internal/convertersopenai/* y docs/MAPPING.md).
//
// Este fichero porta el mismo subconjunto mínimo que ya justificó Task 9 en
// internal/convertersanthropic/converters.go (normalizeModelName +
// getModelIDForKiro, sin la clase ModelResolver completa) como funciones no
// exportadas PROPIAS de este paquete, en vez de importar
// convertersanthropic: ese paquete no las exporta (son funciones internas
// suyas), y duplicar el subconjunto mínimo mantiene el radio de impacto de
// cada adaptador contenido a su propio paquete — el mismo criterio que ya
// aplicó Task 9 frente a reutilizar código de converters_core. Cuando
// internal/modelresolver exista, la tarea que lo cree debería sustituir
// ambas copias (esta y la de convertersanthropic) por una llamada real a ese
// paquete.
//
// HiddenModels es el equivalente de HIDDEN_MODELS (kiro.config, global de
// módulo que converters_openai.py importa con `from kiro.config import
// HIDDEN_MODELS`). Los 38 casos de testdata/converters_openai/build_kiro_payload
// fijan HIDDEN_MODELS={} (verificado por grep sobre todo el corpus): ningún
// caso ejercita una resolución de modelo oculto, así que esta variable no
// tiene cobertura golden más allá de "el mapa vacío no cambia nada" — se
// mantiene de todos modos por fidelidad al mecanismo real.
var HiddenModels = map[string]string{}

// Los cinco patrones de kiro.model_resolver.normalize_model_name
// (.upstream/kiro/model_resolver.py:93-175), copiados literalmente — misma
// fuente y mismo orden de intento que convertersanthropic.
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
// comprueba hiddenModels.
func getModelIDForKiro(modelName string, hiddenModels map[string]string) string {
	normalized := normalizeModelName(modelName)
	if internal, ok := hiddenModels[normalized]; ok {
		return internal
	}
	return normalized
}

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

// ConvertOpenAIToolsToUnified convierte una lista de tools en formato OpenAI
// al formato unificado. Port literal de
// kiro.converters_openai.convert_openai_tools_to_unified
// (.upstream/kiro/converters_openai.py:252-293), verificado contra los 25
// casos de testdata/converters_openai/convert_openai_tools_to_unified.
//
// tools vacío o nil → nil. Por tool: solo se procesan las de tool.Type ==
// "function" (cualquier otro type se descarta en silencio); dentro de esas,
// el formato estándar OpenAI (tool.Function != nil) tiene prioridad sobre el
// formato plano estilo Cursor (tool.Name != nil): name/description/
// input_schema salen de tool.Function.{Name,Description,Parameters} en el
// primer caso, de tool.{Name,Description,InputSchema} en el segundo. Si
// ninguno de los dos aplica (ni Function ni Name), la tool se descarta en
// silencio (el original solo registra un warning). Description es *string
// en ambas fuentes: un puntero nil se aplana a "" (misma pérdida de la
// distinción None/"" que ya documentó Task 4/9 para UnifiedTool.Description
// — converters_test.go normaliza a mano los casos del corpus donde importa).
// InputSchema/Parameters son json.RawMessage: se decodifican a
// map[string]any tal cual, incluido el literal JSON null → nil map (no {}) —
// ninguno de los dos campos se defaultea a un mapa vacío cuando falta o es
// null, igual que el Optional[Dict[str,Any]] = None del original.
//
// Si tras filtrar todas las tools resultan inválidas, el resultado final
// vuelve a ser nil (mismo "return unified_tools if unified_tools else None"
// del original).
func ConvertOpenAIToolsToUnified(tools []modelsopenai.Tool) []converterscore.UnifiedTool {
	if len(tools) == 0 {
		return nil
	}

	unified := make([]converterscore.UnifiedTool, 0, len(tools))
	for _, tool := range tools {
		if tool.Type != "function" {
			continue
		}

		switch {
		case tool.Function != nil:
			description := ""
			if tool.Function.Description != nil {
				description = *tool.Function.Description
			}
			var inputSchema map[string]any
			if len(tool.Function.Parameters) > 0 {
				_ = json.Unmarshal(tool.Function.Parameters, &inputSchema)
			}
			unified = append(unified, converterscore.UnifiedTool{
				Name:        tool.Function.Name,
				Description: description,
				InputSchema: inputSchema,
			})

		case tool.Name != nil:
			description := ""
			if tool.Description != nil {
				description = *tool.Description
			}
			var inputSchema map[string]any
			if len(tool.InputSchema) > 0 {
				_ = json.Unmarshal(tool.InputSchema, &inputSchema)
			}
			unified = append(unified, converterscore.UnifiedTool{
				Name:        *tool.Name,
				Description: description,
				InputSchema: inputSchema,
			})

		default:
			// Tool inválida: ni function ni name. El original solo registra
			// un warning y continúa.
		}
	}

	if len(unified) == 0 {
		return nil
	}
	return unified
}

// ==================================================================================================
// Thinking configuration
// ==================================================================================================

// reasoningEffortPercent es la tabla de porcentajes de
// kiro.converters_openai.reasoning_effort_to_budget
// (.upstream/kiro/converters_openai.py:320-327), copiada literalmente:
// none=0%, minimal=10%, low=20%, medium=50%, high=80%, xhigh=95%.
var reasoningEffortPercent = map[string]float64{
	"none":    0.0,
	"minimal": 0.10,
	"low":     0.20,
	"medium":  0.50,
	"high":    0.80,
	"xhigh":   0.95,
}

// ReasoningEffortToBudget convierte reasoning_effort a un presupuesto de
// thinking en tokens. Port literal de
// kiro.converters_openai.reasoning_effort_to_budget
// (.upstream/kiro/converters_openai.py:300-328), verificado contra los 10
// casos de testdata/converters_openai/reasoning_effort_to_budget.
//
// int(max_tokens * percent[effort]) — la conversión float64→int de Go trunca
// hacia cero para valores positivos, igual que int() de Python sobre un
// float positivo; maxTokens y los porcentajes de la tabla nunca son
// negativos en ningún caso del corpus ni en el uso real (proviene de
// max_tokens de la request, siempre >= 0).
//
// # Decisión de manejo de errores para effort desconocido
//
// El original indexa el dict directamente (`percent[effort]`, sin
// `.get()`): una clave fuera de las seis de la tabla lanzaría KeyError sin
// capturar, que se propagaría hasta el llamador HTTP (fase 4/5, todavía sin
// construir). Ningún caso del corpus ejercita esa ruta. Esta implementación
// sigue el mismo criterio que converterscore.BuildKiroPayload ya fijó para
// sus dos ValueError del original (payload.go, "Decisión de manejo de
// errores"): panic(), porque la firma de esta función no tiene hueco para
// devolver un error y no tiene sentido tragar en silencio una entrada que el
// original tampoco tolera.
func ReasoningEffortToBudget(maxTokens int, effort string) int {
	percent, ok := reasoningEffortPercent[effort]
	if !ok {
		panic(fmt.Sprintf("convertersopenai: ReasoningEffortToBudget: reasoning_effort desconocido: %q", effort))
	}
	return int(float64(maxTokens) * percent)
}

// ExtractThinkingConfigFromOpenAI extrae la configuración de thinking del
// campo reasoning_effort de una ChatCompletionRequest. Port literal de
// kiro.converters_openai.extract_thinking_config_from_openai
// (.upstream/kiro/converters_openai.py:331-386), verificado contra los 37
// casos de testdata/converters_openai/extract_thinking_config_from_openai.
//
// Reglas, en el orden que comprueba el original:
//
//   - request.ReasoningEffort nil o "" (el "if not request.reasoning_effort"
//     del original, que cubre tanto None como una cadena vacía): {enabled:
//     true, budget_tokens: nil} — el default.
//   - "none": {enabled: false, budget_tokens: nil}.
//   - cualquier otro valor: se calcula un presupuesto. maxTokens sale de
//     request.MaxTokens si es "truthy" en el sentido de Python (no nil, no
//     cero) — el `or` del original (`request.max_tokens or
//     request.max_completion_tokens`) cae a MaxCompletionTokens en los
//     mismos términos si MaxTokens no calificó, y si NINGUNO de los dos
//     calificó, el resultado es 0 y el "if not max_tokens: max_tokens =
//     4096" del original lo sustituye por el default 4096. Ningún caso del
//     corpus ejercita max_tokens=0 explícito (que con esta regla caería al
//     default 4096 igual que ausente, fielmente al original), pero la regla
//     se implementa completa por fidelidad. budget_tokens final sale de
//     ReasoningEffortToBudget(maxTokens, *request.ReasoningEffort).
func ExtractThinkingConfigFromOpenAI(req *modelsopenai.ChatCompletionRequest) converterscore.ThinkingConfig {
	if req.ReasoningEffort == nil || *req.ReasoningEffort == "" {
		return converterscore.ThinkingConfig{Enabled: true, BudgetTokens: nil}
	}
	if *req.ReasoningEffort == "none" {
		return converterscore.ThinkingConfig{Enabled: false, BudgetTokens: nil}
	}

	maxTokens := 0
	if req.MaxTokens != nil && *req.MaxTokens != 0 {
		maxTokens = *req.MaxTokens
	} else if req.MaxCompletionTokens != nil && *req.MaxCompletionTokens != 0 {
		maxTokens = *req.MaxCompletionTokens
	}
	if maxTokens == 0 {
		maxTokens = 4096
	}

	budget := ReasoningEffortToBudget(maxTokens, *req.ReasoningEffort)
	return converterscore.ThinkingConfig{Enabled: true, BudgetTokens: &budget}
}

// ==================================================================================================
// Integrador
// ==================================================================================================

// BuildKiroPayload convierte una ChatCompletionRequest en formato OpenAI al
// payload de la API de Kiro. Port literal de
// kiro.converters_openai.build_kiro_payload
// (.upstream/kiro/converters_openai.py:393-446), verificado contra los 38
// casos de testdata/converters_openai/build_kiro_payload.
//
// # Firma
//
// TRES parámetros: req, conversationID, profileArn — NO un *config.Config.
// Ver el comentario de cabecera del paquete para la corrección sobre la
// firma que proponía el brief.
//
// # Orden de la cadena (verificado línea a línea contra el original)
//
//  1. ConvertOpenAIMessagesToUnified(req.Messages) → (systemPrompt,
//     unifiedMessages).
//  2. ConvertOpenAIToolsToUnified(req.Tools).
//  3. modelID = getModelIDForKiro(req.Model, HiddenModels).
//  4. ExtractThinkingConfigFromOpenAI(req).
//  5. converterscore.BuildKiroPayload(unifiedMessages, systemPrompt, modelID,
//     unifiedTools, conversationID, profileArn, thinkingCfg) — el original
//     devuelve result.payload (línea 446), NO el KiroPayloadResult completo:
//     el corpus lo confirma, "output" en los 38 casos de build_kiro_payload
//     es directamente el dict de payload. Esta implementación, igual que
//     convertersanthropic.AnthropicToKiro (Task 9), SÍ devuelve
//     converterscore.KiroPayloadResult completo (con ToolDocumentation): es
//     la firma que fija la interfaz del plan de fase 3, y el corpus solo
//     compara el campo Payload (ver TestBuildKiroPayload) — ToolDocumentation
//     hereda la cobertura golden de converterscore.BuildKiroPayload (Task
//     8).
func BuildKiroPayload(req *modelsopenai.ChatCompletionRequest, conversationID string, profileArn string) converterscore.KiroPayloadResult {
	systemPrompt, unifiedMessages := ConvertOpenAIMessagesToUnified(req.Messages)
	unifiedTools := ConvertOpenAIToolsToUnified(req.Tools)
	modelID := getModelIDForKiro(req.Model, HiddenModels)
	thinkingCfg := ExtractThinkingConfigFromOpenAI(req)

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
