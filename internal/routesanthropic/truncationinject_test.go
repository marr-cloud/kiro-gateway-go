// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationrecovery"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// --- Task 8a: inyección de recuperación de truncación (routes_anthropic.py:156-244) ---

// withTruncationRecoveryEnabled fija converterscore.TruncationRecoveryEnabled
// para la duración del test y lo restaura al terminar — mismo patrón que
// payload_test.go/thinking_test.go (converterscore).
func withTruncationRecoveryEnabled(t *testing.T, enabled bool) {
	t.Helper()
	orig := converterscore.TruncationRecoveryEnabled
	converterscore.TruncationRecoveryEnabled = enabled
	t.Cleanup(func() { converterscore.TruncationRecoveryEnabled = orig })
}

// newMessagesRequestWithToolResult construye una petición /v1/messages cuyo
// único mensaje user trae un bloque tool_result con el tool_use_id dado.
func newMessagesRequestWithToolResult(t *testing.T, toolUseID, content string) *http.Request {
	t.Helper()
	payload := map[string]any{
		"model":      "claude-sonnet-4",
		"max_tokens": 100,
		"stream":     false,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "tool_result", "tool_use_id": toolUseID, "content": content},
				},
			},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// newMessagesRequestWithAssistantText construye una petición /v1/messages con
// un turno user + un mensaje assistant cuyo content es el texto dado.
func newMessagesRequestWithAssistantText(t *testing.T, assistantText string) *http.Request {
	t.Helper()
	payload := map[string]any{
		"model":      "claude-sonnet-4",
		"max_tokens": 100,
		"stream":     false,
		"messages": []map[string]any{
			{"role": "user", "content": "continue"},
			{"role": "assistant", "content": assistantText},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// --- Escenario 1: tool_result con match en el estado → contenido con aviso PREPENDIDO ---

func TestMessages_TruncationRecovery_InjectsToolResultNotice(t *testing.T) {
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

	req := newMessagesRequestWithToolResult(t, "call_abc123", "original tool output")
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	synthetic := truncationrecovery.GenerateTruncationToolResult("Write", "call_abc123", map[string]any{"size_bytes": float64(5000), "reason": "missing 2 closing braces"})
	wantContent := synthetic["content"].(string) + "\n\n---\n\nOriginal tool result:\noriginal tool output"

	// gotBody es JSON: los saltos de línea de wantContent van escapados como
	// `\n` dentro de la cadena JSON, no como bytes 0x0A crudos — se compara
	// contra la forma JSON-serializada (sin las comillas envolventes), no
	// contra wantContent tal cual.
	wantContentJSON, err := json.Marshal(wantContent)
	if err != nil {
		t.Fatalf("marshal wantContent: %v", err)
	}
	wantContentEscaped := strings.Trim(string(wantContentJSON), `"`)

	if !strings.Contains(string(gotBody), wantContentEscaped) {
		t.Errorf("Kiro payload does not contain the expected prepended tool_result content.\nwant substring (JSON-escaped): %s\ngot payload: %s", wantContentEscaped, gotBody)
	}

	// GetTool es destructivo: una segunda petición con el mismo tool_use_id
	// ya no debe encontrar nada que inyectar.
	if _, ok := ts.GetTool("call_abc123"); ok {
		t.Error("record still present after inject; GetTool inside injectTruncationRecovery should have consumed it")
	}
}

// --- Escenario 2: assistant con content truncado → mensaje user sintético inyectado DESPUÉS ---

func TestMessages_TruncationRecovery_InjectsUserNoticeAfterAssistant(t *testing.T) {
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

	req := newMessagesRequestWithAssistantText(t, truncatedText)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	wantNotice := truncationrecovery.GenerateTruncationUserMessage()
	if !strings.Contains(string(gotBody), wantNotice) {
		t.Errorf("Kiro payload does not contain the synthetic recovery notice.\nwant substring: %s\ngot payload: %s", wantNotice, gotBody)
	}

	if _, ok := ts.GetContent(truncatedText); ok {
		t.Error("record still present after inject; GetContent inside injectTruncationRecovery should have consumed it")
	}
}

// --- Escenario 3: TruncationRecoveryEnabled=false → no hay inyección, aunque haya match ---

func TestMessages_TruncationRecovery_DisabledDoesNotInject(t *testing.T) {
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

	req := newMessagesRequestWithToolResult(t, "call_xyz", "original tool output")
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(string(gotBody), "[API Limitation]") {
		t.Errorf("Kiro payload contains the truncation notice with TruncationRecoveryEnabled=false; body=%s", gotBody)
	}
	// La record NO debe consumirse cuando el gate está apagado (routes_anthropic.py
	// nunca llama a get_tool_truncation/get_content_truncation si should_inject_recovery() es false).
	if _, ok := ts.GetTool("call_xyz"); !ok {
		t.Error("record was consumed even though TruncationRecoveryEnabled=false; ShouldInjectRecovery should gate the whole read")
	}
}
