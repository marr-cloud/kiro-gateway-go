// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// TestTruncationRecoverySaveHook drives serveStreaming with a truncated tool
// call and verifies the save hook persists it. The hook collects
// formatter.TruncatedTools() and calls SetTool, exercising the real wiring.
func TestTruncationRecoverySaveHook(t *testing.T) {
	origEnabled := converterscore.TruncationRecoveryEnabled
	t.Cleanup(func() { converterscore.TruncationRecoveryEnabled = origEnabled })
	converterscore.TruncationRecoveryEnabled = true

	state := truncationstate.New()
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-hook-test"})
	h := newTestHandler(t, manager, cfg, "http://example.com")
	h.truncation = state

	// Construct a Kiro response with truncated tool (incomplete JSON args).
	kiroBody := kiroTruncatedToolBody("tc_trunc_001", "search")
	fakeResp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(kiroBody)),
		Header:     make(http.Header),
	}

	req := newMessagesRequest(t, "claude-sonnet-4", true)
	recorder := httptest.NewRecorder()

	// Call serveStreaming, driving the real save hook path: pipeline →
	// formatter.TruncatedTools collection → save hook calls SetTool.
	h.serveStreaming(recorder, mustParseMessagesRequest(t, req), fakeResp)

	// Verify persistence via the save hook (not direct SetTool call)
	retrieved, ok := state.GetTool("tc_trunc_001")
	if !ok {
		t.Fatalf("truncated tool not persisted (save hook failed)")
	}

	record, ok := retrieved.(truncationstate.ToolRecord)
	if !ok {
		t.Fatalf("not a ToolRecord")
	}

	if record.ToolName != "search" {
		t.Errorf("ToolName: got %q, want search", record.ToolName)
	}
	if record.TruncationInfo == nil || record.TruncationInfo["reason"] == "" {
		t.Errorf("TruncationInfo missing reason")
	}
}

// TestTruncationRecoverySaveHookGate verifies the save hook gate: with
// TruncationRecoveryEnabled=false, truncated tools must NOT persist.
func TestTruncationRecoverySaveHookGate(t *testing.T) {
	origEnabled := converterscore.TruncationRecoveryEnabled
	t.Cleanup(func() { converterscore.TruncationRecoveryEnabled = origEnabled })
	converterscore.TruncationRecoveryEnabled = false

	state := truncationstate.New()
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-gate-test"})
	h := newTestHandler(t, manager, cfg, "http://example.com")
	h.truncation = state

	kiroBody := kiroTruncatedToolBody("tc_gate_001", "search")
	fakeResp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(kiroBody)),
		Header:     make(http.Header),
	}

	req := newMessagesRequest(t, "claude-sonnet-4", true)
	recorder := httptest.NewRecorder()
	h.serveStreaming(recorder, mustParseMessagesRequest(t, req), fakeResp)

	// Verify the gate blocks persistence
	_, ok := state.GetTool("tc_gate_001")
	if ok {
		t.Fatalf("tool persisted despite TruncationRecoveryEnabled=false")
	}
}

// kiroTruncatedToolBody constructs Kiro response with truncated tool call.
// Uses parser's event prefixes: {"name": ... for tool_start, {"input": ...
// for tool_input. The input is incomplete JSON, triggering truncation detection.
func kiroTruncatedToolBody(toolID, toolName string) []byte {
	var buf bytes.Buffer

	// Tool start event: {"name": ...}
	event1 := map[string]any{
		"toolUseId": toolID,
		"name":      toolName,
	}
	buf.Write(jsonLine(event1))

	// Tool input event with incomplete/truncated JSON: {"input": ...}
	// The JSON is deliberately incomplete (missing closing brace) to trigger
	// parser.diagnoseJSONTruncation → truncation detection.
	event2 := map[string]any{
		"input": `{"query": "test"`, // Missing closing brace
	}
	buf.Write(jsonLine(event2))

	// Tool stop event to finalize the tool call
	event3 := map[string]any{
		"stop": true,
	}
	buf.Write(jsonLine(event3))

	return buf.Bytes()
}

func jsonLine(v any) []byte {
	b, _ := json.Marshal(v)
	return append(b, '\n')
}

func mustParseMessagesRequest(t *testing.T, req *http.Request) *modelsanthropic.AnthropicMessagesRequest {
	var payload modelsanthropic.AnthropicMessagesRequest
	if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
		t.Fatalf("parse request: %v", err)
	}
	return &payload
}
