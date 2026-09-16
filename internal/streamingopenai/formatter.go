// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingopenai

import (
	"encoding/json"
	"io"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/sse"
	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
	"github.com/marr-cloud/kiro-gateway-go/internal/tokenizer"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// ThinkingHandling determines how thinking content is formatted in OpenAI chunks.
type ThinkingHandling string

const (
	// AsReasoningContent emits thinking as "reasoning_content" delta
	AsReasoningContent ThinkingHandling = "as_reasoning_content"
	// AsContent emits thinking as "content" delta (legacy)
	AsContent ThinkingHandling = "as_content"
)

// Formatter converts streamingcore.KiroEvent objects into OpenAI SSE-formatted chunks.
// It accumulates state across multiple Handle() calls and finalizes with Finish().
//
// Not safe for concurrent use. Each streaming response owns one Formatter instance.
type Formatter struct {
	completionID           string
	model                  string
	created                int64
	firstChunk             bool
	fullContent            string
	fullThinkingContent    string
	toolCallsFromStream    []map[string]any
	creditsUsed            float64           // metering data float value
	contextUsagePercentage float64
	completionTokens       int
	thinkingHandling       ThinkingHandling
	receivedStopSignal     bool              // did we get a "usage" or "context_usage" event?
	requestMessages        []map[string]any  // for fallback prompt token calculation
	requestTools           []map[string]any  // for fallback prompt token calculation
}

// New creates a new Formatter for the given model.
// thinkingHandling determines how to format thinking content (defaults to AsContent).
// requestMessages and requestTools are used for fallback prompt_tokens calculation if context_usage is not available.
func New(model string, thinkingHandling ...ThinkingHandling) *Formatter {
	handling := AsContent
	if len(thinkingHandling) > 0 {
		handling = thinkingHandling[0]
	}

	return &Formatter{
		completionID:     utils.GenerateCompletionID(),
		model:            model,
		created:          time.Now().Unix(),
		firstChunk:       true,
		thinkingHandling: handling,
	}
}

// SetRequestContext sets the request messages and tools for prompt token calculation fallback.
func (f *Formatter) SetRequestContext(messages []map[string]any, tools []map[string]any) {
	f.requestMessages = messages
	f.requestTools = tools
}

// Handle processes a single KiroEvent and writes the corresponding SSE chunk(s) to w.
func (f *Formatter) Handle(ev streamingcore.KiroEvent, w io.Writer) error {
	switch ev.Kind {
	case "content":
		if ev.Content != "" {
			f.fullContent += ev.Content
			return f.emitContentChunk(ev.Content, w)
		}

	case "thinking":
		if ev.Thinking != "" {
			f.fullThinkingContent += ev.Thinking
			return f.emitThinkingChunk(ev.Thinking, w)
		}

	case "tool_use":
		if ev.ToolUse != nil {
			toolCall := f.toolUseToOpenAI(ev.ToolUse)
			f.toolCallsFromStream = append(f.toolCallsFromStream, toolCall)
		}

	case "usage":
		// usage event is just a float for credits_used
		f.receivedStopSignal = true

	case "context_usage":
		f.contextUsagePercentage = ev.ContextUsage
		f.receivedStopSignal = true
	}

	return nil
}

// Finish completes the stream by emitting any pending tool calls, the final chunk with usage,
// and the [DONE] marker.
func (f *Formatter) Finish(w io.Writer) error {
	// Emit tool calls if any were collected
	if len(f.toolCallsFromStream) > 0 {
		if err := f.emitToolCallsChunk(f.toolCallsFromStream, w); err != nil {
			return err
		}
	}

	// Calculate tokens for completion
	f.completionTokens = len(tokenizer.EncodeOrdinary(f.fullContent + f.fullThinkingContent))

	// Determine finish_reason
	finishReason := f.determineFinishReason()

	// Emit final chunk with usage
	usage := f.calculateUsage()
	if err := f.emitFinalChunk(finishReason, usage, w); err != nil {
		return err
	}

	// Emit [DONE]
	if _, err := w.Write(sse.FormatDone()); err != nil {
		return err
	}

	return nil
}

// chunkDelta holds the delta object in a chunk, with ordered fields.
type chunkDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall      `json:"tool_calls,omitempty"`
}

// toolCall holds a tool call in the delta.
type toolCall struct {
	Index    int    `json:"index"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chunkChoice holds a choice in a chunk.
type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason *string    `json:"finish_reason"`
}

// chunkUsage holds the usage object in a chunk.
type chunkUsage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	CreditsUsed      *float64 `json:"credits_used,omitempty"`
}

// chatCompletionChunk is the full chunk structure with ordered fields.
type chatCompletionChunk struct {
	ID      string        `json:"id"`
	Object  string        `json:"object"`
	Created int64         `json:"created"`
	Model   string        `json:"model"`
	Choices []chunkChoice `json:"choices"`
	Usage   *chunkUsage   `json:"usage,omitempty"`
}

// emitContentChunk emits a single content chunk.
func (f *Formatter) emitContentChunk(content string, w io.Writer) error {
	delta := chunkDelta{Content: content}
	if f.firstChunk {
		delta.Role = "assistant"
		f.firstChunk = false
	}

	chunk := f.makeChunk(delta, nil)
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	_, err = w.Write(sse.FormatEvent("", data))
	return err
}

// emitThinkingChunk emits a thinking/reasoning chunk.
func (f *Formatter) emitThinkingChunk(thinking string, w io.Writer) error {
	delta := chunkDelta{}
	if f.thinkingHandling == AsReasoningContent {
		delta.ReasoningContent = thinking
	} else {
		delta.Content = thinking
	}

	if f.firstChunk {
		delta.Role = "assistant"
		f.firstChunk = false
	}

	chunk := f.makeChunk(delta, nil)
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	_, err = w.Write(sse.FormatEvent("", data))
	return err
}

// emitToolCallsChunk emits the tool calls in a single chunk.
func (f *Formatter) emitToolCallsChunk(toolCalls []map[string]any, w io.Writer) error {
	// Index tool calls for OpenAI format
	indexedToolCalls := make([]toolCall, len(toolCalls))
	for i, tc := range toolCalls {
		func_ := tc["function"].(map[string]any)
		indexedToolCalls[i] = toolCall{
			Index: i,
			ID:    tc["id"].(string),
			Type:  tc["type"].(string),
		}
		indexedToolCalls[i].Function.Name = func_["name"].(string)
		indexedToolCalls[i].Function.Arguments = func_["arguments"].(string)
	}

	delta := chunkDelta{ToolCalls: indexedToolCalls}
	chunk := f.makeChunk(delta, nil)
	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	_, err = w.Write(sse.FormatEvent("", data))
	return err
}

// emitFinalChunk emits the final chunk with finish_reason and usage.
func (f *Formatter) emitFinalChunk(finishReason string, usage map[string]any, w io.Writer) error {
	chunk := f.makeChunk(chunkDelta{}, &finishReason)

	// Convert usage map to chunkUsage struct
	chunkUsage := &chunkUsage{
		PromptTokens:     toInt(usage["prompt_tokens"]),
		CompletionTokens: toInt(usage["completion_tokens"]),
		TotalTokens:      toInt(usage["total_tokens"]),
	}

	if creditsUsed, ok := usage["credits_used"]; ok {
		if f, isFloat := creditsUsed.(float64); isFloat {
			chunkUsage.CreditsUsed = &f
		}
	}

	chunk.Usage = chunkUsage

	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	_, err = w.Write(sse.FormatEvent("", data))
	return err
}

// makeChunk creates a base chunk structure with the given delta and optional finish_reason.
func (f *Formatter) makeChunk(delta chunkDelta, finishReason *string) *chatCompletionChunk {
	return &chatCompletionChunk{
		ID:      f.completionID,
		Object:  "chat.completion.chunk",
		Created: f.created,
		Model:   f.model,
		Choices: []chunkChoice{
			{
				Index:        0,
				Delta:        delta,
				FinishReason: finishReason,
			},
		},
	}
}

// toInt converts a value to int.
func toInt(v any) int {
	switch val := v.(type) {
	case int:
		return val
	case float64:
		return int(val)
	default:
		return 0
	}
}

// toolUseToOpenAI converts a ToolUseData to the OpenAI tool call format.
func (f *Formatter) toolUseToOpenAI(tu *streamingcore.ToolUseData) map[string]any {
	// Marshal Input to JSON string
	inputBytes, _ := json.Marshal(tu.Input)
	inputStr := string(inputBytes)

	return map[string]any{
		"id":   tu.ID,
		"type": "function",
		"function": map[string]any{
			"name":      tu.Name,
			"arguments": inputStr,
		},
	}
}

// determineFinishReason determines the finish reason based on what was emitted.
// finish_reason = "stop" when normal completion,
//   "tool_calls" when tool calls were emitted,
//   "length" when content was truncated (no completion signal received).
func (f *Formatter) determineFinishReason() string {
	if len(f.toolCallsFromStream) > 0 {
		return "tool_calls"
	}

	// If we received a stop signal (usage or context_usage event), finish with "stop"
	if f.receivedStopSignal {
		return "stop"
	}

	// If we have content but no stop signal, it was truncated
	if len(f.fullContent) > 0 {
		return "length"
	}

	return "stop"
}

// calculateUsage calculates the token usage for the response.
func (f *Formatter) calculateUsage() map[string]any {
	promptTokens := 0

	// If we have context_usage_percentage, use it to calculate prompt_tokens
	if f.contextUsagePercentage > 0 {
		const defaultMaxInputTokens = 200000
		totalContextUsed := int(float64(defaultMaxInputTokens) * f.contextUsagePercentage)
		promptTokens = totalContextUsed - f.completionTokens
		if promptTokens < 0 {
			promptTokens = 0
		}
	} else if f.requestMessages != nil {
		// Fallback: count tokens from request_messages and request_tools
		// This matches upstream's count_message_tokens + count_tools_tokens behavior
		promptTokens = f.countRequestTokens()
	}

	usage := map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": f.completionTokens,
		"total_tokens":      promptTokens + f.completionTokens,
	}

	// Add credits_used if we have it
	if f.creditsUsed > 0 {
		usage["credits_used"] = f.creditsUsed
	}

	return usage
}

// countRequestTokens estimates prompt tokens by tokenizing the request messages and tools.
// This is a simplified approximation matching upstream's behavior when context_usage is not available.
func (f *Formatter) countRequestTokens() int {
	count := 0

	// Count tokens from messages
	if f.requestMessages != nil {
		for _, msg := range f.requestMessages {
			// Count role: "assistant", "user", etc.
			if role, ok := msg["role"]; ok {
				if s, isStr := role.(string); isStr {
					count += len(tokenizer.EncodeOrdinary(s))
				}
			}

			// Count content
			if content, ok := msg["content"]; ok {
				switch c := content.(type) {
				case string:
					count += len(tokenizer.EncodeOrdinary(c))
				default:
					// Content blocks or other structures
					if b, err := json.Marshal(c); err == nil {
						count += len(tokenizer.EncodeOrdinary(string(b)))
					}
				}
			}
		}
	}

	// Count tokens from tools
	if f.requestTools != nil {
		if b, err := json.Marshal(f.requestTools); err == nil {
			count += len(tokenizer.EncodeOrdinary(string(b)))
		}
	}

	return count
}
