// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingopenai

import (
	"bytes"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
)

// TestHandleTruncatedToolUse verifies that tool calls flagged with
// TruncationDetected are collected in the formatter's truncatedTools side-channel.
func TestHandleTruncatedToolUse(t *testing.T) {
	f := New("gpt-4o", AsContent)
	var buf bytes.Buffer

	ev := streamingcore.KiroEvent{
		Kind: "tool_use",
		ToolUse: &streamingcore.ToolUseData{
			ID:   "tc123",
			Name: "truncated_tool",
			Input: map[string]any{
				"query": "test",
			},
			TruncationDetected: true,
			TruncationInfo: map[string]any{
				"size_bytes": 5000,
				"reason":     "max_tokens",
			},
		},
	}

	if err := f.Handle(ev, &buf); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	truncated := f.TruncatedTools()
	if len(truncated) != 1 {
		t.Fatalf("Expected 1 truncated tool, got %d", len(truncated))
	}

	if truncated[0].ID != "tc123" {
		t.Errorf("Expected ID 'tc123', got %q", truncated[0].ID)
	}
	if truncated[0].Name != "truncated_tool" {
		t.Errorf("Expected Name 'truncated_tool', got %q", truncated[0].Name)
	}
	if truncated[0].TruncationInfo["size_bytes"] != 5000 {
		t.Errorf("Expected size_bytes 5000, got %v", truncated[0].TruncationInfo["size_bytes"])
	}
}

// TestHandleNonTruncatedToolUse verifies that tool calls without
// TruncationDetected are NOT collected.
func TestHandleNonTruncatedToolUse(t *testing.T) {
	f := New("gpt-4o", AsContent)
	var buf bytes.Buffer

	ev := streamingcore.KiroEvent{
		Kind: "tool_use",
		ToolUse: &streamingcore.ToolUseData{
			ID:   "tc456",
			Name: "normal_tool",
			Input: map[string]any{
				"query": "test",
			},
			TruncationDetected: false,
		},
	}

	if err := f.Handle(ev, &buf); err != nil {
		t.Fatalf("Handle failed: %v", err)
	}

	truncated := f.TruncatedTools()
	if len(truncated) != 0 {
		t.Fatalf("Expected 0 truncated tools, got %d", len(truncated))
	}
}

// TestContentWasTruncated verifies the ContentWasTruncated logic:
// stream_completed_normally = received_usage or received_context_usage
// content_was_truncated = not stream_completed_normally and len(content) > 0
// and len(tool_calls) == 0.
func TestContentWasTruncated(t *testing.T) {
	tests := []struct {
		name                 string
		content              string
		toolCallCount        int
		hasCreditsUsed       bool
		receivedContextUsage bool
		expectTruncated      bool
	}{
		{
			name:                 "content, no completion signal, no tools -> truncated",
			content:              "some output",
			toolCallCount:        0,
			hasCreditsUsed:       false,
			receivedContextUsage: false,
			expectTruncated:      true,
		},
		{
			name:                 "content with credits_used -> not truncated",
			content:              "some output",
			toolCallCount:        0,
			hasCreditsUsed:       true,
			receivedContextUsage: false,
			expectTruncated:      false,
		},
		{
			name:                 "content with context_usage -> not truncated",
			content:              "some output",
			toolCallCount:        0,
			hasCreditsUsed:       false,
			receivedContextUsage: true,
			expectTruncated:      false,
		},
		{
			name:                 "content with tool calls, no completion signal -> not truncated",
			content:              "some output",
			toolCallCount:        1,
			hasCreditsUsed:       false,
			receivedContextUsage: false,
			expectTruncated:      false,
		},
		{
			name:                 "no content, no completion signal -> not truncated",
			content:              "",
			toolCallCount:        0,
			hasCreditsUsed:       false,
			receivedContextUsage: false,
			expectTruncated:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New("gpt-4o", AsContent)
			var buf bytes.Buffer

			// Add content events to set fullContent
			if tt.content != "" {
				f.Handle(streamingcore.KiroEvent{
					Kind:    "content",
					Content: tt.content,
				}, &buf)
			}

			// Add tool calls
			for i := 0; i < tt.toolCallCount; i++ {
				f.Handle(streamingcore.KiroEvent{
					Kind: "tool_use",
					ToolUse: &streamingcore.ToolUseData{
						ID:   "tc" + string(rune(i)),
						Name: "tool",
					},
				}, &buf)
			}

			// Set other fields
			if tt.hasCreditsUsed {
				f.creditsUsedRaw = []byte(`{"some":"usage"}`)
			}
			f.receivedContextUsage = tt.receivedContextUsage

			// Call Finish to compute and store contentWasTruncated
			_ = f.Finish(&buf)

			got := f.ContentWasTruncated()
			if got != tt.expectTruncated {
				t.Errorf("ContentWasTruncated: expected %v, got %v", tt.expectTruncated, got)
			}
		})
	}
}

// TestFullContent verifies that FullContent returns the accumulated content.
func TestFullContent(t *testing.T) {
	f := New("gpt-4o", AsContent)
	var buf bytes.Buffer

	// Emit content events
	events := []streamingcore.KiroEvent{
		{Kind: "content", Content: "Hello "},
		{Kind: "content", Content: "World"},
	}

	for _, ev := range events {
		if err := f.Handle(ev, &buf); err != nil {
			t.Fatalf("Handle failed: %v", err)
		}
	}

	content := f.FullContent()
	if content != "Hello World" {
		t.Errorf("Expected 'Hello World', got %q", content)
	}
}

// TestMultipleTruncatedTools verifies that multiple truncated tool calls
// are all collected.
func TestMultipleTruncatedTools(t *testing.T) {
	f := New("gpt-4o", AsContent)
	var buf bytes.Buffer

	events := []streamingcore.KiroEvent{
		{
			Kind: "tool_use",
			ToolUse: &streamingcore.ToolUseData{
				ID:                 "tc1",
				Name:               "tool1",
				TruncationDetected: true,
				TruncationInfo:     map[string]any{"size_bytes": 100},
			},
		},
		{
			Kind: "tool_use",
			ToolUse: &streamingcore.ToolUseData{
				ID:                 "tc2",
				Name:               "tool2",
				TruncationDetected: false,
			},
		},
		{
			Kind: "tool_use",
			ToolUse: &streamingcore.ToolUseData{
				ID:                 "tc3",
				Name:               "tool3",
				TruncationDetected: true,
				TruncationInfo:     map[string]any{"size_bytes": 200},
			},
		},
	}

	for _, ev := range events {
		if err := f.Handle(ev, &buf); err != nil {
			t.Fatalf("Handle failed: %v", err)
		}
	}

	truncated := f.TruncatedTools()
	if len(truncated) != 2 {
		t.Fatalf("Expected 2 truncated tools, got %d", len(truncated))
	}

	// Verify order and contents
	if truncated[0].ID != "tc1" || truncated[1].ID != "tc3" {
		t.Errorf("Unexpected tool order: %v", truncated)
	}
}

// TestContentWasTruncatedWithBracketToolCalls verifies that bracket-style
// tool calls are correctly accounted for in ContentWasTruncated (Task 8b).
// When content contains bracket tool calls, ContentWasTruncated must be FALSE.
func TestContentWasTruncatedWithBracketToolCalls(t *testing.T) {
	f := New("gpt-4o", AsContent)
	var buf bytes.Buffer

	// Content with bracket-style tool call pattern, no stream tool calls
	contentWithBracket := "Some text [Called search with args: {\"query\": \"test\"}] more"
	f.Handle(streamingcore.KiroEvent{
		Kind:    "content",
		Content: contentWithBracket,
	}, &buf)

	// No stream tool calls, no context_usage → normally would be truncated
	// But bracket calls should make it NOT truncated
	// Must call Finish to compute and store contentWasTruncated
	if err := f.Finish(&buf); err != nil {
		t.Fatalf("Finish failed: %v", err)
	}

	// Despite no context_usage and no stream tool calls, ContentWasTruncated
	// must be FALSE because bracket calls are counted in Finish's allToolCalls
	got := f.ContentWasTruncated()
	if got {
		t.Errorf("ContentWasTruncated: expected FALSE (bracket calls present), got TRUE")
	}
}
