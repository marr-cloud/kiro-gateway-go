// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// Divergencia intencional (DIFFERENCES §14): mientras Kiro calla a mitad de
// stream, el gateway emite eventos ping para que el idle watchdog de Claude Code
// no corte la respuesta.
func TestMessages_StreamingEmitsPingWhileUpstreamIsSilent(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		first, _ := json.Marshal(map[string]string{"content": "Hello"})
		w.Write(first)
		w.(http.Flusher).Flush()
		time.Sleep(150 * time.Millisecond)
		w.Write(kiroContentBody(" world"))
	}))
	defer server.Close()

	h := newTestHandler(t, manager, cfg, server.URL)
	h.pingInterval = 20 * time.Millisecond

	rec := httptest.NewRecorder()
	h.Messages(rec, newMessagesRequest(t, "claude-sonnet-4", true))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}

	var types []string
	var fullText string
	for _, data := range splitSSEData(rec.Body.Bytes()) {
		var ev sseEventIn
		if err := json.Unmarshal(data, &ev); err != nil {
			t.Fatalf("decode event %s: %v", data, err)
		}
		types = append(types, ev.Type)
		if ev.Type == "content_block_delta" {
			var d deltaIn
			_ = json.Unmarshal(ev.Delta, &d)
			fullText += d.Text
		}
	}

	pings := 0
	for _, typ := range types {
		if typ == "ping" {
			pings++
		}
	}
	if pings == 0 {
		t.Fatalf("no ping events emitted; types=%v", types)
	}
	if types[0] != "message_start" {
		t.Errorf("first event = %q, want message_start", types[0])
	}
	if types[len(types)-1] != "message_stop" {
		t.Errorf("last event = %q, want message_stop", types[len(types)-1])
	}
	if fullText != "Hello world" {
		t.Errorf("accumulated text = %q, want %q", fullText, "Hello world")
	}
}

// El intervalo por defecto es el de producción.
func TestNew_DefaultPingInterval(t *testing.T) {
	h := New(nil, nil, testConfig(), nil)
	if h.pingInterval != 15*time.Second {
		t.Errorf("pingInterval = %v, want 15s", h.pingInterval)
	}
}
