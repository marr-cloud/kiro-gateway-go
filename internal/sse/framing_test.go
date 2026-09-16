// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package sse

import (
	"bytes"
	"testing"
)

// TestFormatEventOpenAI verifies OpenAI-style SSE format (name == "").
func TestFormatEventOpenAI(t *testing.T) {
	data := []byte(`{"choices":[{"delta":{"content":"hello"}}]}`)
	got := FormatEvent("", data)

	expected := []byte(`data: {"choices":[{"delta":{"content":"hello"}}]}` + "\n\n")
	if !bytes.Equal(got, expected) {
		t.Errorf("FormatEvent(\"\", data) = %q, want %q", got, expected)
	}
}

// TestFormatEventAnthropic verifies Anthropic-style SSE format (name != "").
func TestFormatEventAnthropic(t *testing.T) {
	data := []byte(`{"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}`)
	got := FormatEvent("content_block_delta", data)

	expected := []byte(`event: content_block_delta` + "\n" + `data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"hello"}}` + "\n\n")
	if !bytes.Equal(got, expected) {
		t.Errorf("FormatEvent(\"content_block_delta\", data) = %q, want %q", got, expected)
	}
}

// TestFormatEventOpenAIEmpty tests OpenAI format with empty data.
func TestFormatEventOpenAIEmpty(t *testing.T) {
	data := []byte(`{}`)
	got := FormatEvent("", data)

	expected := []byte(`data: {}` + "\n\n")
	if !bytes.Equal(got, expected) {
		t.Errorf("FormatEvent(\"\", {{}}) = %q, want %q", got, expected)
	}
}

// TestFormatEventAnthropicMultipleTypes tests various Anthropic event types.
func TestFormatEventAnthropicMultipleTypes(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		data      []byte
		expected  []byte
	}{
		{
			name:      "message_start",
			eventType: "message_start",
			data:      []byte(`{"type":"message_start","message":{"id":"msg_123"}}`),
			expected:  []byte(`event: message_start` + "\n" + `data: {"type":"message_start","message":{"id":"msg_123"}}` + "\n\n"),
		},
		{
			name:      "message_stop",
			eventType: "message_stop",
			data:      []byte(`{"type":"message_stop"}`),
			expected:  []byte(`event: message_stop` + "\n" + `data: {"type":"message_stop"}` + "\n\n"),
		},
		{
			name:      "content_block_start",
			eventType: "content_block_start",
			data:      []byte(`{"type":"content_block_start","content_block":{"type":"text"}}`),
			expected:  []byte(`event: content_block_start` + "\n" + `data: {"type":"content_block_start","content_block":{"type":"text"}}` + "\n\n"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatEvent(tt.eventType, tt.data)
			if !bytes.Equal(got, tt.expected) {
				t.Errorf("FormatEvent(%q, ...) = %q, want %q", tt.eventType, got, tt.expected)
			}
		})
	}
}

// TestFormatEventWithEmbeddedNewlines tests that embedded newlines in JSON payload are preserved.
func TestFormatEventWithEmbeddedNewlines(t *testing.T) {
	// JSON with embedded newline in a string value
	data := []byte(`{"content":"line1\nline2"}`)
	got := FormatEvent("", data)

	expected := []byte(`data: {"content":"line1\nline2"}` + "\n\n")
	if !bytes.Equal(got, expected) {
		t.Errorf("FormatEvent with embedded newline = %q, want %q", got, expected)
	}
}

// TestFormatEventWithCarriageReturn tests payload with \r\n.
func TestFormatEventWithCarriageReturn(t *testing.T) {
	// JSON with \r\n embedded
	data := []byte(`{"content":"line1\r\nline2"}`)
	got := FormatEvent("", data)

	expected := []byte(`data: {"content":"line1\r\nline2"}` + "\n\n")
	if !bytes.Equal(got, expected) {
		t.Errorf("FormatEvent with \\r\\n = %q, want %q", got, expected)
	}
}

// TestFormatEventBothDialectsExactFormat verifies exact byte-for-byte format.
func TestFormatEventBothDialectsExactFormat(t *testing.T) {
	// OpenAI: "data: {json}\n\n"
	openaiData := []byte(`{"id":"chatcmpl-1"}`)
	openaiGot := FormatEvent("", openaiData)
	openaiExpected := []byte("data: " + string(openaiData) + "\n\n")
	if !bytes.Equal(openaiGot, openaiExpected) {
		t.Errorf("OpenAI format mismatch:\n  got:  %q\n  want: %q", openaiGot, openaiExpected)
	}

	// Anthropic: "event: {name}\ndata: {json}\n\n"
	anthropicData := []byte(`{"type":"message_start"}`)
	anthropicGot := FormatEvent("message_start", anthropicData)
	anthropicExpected := []byte("event: message_start\ndata: " + string(anthropicData) + "\n\n")
	if !bytes.Equal(anthropicGot, anthropicExpected) {
		t.Errorf("Anthropic format mismatch:\n  got:  %q\n  want: %q", anthropicGot, anthropicExpected)
	}
}

// TestFormatDone verifies the [DONE] message format.
func TestFormatDone(t *testing.T) {
	got := FormatDone()
	expected := []byte("data: [DONE]\n\n")
	if !bytes.Equal(got, expected) {
		t.Errorf("FormatDone() = %q, want %q", got, expected)
	}
}

// TestFormatDoneIsOpenAIOnly verifies [DONE] uses OpenAI format (no event line).
func TestFormatDoneIsOpenAIOnly(t *testing.T) {
	got := FormatDone()

	// Should not contain "event:" prefix
	if bytes.Contains(got, []byte("event:")) {
		t.Errorf("FormatDone() should not contain 'event:' prefix: %q", got)
	}

	// Should start with "data: [DONE]"
	if !bytes.HasPrefix(got, []byte("data: [DONE]")) {
		t.Errorf("FormatDone() should start with 'data: [DONE]': %q", got)
	}

	// Should end with \n\n
	if !bytes.HasSuffix(got, []byte("\n\n")) {
		t.Errorf("FormatDone() should end with \\n\\n: %q", got)
	}
}

// TestFormatEventLargePayload tests with a large JSON payload.
func TestFormatEventLargePayload(t *testing.T) {
	// Build a moderately large JSON object
	largeData := []byte(`{"choices":[{"index":0,"delta":{"content":"` +
		"this is a longer piece of content that might span multiple tokens and be part of a larger message in a streaming response" +
		`"}}]}`)

	got := FormatEvent("", largeData)

	// Verify structure
	if !bytes.HasPrefix(got, []byte("data: {")) {
		t.Errorf("Large payload should start with 'data: {': %q...", got[:20])
	}
	if !bytes.HasSuffix(got, []byte("\n\n")) {
		t.Errorf("Large payload should end with \\n\\n")
	}
}
