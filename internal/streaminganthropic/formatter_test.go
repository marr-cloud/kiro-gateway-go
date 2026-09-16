// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streaminganthropic

import (
	"bytes"
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
	"github.com/marr-cloud/kiro-gateway-go/internal/sse"
	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// messageIDPattern matches Anthropic message ids: "msg_" + 24 lowercase hex
// chars (utils.GenerateMessageID / .upstream/kiro/streaming_anthropic.py:65-67).
//
// Shape-only comparison ruling (docs/MAPPING.md task-8): the corpus recorder
// ran the whole upstream test suite behind a monkeypatched uuid4 that
// returns a process-wide incrementing counter (internal/utils/ids.go's
// "Contrato de congelación" doc comment), so each fixture's literal id
// reflects wherever that global counter stood when ITS test ran — not
// anything derivable from the fixture's own input. Comparing by shape,
// exactly like Task 7's chatcmpl-<32hex> ruling, is the only reproducible
// check available.
var messageIDPattern = regexp.MustCompile(`^msg_[0-9a-f]{24}$`)

// --- format_sse_event corpus: exercises internal/sse.FormatEvent + pyjson.Dumps directly ---
//
// .upstream/kiro/streaming_anthropic.py's format_sse_event is a two-line
// wrapper: `f"event: {event_type}\ndata: {json.dumps(data, ensure_ascii=False)}\n\n"`.
// internal/sse.FormatEvent (Task 5) already ports the framing; this test
// verifies the JSON half (internal/pyjson.Dumps) against the corpus that
// upstream recorded for format_sse_event, per the brief's option (a): this
// corpus overlaps with Task 5's but belongs to streaming_anthropic.py's test
// surface, so it's covered here rather than moved into internal/sse.

func TestFormatSSEEventCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "streaming_anthropic/format_sse_event")

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			eventTypeRaw := testutil.Arg(t, c.Input, 0)
			var eventType string
			if err := json.Unmarshal(eventTypeRaw, &eventType); err != nil {
				t.Fatalf("unmarshalling event_type: %v", err)
			}
			data := testutil.Arg(t, c.Input, 1)

			formatted, err := pyjson.Dumps(data)
			if err != nil {
				t.Fatalf("pyjson.Dumps: %v", err)
			}
			actual := string(sse.FormatEvent(eventType, []byte(formatted)))

			var expected string
			if err := json.Unmarshal(c.Output, &expected); err != nil {
				t.Fatalf("unmarshalling expected output: %v", err)
			}

			if actual != expected {
				t.Errorf("mismatch:\n actual:   %q\n expected: %q", actual, expected)
			}
		})
	}
}

// --- stream_kiro_to_anthropic corpus: the full Handle/Finish E2E pipeline ---

func TestStreamKiroToAnthropicCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "streaming_anthropic/stream_kiro_to_anthropic")

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			testStreamCase(t, c)
		})
	}
}

// mockedFixtureSkips documents corpus fixtures whose source Python test
// monkeypatches a helper function `stream_kiro_to_anthropic` calls, so the
// recorded output reflects a canned test double, not the real function
// (.upstream/kiro/tokenizer.py's estimate_request_tokens or
// .upstream/kiro/parsers.py's parse_bracket_tool_calls) — the same class of
// documented skip as Task 7's monkey-patch fixture. This port calls the real
// functions (tokenizer.CountMessageTokens/CountToolsTokens/CountSystemTokens,
// parsers.ParseBracketToolCalls), so it reproduces upstream's actual
// behavior rather than the mock, and these fixtures necessarily disagree
// with it. Every test in .upstream/tests/unit/test_streaming_anthropic.py
// that reaches these call sites patches them — grep confirms the real
// implementations are never exercised through this corpus, so there's no
// "real" value these fixtures could have captured instead.
var mockedFixtureSkips = map[string]string{
	// test_uses_request_messages_for_input_tokens (test_streaming_anthropic.py:1104-1121):
	// `with patch('kiro.streaming_anthropic.estimate_request_tokens', return_value={"total_tokens": 10})`
	// for request_messages=[{"role":"user","content":"Hi there!"}]. Real
	// tokenizer.CountMessageTokens for that message is 11, not 10.
	"b0f2a0cabe68929e": `estimate_request_tokens is mocked to return_value={"total_tokens": 10} regardless of input (test_streaming_anthropic.py:1105); real count is 11`,

	// test_context_usage_zero_keeps_fallback_estimate (test_streaming_anthropic.py:1160-1178):
	// `with patch('kiro.streaming_anthropic.estimate_request_tokens', return_value={"total_tokens": 99})`
	// for request_messages=[{"role":"user","content":"hi"}]. Real count is 9.
	"e21af20711a4832e": `estimate_request_tokens is mocked to return_value={"total_tokens": 99} regardless of input (test_streaming_anthropic.py:1171); real count is 9`,

	// test_uses_tools_and_system_for_input_tokens (test_streaming_anthropic.py:1125-1157):
	// `with patch('kiro.streaming_anthropic.estimate_request_tokens', return_value={"total_tokens": 12})`
	// for messages=[{"role":"user","content":"Hi"}], tools=[get_weather],
	// system=[{"type":"text","text":"你是助手"}]. Real count is 26.
	"f0f3c8b21b133a12": `estimate_request_tokens is mocked to return_value={"total_tokens": 12} regardless of input (test_streaming_anthropic.py:1139); real count is 26`,

	// test_handles_bracket_tool_calls (test_streaming_anthropic.py:444-469):
	// `with patch('kiro.streaming_anthropic.parse_bracket_tool_calls', return_value=[{"id": "call_1", "function": {"name": "func1", "arguments": "{}"}}])`
	// for content "[tool_call: func1]", which does NOT match the real
	// bracket pattern `\[Called\s+(\w+)\s+with\s+args:\s*`
	// (.upstream/kiro/parsers.py:115, internal/parsers/toolcalls.go's
	// bracketToolCallPattern) — every OTHER test in this file mocks
	// parse_bracket_tool_calls to return_value=[], confirming the real
	// function is never exercised via this corpus at all.
	"51e4de551161d0ff": `parse_bracket_tool_calls is mocked to a canned non-matching result (test_streaming_anthropic.py:463); the real function returns [] for "[tool_call: func1]"`,
}

func testStreamCase(t *testing.T, c testutil.Case) {
	t.Helper()

	if reason, skip := mockedFixtureSkips[c.Name]; skip {
		t.Skip(reason)
	}

	kwargs := testutil.Kwargs(t, c.Input)

	var model string
	if err := json.Unmarshal(kwargs["model"], &model); err != nil {
		t.Fatalf("unmarshalling model: %v", err)
	}

	thinkingHandling := AsReasoningContent
	if config := testutil.Config(c.Input); config != nil {
		if raw, ok := config["FAKE_REASONING_HANDLING"]; ok {
			var mode string
			if err := json.Unmarshal(raw, &mode); err == nil {
				switch ThinkingHandling(mode) {
				case IncludeAsText:
					thinkingHandling = IncludeAsText
				case Strip:
					thinkingHandling = Strip
				default:
					thinkingHandling = AsReasoningContent
				}
			}
		}
	}

	var requestMessages []json.RawMessage
	if raw, ok := kwargs["request_messages"]; ok && !isJSONNull(raw) {
		_ = json.Unmarshal(raw, &requestMessages)
	}
	var requestTools []json.RawMessage
	if raw, ok := kwargs["request_tools"]; ok && !isJSONNull(raw) {
		_ = json.Unmarshal(raw, &requestTools)
	}
	var requestSystem json.RawMessage
	if raw, ok := kwargs["request_system"]; ok && !isJSONNull(raw) {
		requestSystem = raw
	}

	events := testutil.Events(t, c.Input)

	formatter := New(model, thinkingHandling)
	formatter.SetRequestContext(requestMessages, requestTools, requestSystem)

	var buf bytes.Buffer
	if err := formatter.EmitMessageStart(&buf); err != nil {
		t.Fatalf("EmitMessageStart: %v", err)
	}

	exceptionOccurred := false
	for _, rawEvent := range events {
		if testutil.IsException(rawEvent) {
			exceptionOccurred = true
			exc, err := testutil.DecodeException(rawEvent)
			if err != nil {
				t.Fatalf("decoding exception: %v", err)
			}
			// FirstTokenTimeoutError re-raises silently, with no error SSE
			// event (streaming_anthropic.py:695-696).
			if exc.Type != "FirstTokenTimeoutError" {
				if err := formatter.EmitError(&buf, exc.Str); err != nil {
					t.Fatalf("EmitError: %v", err)
				}
			}
			break
		}

		kiroEvent := corpusEventToKiroEvent(t, rawEvent)
		if err := formatter.Handle(kiroEvent, &buf); err != nil {
			t.Fatalf("Handle: %v", err)
		}
	}

	if !exceptionOccurred {
		if err := formatter.Finish(&buf); err != nil {
			t.Fatalf("Finish: %v", err)
		}
	}

	var expectedOutputs []string
	if err := json.Unmarshal(c.Output, &expectedOutputs); err != nil {
		t.Fatalf("unmarshalling expected output: %v", err)
	}

	actualChunks := splitSSEChunks(buf.String())
	expectedChunks := make([]string, 0, len(expectedOutputs))
	for _, s := range expectedOutputs {
		expectedChunks = append(expectedChunks, splitSSEChunks(s)...)
	}

	if len(actualChunks) != len(expectedChunks) {
		t.Fatalf("chunk count mismatch: got %d, want %d\n actual:   %q\n expected: %q",
			len(actualChunks), len(expectedChunks), actualChunks, expectedChunks)
	}

	for i := range actualChunks {
		compareSSEChunk(t, i, actualChunks[i], expectedChunks[i])
	}
}

// isJSONNull reports whether raw is absent or the JSON literal null.
func isJSONNull(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) == 0 || string(trimmed) == "null"
}

// corpusEvent mirrors the KiroEvent dataclass fields the Python corpus
// recorder serializes per event (.upstream/kiro/streaming_core.py), the same
// shape internal/streamingopenai's corpus test decodes.
type corpusEvent struct {
	Type                   string          `json:"type"`
	Content                *string         `json:"content"`
	ThinkingContent        *string         `json:"thinking_content"`
	ToolUse                json.RawMessage `json:"tool_use"`
	Usage                  json.RawMessage `json:"usage"`
	ContextUsagePercentage *float64        `json:"context_usage_percentage"`
}

// corpusEventToKiroEvent converts a raw corpus event into a streamingcore.KiroEvent.
func corpusEventToKiroEvent(t *testing.T, rawEvent json.RawMessage) streamingcore.KiroEvent {
	t.Helper()

	var ce corpusEvent
	if err := json.Unmarshal(rawEvent, &ce); err != nil {
		t.Fatalf("unmarshalling event: %v", err)
	}

	ev := streamingcore.KiroEvent{Kind: ce.Type}

	if ce.Content != nil {
		ev.Content = *ce.Content
	}
	if ce.ThinkingContent != nil {
		ev.Thinking = *ce.ThinkingContent
	}

	if len(ce.ToolUse) > 0 && !isJSONNull(ce.ToolUse) {
		var toolUseMap map[string]any
		if err := json.Unmarshal(ce.ToolUse, &toolUseMap); err == nil {
			tu := &streamingcore.ToolUseData{
				ID:   getStringField(toolUseMap, "id"),
				Name: getStringField(getMapField(toolUseMap, "function"), "name"),
			}
			if args, ok := getMapField(toolUseMap, "function")["arguments"]; ok {
				if argStr, isStr := args.(string); isStr {
					if err := json.Unmarshal([]byte(argStr), &tu.Input); err != nil {
						tu.Input = map[string]any{}
					}
				}
			}
			if tu.Input == nil {
				tu.Input = map[string]any{}
			}
			ev.ToolUse = tu
		}
	}

	if len(ce.Usage) > 0 && !isJSONNull(ce.Usage) {
		ev.UsageRaw = ce.Usage
	}

	if ce.ContextUsagePercentage != nil {
		ev.ContextUsage = *ce.ContextUsagePercentage
	}

	return ev
}

func getStringField(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, isStr := v.(string); isStr {
			return s
		}
	}
	return ""
}

func getMapField(m map[string]any, key string) map[string]any {
	if v, ok := m[key]; ok {
		if mp, isMap := v.(map[string]any); isMap {
			return mp
		}
	}
	return map[string]any{}
}

// splitSSEChunks splits a run of SSE text into individual "event: ...\ndata:
// ...\n\n" chunks (without the trailing blank line).
func splitSSEChunks(s string) []string {
	parts := strings.Split(s, "\n\n")
	chunks := make([]string, 0, len(parts))
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			chunks = append(chunks, p)
		}
	}
	return chunks
}

// compareSSEChunk compares one "event: X\ndata: Y" chunk against its
// expected counterpart: the event line must match exactly, and the JSON
// payload is compared semantically (decoded, not byte-for-byte) so map key
// order and pyjson's exact spacing don't matter here — TestFormatSSEEventCorpus
// already covers formatting fidelity directly. message_start's
// message.id is compared by shape only (see messageIDPattern).
func compareSSEChunk(t *testing.T, i int, actual, expected string) {
	t.Helper()

	actualEvent, actualData := splitEventLine(t, actual)
	expectedEvent, expectedData := splitEventLine(t, expected)

	if actualEvent != expectedEvent {
		t.Errorf("chunk %d: event mismatch: got %q, want %q", i, actualEvent, expectedEvent)
		return
	}

	var actualJSON, expectedJSON map[string]any
	if err := json.Unmarshal([]byte(actualData), &actualJSON); err != nil {
		t.Errorf("chunk %d: unmarshalling actual data: %v\ndata: %s", i, err, actualData)
		return
	}
	if err := json.Unmarshal([]byte(expectedData), &expectedJSON); err != nil {
		t.Errorf("chunk %d: unmarshalling expected data: %v\ndata: %s", i, err, expectedData)
		return
	}

	if actualEvent == "message_start" {
		normalizeMessageID(t, i, actualJSON)
		normalizeMessageID(t, i, expectedJSON)
	}

	if !reflect.DeepEqual(actualJSON, expectedJSON) {
		t.Errorf("chunk %d (%s): payload mismatch:\n actual:   %s\n expected: %s", i, actualEvent, actualData, expectedData)
	}
}

// splitEventLine parses a "event: X\ndata: Y" chunk into (X, Y).
func splitEventLine(t *testing.T, chunk string) (event, data string) {
	t.Helper()
	lines := strings.SplitN(chunk, "\n", 2)
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "event: ") || !strings.HasPrefix(lines[1], "data: ") {
		t.Fatalf("malformed SSE chunk: %q", chunk)
	}
	return strings.TrimPrefix(lines[0], "event: "), strings.TrimPrefix(lines[1], "data: ")
}

// normalizeMessageID asserts message.id matches the Anthropic msg_<24hex>
// shape, then clears it so the rest of the message_start payload can be
// compared for exact equality.
func normalizeMessageID(t *testing.T, chunkIdx int, data map[string]any) {
	t.Helper()
	message, ok := data["message"].(map[string]any)
	if !ok {
		return
	}
	id, _ := message["id"].(string)
	if !messageIDPattern.MatchString(id) {
		t.Errorf("chunk %d: message.id %q doesn't match msg_<24hex> shape", chunkIdx, id)
	}
	message["id"] = ""
}
