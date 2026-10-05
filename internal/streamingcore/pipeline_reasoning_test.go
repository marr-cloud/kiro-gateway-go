// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingcore

import (
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/thinkingparser"
)

// El razonamiento nativo no pasa por el thinkingparser (no viene envuelto en
// <thinking>): sale como native_thinking + thinking_signature, o como
// redacted_thinking (DIFFERENCES §18).
func TestFeedNativeReasoning(t *testing.T) {
	events := NewPipeline(thinkingparser.HandlingAsReasoningContent, 20).Feed([]byte(
		`{"text":"I need"}{"text":" to think"}{"signature":"SIG"}{"redactedContent":"AAEC"}`))

	want := []KiroEvent{
		{Kind: "native_thinking", Thinking: "I need"},
		{Kind: "native_thinking", Thinking: " to think"},
		{Kind: "thinking_signature", Signature: "SIG"},
		{Kind: "redacted_thinking", RedactedData: "AAEC"},
	}
	if len(events) != len(want) {
		t.Fatalf("eventos = %+v", events)
	}
	for i := range want {
		if events[i].Kind != want[i].Kind || events[i].Thinking != want[i].Thinking ||
			events[i].Signature != want[i].Signature || events[i].RedactedData != want[i].RedactedData {
			t.Errorf("evento %d = %+v, quiero %+v", i, events[i], want[i])
		}
	}
}
