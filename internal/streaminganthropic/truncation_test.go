// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streaminganthropic

import (
	"bytes"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
)

// TestHandleTruncatedToolUse verifies that tool calls flagged with
// TruncationDetected are collected in the formatter's truncatedTools side-channel.
func TestHandleTruncatedToolUse(t *testing.T) {
	f := New("claude-3-5-sonnet-20241022")
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
	f := New("claude-3-5-sonnet-20241022")
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
// content_was_truncated = not received_context_usage and len(content) > 0
// and len(tool_blocks) == 0.
func TestContentWasTruncated(t *testing.T) {
	tests := []struct {
		name                 string
		content              string
		toolBlockCount       int
		receivedContextUsage bool
		expectTruncated      bool
	}{
		{
			name:                 "content with no context_usage, no tool calls -> truncated",
			content:              "some output",
			toolBlockCount:       0,
			receivedContextUsage: false,
			expectTruncated:      true,
		},
		{
			name:                 "content with context_usage -> not truncated",
			content:              "some output",
			toolBlockCount:       0,
			receivedContextUsage: true,
			expectTruncated:      false,
		},
		{
			name:                 "content with tool calls, no context_usage -> not truncated",
			content:              "some output",
			toolBlockCount:       1,
			receivedContextUsage: false,
			expectTruncated:      false,
		},
		{
			name:                 "no content, no context_usage -> not truncated",
			content:              "",
			toolBlockCount:       0,
			receivedContextUsage: false,
			expectTruncated:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := New("claude-3-5-sonnet-20241022")
			f.fullContent = tt.content
			f.toolBlockCount = tt.toolBlockCount
			f.receivedContextUsage = tt.receivedContextUsage

			got := f.ContentWasTruncated()
			if got != tt.expectTruncated {
				t.Errorf("ContentWasTruncated: expected %v, got %v", tt.expectTruncated, got)
			}
		})
	}
}

// TestFullContent verifies that FullContent returns the accumulated content.
func TestFullContent(t *testing.T) {
	f := New("claude-3-5-sonnet-20241022")
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
	f := New("claude-3-5-sonnet-20241022")
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
