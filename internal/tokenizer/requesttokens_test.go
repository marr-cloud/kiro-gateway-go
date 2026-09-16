// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package tokenizer

import (
	"encoding/json"
	"testing"
)

// The streaming corpus only ever exercises CountMessageTokens/CountToolsTokens/
// CountSystemTokens with apply_claude_correction=False (message_start and the
// OpenAI fallback both pass False). The /v1/messages/count_tokens endpoint
// (.upstream/kiro/routes_anthropic.py:948-952) passes True, so these tests pin
// the True path directly: upstream scales only the *final* total by
// CLAUDE_CORRECTION_FACTOR and truncates toward zero
// (int(total_tokens * 1.15)), never the per-string sub-counts.

func TestCountMessageTokensCorrection(t *testing.T) {
	messages := []json.RawMessage{
		json.RawMessage(`{"role":"user","content":"The quick brown fox jumps over the lazy dog"}`),
		json.RawMessage(`{"role":"assistant","content":"A pangram contains every letter of the alphabet at least once"}`),
	}
	assertCorrection(t, "CountMessageTokens",
		CountMessageTokens(messages, false),
		CountMessageTokens(messages, true))
}

func TestCountToolsTokensCorrection(t *testing.T) {
	tools := []json.RawMessage{
		json.RawMessage(`{"type":"function","function":{"name":"get_weather","description":"Get the current weather for a given city","parameters":{"type":"object","properties":{"city":{"type":"string"}}}}}`),
	}
	assertCorrection(t, "CountToolsTokens",
		CountToolsTokens(tools, false),
		CountToolsTokens(tools, true))
}

func TestCountSystemTokensCorrection(t *testing.T) {
	system := json.RawMessage(`"You are a careful assistant that always answers in complete sentences."`)
	assertCorrection(t, "CountSystemTokens",
		CountSystemTokens(system, false),
		CountSystemTokens(system, true))
}

// assertCorrection verifies corrected == int(uncorrected * 1.15) and that the
// input was large enough for the correction to actually change the value (so
// the assertion isn't trivially satisfied by a tiny count).
func assertCorrection(t *testing.T, name string, uncorrected, corrected int) {
	t.Helper()
	if uncorrected <= 6 {
		t.Fatalf("%s: test input too small to distinguish correction (uncorrected=%d)", name, uncorrected)
	}
	want := int(float64(uncorrected) * claudeCorrectionFactor)
	if corrected != want {
		t.Errorf("%s: corrected=%d, want int(%d*%.2f)=%d", name, corrected, uncorrected, claudeCorrectionFactor, want)
	}
	if corrected <= uncorrected {
		t.Errorf("%s: correction should increase the count (corrected=%d, uncorrected=%d)", name, corrected, uncorrected)
	}
}

// TestCountTokensEmptyIsZero guards the empty-input short circuits: they return
// 0 before any correction, matching upstream's `if not messages: return 0`
// (etc.) which run before the correction tail.
func TestCountTokensEmptyIsZero(t *testing.T) {
	if got := CountMessageTokens(nil, true); got != 0 {
		t.Errorf("CountMessageTokens(nil, true) = %d, want 0", got)
	}
	if got := CountToolsTokens(nil, true); got != 0 {
		t.Errorf("CountToolsTokens(nil, true) = %d, want 0", got)
	}
	if got := CountSystemTokens(nil, true); got != 0 {
		t.Errorf("CountSystemTokens(nil, true) = %d, want 0", got)
	}
}
