// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingopenai

import (
	"bytes"
	"encoding/json"

	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
	"github.com/marr-cloud/kiro-gateway-go/internal/tokenizer"
)

// claudeCorrectionFactor mirrors kiro.tokenizer.CLAUDE_CORRECTION_FACTOR
// (.upstream/kiro/tokenizer.py:45): tiktoken (cl100k_base) undercounts
// Claude tokens by roughly 15%, empirically.
const claudeCorrectionFactor = 1.15

// countTokens mirrors kiro.tokenizer.count_tokens (.upstream/kiro/tokenizer.py:77-107).
// When applyCorrection is true the raw tiktoken count is scaled by
// claudeCorrectionFactor and truncated toward zero, exactly like Python's
// int(base_tokens * CLAUDE_CORRECTION_FACTOR).
func countTokens(text string, applyCorrection bool) int {
	if text == "" {
		return 0
	}
	base := len(tokenizer.EncodeOrdinary(text))
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

// countMessageTokens mirrors kiro.tokenizer.count_message_tokens(messages,
// apply_claude_correction=False) (.upstream/kiro/tokenizer.py:110-210), the
// fallback used when Kiro didn't return context_usage_percentage
// (.upstream/kiro/streaming_openai.py:317-318). messages carries each
// message's original JSON bytes so nested dumps (tool_use input, tool
// schemas) preserve source key order like Python's json.dumps on a parsed
// dict does.
func countMessageTokens(messages []json.RawMessage) int {
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

		total += countTokens(msg.Role, false)
		total += countContentTokens(msg.Content)

		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				total += 4
				total += countTokens(tc.Function.Name, false)
				total += countTokens(tc.Function.Arguments, false)
			}
		}

		if msg.ToolCallID != nil && *msg.ToolCallID != "" {
			total += countTokens(*msg.ToolCallID, false)
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
		return countTokens(s, false)
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
		return countTokens(pyjson.Str(trimmed), false)
	}

	var block contentBlockFields
	if err := json.Unmarshal(trimmed, &block); err != nil {
		return 0
	}

	switch block.Type {
	case "text":
		return countTokens(block.Text, false)
	case "image_url", "image":
		return 100
	case "tool_use":
		total := countTokens(block.ID, false)
		total += countTokens(block.Name, false)
		inputStr := "{}"
		if block.Input != nil {
			if s, err := pyjson.Dumps(block.Input); err == nil {
				inputStr = s
			}
		}
		total += countTokens(inputStr, false)
		return total
	case "tool_result":
		total := countTokens(block.ToolUseID, false)
		if !isJSONNullOrEmpty(bytes.TrimSpace(block.IsError)) {
			total += countTokens(pyjson.Str(block.IsError), false)
		}
		total += countToolResultContentTokens(block.Content)
		return total
	default:
		s, err := pyjson.Dumps(trimmed)
		if err != nil {
			return 0
		}
		return countTokens(s, false)
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
		return countTokens(s, false)
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
						total += countTokens(rb.Text, false)
					case "image_url", "image":
						total += 100
					}
				}
			} else {
				total += countTokens(pyjson.Str(item), false)
			}
		}
		return total
	default:
		return countTokens(pyjson.Str(trimmed), false)
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

// countToolsTokens mirrors kiro.tokenizer.count_tools_tokens(tools,
// apply_claude_correction=False) (.upstream/kiro/tokenizer.py:213-253), the
// tool half of the request_messages/request_tools fallback
// (.upstream/kiro/streaming_openai.py:319-320).
func countToolsTokens(tools []json.RawMessage) int {
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

		total += countTokens(name, false)
		total += countTokens(description, false)

		params := inputSchema
		if isJSONNullOrEmpty(bytes.TrimSpace(params)) {
			params = parameters
		}
		if !isJSONNullOrEmpty(bytes.TrimSpace(params)) {
			if s, err := pyjson.Dumps(params); err == nil {
				total += countTokens(s, false)
			}
		}
	}

	return total
}

// isJSONNullOrEmpty reports whether trimmed JSON bytes represent an absent
// field (nil/empty slice) or the literal `null`.
func isJSONNullOrEmpty(trimmed []byte) bool {
	return len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null"))
}

// isJSONTruthy reports whether raw JSON bytes represent a Python-truthy
// value: present, and not one of None/False/0/0.0/""/{}/[] . Mirrors the
// `if event.usage:` / `if metering_data:` checks
// (.upstream/kiro/streaming_openai.py:265-266,405 and
// .upstream/kiro/streaming_core.py:320) that gate whether a "usage" event
// counts as a received completion signal and whether metering_data gets
// echoed back as credits_used.
func isJSONTruthy(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", "false", "0", "0.0", "{}", "[]", `""`:
		return false
	default:
		return true
	}
}
