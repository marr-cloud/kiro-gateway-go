// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"encoding/json"

	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
	"github.com/marr-cloud/kiro-gateway-go/internal/sse"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// Separado de sse.go (que se acercaba al límite de 400 líneas del plan) sin
// otro motivo que el tamaño de archivo — sigue siendo el mismo paquete, y
// ambos generadores comparten chunkRunes/outputTokenCount/nowUnix definidos
// en sse.go.

// writeOpenAIChunk serializa chunk, lo reformatea con pyjson.Dumps —
// mcp_tools.py:488,505,524 pasan explícitamente `json.dumps(chunk,
// ensure_ascii=False)` en los tres sitios — y lo emite framed sin prefijo
// "event:" (dialecto OpenAI). Ver el comentario de cabecera de sse.go sobre
// por qué pyjson.Dumps y no encoding/json.Marshal a secas.
func writeOpenAIChunk(buf *[]byte, chunk any) error {
	raw, err := json.Marshal(chunk)
	if err != nil {
		return err
	}
	formatted, err := pyjson.Dumps(raw)
	if err != nil {
		return err
	}
	*buf = append(*buf, sse.FormatEvent("", []byte(formatted))...)
	return nil
}

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
	if err := writeOpenAIChunk(&out, roleChunk); err != nil {
		return nil, err
	}

	for _, chunk := range chunkRunes(summary, 100) {
		contentChunk := openAIChunk{
			ID: completionID, Object: "chat.completion.chunk", Created: createdTime, Model: model,
			Choices: []openAIChunkChoice{{Index: 0, Delta: openAIChoiceDelta{Content: chunk}, FinishReason: nil}},
		}
		if err := writeOpenAIChunk(&out, contentChunk); err != nil {
			return nil, err
		}
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
	if err := writeOpenAIChunk(&out, finalChunk); err != nil {
		return nil, err
	}

	out = append(out, sse.FormatDone()...)
	return out, nil
}
