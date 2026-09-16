// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streaminganthropic

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
)

// The stream_kiro_to_anthropic corpus (formatter_test.go) never drives a
// "thinking" KiroEvent through the AsReasoningContent / IncludeAsText / Strip
// branches, so the native-thinking emit path (emitThinkingStart /
// emitThinkingDelta, block index 0) had zero coverage — spec §11 riesgo #6.
// These hand-written cases exercise it directly and pin the block-index
// transitions upstream documents (thinking = index 0, text = index 1 when a
// thinking block preceded it: .upstream/kiro/streaming_anthropic.py:186-190).

// parsedSSE is one decoded SSE chunk: its event name and its JSON data object.
type parsedSSE struct {
	event string
	data  map[string]any
}

// collectSSE decodes a run of accumulated SSE text into (event, data) chunks,
// reusing the same splitters the corpus test uses (formatter_test.go).
func collectSSE(t *testing.T, s string) []parsedSSE {
	t.Helper()
	raw := splitSSEChunks(s)
	out := make([]parsedSSE, 0, len(raw))
	for _, chunk := range raw {
		event, data := splitEventLine(t, chunk)
		var m map[string]any
		if err := json.Unmarshal([]byte(data), &m); err != nil {
			t.Fatalf("decoding chunk data %q: %v", data, err)
		}
		out = append(out, parsedSSE{event: event, data: m})
	}
	return out
}

// wantEvent asserts chunk i exists and carries the given event name, returning
// its decoded data object.
func wantEvent(t *testing.T, chunks []parsedSSE, i int, event string) map[string]any {
	t.Helper()
	if i >= len(chunks) {
		t.Fatalf("expected chunk %d (%s) but stream has only %d chunks", i, event, len(chunks))
	}
	if chunks[i].event != event {
		t.Fatalf("chunk %d: got event %q, want %q", i, chunks[i].event, event)
	}
	return chunks[i].data
}

func fieldStr(t *testing.T, m map[string]any, key string) string {
	t.Helper()
	v, ok := m[key].(string)
	if !ok {
		t.Fatalf("field %q is not a string in %v", key, m)
	}
	return v
}

func fieldInt(t *testing.T, m map[string]any, key string) int {
	t.Helper()
	v, ok := m[key].(float64)
	if !ok {
		t.Fatalf("field %q is not a number in %v", key, m)
	}
	return int(v)
}

func fieldObj(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("field %q is not an object in %v", key, m)
	}
	return v
}

// assertBlockStart checks a content_block_start chunk: its index, and the
// nested content_block's type.
func assertBlockStart(t *testing.T, chunks []parsedSSE, i, index int, blockType string) map[string]any {
	t.Helper()
	data := wantEvent(t, chunks, i, "content_block_start")
	if got := fieldInt(t, data, "index"); got != index {
		t.Errorf("chunk %d: content_block_start index = %d, want %d", i, got, index)
	}
	cb := fieldObj(t, data, "content_block")
	if got := fieldStr(t, cb, "type"); got != blockType {
		t.Errorf("chunk %d: content_block.type = %q, want %q", i, got, blockType)
	}
	return cb
}

// assertDelta checks a content_block_delta chunk: its index and delta type,
// returning the nested delta object for further field checks.
func assertDelta(t *testing.T, chunks []parsedSSE, i, index int, deltaType string) map[string]any {
	t.Helper()
	data := wantEvent(t, chunks, i, "content_block_delta")
	if got := fieldInt(t, data, "index"); got != index {
		t.Errorf("chunk %d: content_block_delta index = %d, want %d", i, got, index)
	}
	delta := fieldObj(t, data, "delta")
	if got := fieldStr(t, delta, "type"); got != deltaType {
		t.Errorf("chunk %d: delta.type = %q, want %q", i, got, deltaType)
	}
	return delta
}

// assertBlockStop checks a content_block_stop chunk's index.
func assertBlockStop(t *testing.T, chunks []parsedSSE, i, index int) {
	t.Helper()
	data := wantEvent(t, chunks, i, "content_block_stop")
	if got := fieldInt(t, data, "index"); got != index {
		t.Errorf("chunk %d: content_block_stop index = %d, want %d", i, got, index)
	}
}

// assertStopReason checks the terminal message_delta / message_stop pair.
func assertStopReason(t *testing.T, chunks []parsedSSE, i int, stopReason string) {
	t.Helper()
	data := wantEvent(t, chunks, i, "message_delta")
	delta := fieldObj(t, data, "delta")
	if got := fieldStr(t, delta, "stop_reason"); got != stopReason {
		t.Errorf("chunk %d: message_delta stop_reason = %q, want %q", i, got, stopReason)
	}
	wantEvent(t, chunks, i+1, "message_stop")
}

func thinkingEvent(text string) streamingcore.KiroEvent {
	return streamingcore.KiroEvent{Kind: "thinking", Thinking: text}
}

func contentEvent(text string) streamingcore.KiroEvent {
	return streamingcore.KiroEvent{Kind: "content", Content: text}
}

func contextUsageEvent(pct float64) streamingcore.KiroEvent {
	return streamingcore.KiroEvent{Kind: "context_usage", ContextUsage: pct}
}

func toolUseEvent(id, name string, input map[string]any) streamingcore.KiroEvent {
	return streamingcore.KiroEvent{
		Kind:    "tool_use",
		ToolUse: &streamingcore.ToolUseData{ID: id, Name: name, Input: input},
	}
}

// drive runs message_start, then each event through Handle, then Finish
// (unless finish is false, for the error path), returning decoded chunks.
func drive(t *testing.T, f *Formatter, finish bool, events ...streamingcore.KiroEvent) []parsedSSE {
	t.Helper()
	var buf bytes.Buffer
	if err := f.EmitMessageStart(&buf); err != nil {
		t.Fatalf("EmitMessageStart: %v", err)
	}
	for i, ev := range events {
		if err := f.Handle(ev, &buf); err != nil {
			t.Fatalf("Handle event %d: %v", i, err)
		}
	}
	if finish {
		if err := f.Finish(&buf); err != nil {
			t.Fatalf("Finish: %v", err)
		}
	}
	return collectSSE(t, buf.String())
}

// TestThinkingThenText: a thinking block (index 0) is interrupted by content,
// which closes it and opens a text block (index 1). A context_usage event
// keeps the stop_reason at end_turn (no truncation).
func TestThinkingThenText(t *testing.T) {
	f := New("claude-test", AsReasoningContent)
	chunks := drive(t, f, true,
		thinkingEvent("razonando"),
		contentEvent("Hola"),
		contextUsageEvent(3.5),
	)

	wantEvent(t, chunks, 0, "message_start")
	cb := assertBlockStart(t, chunks, 1, 0, "thinking")
	if sig := fieldStr(t, cb, "signature"); sig == "" {
		t.Error("chunk 1: thinking content_block.signature is empty")
	}
	if delta := assertDelta(t, chunks, 2, 0, "thinking_delta"); fieldStr(t, delta, "thinking") != "razonando" {
		t.Errorf("chunk 2: thinking_delta text = %q, want %q", fieldStr(t, delta, "thinking"), "razonando")
	}
	assertBlockStop(t, chunks, 3, 0)
	assertBlockStart(t, chunks, 4, 1, "text")
	if delta := assertDelta(t, chunks, 5, 1, "text_delta"); fieldStr(t, delta, "text") != "Hola" {
		t.Errorf("chunk 5: text_delta text = %q, want %q", fieldStr(t, delta, "text"), "Hola")
	}
	assertBlockStop(t, chunks, 6, 1)
	assertStopReason(t, chunks, 7, "end_turn")
	if len(chunks) != 9 {
		t.Errorf("chunk count = %d, want 9", len(chunks))
	}
}

// TestThinkingThenToolUse: a thinking block (index 0) is closed by a tool_use
// event, which reserves and emits a tool_use block (index 1). stop_reason is
// tool_use.
func TestThinkingThenToolUse(t *testing.T) {
	f := New("claude-test", AsReasoningContent)
	chunks := drive(t, f, true,
		thinkingEvent("pienso"),
		toolUseEvent("toolu_fixed01", "get_weather", map[string]any{"city": "Bogota"}),
	)

	wantEvent(t, chunks, 0, "message_start")
	assertBlockStart(t, chunks, 1, 0, "thinking")
	assertDelta(t, chunks, 2, 0, "thinking_delta")
	assertBlockStop(t, chunks, 3, 0)

	cb := assertBlockStart(t, chunks, 4, 1, "tool_use")
	if got := fieldStr(t, cb, "id"); got != "toolu_fixed01" {
		t.Errorf("chunk 4: tool_use id = %q, want %q", got, "toolu_fixed01")
	}
	if got := fieldStr(t, cb, "name"); got != "get_weather" {
		t.Errorf("chunk 4: tool_use name = %q, want %q", got, "get_weather")
	}

	delta := assertDelta(t, chunks, 5, 1, "input_json_delta")
	assertPartialJSON(t, delta, map[string]any{"city": "Bogota"})
	assertBlockStop(t, chunks, 6, 1)
	assertStopReason(t, chunks, 7, "tool_use")
	if len(chunks) != 9 {
		t.Errorf("chunk count = %d, want 9", len(chunks))
	}
}

// TestTextThenToolUse: a text block (index 0) is closed by a tool_use event
// (index 1). No thinking block ever opens, so text takes index 0.
func TestTextThenToolUse(t *testing.T) {
	f := New("claude-test", AsReasoningContent)
	chunks := drive(t, f, true,
		contentEvent("Hi"),
		toolUseEvent("", "lookup", map[string]any{}),
	)

	wantEvent(t, chunks, 0, "message_start")
	assertBlockStart(t, chunks, 1, 0, "text")
	if delta := assertDelta(t, chunks, 2, 0, "text_delta"); fieldStr(t, delta, "text") != "Hi" {
		t.Errorf("chunk 2: text_delta text = %q, want %q", fieldStr(t, delta, "text"), "Hi")
	}
	assertBlockStop(t, chunks, 3, 0)

	cb := assertBlockStart(t, chunks, 4, 1, "tool_use")
	// Empty id → generated toolu_<hex> fallback (streaming_anthropic.py:346).
	if got := fieldStr(t, cb, "id"); len(got) == 0 {
		t.Error("chunk 4: tool_use id should be generated, got empty")
	}
	delta := assertDelta(t, chunks, 5, 1, "input_json_delta")
	assertPartialJSON(t, delta, map[string]any{})
	assertBlockStop(t, chunks, 6, 1)
	assertStopReason(t, chunks, 7, "tool_use")
	if len(chunks) != 9 {
		t.Errorf("chunk count = %d, want 9", len(chunks))
	}
}

// TestErrorMidStream: after some content, an exception mid-stream emits the
// error event and stops — no Finish, so no message_delta / message_stop, and
// the open text block is deliberately left unclosed (streaming_anthropic.py:700-712).
func TestErrorMidStream(t *testing.T) {
	f := New("claude-test", AsReasoningContent)
	var buf bytes.Buffer
	if err := f.EmitMessageStart(&buf); err != nil {
		t.Fatalf("EmitMessageStart: %v", err)
	}
	if err := f.Handle(contentEvent("partial answer"), &buf); err != nil {
		t.Fatalf("Handle content: %v", err)
	}
	if err := f.EmitError(&buf, "kaboom"); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	chunks := collectSSE(t, buf.String())

	wantEvent(t, chunks, 0, "message_start")
	assertBlockStart(t, chunks, 1, 0, "text")
	assertDelta(t, chunks, 2, 0, "text_delta")

	data := wantEvent(t, chunks, 3, "error")
	errObj := fieldObj(t, data, "error")
	if got := fieldStr(t, errObj, "type"); got != "api_error" {
		t.Errorf("error.type = %q, want %q", got, "api_error")
	}
	if got := fieldStr(t, errObj, "message"); got != "Internal error: kaboom" {
		t.Errorf("error.message = %q, want %q", got, "Internal error: kaboom")
	}

	if len(chunks) != 4 {
		t.Errorf("chunk count = %d, want 4 (no message_stop after an error)", len(chunks))
	}
	for _, c := range chunks {
		if c.event == "message_stop" || c.event == "message_delta" {
			t.Errorf("unexpected %q after an error mid-stream", c.event)
		}
	}
}

// TestThinkingIncludeAsText: in IncludeAsText mode, thinking content is folded
// into a text block (no thinking block ever opens), and following content
// appends to the same text block (streaming_anthropic.py:291-323).
func TestThinkingIncludeAsText(t *testing.T) {
	f := New("claude-test", IncludeAsText)
	chunks := drive(t, f, true,
		thinkingEvent("razon"),
		contentEvent("resp"),
		contextUsageEvent(2.0),
	)

	wantEvent(t, chunks, 0, "message_start")
	assertBlockStart(t, chunks, 1, 0, "text")
	if delta := assertDelta(t, chunks, 2, 0, "text_delta"); fieldStr(t, delta, "text") != "razon" {
		t.Errorf("chunk 2: text_delta text = %q, want %q", fieldStr(t, delta, "text"), "razon")
	}
	if delta := assertDelta(t, chunks, 3, 0, "text_delta"); fieldStr(t, delta, "text") != "resp" {
		t.Errorf("chunk 3: text_delta text = %q, want %q", fieldStr(t, delta, "text"), "resp")
	}
	assertBlockStop(t, chunks, 4, 0)
	assertStopReason(t, chunks, 5, "end_turn")
	if len(chunks) != 7 {
		t.Errorf("chunk count = %d, want 7", len(chunks))
	}
	for _, c := range chunks {
		if c.event == "content_block_start" {
			if fieldStr(t, fieldObj(t, c.data, "content_block"), "type") == "thinking" {
				t.Error("IncludeAsText should never open a thinking block")
			}
		}
	}
}

// TestThinkingStrip: in Strip mode, thinking content produces no SSE output at
// all, though it still counts toward output_tokens at Finish
// (streaming_anthropic.py:324,625). Only the text block is emitted.
func TestThinkingStrip(t *testing.T) {
	f := New("claude-test", Strip)
	chunks := drive(t, f, true,
		thinkingEvent("secreto"),
		contentEvent("visible"),
		contextUsageEvent(1.0),
	)

	wantEvent(t, chunks, 0, "message_start")
	assertBlockStart(t, chunks, 1, 0, "text")
	if delta := assertDelta(t, chunks, 2, 0, "text_delta"); fieldStr(t, delta, "text") != "visible" {
		t.Errorf("chunk 2: text_delta text = %q, want %q", fieldStr(t, delta, "text"), "visible")
	}
	assertBlockStop(t, chunks, 3, 0)
	assertStopReason(t, chunks, 4, "end_turn")
	if len(chunks) != 6 {
		t.Errorf("chunk count = %d, want 6", len(chunks))
	}
	for _, c := range chunks {
		if c.event == "content_block_delta" && fieldStr(t, fieldObj(t, c.data, "delta"), "type") == "thinking_delta" {
			t.Error("Strip mode should emit no thinking_delta")
		}
	}
}

// assertPartialJSON decodes a delta's partial_json string field and compares
// it to want, so pyjson's exact spacing (already covered byte-for-byte by
// TestFormatSSEEventCorpus) doesn't make these transition tests brittle.
func assertPartialJSON(t *testing.T, delta map[string]any, want map[string]any) {
	t.Helper()
	pj := fieldStr(t, delta, "partial_json")
	var got map[string]any
	if err := json.Unmarshal([]byte(pj), &got); err != nil {
		t.Fatalf("partial_json %q is not valid JSON: %v", pj, err)
	}
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("partial_json = %v, want %v", got, want)
	}
}
