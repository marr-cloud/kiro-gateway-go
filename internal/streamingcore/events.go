// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package streamingcore implements the unified KiroEvent pipeline that stitches
// parsers.Parser output through thinkingparser.Parser into a stream of KiroEvent
// objects consumed by streaming formatters (streamingopenai, streaminganthropic).
package streamingcore

import "encoding/json"

// KiroEvent represents a unified event from the Kiro API stream.
// This format is API-agnostic and can be converted to both OpenAI and Anthropic formats.
type KiroEvent struct {
	// Kind identifies the event type:
	// "content" - regular text output
	// "thinking" - reasoning/thinking block content
	// "tool_use" - function call invocation
	// "usage" - token usage metrics
	// "context_usage" - context window usage percentage
	// "error" - truncation diagnosis or other error
	Kind string

	// Content holds text output (for Kind == "content")
	Content string

	// Thinking holds reasoning/thinking content (for Kind == "thinking")
	Thinking string

	// ToolUse holds tool/function call data (for Kind == "tool_use")
	ToolUse *ToolUseData

	// Usage holds token usage metrics (for Kind == "usage")
	Usage *UsageData

	// UsageRaw holds the raw JSON bytes of the usage payload as received
	// (for Kind == "usage"), preserving key order and original number
	// formatting (e.g. "1.0" vs "1"). Downstream formatters pass this
	// through verbatim as `credits_used`, matching upstream's
	// `final_chunk["usage"]["credits_used"] = metering_data`
	// (.upstream/kiro/streaming_openai.py:405-406), which echoes whatever
	// shape the Kiro API sent — not necessarily the typed Input/Output/
	// CacheRead/CacheCreation breakdown captured in Usage above.
	UsageRaw json.RawMessage

	// ContextUsage holds context usage percentage (for Kind == "context_usage")
	ContextUsage float64

	// Error holds error/diagnosis message (for Kind == "error")
	Error string
}

// ToolUseData represents a function call invocation within a KiroEvent.
type ToolUseData struct {
	// ID is the unique identifier for this tool call
	ID string

	// Name is the function/tool name
	Name string

	// Input is the parsed function arguments as a map
	Input map[string]any

	// TruncationDetected flags whether this tool call was truncated
	// (set by the parser when arguments could not be fully parsed).
	// Mirror of parsers.Parser's _truncation_detected.
	TruncationDetected bool

	// TruncationInfo holds diagnostic data about the truncation
	// (e.g., size_bytes, reason). Mirror of parsers.Parser's
	// _truncation_info. Only meaningful when TruncationDetected is true.
	TruncationInfo map[string]any
}

// UsageData represents token usage metrics within a KiroEvent.
type UsageData struct {
	// Input is the number of input tokens
	Input int

	// Output is the number of output tokens
	Output int

	// CacheRead is the number of cache read tokens
	CacheRead int

	// CacheCreation is the number of cache creation tokens
	CacheCreation int
}
