// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package convertersopenai

import (
	"encoding/json"
	"fmt"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelresolver"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
)

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
//  3. modelID = modelresolver.GetModelIDForKiro(req.Model, HiddenModels).
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
	modelID := modelresolver.GetModelIDForKiro(req.Model, HiddenModels)
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
