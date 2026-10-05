// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// Razonamiento nativo de Kiro en modo no-streaming: bloque thinking con la
// firma real, bloque redacted_thinking y el texto (DIFFERENCES §18).
func TestMessages_NativeThinkingNonStreaming(t *testing.T) {
	cfg := testConfig()
	cfg.FakeReasoningHandling = "as_reasoning_content"
	manager := newTestManager(t, cfg, []string{"tok-only"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"Pienso"}{"text":" esto"}{"signature":"SIG"}{"redactedContent":"AAEC"}` +
			`{"content":"Hola"}{"contextUsagePercentage":1.5}`))
	}))
	defer server.Close()

	rec := httptest.NewRecorder()
	newTestHandler(t, manager, cfg, server.URL).Messages(rec, newMessagesRequest(t, "claude-sonnet-5", false))

	var resp struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	want := []map[string]any{
		{"type": "thinking", "thinking": "Pienso esto", "signature": "SIG"},
		{"type": "redacted_thinking", "data": "AAEC"},
		{"type": "text", "text": "Hola"},
	}
	if !reflect.DeepEqual(resp.Content, want) {
		t.Errorf("content = %#v\nquiero %#v", resp.Content, want)
	}
}
