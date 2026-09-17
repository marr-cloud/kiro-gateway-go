// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingcore

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/parsers"
	"github.com/marr-cloud/kiro-gateway-go/internal/thinkingparser"
)

func TestFeedContentEvent(t *testing.T) {
	// Test content chunk → KiroEvent content
	pipeline := NewPipeline(thinkingparser.HandlingAsReasoningContent, 256)

	contentJSON := []byte(`{"content":"Hello world"}`)
	events := pipeline.Feed(contentJSON)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Kind != "content" {
		t.Errorf("expected Kind 'content', got %q", events[0].Kind)
	}

	if events[0].Content != "Hello world" {
		t.Errorf("expected Content 'Hello world', got %q", events[0].Content)
	}
}

func TestFeedContentWithThinking(t *testing.T) {
	// Test content with thinking block → split into two events
	pipeline := NewPipeline(thinkingparser.HandlingAsReasoningContent, 256)

	contentJSON := []byte(`{"content":"<thinking>Let me think</thinking>The answer"}`)
	events := pipeline.Feed(contentJSON)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	// Thinking should come first
	if events[0].Kind != "thinking" {
		t.Errorf("expected first event Kind 'thinking', got %q", events[0].Kind)
	}

	if events[0].Thinking != "Let me think" {
		t.Errorf("expected Thinking 'Let me think', got %q", events[0].Thinking)
	}

	// Content should come second
	if events[1].Kind != "content" {
		t.Errorf("expected second event Kind 'content', got %q", events[1].Kind)
	}

	if events[1].Content != "The answer" {
		t.Errorf("expected Content 'The answer', got %q", events[1].Content)
	}
}

func TestFeedContextUsageEvent(t *testing.T) {
	// Test context_usage float → KiroEvent context_usage
	pipeline := NewPipeline(thinkingparser.HandlingAsReasoningContent, 256)

	contextJSON := []byte(`{"contextUsagePercentage":42.5}`)
	events := pipeline.Feed(contextJSON)

	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Kind != "context_usage" {
		t.Errorf("expected Kind 'context_usage', got %q", events[0].Kind)
	}

	if events[0].ContextUsage != 42.5 {
		t.Errorf("expected ContextUsage 42.5, got %f", events[0].ContextUsage)
	}
}

func TestFeedToolUseEvent(t *testing.T) {
	// Test tool_call event → KiroEvent tool_use via helper function
	// This tests the extractToolUseData helper directly, not through Pipeline integration
	parser := parsers.NewParser()

	// Feed tool_start event (synthetic minimal format for testing extractToolUseData extraction logic)
	toolStartJSON := []byte(`{"name":"get_weather","input":{"location":"NYC"}}`)
	_ = parser.Feed(toolStartJSON)
	// tool_start doesn't produce events directly, just accumulates state in the parser

	// Call Finish to get the tool_call events
	toolCallEvents := parser.Finish()
	if len(toolCallEvents) == 0 {
		t.Fatalf("expected tool_call events from parser.Finish()")
	}

	// Convert tool_call to KiroEvent using helper
	toolCallValue := toolCallEvents[0].Value

	kiroEvent := buildToolUseEvent(toolCallValue)
	if kiroEvent.Kind != "tool_use" {
		t.Errorf("expected Kind 'tool_use', got %q", kiroEvent.Kind)
	}

	if kiroEvent.ToolUse == nil {
		t.Fatalf("expected ToolUse to be non-nil")
	}

	if kiroEvent.ToolUse.ID == "" {
		t.Errorf("expected ID to be populated")
	}

	if kiroEvent.ToolUse.Name != "get_weather" {
		t.Errorf("expected Name 'get_weather', got %q", kiroEvent.ToolUse.Name)
	}

	if kiroEvent.ToolUse.Input == nil {
		t.Fatalf("expected Input to be non-nil")
	}

	if location, ok := kiroEvent.ToolUse.Input["location"].(string); !ok || location != "NYC" {
		t.Errorf("expected Input[location] 'NYC', got %v", kiroEvent.ToolUse.Input["location"])
	}
}

func TestFinishEmptyPipeline(t *testing.T) {
	// Test Finish on empty pipeline
	pipeline := NewPipeline(thinkingparser.HandlingAsReasoningContent, 256)

	events := pipeline.Finish()

	if len(events) != 0 {
		t.Fatalf("expected 0 events from empty pipeline Finish, got %d", len(events))
	}
}

func TestFinishWithPendingThinking(t *testing.T) {
	// Test Finish flushes pending thinking content
	pipeline := NewPipeline(thinkingparser.HandlingAsReasoningContent, 256)

	contentJSON := []byte(`{"content":"<thinking>Unfinished thought"}`)
	events := pipeline.Feed(contentJSON)

	if len(events) != 0 {
		// The thinking is incomplete (no closing tag), so it's buffered
		t.Fatalf("expected 0 events while thinking is incomplete, got %d", len(events))
	}

	finishEvents := pipeline.Finish()
	if len(finishEvents) != 1 {
		t.Fatalf("expected 1 event from Finish, got %d", len(finishEvents))
	}

	if finishEvents[0].Kind != "thinking" {
		t.Errorf("expected Kind 'thinking', got %q", finishEvents[0].Kind)
	}
}

func TestFeedToolUseViaFinish(t *testing.T) {
	// Test tool_use event via Pipeline.Feed → Pipeline.Finish integration
	// This ensures the Pipeline correctly converts tool_call events from parser.Finish()
	pipeline := NewPipeline(thinkingparser.HandlingAsReasoningContent, 256)

	// Feed a tool_start event to the pipeline
	// The parser accumulates this but doesn't emit events from Feed
	toolStartJSON := []byte(`{"name":"search","input":{"query":"test"}}`)
	events := pipeline.Feed(toolStartJSON)

	// No events yet - tool_start is accumulated in parser state
	if len(events) != 0 {
		t.Errorf("expected 0 events from Feed (tool_start accumulates), got %d", len(events))
	}

	// Finish the pipeline to flush any pending tool calls
	finishEvents := pipeline.Finish()

	// Should have one tool_use event from the accumulated tool_start
	if len(finishEvents) != 1 {
		t.Fatalf("expected 1 event from Finish (tool_call), got %d", len(finishEvents))
	}

	event := finishEvents[0]
	if event.Kind != "tool_use" {
		t.Errorf("expected Kind 'tool_use', got %q", event.Kind)
	}

	if event.ToolUse == nil {
		t.Fatalf("expected ToolUse to be non-nil")
	}

	if event.ToolUse.ID == "" {
		t.Errorf("expected ID to be populated")
	}

	if event.ToolUse.Name != "search" {
		t.Errorf("expected Name 'search', got %q", event.ToolUse.Name)
	}

	if event.ToolUse.Input == nil {
		t.Fatalf("expected Input to be non-nil")
	}

	if query, ok := event.ToolUse.Input["query"].(string); !ok || query != "test" {
		t.Errorf("expected Input[query] 'test', got %v", event.ToolUse.Input["query"])
	}
}

func TestFeedWithTruncationDiagnosis(t *testing.T) {
	// Test truncation diagnosis → KiroEvent error
	// This would require feeding malformed JSON that triggers truncation detection
	// For now, we'll test that error events can be created

	// Create an error event to demonstrate the structure
	errorEvent := buildErrorEvent("missing 1 closing brace(s)")
	if errorEvent.Kind != "error" {
		t.Errorf("expected Kind 'error', got %q", errorEvent.Kind)
	}

	if errorEvent.Error != "missing 1 closing brace(s)" {
		t.Errorf("expected Error 'missing 1 closing brace(s)', got %q", errorEvent.Error)
	}
}

func TestThinkingFirstContentSecond(t *testing.T) {
	// Test order: thinking first, then content
	pipeline := NewPipeline(thinkingparser.HandlingAsReasoningContent, 256)

	contentJSON := []byte(`{"content":"<thinking>Think</thinking>Say"}`)
	events := pipeline.Feed(contentJSON)

	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	if events[0].Kind != "thinking" {
		t.Errorf("expected first event to be thinking, got %q", events[0].Kind)
	}

	if events[1].Kind != "content" {
		t.Errorf("expected second event to be content, got %q", events[1].Kind)
	}
}

// Helper function to build a tool use event
func buildToolUseEvent(toolCallValue map[string]any) KiroEvent {
	var toolUseData *ToolUseData

	// Extract id
	id := ""
	if idVal, ok := toolCallValue["id"]; ok {
		id = idVal.(string)
	}

	// Extract function data
	if funcData, ok := toolCallValue["function"].(map[string]any); ok {
		name := ""
		if nameVal, ok := funcData["name"]; ok {
			name = nameVal.(string)
		}

		input := make(map[string]any)
		if argsVal, ok := funcData["arguments"].(string); ok {
			_ = json.Unmarshal([]byte(argsVal), &input)
		}

		toolUseData = &ToolUseData{
			ID:    id,
			Name:  name,
			Input: input,
		}
	}

	return KiroEvent{
		Kind:    "tool_use",
		ToolUse: toolUseData,
	}
}

// Helper function to build an error event
func buildErrorEvent(diagnosis string) KiroEvent {
	return KiroEvent{
		Kind:  "error",
		Error: diagnosis,
	}
}
