// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingcore

import (
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/thinkingparser"
)

func TestFeedRefusalEvent(t *testing.T) {
	pipeline := NewPipeline(thinkingparser.HandlingAsReasoningContent, 20)

	events := pipeline.Feed([]byte(`{"content":"Hola"}` +
		`{"stopDetails":{"refusal":{"category":"REASONING_EXTRACTION","explanation":"The selected model cannot continue this conversation."}},"stopReason":"CONTENT_FILTERED"}`))

	last := events[len(events)-1]
	if last.Kind != "refusal" || last.Refusal == nil {
		t.Fatalf("último evento = %+v, quiero Kind refusal con Refusal", last)
	}
	if last.Refusal.Category != "REASONING_EXTRACTION" || last.Refusal.Explanation != "The selected model cannot continue this conversation." {
		t.Errorf("Refusal = %+v", *last.Refusal)
	}
}

// Sin stopDetails (no debería pasar, pero Kiro no lo garantiza) el evento
// sale igual, con los campos vacíos.
func TestFeedRefusalWithoutDetails(t *testing.T) {
	events := NewPipeline(thinkingparser.HandlingAsReasoningContent, 20).Feed([]byte(`{"stopReason":"CONTENT_FILTERED"}`))
	if len(events) != 1 || events[0].Kind != "refusal" || events[0].Refusal == nil {
		t.Fatalf("eventos = %+v, quiero un refusal", events)
	}
}
