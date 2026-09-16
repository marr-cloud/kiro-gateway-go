// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package tokenizer

import (
	"bytes"
	"encoding/json"

	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
)

// claudeCorrectionFactor mirrors kiro.tokenizer.CLAUDE_CORRECTION_FACTOR
// (.upstream/kiro/tokenizer.py:45): tiktoken (cl100k_base) undercounts
// Claude tokens by roughly 15%, empirically.
const claudeCorrectionFactor = 1.15

// CountTokens mirrors kiro.tokenizer.count_tokens (.upstream/kiro/tokenizer.py:77-107).
// When applyCorrection is true the raw tiktoken count is scaled by
// claudeCorrectionFactor and truncated toward zero, exactly like Python's
// int(base_tokens * CLAUDE_CORRECTION_FACTOR).
//
// This is the shared primitive consumed by both internal/streamingopenai
// (streaming_openai.py's request_messages/request_tools fallback) and
// internal/streaminganthropic (streaming_anthropic.py's estimate_request_tokens,
// tokenizer.py:296-327). See docs/MAPPING.md task-8 ruling for why this file
// lives here instead of duplicated per-dialect package.
func CountTokens(text string, applyCorrection bool) int {
	if text == "" {
		return 0
	}
	base := len(EncodeOrdinary(text))
	if applyCorrection {
		return int(float64(base) * claudeCorrectionFactor)
	}
	return base
}

// rawMessageFields is the subset of an OpenAI-shaped chat message this
// package needs to replicate count_message_tokens. Content is kept raw so
// countContentTokens can distinguish string vs block-list vs null without
// losing key order inside nested JSON (needed for tool_use "input" dumps).
type rawMessageFields struct {
	Role       string              `json:"role"`
	Content    json.RawMessage     `json:"content"`
	ToolCalls  []rawToolCallFields `json:"tool_calls"`
	ToolCallID *string             `json:"tool_call_id"`
}

type rawToolCallFields struct {
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// CountMessageTokens mirrors kiro.tokenizer.count_message_tokens(messages,
// apply_claude_correction=False) (.upstream/kiro/tokenizer.py:110-210), the
// fallback used when Kiro didn't return context_usage_percentage
// (.upstream/kiro/streaming_openai.py:317-318,
// .upstream/kiro/streaming_anthropic.py:177-183 via estimate_request_tokens).
// messages carries each message's original JSON bytes so nested dumps
// (tool_use input, tool schemas) preserve source key order like Python's
// json.dumps on a parsed dict does.
func CountMessageTokens(messages []json.RawMessage) int {
	if len(messages) == 0 {
		return 0
	}

	total := 0
	for _, raw := range messages {
		total += 4 // service tokens per message

		var msg rawMessageFields
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}

		total += CountTokens(msg.Role, false)
		total += countContentTokens(msg.Content)

		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				total += 4
				total += CountTokens(tc.Function.Name, false)
				total += CountTokens(tc.Function.Arguments, false)
			}
		}

		if msg.ToolCallID != nil && *msg.ToolCallID != "" {
			total += CountTokens(*msg.ToolCallID, false)
		}
	}

	total += 3 // final service tokens
	return total
}

// countContentTokens counts a message's "content" field, which may be
// absent/null, a plain string, or a list of content blocks
// (.upstream/kiro/tokenizer.py:139-189).
func countContentTokens(raw json.RawMessage) int {
	trimmed := bytes.TrimSpace(raw)
	if isJSONNullOrEmpty(trimmed) {
		return 0
	}

	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return 0
		}
		return CountTokens(s, false)
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return 0
		}
		total := 0
		for _, item := range items {
			total += countContentBlockTokens(item)
		}
		return total
	default:
		return 0
	}
}

// contentBlockFields is the subset of an Anthropic/OpenAI content block this
// package needs. Input/IsError/Content stay raw for the same reason as
// rawMessageFields.Content.
type contentBlockFields struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   json.RawMessage `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// countContentBlockTokens mirrors the per-item branch of count_message_tokens
// (.upstream/kiro/tokenizer.py:146-189): text/image/tool_use/tool_result get
// dedicated handling, unknown dict blocks fall back to a JSON dump, and
// non-dict items fall back to str().
func countContentBlockTokens(raw json.RawMessage) int {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return 0
	}
	if trimmed[0] != '{' {
		return CountTokens(pyjson.Str(trimmed), false)
	}

	var block contentBlockFields
	if err := json.Unmarshal(trimmed, &block); err != nil {
		return 0
	}

	switch block.Type {
	case "text":
		return CountTokens(block.Text, false)
	case "image_url", "image":
		return 100
	case "tool_use":
		total := CountTokens(block.ID, false)
		total += CountTokens(block.Name, false)
		inputStr := "{}"
		if block.Input != nil {
			if s, err := pyjson.Dumps(block.Input); err == nil {
				inputStr = s
			}
		}
		total += CountTokens(inputStr, false)
		return total
	case "tool_result":
		total := CountTokens(block.ToolUseID, false)
		if !isJSONNullOrEmpty(bytes.TrimSpace(block.IsError)) {
			total += CountTokens(pyjson.Str(block.IsError), false)
		}
		total += countToolResultContentTokens(block.Content)
		return total
	default:
		s, err := pyjson.Dumps(trimmed)
		if err != nil {
			return 0
		}
		return CountTokens(s, false)
	}
}

// countToolResultContentTokens mirrors the tool_result_content branch of
// count_message_tokens (.upstream/kiro/tokenizer.py:164-181): a string, a
// list of text/image blocks, or any other scalar via str().
func countToolResultContentTokens(raw json.RawMessage) int {
	trimmed := bytes.TrimSpace(raw)
	if isJSONNullOrEmpty(trimmed) {
		return 0
	}

	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return 0
		}
		return CountTokens(s, false)
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return 0
		}
		total := 0
		for _, item := range items {
			it := bytes.TrimSpace(item)
			if len(it) > 0 && it[0] == '{' {
				var rb struct {
					Type string `json:"type"`
					Text string `json:"text"`
				}
				if err := json.Unmarshal(it, &rb); err == nil {
					switch rb.Type {
					case "text":
						total += CountTokens(rb.Text, false)
					case "image_url", "image":
						total += 100
					}
				}
			} else {
				total += CountTokens(pyjson.Str(item), false)
			}
		}
		return total
	default:
		return CountTokens(pyjson.Str(trimmed), false)
	}
}

// toolFields is the subset of an OpenAI tool definition (or its Anthropic
// flat-schema equivalent) this package needs.
type toolFields struct {
	Type        string          `json:"type"`
	Function    json.RawMessage `json:"function"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
	Parameters  json.RawMessage `json:"parameters"`
}

// CountToolsTokens mirrors kiro.tokenizer.count_tools_tokens(tools,
// apply_claude_correction=False) (.upstream/kiro/tokenizer.py:213-253), the
// tool half of the request_messages/request_tools fallback
// (.upstream/kiro/streaming_openai.py:319-320,
// .upstream/kiro/streaming_anthropic.py:177-183 via estimate_request_tokens).
func CountToolsTokens(tools []json.RawMessage) int {
	if len(tools) == 0 {
		return 0
	}

	total := 0
	for _, raw := range tools {
		total += 4 // service tokens per tool

		var tool toolFields
		if err := json.Unmarshal(raw, &tool); err != nil {
			continue
		}

		name, description := tool.Name, tool.Description
		inputSchema, parameters := tool.InputSchema, tool.Parameters

		if tool.Type == "function" {
			trimmedFn := bytes.TrimSpace(tool.Function)
			if len(trimmedFn) > 0 && trimmedFn[0] == '{' {
				var payload struct {
					Name        string          `json:"name"`
					Description string          `json:"description"`
					InputSchema json.RawMessage `json:"input_schema"`
					Parameters  json.RawMessage `json:"parameters"`
				}
				if err := json.Unmarshal(trimmedFn, &payload); err == nil {
					name, description = payload.Name, payload.Description
					inputSchema, parameters = payload.InputSchema, payload.Parameters
				}
			}
		}

		total += CountTokens(name, false)
		total += CountTokens(description, false)

		params := inputSchema
		if isJSONNullOrEmpty(bytes.TrimSpace(params)) {
			params = parameters
		}
		if !isJSONNullOrEmpty(bytes.TrimSpace(params)) {
			if s, err := pyjson.Dumps(params); err == nil {
				total += CountTokens(s, false)
			}
		}
	}

	return total
}

// CountSystemTokens mirrors kiro.tokenizer.count_system_tokens(system_prompt,
// apply_claude_correction=False) (.upstream/kiro/tokenizer.py:256-293), the
// system-prompt half of streaming_anthropic.py's estimate_request_tokens
// call (.upstream/kiro/streaming_anthropic.py:177-183). system_prompt in
// Python can be a plain string, a list of Anthropic content blocks (each
// optionally carrying "cache_control"), or any other JSON scalar — the
// str(block) fallback below reproduces the last branch.
//
// raw is the request's "system" field as originally-ordered JSON bytes, or
// nil/empty when absent.
func CountSystemTokens(raw json.RawMessage) int {
	trimmed := bytes.TrimSpace(raw)
	if isJSONNullOrEmpty(trimmed) {
		return 0
	}

	total := 0
	switch trimmed[0] {
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return 0
		}
		total += CountTokens(s, false)
	case '[':
		var items []json.RawMessage
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return 0
		}
		for _, item := range items {
			it := bytes.TrimSpace(item)
			if len(it) > 0 && it[0] == '{' {
				var block struct {
					Text         string          `json:"text"`
					CacheControl json.RawMessage `json:"cache_control"`
				}
				if err := json.Unmarshal(it, &block); err == nil {
					total += CountTokens(block.Text, false)
					if !isJSONNullOrEmpty(bytes.TrimSpace(block.CacheControl)) {
						if s, err := pyjson.Dumps(block.CacheControl); err == nil {
							total += CountTokens(s, false)
						}
					}
				}
			} else {
				total += CountTokens(pyjson.Str(item), false)
			}
		}
	default:
		// Any other scalar (number/bool): Python's str(system_prompt).
		total += CountTokens(pyjson.Str(trimmed), false)
	}

	return total
}

// isJSONNullOrEmpty reports whether trimmed JSON bytes represent an absent
// field (nil/empty slice) or the literal `null`.
func isJSONNullOrEmpty(trimmed []byte) bool {
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}
