// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationrecovery"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// --- Task 8a: inyección de recuperación de truncación (routes_openai.py:185-234) ---

// withTruncationRecoveryEnabled fija converterscore.TruncationRecoveryEnabled
// para la duración del test y lo restaura al terminar — mismo patrón que
// payload_test.go/thinking_test.go (converterscore).
func withTruncationRecoveryEnabled(t *testing.T, enabled bool) {
	t.Helper()
	orig := converterscore.TruncationRecoveryEnabled
	converterscore.TruncationRecoveryEnabled = enabled
	t.Cleanup(func() { converterscore.TruncationRecoveryEnabled = orig })
}

// newChatRequestWithToolMessage construye una petición /v1/chat/completions
// con un mensaje role="tool" cuyo tool_call_id y content son los dados.
func newChatRequestWithToolMessage(t *testing.T, toolCallID, content string) *http.Request {
	t.Helper()
	payload := modelsopenai.ChatCompletionRequest{
		Model:  "claude-sonnet-4",
		Stream: false,
		Messages: []modelsopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"call the tool"`)},
			{Role: "tool", Content: mustMarshalRaw(t, content), ToolCallID: &toolCallID},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// newChatRequestWithAssistantText construye una petición /v1/chat/completions
// con un turno user + un mensaje assistant cuyo content es el texto dado.
func newChatRequestWithAssistantText(t *testing.T, assistantText string) *http.Request {
	t.Helper()
	payload := modelsopenai.ChatCompletionRequest{
		Model:  "claude-sonnet-4",
		Stream: false,
		Messages: []modelsopenai.ChatMessage{
			{Role: "user", Content: json.RawMessage(`"continue"`)},
			{Role: "assistant", Content: mustMarshalRaw(t, assistantText)},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func mustMarshalRaw(t *testing.T, s string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal raw content: %v", err)
	}
	return json.RawMessage(b)
}

// jsonEscapedSubstring devuelve la representación JSON de s SIN las comillas
// envolventes, para comparar contra un payload JSON-serializado que contiene
// s como valor de una cadena (los saltos de línea de s van escapados como
// `\n`, no como bytes 0x0A crudos).
func jsonEscapedSubstring(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return strings.Trim(string(b), `"`)
}

// --- Escenario 1: mensaje tool con match en el estado → contenido con aviso PREPENDIDO ---

func TestChatCompletions_TruncationRecovery_InjectsToolResultNotice(t *testing.T) {
	withTruncationRecoveryEnabled(t, true)

	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = readAllBody(r)
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("ok"))
	}))
	defer server.Close()

	ts := truncationstate.New()
	ts.SetTool("call_abc123", truncationstate.ToolRecord{
		ToolName:       "Write",
		TruncationInfo: map[string]any{"size_bytes": float64(5000), "reason": "missing 2 closing braces"},
	})

	h := New(manager, mustHTTPClient(t, cfg), cfg, ts)
	h.apiURL = func(acc *accountmanager.Account) string { return server.URL + "/generateAssistantResponse" }

	req := newChatRequestWithToolMessage(t, "call_abc123", "original tool output")
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	synthetic := truncationrecovery.GenerateTruncationToolResult("Write", "call_abc123", map[string]any{"size_bytes": float64(5000), "reason": "missing 2 closing braces"})
	wantContent := synthetic["content"].(string) + "\n\n---\n\nOriginal tool result:\noriginal tool output"
	wantEscaped := jsonEscapedSubstring(t, wantContent)

	if !strings.Contains(string(gotBody), wantEscaped) {
		t.Errorf("Kiro payload does not contain the expected prepended tool_result content.\nwant substring (JSON-escaped): %s\ngot payload: %s", wantEscaped, gotBody)
	}

	if _, ok := ts.GetTool("call_abc123"); ok {
		t.Error("record still present after inject; GetTool inside injectTruncationRecovery should have consumed it")
	}
}

// --- Escenario 2: assistant con content truncado → mensaje user sintético inyectado DESPUÉS ---

func TestChatCompletions_TruncationRecovery_InjectsUserNoticeAfterAssistant(t *testing.T) {
	withTruncationRecoveryEnabled(t, true)

	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = readAllBody(r)
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("ok"))
	}))
	defer server.Close()

	const truncatedText = "This response got cut off mid-sent"

	ts := truncationstate.New()
	ts.SetContent(truncatedText, truncationstate.ContentRecord{MessageHash: "deadbeef01234567"})

	h := New(manager, mustHTTPClient(t, cfg), cfg, ts)
	h.apiURL = func(acc *accountmanager.Account) string { return server.URL + "/generateAssistantResponse" }

	req := newChatRequestWithAssistantText(t, truncatedText)
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	wantNotice := truncationrecovery.GenerateTruncationUserMessage()
	wantEscaped := jsonEscapedSubstring(t, wantNotice)
	if !strings.Contains(string(gotBody), wantEscaped) {
		t.Errorf("Kiro payload does not contain the synthetic recovery notice.\nwant substring (JSON-escaped): %s\ngot payload: %s", wantEscaped, gotBody)
	}

	if _, ok := ts.GetContent(truncatedText); ok {
		t.Error("record still present after inject; GetContent inside injectTruncationRecovery should have consumed it")
	}
}

// --- Escenario 3: TruncationRecoveryEnabled=false → no hay inyección, aunque haya match ---

func TestChatCompletions_TruncationRecovery_DisabledDoesNotInject(t *testing.T) {
	withTruncationRecoveryEnabled(t, false)

	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = readAllBody(r)
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("ok"))
	}))
	defer server.Close()

	ts := truncationstate.New()
	ts.SetTool("call_xyz", truncationstate.ToolRecord{
		ToolName:       "Write",
		TruncationInfo: map[string]any{"size_bytes": float64(1000), "reason": "test"},
	})

	h := New(manager, mustHTTPClient(t, cfg), cfg, ts)
	h.apiURL = func(acc *accountmanager.Account) string { return server.URL + "/generateAssistantResponse" }

	req := newChatRequestWithToolMessage(t, "call_xyz", "original tool output")
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(string(gotBody), "[API Limitation]") {
		t.Errorf("Kiro payload contains the truncation notice with TruncationRecoveryEnabled=false; body=%s", gotBody)
	}
	if _, ok := ts.GetTool("call_xyz"); !ok {
		t.Error("record was consumed even though TruncationRecoveryEnabled=false; ShouldInjectRecovery should gate the whole read")
	}
}
