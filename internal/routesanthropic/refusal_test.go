// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// Un corte real de Kiro (CONTENT_FILTERED, fixture de internal/parsers)
// llega al cliente como stop_reason "refusal", no como un end_turn vacío
// (DIFFERENCES §17).
func TestMessages_KiroRefusalIsStopReasonRefusal(t *testing.T) {
	raw, err := os.ReadFile("../parsers/testdata/kiro_refusal.bin")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(raw)
	}))
	defer server.Close()

	rec := httptest.NewRecorder()
	newTestHandler(t, manager, cfg, server.URL).Messages(rec, newMessagesRequest(t, "claude-opus-5", false))

	var resp nonStreamResp
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v; body=%s", err, rec.Body.String())
	}
	if resp.StopReason != "refusal" {
		t.Errorf("stop_reason = %q, want refusal; body=%s", resp.StopReason, rec.Body.String())
	}
}
