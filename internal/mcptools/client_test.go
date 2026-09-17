// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

// fakeTokenProvider satisface utils.TokenProvider para los tests de este
// paquete, sin depender de internal/auth.
type fakeTokenProvider struct {
	token   string
	err     error
	profile string
}

func (f fakeTokenProvider) AccessToken(ctx context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.token, nil
}

func (f fakeTokenProvider) ProfileARN() string { return f.profile }

// mcpRequestBody replica la forma que CallKiroMCPAPI debe mandar, para que
// el fake server pueda verificar el request saliente.
type mcpRequestBody struct {
	ID      string `json:"id"`
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  struct {
		Name      string `json:"name"`
		Arguments struct {
			Query string `json:"query"`
		} `json:"arguments"`
	} `json:"params"`
}

var toolUseIDPattern = regexp.MustCompile(`^srvtoolu_[0-9a-f]{32}$`)

// TestCallKiroMCPAPI_DoubleDeserialize es el escenario nombrado del brief:
// result.content[0].text es una CADENA que a su vez contiene JSON
// (mcp_tools.py:119,184-186) — hay que deserializar dos veces. Además
// verifica: método/URL/headers del POST saliente, y que tool_use_id matchea
// srvtoolu_[0-9a-f]{32}.
func TestCallKiroMCPAPI_DoubleDeserialize(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody mcpRequestBody
	var gotAuth, gotOptout, gotContentType string

	innerJSON := `{"results":[{"title":"Go Tutorial","url":"https://go.dev/tour","snippet":"Learn Go","publishedDate":1710339825000}],"totalResults":1,"query":"golang"}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotOptout = r.Header.Get("x-amzn-codewhisperer-optout")
		gotContentType = r.Header.Get("Content-Type")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decodificando el body del request MCP: %v", err)
		}

		envelope := map[string]any{
			"id":      gotBody.ID,
			"jsonrpc": "2.0",
			"result": map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": innerJSON}, // string que contiene JSON — la doble deserialización
				},
				"isError": false,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(envelope)
	}))
	defer srv.Close()

	tp := fakeTokenProvider{token: "tok-123"}
	toolUseID, results, err := CallKiroMCPAPI(context.Background(), srv.URL, "golang", tp)
	if err != nil {
		t.Fatalf("CallKiroMCPAPI devolvió error inesperado: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("método = %q, want POST", gotMethod)
	}
	if gotPath != "/mcp" {
		t.Errorf("path = %q, want /mcp", gotPath)
	}
	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer tok-123")
	}
	if gotOptout != "false" {
		t.Errorf("x-amzn-codewhisperer-optout = %q, want %q (mcp_tools.py:153)", gotOptout, "false")
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (mcp_tools.py:154)", gotContentType)
	}
	if gotBody.JSONRPC != "2.0" || gotBody.Method != "tools/call" {
		t.Errorf("jsonrpc/method = %q/%q, want 2.0/tools/call", gotBody.JSONRPC, gotBody.Method)
	}
	if gotBody.Params.Name != "web_search" {
		t.Errorf("params.name = %q, want web_search", gotBody.Params.Name)
	}
	if gotBody.Params.Arguments.Query != "golang" {
		t.Errorf("params.arguments.query = %q, want golang", gotBody.Params.Arguments.Query)
	}

	if !toolUseIDPattern.MatchString(toolUseID) {
		t.Errorf("tool_use_id = %q, no matchea %s", toolUseID, toolUseIDPattern.String())
	}

	if results == nil {
		t.Fatal("results es nil, want el mapa deserializado desde el string interno")
	}
	if got, want := results["totalResults"], float64(1); got != want {
		t.Errorf("results[totalResults] = %v, want %v", got, want)
	}
	items, ok := results["results"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("results[results] = %#v, want slice de 1 elemento", results["results"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("results[results][0] no es un map: %#v", items[0])
	}
	if item["title"] != "Go Tutorial" {
		t.Errorf("title = %v, want Go Tutorial", item["title"])
	}
}

// TestCallKiroMCPAPI_NonOKStatus: un status != 200 es un fallo (mcp_tools.py:163-165
// devuelve (None, None) sin reintentar).
func TestCallKiroMCPAPI_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	toolUseID, results, err := CallKiroMCPAPI(context.Background(), srv.URL, "q", fakeTokenProvider{token: "t"})
	if err == nil {
		t.Fatal("want error para status 500, got nil")
	}
	if toolUseID != "" || results != nil {
		t.Errorf("toolUseID/results = %q/%v, want vacío/nil en el camino de error", toolUseID, results)
	}
}

// TestCallKiroMCPAPI_JSONRPCError: mcp_response["error"] no nulo es un fallo
// (mcp_tools.py:180-182).
func TestCallKiroMCPAPI_JSONRPCError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "x",
			"jsonrpc": "2.0",
			"error":   map[string]any{"code": -32000, "message": "boom"},
		})
	}))
	defer srv.Close()

	_, results, err := CallKiroMCPAPI(context.Background(), srv.URL, "q", fakeTokenProvider{token: "t"})
	if err == nil {
		t.Fatal("want error cuando mcp_response.error no es nulo")
	}
	if results != nil {
		t.Errorf("results = %v, want nil", results)
	}
}

// TestCallKiroMCPAPI_InvalidInnerJSON: si result.content[0].text no es JSON
// válido, json.loads falla en el original (mcp_tools.py:186,197-199) y
// devuelve (None, None).
func TestCallKiroMCPAPI_InvalidInnerJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      "x",
			"jsonrpc": "2.0",
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": "not json"}},
				"isError": false,
			},
		})
	}))
	defer srv.Close()

	_, results, err := CallKiroMCPAPI(context.Background(), srv.URL, "q", fakeTokenProvider{token: "t"})
	if err == nil {
		t.Fatal("want error cuando el texto interno no es JSON válido")
	}
	if results != nil {
		t.Errorf("results = %v, want nil", results)
	}
}

// TestCallKiroMCPAPI_AccessTokenError: si el TokenProvider falla, no se
// llega a mandar el request.
func TestCallKiroMCPAPI_AccessTokenError(t *testing.T) {
	tp := fakeTokenProvider{err: errors.New("boom")}
	_, results, err := CallKiroMCPAPI(context.Background(), "http://127.0.0.1:0", "q", tp)
	if err == nil {
		t.Fatal("want error cuando AccessToken falla")
	}
	if results != nil {
		t.Errorf("results = %v, want nil", results)
	}
}
