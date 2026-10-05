// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package debuglogger

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return string(b)
}

func TestRawBodyLogsEveryRead(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "debug")
	d := New(ModeAll, dir)
	d.PrepareNewRequest(context.Background())

	body := d.RawBody(io.NopCloser(strings.NewReader("chunk-1chunk-2")))
	got, err := io.ReadAll(body)
	if err != nil || string(got) != "chunk-1chunk-2" {
		t.Fatalf("ReadAll = %q, %v", got, err)
	}
	if err := body.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	if raw := readFile(t, dir, "response_stream_raw.txt"); raw != "chunk-1chunk-2" {
		t.Errorf("response_stream_raw.txt = %q", raw)
	}
}

func TestModifiedWriterLogsAndKeepsFlusher(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "debug")
	d := New(ModeAll, dir)
	d.PrepareNewRequest(context.Background())

	rec := httptest.NewRecorder()
	w := d.ModifiedWriter(rec)
	w.Header().Set("X-Test", "1")
	w.Write([]byte("data: a\n\n"))
	f, ok := w.(http.Flusher)
	if !ok {
		t.Fatalf("el writer envuelto perdió http.Flusher")
	}
	f.Flush()

	if rec.Body.String() != "data: a\n\n" || rec.Header().Get("X-Test") != "1" || !rec.Flushed {
		t.Errorf("el cliente no recibió lo mismo: body=%q flushed=%v", rec.Body.String(), rec.Flushed)
	}
	if mod := readFile(t, dir, "response_stream_modified.txt"); mod != "data: a\n\n" {
		t.Errorf("response_stream_modified.txt = %q", mod)
	}
}

// Con el modo off (o sin logger en el context) no se envuelve nada: cero
// coste en el camino normal.
func TestWrappersAreNoopWhenDisabled(t *testing.T) {
	body := io.NopCloser(strings.NewReader("x"))
	rec := httptest.NewRecorder()
	for _, d := range []*DebugLogger{nil, New(ModeOff, t.TempDir())} {
		if d.RawBody(body) != body {
			t.Errorf("%v: RawBody envolvió el cuerpo", d)
		}
		if d.ModifiedWriter(rec) != http.ResponseWriter(rec) {
			t.Errorf("%v: ModifiedWriter envolvió el writer", d)
		}
	}
}

// Las rutas llaman al logger del context sin comprobar si existe.
func TestNilLoggerIsSafe(t *testing.T) {
	var d *DebugLogger
	d.LogKiroRequestBody([]byte("{}"))
	d.LogRawChunk([]byte("x"))
	d.FlushOnError(500, "boom")
	d.DiscardBuffers()
}
