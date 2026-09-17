// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
)

func readAllBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	buf := new(bytes.Buffer)
	_, err := buf.ReadFrom(r.Body)
	return buf.Bytes(), err
}

// --- Escenario 2 (mirror OpenAI): WEB_SEARCH_ENABLED=true + sin tool
// web_search → Path B inyecta la tool function y la petición sigue el flujo
// normal (no hay Path A en esta ruta — ver websearch.go). ---

func TestChatCompletions_PathB_InjectsToolWhenEnabled(t *testing.T) {
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

	req := newChatRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (Path B injects and continues to normal failover); body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(string(gotBody), `"web_search"`) {
		t.Errorf("Kiro payload does not mention web_search; Path B tool was not injected/forwarded. body=%s", gotBody)
	}
}

// TestInjectWebSearchTool_Direct verifica injectWebSearchTool en aislamiento:
// literal exacto de descripción/schema (routes_openai.py:257-269) y dedupe
// por (type=="function", function.name=="web_search") — routes_openai.py:246-250.
func TestInjectWebSearchTool_Direct(t *testing.T) {
	cfg := testConfig()
	cfg.WebSearchEnabled = true
	h := &Handler{cfg: cfg}

	req := &modelsopenai.ChatCompletionRequest{}
	h.injectWebSearchTool(req)

	if len(req.Tools) != 1 {
		t.Fatalf("Tools = %d, want 1", len(req.Tools))
	}
	got := req.Tools[0]
	if got.Type != "function" {
		t.Errorf("Type = %q, want function", got.Type)
	}
	if got.Function == nil || got.Function.Name != "web_search" {
		t.Fatalf("Function = %+v, want Name=web_search", got.Function)
	}
	if got.Function.Description == nil || *got.Function.Description != webSearchToolDescription {
		t.Errorf("Description = %v, want %q", got.Function.Description, webSearchToolDescription)
	}
	if string(got.Function.Parameters) != string(webSearchToolInputSchema) {
		t.Errorf("Parameters = %s, want %s", got.Function.Parameters, webSearchToolInputSchema)
	}

	// Segunda llamada: ya existe una tool function "web_search" → no duplica.
	h.injectWebSearchTool(req)
	if len(req.Tools) != 1 {
		t.Errorf("Tools = %d after second call, want 1 (no dedupe)", len(req.Tools))
	}
}

// --- Escenario 3 (mirror OpenAI): WEB_SEARCH_ENABLED=false → comportamiento normal ---

func TestChatCompletions_WebSearchDisabled_NormalBehavior(t *testing.T) {
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

	req := newChatRequest(t, "claude-sonnet-4", false)
	rec := httptest.NewRecorder()
	h.ChatCompletions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(string(gotBody), `"web_search"`) {
		t.Errorf("Kiro payload mentions web_search with WEB_SEARCH_ENABLED=false; Path B should not fire. body=%s", gotBody)
	}
}
