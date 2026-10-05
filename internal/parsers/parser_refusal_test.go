// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package parsers

import (
	"os"
	"testing"
)

// kiro_refusal.bin es un stream real de Kiro (claude-opus-5, 2026-10-05): un
// metadataEvent con CONTENT_FILTERED / REASONING_EXTRACTION seguido de un
// contextUsageEvent, con el framing binario de AWS event-stream intacto.
func TestFeedEmitsRefusalOnContentFiltered(t *testing.T) {
	raw, err := os.ReadFile("testdata/kiro_refusal.bin")
	if err != nil {
		t.Fatal(err)
	}

	events := NewParser().Feed(raw)

	if len(events) != 2 || events[0].Kind != "refusal" || events[1].Kind != "context_usage" {
		t.Fatalf("eventos = %v, quiero [refusal context_usage]", kinds(events))
	}
	refusal, _ := events[0].Value["stopDetails"].(map[string]any)["refusal"].(map[string]any)
	if refusal["category"] != "REASONING_EXTRACTION" {
		t.Errorf("category = %v, quiero REASONING_EXTRACTION", refusal["category"])
	}
}

// Un metadataEvent normal no produce eventos: el corpus del original sigue
// viendo exactamente lo mismo.
func TestFeedIgnoresNormalStopReason(t *testing.T) {
	stream := []byte(`{"content":"hola"}` + "\x00\x01garbage" + `{"stopReason":"END_TURN"}` + `{"contextUsagePercentage":1.5}`)

	events := NewParser().Feed(stream)

	if got := kinds(events); len(got) != 2 || got[0] != "content" || got[1] != "context_usage" {
		t.Fatalf("eventos = %v, quiero [content context_usage]", got)
	}
}

func kinds(events []Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = e.Kind
	}
	return out
}
