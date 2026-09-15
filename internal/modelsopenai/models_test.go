// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package modelsopenai_test

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestChatMessageRoundTrip decodifica el arg 0 ([]ChatMessage serializado) de
// cada caso golden de kiro.converters_openai:convert_openai_messages_to_unified
// y comprueba que json.Unmarshal no falla. No hay conversión aquí: eso lo hace
// el converter en una fase posterior. Este test solo verifica que el struct
// ChatMessage puede recibir cualquier forma de petición OpenAI grabada por el
// corpus (content como string, como lista de bloques, o ausente).
func TestChatMessageRoundTrip(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_openai/convert_openai_messages_to_unified") {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			var msgs []modelsopenai.ChatMessage
			if err := json.Unmarshal(raw, &msgs); err != nil {
				t.Fatalf("case %s: unmarshal: %v", c.Name, err)
			}
			// No re-marshal check aquí, sólo que decodifica (ver brief del task 1).
		})
	}
}

// TestChatCompletionRequestDecode comprueba que ChatCompletionRequest decodifica
// una petición completa típica de /v1/chat/completions, incluidos tools en
// formato estándar OpenAI y en formato plano (Cursor-style).
func TestChatCompletionRequestDecode(t *testing.T) {
	body := `{
		"model": "claude-sonnet-4-5",
		"messages": [
			{"role": "system", "content": "be terse"},
			{"role": "user", "content": "hi"}
		],
		"stream": true,
		"temperature": 0.5,
		"max_tokens": 100,
		"tools": [
			{"type": "function", "function": {"name": "get_weather", "description": "d", "parameters": {"type": "object"}}},
			{"name": "flat_tool", "description": "d2", "input_schema": {"type": "object"}}
		],
		"tool_choice": "auto"
	}`

	var req modelsopenai.ChatCompletionRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.Model != "claude-sonnet-4-5" {
		t.Errorf("Model = %q, want claude-sonnet-4-5", req.Model)
	}
	if len(req.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(req.Messages))
	}
	if !req.Stream {
		t.Errorf("Stream = false, want true")
	}
	if req.Temperature == nil || *req.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", req.Temperature)
	}
	if req.MaxTokens == nil || *req.MaxTokens != 100 {
		t.Errorf("MaxTokens = %v, want 100", req.MaxTokens)
	}
	if len(req.Tools) != 2 {
		t.Fatalf("len(Tools) = %d, want 2", len(req.Tools))
	}
	if req.Tools[0].Function == nil || req.Tools[0].Function.Name != "get_weather" {
		t.Errorf("Tools[0].Function.Name = %v, want get_weather", req.Tools[0].Function)
	}
	if req.Tools[1].Name == nil || *req.Tools[1].Name != "flat_tool" {
		t.Errorf("Tools[1].Name = %v, want flat_tool", req.Tools[1].Name)
	}
}

// TestChatCompletionResponseRoundTrip verifica que ChatCompletionResponse decodifica
// y vuelve a codificar preservando los campos esperados.
func TestChatCompletionResponseRoundTrip(t *testing.T) {
	body := `{
		"id": "chatcmpl-123",
		"object": "chat.completion",
		"created": 1700000000,
		"model": "claude-sonnet-4-5",
		"choices": [
			{"index": 0, "message": {"role": "assistant", "content": "hi"}, "finish_reason": "stop"}
		],
		"usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15}
	}`

	var resp modelsopenai.ChatCompletionResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.ID != "chatcmpl-123" {
		t.Errorf("ID = %q, want chatcmpl-123", resp.ID)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].FinishReason == nil || *resp.Choices[0].FinishReason != "stop" {
		t.Fatalf("Choices = %+v", resp.Choices)
	}
	if resp.Usage.TotalTokens != 15 {
		t.Errorf("Usage.TotalTokens = %d, want 15", resp.Usage.TotalTokens)
	}

	out, err := json.Marshal(&resp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var roundtrip modelsopenai.ChatCompletionResponse
	if err := json.Unmarshal(out, &roundtrip); err != nil {
		t.Fatalf("unmarshal roundtrip: %v", err)
	}
	if roundtrip.ID != resp.ID || roundtrip.Usage.TotalTokens != resp.Usage.TotalTokens {
		t.Errorf("roundtrip mismatch: %+v vs %+v", roundtrip, resp)
	}
}

// TestChatCompletionChunkDecode verifica el struct de streaming, incluido el
// caso donde Usage está ausente (solo aparece en el último chunk).
func TestChatCompletionChunkDecode(t *testing.T) {
	body := `{
		"id": "chatcmpl-123",
		"object": "chat.completion.chunk",
		"created": 1700000000,
		"model": "claude-sonnet-4-5",
		"choices": [
			{"index": 0, "delta": {"role": "assistant", "content": "hi"}, "finish_reason": null}
		]
	}`

	var chunk modelsopenai.ChatCompletionChunk
	if err := json.Unmarshal([]byte(body), &chunk); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if chunk.Usage != nil {
		t.Errorf("Usage = %+v, want nil", chunk.Usage)
	}
	if len(chunk.Choices) != 1 || chunk.Choices[0].Delta.Content == nil || *chunk.Choices[0].Delta.Content != "hi" {
		t.Fatalf("Choices = %+v", chunk.Choices)
	}
}

// TestModelListDecode verifica el struct de /v1/models.
func TestModelListDecode(t *testing.T) {
	body := `{"object": "list", "data": [{"id": "claude-sonnet-4-5", "object": "model", "created": 1700000000, "owned_by": "anthropic"}]}`

	var list modelsopenai.ModelList
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list.Data) != 1 || list.Data[0].ID != "claude-sonnet-4-5" {
		t.Fatalf("Data = %+v", list.Data)
	}
}
