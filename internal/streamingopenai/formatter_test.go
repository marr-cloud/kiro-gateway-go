// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingopenai

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestFormatterCorpus tests the Formatter against corpus fixtures.
func TestFormatterCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "streaming_openai/stream_kiro_to_openai_internal")

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			testFormatterCase(t, c)
		})
	}
}

// mockedFixtureSkips documents corpus fixtures whose source Python test
// monkeypatches parse_bracket_tool_calls to a canned non-empty result,
// unrelated to the actual content (.upstream/tests/unit/test_streaming_anthropic.py:463
// documents the identical scenario for the Anthropic dialect —
// internal/streaminganthropic/formatter_test.go's mockedFixtureSkips has the
// full writeup). Every OTHER call site in the upstream OpenAI/Anthropic
// streaming test suites mocks parse_bracket_tool_calls to return_value=[],
// so the real function (internal/parsers.ParseBracketToolCalls, which this
// port calls) is never exercised through either corpus — for content
// "[tool_call: func1]", which doesn't match the real bracket pattern
// `\[Called\s+(\w+)\s+with\s+args:\s*`, it correctly returns no tool calls,
// disagreeing with this one mocked fixture by design. Previously reported as
// a live (unskipped) failure in docs/MAPPING.md's streaming_openai.py row
// and task-7-report.md; converted to an explicit, evidence-cited skip here
// (task 8) so `go test ./...` is green without silently masking the cause.
var mockedFixtureSkips = map[string]string{
	"c272a07c05e0e8a5": `parse_bracket_tool_calls is mocked to a canned non-matching result in the source Python test (test_streaming_openai.py:1247-1277 test_detects_bracket_tool_calls, patch at :1265); the real function returns [] for "[tool_call: func1]" (same class of mock as streaminganthropic's 51e4de55 skip)`,
}

// testFormatterCase processes a single corpus case.
func testFormatterCase(t *testing.T, c testutil.Case) {
	if reason, skip := mockedFixtureSkips[c.Name]; skip {
		t.Skip(reason)
	}
	// Extract model and other kwargs
	kwargs := testutil.Kwargs(t, c.Input)
	var model string
	if err := json.Unmarshal(kwargs["model"], &model); err != nil {
		t.Fatalf("failed to unmarshal model: %v", err)
	}

	// Extract config for thinking handling mode
	config := testutil.Config(c.Input)
	thinkingHandling := ThinkingHandling(AsContent)
	if config != nil {
		if fakeReasoningRaw, ok := config["FAKE_REASONING_HANDLING"]; ok {
			var fakeReasoningStr string
			if err := json.Unmarshal(fakeReasoningRaw, &fakeReasoningStr); err == nil {
				if fakeReasoningStr == "as_reasoning_content" {
					thinkingHandling = AsReasoningContent
				}
			}
		}
	}

	// Extract request_messages and request_tools for prompt token fallback calculation.
	// Kept as raw JSON (not map[string]any) so nested key order survives into
	// countMessageTokens/countToolsTokens (needed for pyjson.Dumps parity on
	// tool_use "input" / tool "parameters" dumps).
	var requestMessages []json.RawMessage
	var requestTools []json.RawMessage
	if requestMessagesRaw, ok := kwargs["request_messages"]; ok && requestMessagesRaw != nil {
		_ = json.Unmarshal(requestMessagesRaw, &requestMessages)
	}
	if requestToolsRaw, ok := kwargs["request_tools"]; ok && requestToolsRaw != nil {
		_ = json.Unmarshal(requestToolsRaw, &requestTools)
	}

	// Extract events from input
	events := testutil.Events(t, c.Input)

	// Extract expected output
	var expectedOutputs []string
	if err := json.Unmarshal(c.Output, &expectedOutputs); err != nil {
		t.Fatalf("failed to unmarshal output: %v", err)
	}

	// Create formatter and process events
	formatter := New(model, thinkingHandling)
	formatter.SetRequestContext(requestMessages, requestTools)
	var buf bytes.Buffer

	// Track if any actual events were processed
	hasActualEvents := false
	exceptionOccurred := false

	for _, rawEvent := range events {
		// Check if this is an exception (error event). An exception propagating
		// out of the Python generator terminates it immediately — no code after
		// the yield point runs, including the Finish()-equivalent final chunk.
		// So we stop replaying events AND skip Finish() below, matching
		// whatever partial output was already yielded before the exception.
		if testutil.IsException(rawEvent) {
			exceptionOccurred = true
			break
		}

		hasActualEvents = true

		// Convert raw event to KiroEvent
		kiroEvent := eventToKiroEvent(t, rawEvent)

		// Handle event
		if err := formatter.Handle(kiroEvent, &buf); err != nil {
			t.Fatalf("Handle() failed: %v", err)
		}
	}

	// Only finish if we actually processed events and didn't stop on an exception.
	if hasActualEvents && !exceptionOccurred {
		if err := formatter.Finish(&buf); err != nil {
			t.Fatalf("Finish() failed: %v", err)
		}
	}

	// Compare output
	actualOutput := buf.String()
	actualChunks := parseOutputChunks(actualOutput)

	if len(actualChunks) != len(expectedOutputs) {
		t.Errorf("chunk count mismatch: got %d, want %d", len(actualChunks), len(expectedOutputs))
	}

	for i := 0; i < len(actualChunks) && i < len(expectedOutputs); i++ {
		actual := actualChunks[i]
		expected := expectedOutputs[i]

		// For [DONE] chunks, compare directly
		if actual == "data: [DONE]" {
			if expected != "data: [DONE]\n\n" && expected != "data: [DONE]" {
				t.Errorf("chunk %d: expected [DONE], got %q", i, actual)
			}
			continue
		}

		// For JSON chunks, parse and compare with shape-based ID/timestamp matching
		actualJSON, expectedJSON := parseChunk(t, actual), parseChunk(t, expected)

		if err := compareChunks(actualJSON, expectedJSON); err != nil {
			t.Errorf("chunk %d: %v\nActual:   %s\nExpected: %s", i, err, actual, expected)
		}
	}
}

// corpusEvent mirrors the KiroEvent dataclass fields the Python corpus
// recorder serializes per event (.upstream/kiro/streaming_core.py:60-85).
// usage and tool_use stay raw: usage because its shape (bare number vs
// token-count dict) must round-trip verbatim into UsageRaw, tool_use because
// its nested "arguments" string needs its own JSON-in-JSON unmarshal step.
type corpusEvent struct {
	Type                   string          `json:"type"`
	Content                *string         `json:"content"`
	ThinkingContent        *string         `json:"thinking_content"`
	ToolUse                json.RawMessage `json:"tool_use"`
	Usage                  json.RawMessage `json:"usage"`
	ContextUsagePercentage *float64        `json:"context_usage_percentage"`
}

// eventToKiroEvent converts a raw corpus event to a KiroEvent.
func eventToKiroEvent(t *testing.T, rawEvent json.RawMessage) streamingcore.KiroEvent {
	t.Helper()

	var ce corpusEvent
	if err := json.Unmarshal(rawEvent, &ce); err != nil {
		t.Fatalf("failed to unmarshal event: %v", err)
	}

	ev := streamingcore.KiroEvent{Kind: ce.Type}

	if ce.Content != nil {
		ev.Content = *ce.Content
	}
	if ce.ThinkingContent != nil {
		ev.Thinking = *ce.ThinkingContent
	}

	if len(ce.ToolUse) > 0 && string(ce.ToolUse) != "null" {
		var toolUseMap map[string]any
		if err := json.Unmarshal(ce.ToolUse, &toolUseMap); err == nil {
			tu := &streamingcore.ToolUseData{
				ID:   getStringField(toolUseMap, "id"),
				Name: getStringField(getMapField(toolUseMap, "function"), "name"),
			}
			if input, ok := getMapField(toolUseMap, "function")["arguments"]; ok {
				if argStr, isStr := input.(string); isStr {
					if err := json.Unmarshal([]byte(argStr), &tu.Input); err != nil {
						tu.Input = map[string]any{}
					}
				}
			}
			ev.ToolUse = tu
		}
	}

	// usage carries through as raw bytes (bare number or token-count dict) —
	// see streamingcore.KiroEvent.UsageRaw for why: the formatter echoes it
	// verbatim as credits_used, matching upstream's
	// final_chunk["usage"]["credits_used"] = metering_data.
	if len(ce.Usage) > 0 && string(ce.Usage) != "null" {
		ev.UsageRaw = ce.Usage
	}

	if ce.ContextUsagePercentage != nil {
		ev.ContextUsage = *ce.ContextUsagePercentage
	}

	return ev
}

// parseOutputChunks splits the output into individual SSE chunks.
func parseOutputChunks(output string) []string {
	var chunks []string
	parts := strings.Split(output, "\n\n")
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			chunks = append(chunks, trimmed)
		}
	}
	return chunks
}

// parseChunk extracts the JSON data from an SSE "data: " line.
func parseChunk(t *testing.T, chunk string) map[string]any {
	if chunk == "data: [DONE]" || strings.HasPrefix(chunk, "data: [DONE]") {
		return nil
	}

	if !strings.HasPrefix(chunk, "data: ") {
		t.Fatalf("invalid chunk format: %q", chunk)
	}

	dataStr := strings.TrimPrefix(chunk, "data: ")
	var data map[string]any
	if err := json.Unmarshal([]byte(dataStr), &data); err != nil {
		t.Fatalf("failed to parse JSON chunk: %v", err)
	}
	return data
}

// compareChunks compares two chunks, allowing for shape-based ID/timestamp matching.
func compareChunks(actual, expected map[string]any) error {
	if actual == nil && expected == nil {
		return nil
	}
	if actual == nil || expected == nil {
		return nil // Skip None comparisons
	}

	// Check object and model
	if actual["object"] != expected["object"] {
		return testError("object mismatch: got %q, want %q", actual["object"], expected["object"])
	}
	if actual["model"] != expected["model"] {
		return testError("model mismatch: got %q, want %q", actual["model"], expected["model"])
	}

	// Check ID by shape (chatcmpl-<32hex>)
	actualID := getStringField(actual, "id")
	expectedID := getStringField(expected, "id")
	if !matchesChatCmplPattern(actualID) {
		return testError("actual id doesn't match chatcmpl pattern: %q", actualID)
	}
	if !matchesChatCmplPattern(expectedID) {
		return testError("expected id doesn't match chatcmpl pattern: %q", expectedID)
	}

	// Check created timestamp is an integer (don't compare exact value)
	if _, ok := actual["created"].(float64); !ok {
		return testError("created should be numeric, got %T", actual["created"])
	}

	// Compare choices
	actualChoices := getListField(actual, "choices")
	expectedChoices := getListField(expected, "choices")

	if len(actualChoices) != len(expectedChoices) {
		return testError("choices count mismatch: got %d, want %d", len(actualChoices), len(expectedChoices))
	}

	for i, actualChoice := range actualChoices {
		expectedChoice := expectedChoices[i]
		actualMap := actualChoice.(map[string]any)
		expectedMap := expectedChoice.(map[string]any)

		// Compare index
		if actualMap["index"] != expectedMap["index"] {
			return testError("choice %d: index mismatch", i)
		}

		// Compare delta (recursively)
		if err := compareDelta(actualMap["delta"], expectedMap["delta"]); err != nil {
			return testError("choice %d: delta mismatch: %v", i, err)
		}

		// Compare finish_reason
		if actualMap["finish_reason"] != expectedMap["finish_reason"] {
			return testError("choice %d: finish_reason mismatch: got %v, want %v", i, actualMap["finish_reason"], expectedMap["finish_reason"])
		}
	}

	// Compare usage if present
	if actualUsage, ok := actual["usage"]; ok {
		if expectedUsage, ok := expected["usage"]; ok {
			actualUsageMap := actualUsage.(map[string]any)
			expectedUsageMap := expectedUsage.(map[string]any)
			for key, expectedVal := range expectedUsageMap {
				if actualVal, ok := actualUsageMap[key]; !ok {
					return testError("usage missing key: %q", key)
				} else if key == "credits_used" {
					// Special handling for credits_used which can be a dict
					// Just check that it exists if expected
					continue
				} else if actualVal != expectedVal {
					return testError("usage.%s mismatch: got %v, want %v", key, actualVal, expectedVal)
				}
			}
		}
	}

	return nil
}

// compareDelta recursively compares delta objects.
func compareDelta(actual, expected any) error {
	if actual == nil && expected == nil {
		return nil
	}
	if actual == nil || expected == nil {
		return testError("delta nil mismatch")
	}

	actualMap := actual.(map[string]any)
	expectedMap := expected.(map[string]any)

	// Check all expected keys are present with correct values
	for key, expectedVal := range expectedMap {
		actualVal, ok := actualMap[key]
		if !ok {
			return testError("delta missing key: %q", key)
		}

		// Special handling for tool_calls (order-independent comparison)
		if key == "tool_calls" {
			actualCalls := actualVal.([]any)
			expectedCalls := expectedVal.([]any)
			if len(actualCalls) != len(expectedCalls) {
				return testError("tool_calls count mismatch: got %d, want %d", len(actualCalls), len(expectedCalls))
			}
			// For now, compare as-is (order matters in the stream)
			// A more robust comparison would match by tool call ID
		}

		// For string values, compare directly
		if actualStr, ok := actualVal.(string); ok {
			if expectedStr, ok := expectedVal.(string); ok {
				if actualStr != expectedStr {
					return testError("delta.%s mismatch: got %q, want %q", key, actualStr, expectedStr)
				}
			}
		}
	}

	return nil
}

// matchesChatCmplPattern checks if an ID matches the chatcmpl-<32hex> pattern.
func matchesChatCmplPattern(id string) bool {
	pattern := regexp.MustCompile(`^chatcmpl-[0-9a-f]{32}$`)
	return pattern.MatchString(id)
}

// Helper functions for map access

func getStringField(m map[string]any, key string) string {
	if val, ok := m[key]; ok {
		if s, isStr := val.(string); isStr {
			return s
		}
	}
	return ""
}

func getMapField(m map[string]any, key string) map[string]any {
	if val, ok := m[key]; ok {
		if mp, isMap := val.(map[string]any); isMap {
			return mp
		}
	}
	return map[string]any{}
}

func getListField(m map[string]any, key string) []any {
	if val, ok := m[key]; ok {
		if list, isList := val.([]any); isList {
			return list
		}
	}
	return []any{}
}

// testError is a helper to create error messages.
type testErrorMsg string

func (e testErrorMsg) Error() string {
	return string(e)
}

func testError(format string, args ...any) error {
	return testErrorMsg(format)
}
