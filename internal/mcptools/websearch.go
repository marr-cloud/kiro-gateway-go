// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/tokenizer"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// webSearchQueryPrefix es el prefijo que algunos clientes anteponen al
// texto de búsqueda; se retira si está presente. mcp_tools.py:581.
const webSearchQueryPrefix = "Perform a web search for the query: "

// firstMessageContent es la forma mínima que ExtractQueryFromMessages
// necesita leer del primer mensaje — solo el campo "content", en JSON
// crudo porque puede ser un string o una lista de bloques (mcp_tools.py:557-578).
type firstMessageContent struct {
	Content json.RawMessage `json:"content"`
}

type textBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ExtractQueryFromMessages extrae la query de búsqueda del PRIMER mensaje
// de la petición. Puerto de mcp_tools.py:534-587 (extract_query_from_messages).
//
// apiFormat se recibe por paridad de firma con el original — que también lo
// acepta sin usarlo en ningún punto del cuerpo de la función: la extracción
// de texto de un bloque {"type":"text","text":...} es idéntica en ambos
// dialectos, así que nunca hubo necesidad de ramificar por formato.
//
// LIMITACIÓN heredada: solo mira el primer mensaje (single-turn), aceptable
// para el caso de uso de web_search (mcp_tools.py:541-542).
func ExtractQueryFromMessages(messages []json.RawMessage, apiFormat string) (string, bool) {
	_ = apiFormat
	if len(messages) == 0 {
		return "", false
	}

	var first firstMessageContent
	if err := json.Unmarshal(messages[0], &first); err != nil {
		return "", false
	}
	trimmed := bytes.TrimSpace(first.Content)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false
	}

	var text string
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return "", false
		}
		text = s
	case '[':
		var blocks []textBlock
		if err := json.Unmarshal(trimmed, &blocks); err != nil {
			return "", false
		}
		var b strings.Builder
		for _, block := range blocks {
			if block.Type == "text" {
				b.WriteString(block.Text)
			}
		}
		text = b.String()
	default:
		return "", false
	}

	query := strings.TrimPrefix(text, webSearchQueryPrefix)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", false
	}
	return query, true
}

// NativeWebSearchRequest agrupa lo que HandleNativeWebSearch necesita del
// request entrante, para no acoplar este paquete a los tipos de petición
// concretos de routesopenai/routesanthropic (fuera del alcance de esta
// tarea — Task 7 los traduce a esto). Corresponde a los campos de
// request_data que usa handle_native_web_search (mcp_tools.py:590-753):
// .messages, .model, .stream.
type NativeWebSearchRequest struct {
	Messages []json.RawMessage
	Model    string
	Stream   bool
}

// NativeWebSearchOutcome es lo que HandleNativeWebSearch devuelve: el
// código HTTP y el cuerpo ya serializado, listos para que Task 7 los vuelque
// en el ResponseWriter. Streaming=true implica que Body ya es un stream SSE
// completo (framed vía internal/sse, ver sse.go); Streaming=false implica
// que Body es JSON. Sustituye a JSONResponse/StreamingResponse de FastAPI,
// que no tienen equivalente directo fuera de una capa HTTP concreta.
type NativeWebSearchOutcome struct {
	StatusCode int
	Streaming  bool
	Body       []byte
}

func anthropicErrorBody(errType, message string) []byte {
	body, _ := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]string{
			"type":    errType,
			"message": message,
		},
	})
	return body
}

// HandleNativeWebSearch maneja el Path A (tool nativa web_search): llamada
// directa a la API MCP → emulación SSE o JSON → sin pasar por
// /generateAssistantResponse en absoluto. Puerto de mcp_tools.py:590-753
// (handle_native_web_search).
//
// host es el q_host (ver el comentario de CallKiroMCPAPI en client.go).
//
// Nota de fidelidad: el original construye el cuerpo de error 400/500
// SIEMPRE en forma Anthropic ({"type":"error","error":{...}}), incluso
// cuando api_format == "openai" (mcp_tools.py:613-623,630-640 no ramifican
// por api_format) — se replica tal cual, no es un bug de este port.
func HandleNativeWebSearch(ctx context.Context, req NativeWebSearchRequest, host string, tp utils.TokenProvider, apiFormat string) NativeWebSearchOutcome {
	query, ok := ExtractQueryFromMessages(req.Messages, apiFormat)
	if !ok {
		return NativeWebSearchOutcome{
			StatusCode: 400,
			Body:       anthropicErrorBody("invalid_request_error", "Cannot extract search query from messages"),
		}
	}

	toolUseID, results, err := CallKiroMCPAPI(ctx, host, query, tp)
	if err != nil {
		return NativeWebSearchOutcome{
			StatusCode: 500,
			Body:       anthropicErrorBody("api_error", "Web search failed. Please try again."),
		}
	}

	// mcp_tools.py:643-646: count_message_tokens(..., apply_claude_correction=False).
	inputTokens := tokenizer.CountMessageTokens(req.Messages, false)

	if req.Stream {
		return handleStreamingOutcome(req.Model, query, toolUseID, results, inputTokens, apiFormat)
	}
	return handleNonStreamingOutcome(req.Model, query, toolUseID, results, inputTokens, apiFormat)
}

func handleStreamingOutcome(model, query, toolUseID string, results map[string]any, inputTokens int, apiFormat string) NativeWebSearchOutcome {
	var body []byte
	var err error
	if apiFormat == "openai" {
		body, err = GenerateOpenAIWebSearchSSE(model, query, toolUseID, results, inputTokens)
	} else {
		body, err = GenerateAnthropicWebSearchSSE(model, query, toolUseID, results, inputTokens)
	}
	if err != nil {
		// Sin equivalente directo en el original (un generador Python no
		// falla al serializar sus propios dicts literales); camino
		// defensivo para un json.Marshal que en la práctica nunca falla
		// sobre estos tipos controlados.
		return NativeWebSearchOutcome{StatusCode: 500, Body: anthropicErrorBody("api_error", "Web search failed. Please try again.")}
	}
	return NativeWebSearchOutcome{StatusCode: 200, Streaming: true, Body: body}
}

func handleNonStreamingOutcome(model, query, toolUseID string, results map[string]any, inputTokens int, apiFormat string) NativeWebSearchOutcome {
	summary := GenerateSearchSummary(query, results)
	outputTokens := outputTokenCount(summary)

	var body []byte
	var err error
	if apiFormat == "openai" {
		body, err = json.Marshal(openAINonStreamResponse{
			ID:      utils.GenerateCompletionID(),
			Object:  "chat.completion",
			Created: nowUnix(),
			Model:   model,
			Choices: []openAINonStreamChoice{{
				Index:        0,
				Message:      openAINonStreamMessage{Role: "assistant", Content: summary},
				FinishReason: "stop",
			}},
			Usage: openAIUsage{PromptTokens: inputTokens, CompletionTokens: outputTokens, TotalTokens: inputTokens + outputTokens},
		})
	} else {
		body, err = json.Marshal(anthropicNonStreamResponse{
			ID:   utils.GenerateMessageID(),
			Type: "message",
			Role: "assistant",
			Content: []any{
				serverToolUseBlock{Type: "server_tool_use", ID: toolUseID, Name: "web_search", Input: map[string]any{"query": query}},
				webSearchToolResultBlock{Type: "web_search_tool_result", ToolUseID: toolUseID, Content: buildSearchResultContent(results)},
				textContentBlockShape{Type: "text", Text: summary},
			},
			Model:        model,
			StopReason:   "end_turn",
			StopSequence: nil,
			Usage:        anthropicSSEUsage{InputTokens: inputTokens, OutputTokens: outputTokens},
		})
	}
	if err != nil {
		return NativeWebSearchOutcome{StatusCode: 500, Body: anthropicErrorBody("api_error", "Web search failed. Please try again.")}
	}
	return NativeWebSearchOutcome{StatusCode: 200, Body: body}
}

// --- Formas de respuesta no-streaming (mcp_tools.py:683-751) ---

type openAINonStreamMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAINonStreamChoice struct {
	Index        int                    `json:"index"`
	Message      openAINonStreamMessage `json:"message"`
	FinishReason string                 `json:"finish_reason"`
}

type openAINonStreamResponse struct {
	ID      string                  `json:"id"`
	Object  string                  `json:"object"`
	Created int64                   `json:"created"`
	Model   string                  `json:"model"`
	Choices []openAINonStreamChoice `json:"choices"`
	Usage   openAIUsage             `json:"usage"`
}

type anthropicNonStreamResponse struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	Role         string            `json:"role"`
	Content      []any             `json:"content"`
	Model        string            `json:"model"`
	StopReason   string            `json:"stop_reason"`
	StopSequence *string           `json:"stop_sequence"`
	Usage        anthropicSSEUsage `json:"usage"`
}
