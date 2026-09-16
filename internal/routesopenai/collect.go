// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// defaultUsage es el usage de respaldo de collect_stream_response cuando la
// respuesta no trajo ningún chunk con "usage" (streaming_openai.py:673:
// `final_usage or {"prompt_tokens": 0, "completion_tokens": 0,
// "total_tokens": 0}`). En la práctica esto no debería ocurrir — Finish()
// siempre emite un chunk final con usage — pero se mantiene por fidelidad y
// como red de seguridad defensiva.
var defaultUsage = json.RawMessage(`{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}`)

// sseChunkIn es la forma que collectResponse necesita leer de vuelta de un
// chat.completion.chunk ya serializado (internal/streamingopenai/formatter.go's
// chatCompletionChunk, no exportado — se redeclara aquí solo lo que hace
// falta para decodificar).
type sseChunkIn struct {
	Choices []sseChoiceIn   `json:"choices"`
	Usage   json.RawMessage `json:"usage"`
}

type sseChoiceIn struct {
	Delta struct {
		Content          string            `json:"content"`
		ReasoningContent string            `json:"reasoning_content"`
		ToolCalls        []json.RawMessage `json:"tool_calls"`
	} `json:"delta"`
	FinishReason *string `json:"finish_reason"`
}

// deltaToolCallIn es la forma de un elemento de "tool_calls" dentro de un
// delta de streaming (incluye "index", que collectResponse descarta al
// reconstruir la forma no-streaming — ver outToolCall).
type deltaToolCallIn struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// outToolCall es un tool_call en la respuesta no-streaming: SIN "index"
// (solo hace falta en chunks de streaming, streaming_openai.py:655-656:
// "For non-streaming response remove index field from tool_calls").
type outToolCall struct {
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function outToolCallFunc `json:"function"`
}

type outToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type outMessage struct {
	Role             string        `json:"role"`
	Content          string        `json:"content"`
	ReasoningContent string        `json:"reasoning_content,omitempty"`
	ToolCalls        []outToolCall `json:"tool_calls,omitempty"`
}

type outChoice struct {
	Index        int        `json:"index"`
	Message      outMessage `json:"message"`
	FinishReason string     `json:"finish_reason"`
}

// chatCompletionResponse es la respuesta única de /v1/chat/completions en
// modo no-streaming: forma chat.completion. Usage se conserva como
// json.RawMessage para reemitir el chunk final tal cual llegó (incluido
// credits_used si estaba presente), igual que el original hace
// `usage = final_usage or {...}` sin reconstruir el objeto campo a campo.
type chatCompletionResponse struct {
	ID      string          `json:"id"`
	Object  string          `json:"object"`
	Created int64           `json:"created"`
	Model   string          `json:"model"`
	Choices []outChoice     `json:"choices"`
	Usage   json.RawMessage `json:"usage"`
}

// collectResponse reconstruye una única respuesta chat.completion a partir
// de los bytes SSE que drivePipeline ya escribió — los MISMOS bytes que
// recibiría un cliente en modo streaming. Port de
// kiro.streaming_openai.collect_stream_response
// (.upstream/kiro/streaming_openai.py:576-688): el original construye la
// respuesta no-streaming consumiendo literalmente la salida de su propio
// generador SSE, no leyendo los acumuladores internos del formatter — este
// port replica ese diseño para que streaming y no-streaming NUNCA puedan
// discrepar sobre el contenido devuelto.
func collectResponse(sseBytes []byte, model string) chatCompletionResponse {
	var fullContent, fullReasoning string
	var toolCalls []outToolCall
	finishReason := "stop"
	usage := defaultUsage

	for _, data := range splitSSEData(sseBytes) {
		if len(data) == 0 || string(data) == "[DONE]" {
			continue
		}

		var chunk sseChunkIn
		if err := json.Unmarshal(data, &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		fullContent += choice.Delta.Content
		fullReasoning += choice.Delta.ReasoningContent

		for _, raw := range choice.Delta.ToolCalls {
			var tc deltaToolCallIn
			if err := json.Unmarshal(raw, &tc); err != nil {
				continue
			}
			toolType := tc.Type
			if toolType == "" {
				toolType = "function"
			}
			args := tc.Function.Arguments
			if args == "" {
				args = "{}"
			}
			toolCalls = append(toolCalls, outToolCall{
				ID:   tc.ID,
				Type: toolType,
				Function: outToolCallFunc{
					Name:      tc.Function.Name,
					Arguments: args,
				},
			})
		}

		if choice.FinishReason != nil && *choice.FinishReason != "" {
			finishReason = *choice.FinishReason
		}
		if len(chunk.Usage) > 0 {
			usage = chunk.Usage
		}
	}

	message := outMessage{Role: "assistant", Content: fullContent}
	if fullReasoning != "" {
		message.ReasoningContent = fullReasoning
	}
	if len(toolCalls) > 0 {
		message.ToolCalls = toolCalls
	}

	// El completion_id y created de la respuesta final son FRESCOS —
	// generados aquí, no reutilizados del id/created que el formatter usó
	// internamente para sus chunks (que quedan descartados junto con el
	// buffer SSE intermedio). Así hace también el original:
	// collect_stream_response genera su propio `completion_id` y `created`
	// (streaming_openai.py:608,680), distintos de los que
	// stream_kiro_to_openai_internal generó para sus propios chunks.
	return chatCompletionResponse{
		ID:      utils.GenerateCompletionID(),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   model,
		Choices: []outChoice{{Index: 0, Message: message, FinishReason: finishReason}},
		Usage:   usage,
	}
}

// splitSSEData extrae el payload de cada línea "data: ..." de un buffer de
// eventos SSE separados por "\n\n" (la salida de internal/sse.FormatEvent en
// dialecto OpenAI). Equivalente a
// `chunk_str[len("data:"):].strip()` del original (streaming_openai.py:619-622).
func splitSSEData(sseBytes []byte) [][]byte {
	var out [][]byte
	for _, event := range bytes.Split(sseBytes, []byte("\n\n")) {
		event = bytes.TrimSpace(event)
		if len(event) == 0 {
			continue
		}
		if !bytes.HasPrefix(event, []byte("data:")) {
			continue
		}
		data := bytes.TrimSpace(bytes.TrimPrefix(event, []byte("data:")))
		out = append(out, data)
	}
	return out
}
