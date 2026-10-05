// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package parsers

import "testing"

// reasoningContentEvent de Kiro: trozos {"text":…} y al final
// {"signature":…} o {"redactedContent":…} (DIFFERENCES §18).
func TestFeedReasoningEvents(t *testing.T) {
	stream := []byte("\x00garbage" + `{"text":"I need"}` + "\x01" + `{"text":" to think"}` +
		`{"signature":"EswDCpEB"}` + `{"redactedContent":"AAEC"}` + `{"content":"Hola"}`)

	events := NewParser().Feed(stream)

	want := []string{"reasoning_text", "reasoning_text", "reasoning_signature", "reasoning_redacted", "content"}
	got := kinds(events)
	if len(got) != len(want) {
		t.Fatalf("eventos = %v, quiero %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("eventos = %v, quiero %v", got, want)
		}
	}
	if events[1].Value["text"] != " to think" || events[2].Value["signature"] != "EswDCpEB" {
		t.Errorf("valores = %v / %v", events[1].Value, events[2].Value)
	}
}
