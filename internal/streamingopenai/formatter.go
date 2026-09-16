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

// Formatter converts streamingcore.KiroEvent objects into OpenAI SSE-formatted chunks.
// It accumulates state across multiple Handle() calls and finalizes with Finish().
//
// Each chunk is a chat.completion.chunk with the shape:
//
//	{
//	  "id": "chatcmpl-<32hex>",
//	  "object": "chat.completion.chunk",
//	  "created": <unix timestamp>,
//	  "model": "<model>",
//	  "choices": [{
//	    "index": 0,
//	    "delta": {...},
//	    "finish_reason": null | "stop" | "tool_calls" | "length"
//	  }]
//	}
//
// Not safe for concurrent use. Each streaming response owns one Formatter instance.
// ThinkingHandling determines how thinking content is formatted in OpenAI chunks.
type ThinkingHandling string

const (
	// AsReasoningContent emits thinking as "reasoning_content" delta
	AsReasoningContent ThinkingHandling = "as_reasoning_content"
	// AsContent emits thinking as "content" delta (legacy)
	AsContent ThinkingHandling = "as_content"
)

type Formatter struct {
	completionID           string
	model                  string
	created                int64
	firstChunk             bool
	fullContent            string
	fullThinkingContent    string
	toolCallsFromStream    []map[string]any
	meteringData           *UsageData
	meteringDataRaw        map[string]any // original metering data from event
	contextUsagePercentage float64
	completionTokens       int
	thinkingHandling       ThinkingHandling
}

// UsageData mirrors the usage metrics from KiroEvent.
type UsageData struct {
	Input           int
	Output          int
	CacheRead       int
	CacheCreation   int
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
		fullContent:      "",
		fullThinkingContent: "",
		toolCallsFromStream: []map[string]any{},
		thinkingHandling: handling,
	}
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
			// Collect tool call for later emission
			toolCall := f.toolUseToOpenAI(ev.ToolUse)
			f.toolCallsFromStream = append(f.toolCallsFromStream, toolCall)
		}

	case "usage":
		if ev.Usage != nil {
			f.meteringData = &UsageData{
				Input:         ev.Usage.Input,
				Output:        ev.Usage.Output,
				CacheRead:     ev.Usage.CacheRead,
				CacheCreation: ev.Usage.CacheCreation,
			}
			// Also store as a dict for credits_used field
			f.meteringDataRaw = map[string]any{
				"inputTokenCount":     ev.Usage.Input,
				"outputTokenCount":    ev.Usage.Output,
				"cacheReadTokenCount": ev.Usage.CacheRead,
				"cacheCreationTokenCount": ev.Usage.CacheCreation,
			}
		}

	case "context_usage":
		f.contextUsagePercentage = ev.ContextUsage
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

	// Determine finish_reason (must happen before calculateUsage)
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

// emitContentChunk emits a single content chunk.
func (f *Formatter) emitContentChunk(content string, w io.Writer) error {
	delta := map[string]any{"content": content}
	if f.firstChunk {
		delta["role"] = "assistant"
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
// The delta key depends on the thinkingHandling mode:
// - AsReasoningContent: uses "reasoning_content"
// - AsContent: uses "content"
func (f *Formatter) emitThinkingChunk(thinking string, w io.Writer) error {
	delta := map[string]any{}
	if f.thinkingHandling == AsReasoningContent {
		delta["reasoning_content"] = thinking
	} else {
		delta["content"] = thinking
	}

	if f.firstChunk {
		delta["role"] = "assistant"
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
	indexedToolCalls := make([]map[string]any, len(toolCalls))
	for i, tc := range toolCalls {
		func_ := tc["function"].(map[string]any)
		indexedToolCalls[i] = map[string]any{
			"index": i,
			"id":    tc["id"],
			"type":  tc["type"],
			"function": map[string]any{
				"name":      func_["name"],
				"arguments": func_["arguments"],
			},
		}
	}

	delta := map[string]any{"tool_calls": indexedToolCalls}
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
	chunk := f.makeChunk(map[string]any{}, &finishReason)
	chunk["usage"] = usage

	data, err := json.Marshal(chunk)
	if err != nil {
		return err
	}

	_, err = w.Write(sse.FormatEvent("", data))
	return err
}

// makeChunk creates a base chunk structure with the given delta and optional finish_reason.
func (f *Formatter) makeChunk(delta map[string]any, finishReason *string) map[string]any {
	reason := interface{}(nil)
	if finishReason != nil {
		reason = *finishReason
	}

	return map[string]any{
		"id":      f.completionID,
		"object":  "chat.completion.chunk",
		"created": f.created,
		"model":   f.model,
		"choices": []map[string]any{
			{
				"index":         0,
				"delta":         delta,
				"finish_reason": reason,
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

// determineFinishReason determines the finish reason based on what was emitted.
// Truncation detection: if stream_completed_normally is false (no usage or context_usage received)
// and we have content, then content was truncated.
func (f *Formatter) determineFinishReason() string {
	// Check if we received completion signals (usage or context_usage)
	streamCompletedNormally := f.meteringData != nil || f.contextUsagePercentage > 0

	// Detect content truncation
	contentWasTruncated := !streamCompletedNormally && len(f.fullContent) > 0 && len(f.toolCallsFromStream) == 0

	if contentWasTruncated {
		return "length"
	}
	if len(f.toolCallsFromStream) > 0 {
		return "tool_calls"
	}
	return "stop"
}

// calculateUsage calculates the token usage for the response.
// Uses completion_tokens from tiktoken and calculates prompt_tokens from context_usage if available.
func (f *Formatter) calculateUsage() map[string]any {
	promptTokens := 0
	totalTokens := f.completionTokens

	// If we have context_usage_percentage, use it to calculate prompt_tokens
	// Formula: prompt_tokens = round(total_context × context_usage_percentage) - completion_tokens
	// where total_context is the max input tokens (default 200000 per spec)
	if f.contextUsagePercentage > 0 {
		// Use a reasonable default for max input tokens
		const defaultMaxInputTokens = 200000
		totalContextUsed := int(float64(defaultMaxInputTokens) * f.contextUsagePercentage)
		promptTokens = totalContextUsed - f.completionTokens
		if promptTokens < 0 {
			promptTokens = 0
		}
		totalTokens = promptTokens + f.completionTokens
	}

	usage := map[string]any{
		"prompt_tokens":     promptTokens,
		"completion_tokens": f.completionTokens,
		"total_tokens":      totalTokens,
	}

	// Add metering data if available
	if f.meteringData != nil {
		// metering_data from usage event
		usage["prompt_tokens"] = f.meteringData.Input
		usage["completion_tokens"] = f.meteringData.Output
		usage["total_tokens"] = f.meteringData.Input + f.meteringData.Output
	}

	// Add credits_used (metering data) if available
	if f.meteringDataRaw != nil {
		usage["credits_used"] = f.meteringDataRaw
	}

	return usage
}
