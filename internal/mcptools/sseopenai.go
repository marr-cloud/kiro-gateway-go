// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"encoding/json"

	"github.com/marr-cloud/kiro-gateway-go/internal/sse"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// Separado de sse.go (que se acercaba al límite de 400 líneas del plan) sin
// otro motivo que el tamaño de archivo — sigue siendo el mismo paquete, y
// ambos generadores comparten chunkRunes/outputTokenCount/nowUnix definidos
// en sse.go.

// --- Formato OpenAI (mcp_tools.py:431-527) ---

type openAIChoiceDelta struct {
	Role    string `json:"role,omitempty"`
	Content string `json:"content,omitempty"`
}

type openAIChunkChoice struct {
	Index        int               `json:"index"`
	Delta        openAIChoiceDelta `json:"delta"`
	FinishReason *string           `json:"finish_reason"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type openAIChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Created int64               `json:"created"`
	Model   string              `json:"model"`
	Choices []openAIChunkChoice `json:"choices"`
	Usage   *openAIUsage        `json:"usage,omitempty"`
}

// GenerateOpenAIWebSearchSSE emite el stream OpenAI (chat.completion.chunk)
// que emula la ejecución server-side de web_search: el resumen se manda
// directamente como content, SIN el flujo tool_calls (el modelo no llama a
// ninguna tool, la API MCP ya se ejecutó). Puerto de mcp_tools.py:431-527.
// Igual que GenerateAnthropicWebSearchSSE (sse.go), materializa el stream
// completo de una vez — ver el comentario de esa función.
func GenerateOpenAIWebSearchSSE(model, query, toolUseID string, results map[string]any, inputTokens int) ([]byte, error) {
	_ = toolUseID // no usado en el dialecto OpenAI, igual que el original (mcp_tools.py:453)

	completionID := utils.GenerateCompletionID()
	createdTime := nowUnix()
	summary := GenerateSearchSummary(query, results)
	outputTokens := outputTokenCount(summary)

	var out []byte

	roleChunk := openAIChunk{
		ID: completionID, Object: "chat.completion.chunk", Created: createdTime, Model: model,
		Choices: []openAIChunkChoice{{Index: 0, Delta: openAIChoiceDelta{Role: "assistant"}, FinishReason: nil}},
	}
	data, err := json.Marshal(roleChunk)
	if err != nil {
		return nil, err
	}
	out = append(out, sse.FormatEvent("", data)...)

	for _, chunk := range chunkRunes(summary, 100) {
		contentChunk := openAIChunk{
			ID: completionID, Object: "chat.completion.chunk", Created: createdTime, Model: model,
			Choices: []openAIChunkChoice{{Index: 0, Delta: openAIChoiceDelta{Content: chunk}, FinishReason: nil}},
		}
		data, err := json.Marshal(contentChunk)
		if err != nil {
			return nil, err
		}
		out = append(out, sse.FormatEvent("", data)...)
	}

	stop := "stop"
	finalChunk := openAIChunk{
		ID: completionID, Object: "chat.completion.chunk", Created: createdTime, Model: model,
		Choices: []openAIChunkChoice{{Index: 0, Delta: openAIChoiceDelta{}, FinishReason: &stop}},
		Usage: &openAIUsage{
			PromptTokens:     inputTokens,
			CompletionTokens: outputTokens,
			TotalTokens:      inputTokens + outputTokens,
		},
	}
	data, err = json.Marshal(finalChunk)
	if err != nil {
		return nil, err
	}
	out = append(out, sse.FormatEvent("", data)...)

	out = append(out, sse.FormatDone()...)
	return out, nil
}
