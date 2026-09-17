// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/accountmanager"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
)

// --- Helpers específicos de web_search ---

// newWebSearchToolsRequest construye una *http.Request para POST /v1/messages
// con una tool nativa server-side (p.ej. "web_search_20250305"), que
// hasNativeWebSearchTool debe reconocer (Path A).
func newWebSearchToolsRequest(t *testing.T, stream bool) *http.Request {
	t.Helper()
	payload := map[string]any{
		"model":      "claude-sonnet-4",
		"max_tokens": 100,
		"stream":     stream,
		"messages": []map[string]any{
			{"role": "user", "content": "golang tutorials"},
		},
		"tools": []map[string]any{
			{"type": "web_search_20250305", "name": "web_search"},
		},
	}
	b, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	return req
}

// fakeMCPServer levanta un httptest.Server que responde a POST /mcp con una
// respuesta JSON-RPC 2.0 mínima pero válida: result.content[0].text es la
// cadena JSON con la clave "results" (doble deserialización, ver el
// comentario de cabecera de mcptools/client.go).
func fakeMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	inner, err := json.Marshal(map[string]any{"results": []any{}})
	if err != nil {
		t.Fatalf("marshal inner MCP result: %v", err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/mcp" {
			t.Errorf("unexpected MCP path: %s", r.URL.Path)
		}
		resp := map[string]any{
			"result": map[string]any{
				"content": []map[string]any{{"text": string(inner)}},
				"isError": false,
			},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
}

// --- Escenario 1: Path A — tool nativa web_search_20250305 → SSE, sin failover ---

func TestMessages_PathA_NativeWebSearchTool_ReturnsSSE(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	// El backend normal de Kiro NUNCA debe recibir tráfico: Path A es un
	// early return que bypassa el bucle de failover por completo
	// (routes_anthropic.py:282-310).
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to the normal Kiro backend: %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	mcp := fakeMCPServer(t)
	defer mcp.Close()

	h := newTestHandler(t, manager, cfg, backend.URL)
	h.mcpHost = func(acc *accountmanager.Account) string { return mcp.URL }

	req := newWebSearchToolsRequest(t, true)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/event-stream; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/event-stream; charset=utf-8", ct)
	}

	datas := splitSSEData(rec.Body.Bytes())
	if len(datas) == 0 {
		t.Fatalf("no SSE events produced; body=%s", rec.Body.String())
	}
	var first, last sseEventIn
	if err := json.Unmarshal(datas[0], &first); err != nil {
		t.Fatalf("decode first event: %v", err)
	}
	if err := json.Unmarshal(datas[len(datas)-1], &last); err != nil {
		t.Fatalf("decode last event: %v", err)
	}
	if first.Type != "message_start" {
		t.Errorf("first event = %q, want message_start", first.Type)
	}
	if last.Type != "message_stop" {
		t.Errorf("last event = %q, want message_stop", last.Type)
	}

	sawServerToolUse := false
	for _, data := range datas {
		if bytes.Contains(data, []byte(`"server_tool_use"`)) {
			sawServerToolUse = true
		}
	}
	if !sawServerToolUse {
		t.Error("no server_tool_use content_block emitted; want the web_search emulation block")
	}
}

// --- Escenario 1b: Path A no-streaming — respuesta JSON única ---

func TestMessages_PathA_NativeWebSearchTool_NonStreamingJSON(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request to the normal Kiro backend: %s", r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer backend.Close()

	mcp := fakeMCPServer(t)
	defer mcp.Close()

	h := newTestHandler(t, manager, cfg, backend.URL)
	h.mcpHost = func(acc *accountmanager.Account) string { return mcp.URL }

	req := newWebSearchToolsRequest(t, false)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var resp nonStreamResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if resp.Type != "message" {
		t.Errorf("type = %q, want message", resp.Type)
	}
}

// --- Escenario 1c: Path A sin cuentas inicializadas → 503 dialecto Anthropic ---

func TestMessages_PathA_NoAccounts_Returns503(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, nil)

	h := New(manager, mustHTTPClient(t, cfg), cfg)

	req := newWebSearchToolsRequest(t, false)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	var errResp anthropicError
	if err := json.Unmarshal(rec.Body.Bytes(), &errResp); err != nil {
		t.Fatalf("decode error: %v; body=%s", err, rec.Body.String())
	}
	if errResp.Error.Message != "No initialized accounts available" {
		t.Errorf("error.message = %q, want %q", errResp.Error.Message, "No initialized accounts available")
	}
}

// --- Escenario 2: Path B — WEB_SEARCH_ENABLED=true + sin tool nativa → tool inyectada ---

func TestMessages_PathB_InjectsToolWhenEnabled(t *testing.T) {
	cfg := testConfig()
	cfg.WebSearchEnabled = true
	manager := newTestManager(t, cfg, []string{"tok-only"})

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = readAllBody(r)
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("Hello"))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newMessagesRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (Path B injects and continues to normal failover); body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(gotBody), `"web_search"`) {
		t.Errorf("Kiro payload does not mention web_search; Path B tool was not injected/forwarded. body=%s", gotBody)
	}
}

// TestInjectWebSearchTool_Direct verifica injectWebSearchTool en aislamiento:
// literal exacto de descripción/schema (routes_anthropic.py:268-277) y
// dedupe por nombre (routes_anthropic.py:261-266).
func TestInjectWebSearchTool_Direct(t *testing.T) {
	cfg := testConfig()
	cfg.WebSearchEnabled = true
	h := &Handler{cfg: cfg}

	req := &modelsanthropic.AnthropicMessagesRequest{}
	h.injectWebSearchTool(req)

	if len(req.Tools) != 1 {
		t.Fatalf("Tools = %d, want 1", len(req.Tools))
	}
	got := req.Tools[0]
	if got.Name != "web_search" {
		t.Errorf("Name = %q, want web_search", got.Name)
	}
	if got.Description == nil || *got.Description != webSearchToolDescription {
		t.Errorf("Description = %v, want %q", got.Description, webSearchToolDescription)
	}
	if string(got.InputSchema) != string(webSearchToolInputSchema) {
		t.Errorf("InputSchema = %s, want %s", got.InputSchema, webSearchToolInputSchema)
	}

	// Segunda llamada: ya existe una tool "web_search" por nombre → no duplica.
	h.injectWebSearchTool(req)
	if len(req.Tools) != 1 {
		t.Errorf("Tools = %d after second call, want 1 (no dedupe)", len(req.Tools))
	}
}

// --- Escenario 3: WEB_SEARCH_ENABLED=false + sin tool nativa → comportamiento normal ---

func TestMessages_WebSearchDisabled_NormalBehavior(t *testing.T) {
	cfg := testConfig()
	cfg.WebSearchEnabled = false
	manager := newTestManager(t, cfg, []string{"tok-only"})

	var gotBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = readAllBody(r)
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("Hello"))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)

	req := newMessagesRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.Messages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(string(gotBody), `"web_search"`) {
		t.Errorf("Kiro payload mentions web_search with WEB_SEARCH_ENABLED=false; Path B should not fire. body=%s", gotBody)
	}
}

func readAllBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}
