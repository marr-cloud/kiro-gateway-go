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
	creditsUsedRaw         json.RawMessage // metering_data, echoed verbatim as usage.credits_used (streaming_openai.py:405-406)
	contextUsagePercentage float64
	thinkingHandling       ThinkingHandling
	receivedContextUsage   bool              // upstream `received_context_usage`: a context_usage event arrived (streaming_core.py calculate loop)
	requestMessages        []json.RawMessage // request_messages fallback (streaming_openai.py:317-320)
	requestTools           []json.RawMessage // request_tools fallback (streaming_openai.py:319-320)

	// truncatedTools collects tool calls flagged with truncation during streaming,
	// populated as a side-channel when processing "tool_use" events (Task 8b).
	// Each entry: {ID, Name, TruncationInfo}
	truncatedTools []truncatedToolRecord

	// contentWasTruncated stores the computed value from Finish() (Task 8b).
	// Must be read AFTER Finish() completes; mirrors the Finish logic using
	// allToolCalls (stream + bracket, deduped), not just toolCallsFromStream.
	contentWasTruncated bool
}

// truncatedToolRecord represents a single truncated tool call collected during streaming.
type truncatedToolRecord struct {
	ID             string
	Name           string
	TruncationInfo map[string]any
}

// New creates a new Formatter for the given model.
// thinkingHandling determines how to format thinking content (defaults to AsContent).
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

// SetRequestContext sets the original request messages and tools, each as raw
// JSON bytes (original key order preserved). They feed the fallback
// prompt_tokens calculation used when Kiro doesn't report context_usage_percentage
// (.upstream/kiro/streaming_openai.py:313-323).
func (f *Formatter) SetRequestContext(messages []json.RawMessage, tools []json.RawMessage) {
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

			// Collect truncated tool calls as a side-channel (Task 8b: SAVE side).
			// No SSE bytes are affected; this is purely internal state accumulation.
			if ev.ToolUse.TruncationDetected {
				f.truncatedTools = append(f.truncatedTools, truncatedToolRecord{
					ID:             ev.ToolUse.ID,
					Name:           ev.ToolUse.Name,
					TruncationInfo: ev.ToolUse.TruncationInfo,
				})
			}
		}

	case "usage":
		// metering_data only counts as "received" when truthy, matching
		// `elif event.type == "usage" and event.usage: metering_data = event.usage`
		// (.upstream/kiro/streaming_core.py:320).
		if isJSONTruthy(ev.UsageRaw) {
			f.creditsUsedRaw = ev.UsageRaw
		}

	case "context_usage":
		f.contextUsagePercentage = ev.ContextUsage
		f.receivedContextUsage = true
	}

	return nil
}

// chunkDelta holds the delta object in a chunk, with ordered fields.
type chunkDelta struct {
	Role             string     `json:"role,omitempty"`
	Content          string     `json:"content,omitempty"`
	ReasoningContent string     `json:"reasoning_content,omitempty"`
	ToolCalls        []toolCall `json:"tool_calls,omitempty"`
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

// chunkUsage holds the usage object in a chunk. CreditsUsed carries the raw
// bytes of whatever the "usage" event's payload was (number or object) so it
// round-trips exactly like Python's `final_chunk["usage"]["credits_used"] =
// metering_data` (streaming_openai.py:405-406) — no derived/reformatted value.
type chunkUsage struct {
	PromptTokens     int             `json:"prompt_tokens"`
	CompletionTokens int             `json:"completion_tokens"`
	TotalTokens      int             `json:"total_tokens"`
	CreditsUsed      json.RawMessage `json:"credits_used,omitempty"`
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
func (f *Formatter) emitFinalChunk(finishReason string, promptTokens, completionTokens, totalTokens int, w io.Writer) error {
	chunk := f.makeChunk(chunkDelta{}, &finishReason)

	chunk.Usage = &chunkUsage{
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
		CreditsUsed:      f.creditsUsedRaw,
	}

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

// calculateTokens computes (prompt_tokens, total_tokens) from completionTokens,
// mirroring calculate_tokens_from_context_usage plus its caller's fallback
// (.upstream/kiro/streaming_core.py:337-362, streaming_openai.py:307-323):
//
//  1. context_usage_percentage > 0: total = int(pct/100 * max_input_tokens),
//     prompt = max(0, total - completion). total_tokens is the API-derived
//     total, NOT prompt+completion.
//  2. Otherwise, if request_messages were supplied: prompt = count_message_tokens
//     + count_tools_tokens (no Claude correction), total = prompt + completion.
//  3. Otherwise: prompt = 0, total = completion.
func (f *Formatter) calculateTokens(completionTokens int) (promptTokens, totalTokens int) {
	if f.contextUsagePercentage > 0 {
		// TODO(fase-6): pull the model's real max input tokens from the
		// model resolver/cache (model_cache.get_max_input_tokens(model) in
		// streaming_core.py:356); 200000 is upstream's fallback default.
		const maxInputTokens = 200000
		totalTokens = int((f.contextUsagePercentage / 100.0) * float64(maxInputTokens))
		promptTokens = totalTokens - completionTokens
		if promptTokens < 0 {
			promptTokens = 0
		}
		return promptTokens, totalTokens
	}

	if len(f.requestMessages) > 0 {
		// apply_claude_correction=False for prompt_tokens: upstream calibrated
		// the 1.15 factor for completion_tokens only
		// (.upstream/kiro/streaming_openai.py:315-320).
		promptTokens = tokenizer.CountMessageTokens(f.requestMessages, false) + tokenizer.CountToolsTokens(f.requestTools, false)
		return promptTokens, promptTokens + completionTokens
	}

	return 0, completionTokens
}
