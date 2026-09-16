// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package streaminganthropic converts internal/streamingcore.KiroEvent
// objects into Anthropic Messages API SSE events (message_start,
// content_block_start/delta/stop, message_delta, message_stop), porting
// .upstream/kiro/streaming_anthropic.py's stream_kiro_to_anthropic.
package streaminganthropic

import (
	"encoding/json"
	"io"

	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
	"github.com/marr-cloud/kiro-gateway-go/internal/tokenizer"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// ThinkingHandling mirrors kiro.config.FAKE_REASONING_HANDLING's effect on
// the "thinking" event branch (.upstream/kiro/streaming_anthropic.py:261-324).
type ThinkingHandling string

const (
	// AsReasoningContent renders thinking content as native Anthropic
	// "thinking" content blocks (streaming_anthropic.py:266-289).
	AsReasoningContent ThinkingHandling = "as_reasoning_content"
	// IncludeAsText folds thinking content into the regular text block
	// (streaming_anthropic.py:291-323).
	IncludeAsText ThinkingHandling = "include_as_text"
	// Strip discards thinking content entirely from the SSE output, while
	// it still counts toward output_tokens (streaming_anthropic.py:324,625).
	Strip ThinkingHandling = "strip"
)

// Formatter converts streamingcore.KiroEvent objects into Anthropic
// SSE-formatted events. It accumulates state across multiple Handle() calls
// and finalizes with Finish(). EmitMessageStart must be called once, before
// any Handle() call, matching upstream's yield order (message_start is the
// generator's first yield, before the event loop starts:
// streaming_anthropic.py:206-223).
//
// Not safe for concurrent use. Each streaming response owns one Formatter
// instance.
type Formatter struct {
	messageID         string
	model             string
	thinkingHandling  ThinkingHandling
	thinkingSignature string

	blocks blockIndex

	fullContent         string
	fullThinkingContent string
	toolBlockCount      int // len(tool_blocks): drives stop_reason and truncation detection

	receivedContextUsage     bool // context_usage_percentage is not None (streaming_anthropic.py:527)
	cacheReadInputTokens     *int
	cacheCreationInputTokens *int

	requestMessages []json.RawMessage
	requestTools    []json.RawMessage
	requestSystem   json.RawMessage
}

// New creates a new Formatter for the given model. thinkingHandling
// determines how "thinking" KiroEvents are rendered (defaults to
// AsReasoningContent, matching kiro.config's fallback default when
// FAKE_REASONING_HANDLING is unset or unrecognized: .upstream/kiro/config.py:463-467).
func New(model string, thinkingHandling ...ThinkingHandling) *Formatter {
	handling := AsReasoningContent
	if len(thinkingHandling) > 0 && thinkingHandling[0] != "" {
		handling = thinkingHandling[0]
	}

	return &Formatter{
		messageID:         utils.GenerateMessageID(),
		model:             model,
		thinkingHandling:  handling,
		thinkingSignature: utils.GenerateThinkingSignature(),
	}
}

// SetRequestContext sets the original request messages, tools, and system
// prompt, each as raw JSON bytes (original key order preserved). They feed
// EmitMessageStart's estimate_request_tokens fallback
// (.upstream/kiro/streaming_anthropic.py:176-183), used because Anthropic's
// message_start event requires input_tokens before Kiro has reported
// context_usage_percentage. system may be nil when the request has no
// system prompt.
func (f *Formatter) SetRequestContext(messages, tools []json.RawMessage, system json.RawMessage) {
	f.requestMessages = messages
	f.requestTools = tools
	f.requestSystem = system
}

// EmitMessageStart writes the message_start event. input_tokens is a
// provisional estimate from the request's messages/tools/system
// (estimate_request_tokens, apply_claude_correction=False) — Kiro's more
// accurate context_usage_percentage arrives only at the end of the stream,
// too late for this event, so this number is a deliberate approximation,
// not the final prompt token count (streaming_anthropic.py:169-183).
func (f *Formatter) EmitMessageStart(w io.Writer) error {
	inputTokens := 0
	if len(f.requestMessages) > 0 || len(f.requestTools) > 0 || jsonTruthy(f.requestSystem) {
		// message_start uses apply_claude_correction=False
		// (.upstream/kiro/streaming_anthropic.py:180): this is a provisional
		// pre-stream estimate, not the corrected count the count_tokens
		// endpoint returns.
		inputTokens = tokenizer.CountMessageTokens(f.requestMessages, false) +
			tokenizer.CountToolsTokens(f.requestTools, false) +
			tokenizer.CountSystemTokens(f.requestSystem, false)
	}

	return writeEvent(w, "message_start", messageStartData{
		Type: "message_start",
		Message: messageStartMessage{
			ID:           f.messageID,
			Type:         "message",
			Role:         "assistant",
			Content:      []json.RawMessage{},
			Model:        f.model,
			StopReason:   nil,
			StopSequence: nil,
			Usage:        messageStartUsage{InputTokens: inputTokens, OutputTokens: 0},
		},
	})
}

// Handle processes a single KiroEvent and writes the corresponding SSE
// event(s) to w. Kind "error" (truncation diagnosis) has no branch in
// upstream's event loop and is silently ignored here too — upstream's
// async-for only matches "content"/"thinking"/"tool_use"/"context_usage"/
// "usage" (streaming_anthropic.py:224-524).
func (f *Formatter) Handle(ev streamingcore.KiroEvent, w io.Writer) error {
	switch ev.Kind {
	case "content":
		return f.handleContent(ev.Content, w)

	case "thinking":
		return f.handleThinking(ev.Thinking, w)

	case "tool_use":
		if ev.ToolUse != nil {
			return f.handleToolUse(ev.ToolUse, w)
		}

	case "context_usage":
		// The pipeline only emits this Kind when Kiro's
		// contextUsagePercentage was actually present, so seeing the event
		// at all is equivalent to upstream's `is not None` check
		// (streaming_anthropic.py:521-522,527).
		f.receivedContextUsage = true

	case "usage":
		f.mergeCacheUsage(ev.UsageRaw)
	}

	return nil
}

// handleContent mirrors the "content" branch (streaming_anthropic.py:224-259):
// closes an open thinking block (content interrupts thinking), opens the
// text block if needed, and emits the text delta.
func (f *Formatter) handleContent(content string, w io.Writer) error {
	f.fullContent += content

	if idx, closed := f.blocks.CloseThinking(); closed {
		if err := f.emitBlockStop(idx, w); err != nil {
			return err
		}
	}

	if idx, opened := f.blocks.OpenText(); opened {
		if err := f.emitTextStart(idx, w); err != nil {
			return err
		}
	}

	if content != "" {
		if err := f.emitTextDelta(f.blocks.TextIndex(), content, w); err != nil {
			return err
		}
	}

	return nil
}

// handleThinking mirrors the "thinking" branch (streaming_anthropic.py:261-324).
// full_thinking_content accumulates regardless of mode — even Strip mode,
// which emits nothing to the SSE stream but still counts toward
// output_tokens at Finish (streaming_anthropic.py:263,625).
func (f *Formatter) handleThinking(thinkingContent string, w io.Writer) error {
	f.fullThinkingContent += thinkingContent

	switch f.thinkingHandling {
	case AsReasoningContent:
		if idx, opened := f.blocks.OpenThinking(); opened {
			if err := f.emitThinkingStart(idx, w); err != nil {
				return err
			}
		}
		if thinkingContent != "" {
			if err := f.emitThinkingDelta(f.blocks.ThinkingIndex(), thinkingContent, w); err != nil {
				return err
			}
		}

	case IncludeAsText:
		if idx, closed := f.blocks.CloseThinking(); closed {
			if err := f.emitBlockStop(idx, w); err != nil {
				return err
			}
		}
		if idx, opened := f.blocks.OpenText(); opened {
			if err := f.emitTextStart(idx, w); err != nil {
				return err
			}
		}
		if thinkingContent != "" {
			if err := f.emitTextDelta(f.blocks.TextIndex(), thinkingContent, w); err != nil {
				return err
			}
		}

	default:
		// Strip: content is dropped from the SSE stream (streaming_anthropic.py:324).
	}

	return nil
}

// handleToolUse mirrors the "tool_use" branch (streaming_anthropic.py:326-519),
// minus the web_search MCP-emulation interception (streaming_anthropic.py:354-468):
// that path calls out to a live MCP API via kiro.mcp_tools, which belongs to
// a network/tool-execution layer outside this formatter's scope, and no
// stream_kiro_to_anthropic corpus fixture exercises it (every fixture that
// carries a web_search *tool definition* only ever emits a "content" event,
// never a tool_use event named "web_search" — see docs/MAPPING.md's task-8
// ruling). Every tool_use KiroEvent is therefore treated as a normal
// Anthropic tool_use block.
func (f *Formatter) handleToolUse(tu *streamingcore.ToolUseData, w io.Writer) error {
	if idx, closed := f.blocks.CloseThinking(); closed {
		if err := f.emitBlockStop(idx, w); err != nil {
			return err
		}
	}
	if idx, closed := f.blocks.CloseText(); closed {
		if err := f.emitBlockStop(idx, w); err != nil {
			return err
		}
	}

	toolID := tu.ID
	if toolID == "" {
		toolID = generateToolUseID()
	}
	toolInput := tu.Input
	if toolInput == nil {
		toolInput = map[string]any{}
	}

	return f.emitToolUseBlock(toolID, tu.Name, toolInput, w)
}

// emitToolUseBlock writes the start/delta/stop triplet for one tool_use
// block, shared by handleToolUse and the bracket-tool-call path in Finish
// (streaming_anthropic.py:486-519,561-592).
func (f *Formatter) emitToolUseBlock(id, name string, input map[string]any, w io.Writer) error {
	idx := f.blocks.ReserveToolBlock()

	if err := f.emitToolUseStart(idx, id, name, w); err != nil {
		return err
	}

	inputJSON, err := pythonStyleDumps(input)
	if err != nil {
		return err
	}
	if err := f.emitInputJSONDelta(idx, inputJSON, w); err != nil {
		return err
	}

	if err := f.emitBlockStop(idx, w); err != nil {
		return err
	}

	f.toolBlockCount++
	return nil
}

// mergeCacheUsage extracts cache token fields from a "usage" KiroEvent's raw
// payload and merges them into the running totals, mirroring
// upstream_cache_usage.update(_extract_cache_usage_fields(event.usage))
// (streaming_anthropic.py:101-126,523-524): only keys present in this
// event's payload overwrite the running value, others are left as they
// were.
func (f *Formatter) mergeCacheUsage(raw json.RawMessage) {
	read, creation := extractCacheUsageFields(raw)
	if read != nil {
		f.cacheReadInputTokens = read
	}
	if creation != nil {
		f.cacheCreationInputTokens = creation
	}
}

// Finish, EmitError, the bracket-tool-call tail, and the per-event-type emit
// helpers live in finish.go (split to stay under the 400-line file budget).
