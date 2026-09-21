// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package server

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStatusRecorderCapturesStatusAndBytes verifica que el recorder captura el
// código de estado y los bytes escritos sin alterar la respuesta.
func TestStatusRecorderCapturesStatusAndBytes(t *testing.T) {
	under := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: under, status: http.StatusOK}

	rec.WriteHeader(http.StatusTeapot)
	n, err := rec.Write([]byte("hello"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}

	if rec.status != http.StatusTeapot {
		t.Errorf("status = %d, want %d", rec.status, http.StatusTeapot)
	}
	if rec.written != n || n != 5 {
		t.Errorf("written = %d, want 5", rec.written)
	}
	if under.Body.String() != "hello" || under.Code != http.StatusTeapot {
		t.Errorf("underlying not written through: code=%d body=%q", under.Code, under.Body.String())
	}
}

// TestStatusRecorderIsFlusher es la garantía crítica para el streaming SSE:
// las rutas hacen `w.(http.Flusher)` y llaman Flush() entre chunks. Si el
// recorder no fuese Flusher, el streaming se rompería.
func TestStatusRecorderIsFlusher(t *testing.T) {
	under := httptest.NewRecorder()
	rec := &statusRecorder{ResponseWriter: under}

	f, ok := any(rec).(http.Flusher)
	if !ok {
		t.Fatal("statusRecorder no implementa http.Flusher; el streaming SSE se rompería")
	}
	f.Flush()
	if !under.Flushed {
		t.Error("Flush() no se delegó al ResponseWriter subyacente")
	}
}

// TestLogMiddlewareEmitsRequestLine comprueba que se emite una línea con el
// método, la ruta y el status final.
func TestLogMiddlewareEmitsRequestLine(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	h := logMiddleware(logger, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte("ok"))
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/v1/messages", nil))

	out := buf.String()
	for _, want := range []string{"request", "method=POST", "path=/v1/messages", "status=201"} {
		if !strings.Contains(out, want) {
			t.Errorf("log line missing %q; got: %s", want, out)
		}
	}
}

// TestLogMiddlewareNilIsNoop confirma que un logger nil devuelve un handler
// que sirve normalmente (sin panic, sin envolver la respuesta).
func TestLogMiddlewareNilIsNoop(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	})

	got := logMiddleware(nil, next)
	if got == nil {
		t.Fatal("logMiddleware(nil, next) devolvió nil")
	}

	rec := httptest.NewRecorder()
	got.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if !called || rec.Code != http.StatusNoContent {
		t.Errorf("el handler no se sirvió tal cual: called=%v code=%d", called, rec.Code)
	}
}
