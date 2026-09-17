// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

var msgIDPattern = regexp.MustCompile(`^msg_[0-9a-f]{24}$`)

// extractFirstMatch devuelve el primer grupo de captura de re en s, o falla
// el test si no hay match.
func extractFirstMatch(t *testing.T, s, pattern string) string {
	t.Helper()
	re := regexp.MustCompile(pattern)
	m := re.FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("no se encontró %q en %q", pattern, s)
	}
	return m[1]
}

// newFakeMCPServer arma un httptest.Server que responde con un resultado de
// búsqueda válido, doblemente serializado igual que Kiro real.
func newFakeMCPServer(t *testing.T) *httptest.Server {
	t.Helper()
	inner := `{"results":[{"title":"Go","url":"https://go.dev","snippet":"The Go language"}],"totalResults":1,"query":"golang"}`
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":      body.ID,
			"jsonrpc": "2.0",
			"result": map[string]any{
				"content": []map[string]any{{"type": "text", "text": inner}},
				"isError": false,
			},
		})
	}))
}

func anthropicMsg(text string) json.RawMessage {
	return json.RawMessage(`{"role":"user","content":[{"type":"text","text":"` + text + `"}]}`)
}

// TestHandleNativeWebSearch_QueryExtractionFails: mcp_tools.py:611-623.
func TestHandleNativeWebSearch_QueryExtractionFails(t *testing.T) {
	req := NativeWebSearchRequest{Messages: nil, Model: "m", Stream: false}
	out := HandleNativeWebSearch(context.Background(), req, "http://unused", fakeTokenProvider{token: "t"}, "anthropic")

	if out.StatusCode != 400 {
		t.Fatalf("StatusCode = %d, want 400", out.StatusCode)
	}
	if out.Streaming {
		t.Error("want Streaming=false para una respuesta de error")
	}
	var body map[string]any
	if err := json.Unmarshal(out.Body, &body); err != nil {
		t.Fatalf("body no es JSON válido: %v", err)
	}
	if body["type"] != "error" {
		t.Errorf(`body["type"] = %v, want "error"`, body["type"])
	}
	errObj, _ := body["error"].(map[string]any)
	if errObj["type"] != "invalid_request_error" {
		t.Errorf(`error.type = %v, want invalid_request_error`, errObj["type"])
	}
}

// TestHandleNativeWebSearch_MCPCallFails: mcp_tools.py:630-640.
func TestHandleNativeWebSearch_MCPCallFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	req := NativeWebSearchRequest{Messages: []json.RawMessage{anthropicMsg("golang")}, Model: "m", Stream: false}
	out := HandleNativeWebSearch(context.Background(), req, srv.URL, fakeTokenProvider{token: "t"}, "anthropic")

	if out.StatusCode != 500 {
		t.Fatalf("StatusCode = %d, want 500", out.StatusCode)
	}
	var body map[string]any
	_ = json.Unmarshal(out.Body, &body)
	errObj, _ := body["error"].(map[string]any)
	if errObj["type"] != "api_error" {
		t.Errorf(`error.type = %v, want api_error`, errObj["type"])
	}
}

func TestHandleNativeWebSearch_StreamingAnthropic(t *testing.T) {
	srv := newFakeMCPServer(t)
	defer srv.Close()

	req := NativeWebSearchRequest{Messages: []json.RawMessage{anthropicMsg("golang")}, Model: "claude-x", Stream: true}
	out := HandleNativeWebSearch(context.Background(), req, srv.URL, fakeTokenProvider{token: "t"}, "anthropic")

	if out.StatusCode != 200 || !out.Streaming {
		t.Fatalf("StatusCode/Streaming = %d/%v, want 200/true", out.StatusCode, out.Streaming)
	}
	s := string(out.Body)
	if !strings.Contains(s, "event: message_start") || !strings.Contains(s, "event: message_stop") {
		t.Errorf("el body no parece un stream Anthropic completo: %s", s)
	}
	// Separador con espacio: pyjson.Dumps (fix round 1, parity #1).
	if !toolUseIDPattern.MatchString(extractFirstMatch(t, s, `"id": "(srvtoolu_[0-9a-f]{32})"`)) {
		t.Errorf("no se encontró un tool_use_id válido en el stream: %s", s)
	}
}

func TestHandleNativeWebSearch_StreamingOpenAI(t *testing.T) {
	srv := newFakeMCPServer(t)
	defer srv.Close()

	req := NativeWebSearchRequest{Messages: []json.RawMessage{anthropicMsg("golang")}, Model: "gpt-x", Stream: true}
	out := HandleNativeWebSearch(context.Background(), req, srv.URL, fakeTokenProvider{token: "t"}, "openai")

	if out.StatusCode != 200 || !out.Streaming {
		t.Fatalf("StatusCode/Streaming = %d/%v, want 200/true", out.StatusCode, out.Streaming)
	}
	s := string(out.Body)
	if strings.Contains(s, "event: ") {
		t.Errorf("el dialecto OpenAI no debe llevar 'event: ': %s", s)
	}
	if !strings.HasSuffix(s, "data: [DONE]\n\n") {
		t.Errorf("want sufijo [DONE], got: %s", s)
	}
}

func TestHandleNativeWebSearch_NonStreamingAnthropic(t *testing.T) {
	srv := newFakeMCPServer(t)
	defer srv.Close()

	req := NativeWebSearchRequest{Messages: []json.RawMessage{anthropicMsg("golang")}, Model: "claude-x", Stream: false}
	out := HandleNativeWebSearch(context.Background(), req, srv.URL, fakeTokenProvider{token: "t"}, "anthropic")

	if out.StatusCode != 200 || out.Streaming {
		t.Fatalf("StatusCode/Streaming = %d/%v, want 200/false", out.StatusCode, out.Streaming)
	}

	var body struct {
		ID      string           `json:"id"`
		Type    string           `json:"type"`
		Content []map[string]any `json:"content"`
		Usage   struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(out.Body, &body); err != nil {
		t.Fatalf("body no es JSON válido: %v", err)
	}
	if body.Type != "message" {
		t.Errorf("type = %q, want message", body.Type)
	}
	if !msgIDPattern.MatchString(body.ID) {
		t.Errorf("id = %q, no matchea msg_[0-9a-f]{24}", body.ID)
	}
	if len(body.Content) != 3 {
		t.Fatalf("content tiene %d bloques, want 3 (server_tool_use, web_search_tool_result, text)", len(body.Content))
	}
	if body.Content[0]["type"] != "server_tool_use" || body.Content[1]["type"] != "web_search_tool_result" || body.Content[2]["type"] != "text" {
		t.Errorf("orden/tipo de bloques inesperado: %+v", body.Content)
	}
	if body.Usage.InputTokens <= 0 || body.Usage.OutputTokens <= 0 {
		t.Errorf("usage = %+v, want ambos > 0", body.Usage)
	}
}

func TestHandleNativeWebSearch_NonStreamingOpenAI(t *testing.T) {
	srv := newFakeMCPServer(t)
	defer srv.Close()

	req := NativeWebSearchRequest{Messages: []json.RawMessage{anthropicMsg("golang")}, Model: "gpt-x", Stream: false}
	out := HandleNativeWebSearch(context.Background(), req, srv.URL, fakeTokenProvider{token: "t"}, "openai")

	if out.StatusCode != 200 || out.Streaming {
		t.Fatalf("StatusCode/Streaming = %d/%v, want 200/false", out.StatusCode, out.Streaming)
	}

	var body struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(out.Body, &body); err != nil {
		t.Fatalf("body no es JSON válido: %v", err)
	}
	if body.Object != "chat.completion" {
		t.Errorf("object = %q, want chat.completion", body.Object)
	}
	if len(body.Choices) != 1 || !strings.Contains(body.Choices[0].Message.Content, "<web_search>") {
		t.Errorf("choices inesperado: %+v", body.Choices)
	}
	if body.Choices[0].FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", body.Choices[0].FinishReason)
	}
}
