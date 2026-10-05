// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/debuglogger"
	"github.com/marr-cloud/kiro-gateway-go/internal/debugmiddleware"
)

func withDebugLogger(r *http.Request, mode debuglogger.Mode, dir string) *http.Request {
	d := debuglogger.New(mode, dir)
	ctx := debugmiddleware.WithLogger(r.Context(), d)
	return r.WithContext(d.PrepareNewRequest(ctx))
}

func debugFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// DEBUG_MODE=all: payload a Kiro, chunks crudos y SSE reformateado
// (routes_openai.py:342, streaming_core.py:171,181).
func TestChatCompletions_DebugModeAllWritesStreamFiles(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write(kiroContentBody("Hola"))
	}))
	defer server.Close()

	dir := filepath.Join(t.TempDir(), "debug")
	rec := httptest.NewRecorder()
	req := withDebugLogger(newChatRequest(t, "claude-sonnet-4", true), debuglogger.ModeAll, dir)
	newTestHandler(t, manager, cfg, server.URL).ChatCompletions(rec, req)

	files := debugFiles(t, dir)
	for _, want := range []string{"kiro_request_body.json", "response_stream_raw.txt", "response_stream_modified.txt"} {
		if !slices.Contains(files, want) {
			t.Errorf("falta %s en %v", want, files)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "response_stream_raw.txt"))
	if !strings.Contains(string(raw), `{"content":"Hola"}`) {
		t.Errorf("response_stream_raw.txt sin el chunk de Kiro: %q", raw)
	}
	mod, _ := os.ReadFile(filepath.Join(dir, "response_stream_modified.txt"))
	if string(mod) != rec.Body.String() {
		t.Errorf("response_stream_modified.txt no es lo que recibió el cliente:\n%q\n%q", mod, rec.Body.String())
	}
}

// DEBUG_MODE=errors: un error Fatal de Kiro vuelca los buffers
// (routes_openai.py:480).
func TestChatCompletions_DebugModeErrorsFlushesOnKiroError(t *testing.T) {
	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-only"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"message":"Context too large","reason":"CONTENT_LENGTH_EXCEEDS_THRESHOLD"}`))
	}))
	defer server.Close()

	dir := filepath.Join(t.TempDir(), "debug")
	req := withDebugLogger(newChatRequest(t, "claude-sonnet-4", false), debuglogger.ModeErrors, dir)
	newTestHandler(t, manager, cfg, server.URL).ChatCompletions(httptest.NewRecorder(), req)

	files := debugFiles(t, dir)
	for _, want := range []string{"kiro_request_body.json", "error_info.json"} {
		if !slices.Contains(files, want) {
			t.Errorf("falta %s en %v", want, files)
		}
	}
}
