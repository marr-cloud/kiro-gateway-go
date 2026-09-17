// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingopenai

import (
	"encoding/json"
	"io"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/parsers"
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

// Finish completes the stream by emitting any pending tool calls, the final chunk with usage,
// and the [DONE] marker.
func (f *Formatter) Finish(w io.Writer) error {
	// Bracket-style tool calls ("[Called fn with args: {...}]") detected
	// post-loop in the accumulated content, merged with the stream's tool
	// calls and deduplicated (streaming_openai.py:276-279). NOTE: the OpenAI
	// upstream applies deduplicate_tool_calls here; the streaminganthropic
	// twin does NOT (its upstream has no dedup step), so the two dialects
	// legitimately differ on this line. dedup runs unconditionally, matching
	// upstream, even when there are no bracket calls.
	bracketCalls := parsers.ParseBracketToolCalls(f.fullContent)
	merged := make([]map[string]any, 0, len(f.toolCallsFromStream)+len(bracketCalls))
	merged = append(merged, f.toolCallsFromStream...)
	merged = append(merged, bracketCalls...)
	allToolCalls := parsers.DeduplicateToolCalls(merged)

	// Emit tool calls if any (stream + bracket, deduplicated)
	if len(allToolCalls) > 0 {
		if err := f.emitToolCallsChunk(allToolCalls, w); err != nil {
			return err
		}
	}

	// completion_tokens uses the Claude correction factor by default, matching
	// `count_tokens(full_content + full_thinking_content)` (streaming_openai.py:305),
	// which calls count_tokens with apply_claude_correction defaulting to True.
	completionTokens := tokenizer.CountTokens(f.fullContent+f.fullThinkingContent, true)

	// stream_completed_normally = received_usage or received_context_usage
	// (streaming_openai.py:272-274).
	streamCompletedNormally := len(f.creditsUsedRaw) > 0 || f.receivedContextUsage
	contentTruncated := !streamCompletedNormally && len(f.fullContent) > 0 && len(allToolCalls) == 0

	var finishReason string
	switch {
	case contentTruncated:
		finishReason = "length"
	case len(allToolCalls) > 0:
		finishReason = "tool_calls"
	default:
		finishReason = "stop"
	}

	promptTokens, totalTokens := f.calculateTokens(completionTokens)

	// Emit final chunk with usage
	if err := f.emitFinalChunk(finishReason, promptTokens, completionTokens, totalTokens, w); err != nil {
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

// TruncatedTools devuelve la lista de tool calls que fueron truncados durante
// el stream, recolectados como un side-channel en Handle (Task 8b).
// Cada entrada contiene {ID, Name, TruncationInfo}. Espeja
// streaming_openai.py:366-383 (truncated_tools collection).
func (f *Formatter) TruncatedTools() []truncatedToolRecord {
	return f.truncatedTools
}

// FullContent devuelve el contenido completo acumulado durante el stream.
// Solo text content, no thinking (streaming_openai.py:304-305).
func (f *Formatter) FullContent() string {
	return f.fullContent
}

// ContentWasTruncated retorna true si la respuesta fue truncada por tamaño.
// Espeja la lógica de streaming_openai.py:271-274:
// stream_completed_normally = received_usage or received_context_usage
// content_truncated = not stream_completed_normally and len(full_content) > 0
// and len(tool_calls) == 0.
func (f *Formatter) ContentWasTruncated() bool {
	streamCompletedNormally := len(f.creditsUsedRaw) > 0 || f.receivedContextUsage
	return !streamCompletedNormally && len(f.fullContent) > 0 && len(f.toolCallsFromStream) == 0
}
