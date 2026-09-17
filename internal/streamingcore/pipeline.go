// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingcore

import (
	"encoding/json"

	"github.com/marr-cloud/kiro-gateway-go/internal/parsers"
	"github.com/marr-cloud/kiro-gateway-go/internal/thinkingparser"
)

// Pipeline orchestrates the parsing and thinking detection across streaming events.
// It stitches parsers.Parser output (Task 1) through thinkingparser.Parser (Task 2)
// into a unified KiroEvent stream.
// Pipeline is not safe for concurrent use. Each stream should own one Pipeline instance
// (spec §5.4, D4 explicit-loop model).
type Pipeline struct {
	parser   *parsers.Parser
	thinking *thinkingparser.Parser
}

// NewPipeline creates a new Pipeline with the specified handling mode and initial buffer size
// for the thinking parser.
func NewPipeline(handling string, initialBufferSize int) *Pipeline {
	return &Pipeline{
		parser:   parsers.NewParser(),
		thinking: thinkingparser.NewParser(handling, initialBufferSize),
	}
}

// Feed processes a chunk of bytes through the parser and thinking parser,
// returning a slice of unified KiroEvent objects.
// The order of emitted events is: thinking events before content events,
// followed by other event types (usage, context_usage, tool_use, error).
func (p *Pipeline) Feed(chunk []byte) []KiroEvent {
	var events []KiroEvent

	// Feed chunk to the parsers.Parser
	parserEvents := p.parser.Feed(chunk)

	// Process each event from the parser
	for _, parserEvent := range parserEvents {
		switch parserEvent.Kind {
		case "content":
			// Extract content string and feed to thinking parser
			if contentVal, ok := parserEvent.Value["content"]; ok {
				contentStr, _ := contentVal.(string)

				// Feed to thinking parser
				thinking, content := p.thinking.Feed(contentStr)

				// Emit thinking event first if present
				if thinking != "" {
					events = append(events, KiroEvent{
						Kind:     "thinking",
						Thinking: thinking,
					})
				}

				// Emit content event if present
				if content != "" {
					events = append(events, KiroEvent{
						Kind:    "content",
						Content: content,
					})
				}
			}

		case "usage":
			// Emit usage event with raw JSON bytes
			events = append(events, KiroEvent{
				Kind:     "usage",
				UsageRaw: extractUsageRaw(parserEvent.Raw),
			})

		case "context_usage":
			// Extract context usage percentage
			if contextVal, ok := parserEvent.Value["contextUsagePercentage"]; ok {
				if contextFloat, ok := contextVal.(float64); ok {
					events = append(events, KiroEvent{
						Kind:         "context_usage",
						ContextUsage: contextFloat,
					})
				}
			}

		case "tool_call":
			// Build ToolUseData from tool call structure
			toolUseData := extractToolUseData(parserEvent.Value)
			if toolUseData != nil {
				events = append(events, KiroEvent{
					Kind:    "tool_use",
					ToolUse: toolUseData,
				})
			}
		}
	}

	// Check for truncation diagnosis and emit error event
	if diagnosis, ok := p.parser.TruncationDiagnosis(); ok {
		events = append(events, KiroEvent{
			Kind:  "error",
			Error: diagnosis,
		})
	}

	return events
}

// Finish flushes any remaining buffered content from the thinking parser
// and returns any pending tool calls from the parser.
func (p *Pipeline) Finish() []KiroEvent {
	var events []KiroEvent

	// Finish thinking parser and get any pending content
	thinking, content := p.thinking.Finish()

	// Emit thinking event if present
	if thinking != "" {
		events = append(events, KiroEvent{
			Kind:     "thinking",
			Thinking: thinking,
		})
	}

	// Emit content event if present
	if content != "" {
		events = append(events, KiroEvent{
			Kind:    "content",
			Content: content,
		})
	}

	// Finish parser and get any pending tool calls
	parserEvents := p.parser.Finish()
	for _, parserEvent := range parserEvents {
		if parserEvent.Kind == "tool_call" {
			toolUseData := extractToolUseData(parserEvent.Value)
			if toolUseData != nil {
				events = append(events, KiroEvent{
					Kind:    "tool_use",
					ToolUse: toolUseData,
				})
			}
		}
	}

	// Check for any truncation diagnosis
	if diagnosis, ok := p.parser.TruncationDiagnosis(); ok {
		events = append(events, KiroEvent{
			Kind:  "error",
			Error: diagnosis,
		})
	}

	return events
}

// extractUsageRaw pulls the raw "usage" JSON value out of an event's raw
// bytes (e.g. {"usage": {...}}), preserving the original key order and
// number formatting so it can be echoed verbatim as `credits_used`
// (.upstream/kiro/streaming_openai.py:405-406). Returns nil if raw doesn't
// decode or carries no "usage" key.
func extractUsageRaw(raw []byte) json.RawMessage {
	var envelope struct {
		Usage json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	return envelope.Usage
}

// extractToolUseData converts a tool_call event value into ToolUseData.
// The tool call structure from parsers is:
//
//	{
//	  "id": string,
//	  "type": "function",
//	  "function": {
//	    "name": string,
//	    "arguments": string (JSON formatted)
//	  },
//	  "_truncation_detected": bool (optional, set by parser when truncated),
//	  "_truncation_info": map[string]any (optional, diagnostic data)
//	}
func extractToolUseData(toolCallValue map[string]any) *ToolUseData {
	// Extract id
	var id string
	if idVal, ok := toolCallValue["id"]; ok {
		id = idVal.(string)
	}

	// Extract function data
	var name string
	var input map[string]any
	if funcVal, ok := toolCallValue["function"].(map[string]any); ok {
		if nameVal, ok := funcVal["name"]; ok {
			name, _ = nameVal.(string)
		}

		if argsVal, ok := funcVal["arguments"].(string); ok {
			input = make(map[string]any)
			// TODO(logging): log unmarshal error once observability lands
			_ = json.Unmarshal([]byte(argsVal), &input)
		}
	}

	if input == nil {
		input = make(map[string]any)
	}

	// Extract truncation information (internal/parsers/toolcalls.go:177-182)
	truncationDetected := false
	var truncationInfo map[string]any
	if detected, ok := toolCallValue["_truncation_detected"].(bool); ok && detected {
		truncationDetected = true
		if info, ok := toolCallValue["_truncation_info"].(map[string]any); ok {
			truncationInfo = info
		}
	}

	return &ToolUseData{
		ID:                 id,
		Name:               name,
		Input:              input,
		TruncationDetected: truncationDetected,
		TruncationInfo:     truncationInfo,
	}
}
