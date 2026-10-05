// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package server

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelcaps"
	"github.com/marr-cloud/kiro-gateway-go/internal/version"
)

func TestKiroStatus_NoAuth_Returns401(t *testing.T) {
	s := newTestServer(t)
	rec := doRequest(s, http.MethodGet, "/kiro/status", nil, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, quiero 401", rec.Code)
	}
	rec = doRequest(s, http.MethodGet, "/kiro/status", map[string]string{"x-api-key": "otra"}, nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("key incorrecta: status = %d, quiero 401", rec.Code)
	}
}

func TestKiroStatus_ReturnsModelsAndDebug(t *testing.T) {
	t.Cleanup(modelcaps.Reset)
	modelcaps.Reset()
	modelcaps.Set("claude-sonnet-5.5", modelcaps.Caps{
		ThinkingTypes: []string{"adaptive", "between_tools"},
		EffortPath:    "output_config",
		EffortLevels:  []string{"low", "high"},
	})
	modelcaps.SetRefusalFallback("claude-sonnet-5.5", "claude-sonnet-5")

	s := newTestServer(t)
	s.cfg.DebugMode = "errors"
	s.cfg.DebugDir = "debug_logs"
	s.availableModels = func() []string { return []string{"claude-haiku-4.5", "claude-sonnet-5.5"} }

	for _, hdr := range []map[string]string{
		{"Authorization": "Bearer " + testAPIKey},
		{"x-api-key": testAPIKey},
	} {
		rec := doRequest(s, http.MethodGet, "/kiro/status", hdr, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%v: status = %d, cuerpo %s", hdr, rec.Code, rec.Body.String())
		}
		var got kiroStatusResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Version != version.Version() {
			t.Errorf("version = %q", got.Version)
		}
		if got.ActiveAccount == nil || *got.ActiveAccount == "" {
			t.Errorf("active_account vacío")
		}
		if got.UptimeSeconds <= 0 {
			t.Errorf("uptime_seconds = %v", got.UptimeSeconds)
		}
		if got.Debug.Mode != "errors" || !filepath.IsAbs(got.Debug.Dir) || filepath.Base(got.Debug.Dir) != "debug_logs" {
			t.Errorf("debug = %+v, quiero mode errors y dir absoluto acabado en debug_logs", got.Debug)
		}
		if len(got.Models) != 2 {
			t.Fatalf("models = %+v", got.Models)
		}
		haiku, sonnet := got.Models[0], got.Models[1]
		if haiku.ID != "claude-haiku-4.5" || len(haiku.NativeThinking) != 0 || len(haiku.EffortLevels) != 0 || haiku.RefusalFallback != "" {
			t.Errorf("haiku = %+v", haiku)
		}
		if sonnet.ID != "claude-sonnet-5.5" || sonnet.RefusalFallback != "claude-sonnet-5" ||
			strings.Join(sonnet.NativeThinking, ",") != "adaptive,between_tools" || strings.Join(sonnet.EffortLevels, ",") != "low,high" {
			t.Errorf("sonnet = %+v", sonnet)
		}
		// Listas vacías como [] (el mod hace .join sobre ellas).
		if !strings.Contains(rec.Body.String(), `"native_thinking":[]`) {
			t.Errorf("native_thinking vacío no sale como []: %s", rec.Body.String())
		}
	}
}
