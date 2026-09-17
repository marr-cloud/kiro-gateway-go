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
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationrecovery"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// --- Task 8a: inyección de recuperación de truncación (routes_anthropic.py:156-244) ---
//
// Fix round 1 (revisión post-8a): el lado READ NO lee
// TruncationRecoveryEnabled/ShouldInjectRecovery — el original
// (routes_anthropic.py:156-244) llama a get_tool_truncation/
// get_content_truncation INCONDICIONALMENTE en cada petición; el gate
// should_inject_recovery() solo existe del lado SAVE
// (streaming_anthropic.py:666-669, Task 8b). Por eso estos tests NO tocan
// converterscore.TruncationRecoveryEnabled: la propiedad observable de este
// paquete en aislamiento es "cache vacía → sin cambios" (Escenario 3), no
// "flag apagado → sin cambios" — ese último caso end-to-end (flag
// apagado ⇒ SAVE no guarda nada ⇒ READ de la siguiente petición no
// encuentra nada) pertenece al test 8a+8b, no a este paquete.

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

// --- Escenario 3: cache vacía (sin records guardados) → no hay inyección ---
//
// Propiedad honesta del lado READ en aislamiento: injectTruncationRecovery
// corre siempre (fix round 1, ver el comentario de cabecera de este
// fichero), pero una cache SIN ningún record para el tool_use_id de la
// petición simplemente no encuentra nada que inyectar — GetTool devuelve
// ok=false y el bloque tool_result pasa sin modificar. El efecto real de
// TRUNCATION_RECOVERY=false (nada se guarda del lado SAVE, así que la
// SIGUIENTE petición ve exactamente esta misma cache vacía) es una
// propiedad del gate de Task 8b, no de este paquete.
func TestMessages_TruncationRecovery_EmptyCacheDoesNotInject(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = readAllBody(r)
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("ok"))
	}))
	defer server.Close()

	ts := truncationstate.New() // vacía a propósito: ningún SetTool/SetContent previo.

	h := New(manager, mustHTTPClient(t, cfg), cfg, ts)
	h.apiURL = func(acc *accountmanager.Account) string { return server.URL + "/generateAssistantResponse" }

	req := newMessagesRequestWithToolResult(t, "call_xyz", "original tool output")
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	// "[API Limitation]" por sí solo NO sirve de marcador: el system prompt
	// base (converterscore, gateado por su propio TruncationRecoveryEnabled,
	// Task 7 — sin relación con este fix) SIEMPRE menciona ese tag como
	// ejemplo ("indicates a tool call result was truncated"). El marcador
	// inequívoco de una inyección real es la frase completa del contenido
	// sintético (truncationrecovery.GenerateTruncationToolResult).
	const syntheticMarker = "Your tool call was truncated by the upstream API due to output size limits"
	if strings.Contains(string(gotBody), syntheticMarker) {
		t.Errorf("Kiro payload contains the synthetic truncation notice despite an empty cache; body=%s", gotBody)
	}
	if !strings.Contains(string(gotBody), "original tool output") {
		t.Errorf("Kiro payload does not contain the original tool_result content unmodified; body=%s", gotBody)
	}
}
