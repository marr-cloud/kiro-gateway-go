// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package streamingcore implements the unified KiroEvent pipeline that stitches
// parsers.Parser output through thinkingparser.Parser into a stream of KiroEvent
// objects consumed by streaming formatters (streamingopenai, streaminganthropic).
package streamingcore

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
