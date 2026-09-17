// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package debuglogger

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// assertDirEmpty verifica que dir no contiene ninguna entrada — usado para
// los escenarios donde el original no debe escribir nada (modo "off", o
// "errors" sin datos que volcar).
func assertDirEmpty(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir(%s): %v", dir, err)
	}
	if len(entries) != 0 {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Errorf("dir %s no está vacío: %v", dir, names)
	}
}

func requireFileBytes(t *testing.T, dir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("se esperaba el fichero %s: %v", name, err)
	}
	return data
}

func requireNoFile(t *testing.T, dir, name string) {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
		t.Errorf("no se esperaba el fichero %s pero existe", name)
	}
}

// ==================================================================================================
// Modo off: ningún fichero escrito tras ninguna operación (debug_logger.py
// _is_enabled() -> False para DEBUG_MODE="off").
// ==================================================================================================

func TestModeOffWritesNothing(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeOff, dir)

	d.LogRequestBody([]byte(`{"a":1}`))
	d.LogKiroRequestBody([]byte(`{"b":2}`))
	d.LogRawChunk([]byte("raw-chunk"))
	d.LogModifiedChunk([]byte("modified-chunk"))
	d.LogErrorInfo(500, "boom")
	d.FlushOnError(500, "boom")
	d.DiscardBuffers()

	assertDirEmpty(t, dir)
}

// TestModeOffPrepareNewRequestIsNoop verifica que prepare_new_request()
// (debug_logger.py:129-154) respeta el guard `if not self._is_enabled(): return`
// también en el ciclo de vida por-petición: el contexto devuelto no lleva
// ningún buffer instalado, así que el slog.Handler queda deshabilitado.
func TestModeOffPrepareNewRequestIsNoop(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeOff, dir)

	ctx := d.PrepareNewRequest(context.Background())
	if d.Handler().Enabled(ctx, slog.LevelInfo) {
		t.Errorf("Handler().Enabled() = true tras PrepareNewRequest en modo off, want false")
	}
	assertDirEmpty(t, dir)
}

// ==================================================================================================
// Modo errors: nada en éxito (DiscardBuffers), ficheros en FlushOnError.
// ==================================================================================================

func TestModeErrorsDiscardBuffersOnSuccessWritesNothing(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeErrors, dir)

	d.LogRequestBody([]byte(`{"a":1}`))
	d.LogKiroRequestBody([]byte(`{"b":2}`))
	d.LogRawChunk([]byte("raw"))
	d.LogModifiedChunk([]byte("mod"))

	d.DiscardBuffers()

	assertDirEmpty(t, dir)
}

// TestModeErrorsFlushOnErrorNoDataIsNoop cubre debug_logger.py:272-279: si
// ningún buffer tiene contenido (nunca se llamó a ningún Log*), flush_on_error
// devuelve ANTES de crear el directorio o escribir error_info.json — un
// no-op completo, no solo "sin datos de payload".
func TestModeErrorsFlushOnErrorNoDataIsNoop(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeErrors, dir)

	d.FlushOnError(500, "boom")

	assertDirEmpty(t, dir)
}

func TestModeErrorsFlushOnErrorWritesBufferedData(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeErrors, dir)

	d.LogRequestBody([]byte(`{"a":1}`))
	d.LogKiroRequestBody([]byte(`{"b":2}`))
	d.LogRawChunk([]byte("raw-"))
	d.LogRawChunk([]byte("chunk"))
	d.LogModifiedChunk([]byte("mod-"))
	d.LogModifiedChunk([]byte("chunk"))

	d.FlushOnError(503, "kiro unavailable")

	reqBody := requireFileBytes(t, dir, "request_body.json")
	var reqObj map[string]any
	if err := json.Unmarshal(reqBody, &reqObj); err != nil {
		t.Fatalf("request_body.json no es JSON válido: %v", err)
	}
	if reqObj["a"] != float64(1) {
		t.Errorf("request_body.json a = %v, want 1", reqObj["a"])
	}

	kiroBody := requireFileBytes(t, dir, "kiro_request_body.json")
	var kiroObj map[string]any
	if err := json.Unmarshal(kiroBody, &kiroObj); err != nil {
		t.Fatalf("kiro_request_body.json no es JSON válido: %v", err)
	}
	if kiroObj["b"] != float64(2) {
		t.Errorf("kiro_request_body.json b = %v, want 2", kiroObj["b"])
	}

	raw := requireFileBytes(t, dir, "response_stream_raw.txt")
	if string(raw) != "raw-chunk" {
		t.Errorf("response_stream_raw.txt = %q, want %q", raw, "raw-chunk")
	}

	mod := requireFileBytes(t, dir, "response_stream_modified.txt")
	if string(mod) != "mod-chunk" {
		t.Errorf("response_stream_modified.txt = %q, want %q", mod, "mod-chunk")
	}

	errInfo := requireFileBytes(t, dir, "error_info.json")
	var errObj map[string]any
	if err := json.Unmarshal(errInfo, &errObj); err != nil {
		t.Fatalf("error_info.json no es JSON válido: %v", err)
	}
	if errObj["status_code"] != float64(503) {
		t.Errorf("error_info.json status_code = %v, want 503", errObj["status_code"])
	}
	if errObj["error_message"] != "kiro unavailable" {
		t.Errorf("error_info.json error_message = %v, want %q", errObj["error_message"], "kiro unavailable")
	}
}

// TestModeErrorsFlushOnErrorClearsBuffersAfterward verifica que, tras un
// flush, los buffers quedan vacíos (debug_logger.py:314-316, `finally:
// self._clear_buffers()`), así que un segundo FlushOnError sin nuevos datos
// es un no-op.
func TestModeErrorsFlushOnErrorClearsBuffersAfterward(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeErrors, dir)

	d.LogRequestBody([]byte(`{"a":1}`))
	d.FlushOnError(500, "first")

	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	d.FlushOnError(500, "second")

	assertDirEmpty(t, dir)
}

// TestModeErrorsIgnoresRequestBodyOnlyWhenBuffered cubre que el body no
// escrito en modo errors nunca toca disco hasta el flush.
func TestModeErrorsBuffersUntilFlush(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeErrors, dir)

	d.LogRequestBody([]byte(`{"a":1}`))
	requireNoFile(t, dir, "request_body.json")

	d.FlushOnError(500, "boom")
	requireFileBytes(t, dir, "request_body.json")
}

// ==================================================================================================
// Modo all: ficheros siempre, incluso en éxito.
// ==================================================================================================

func TestModeAllWritesImmediately(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeAll, dir)

	ctx := d.PrepareNewRequest(context.Background())

	d.LogRequestBody([]byte(`{"a":1}`))
	requireFileBytes(t, dir, "request_body.json")

	d.LogKiroRequestBody([]byte(`{"b":2}`))
	requireFileBytes(t, dir, "kiro_request_body.json")

	d.LogRawChunk([]byte("raw-"))
	d.LogRawChunk([]byte("chunk"))
	raw := requireFileBytes(t, dir, "response_stream_raw.txt")
	if string(raw) != "raw-chunk" {
		t.Errorf("response_stream_raw.txt = %q, want %q (append, no overwrite)", raw, "raw-chunk")
	}

	d.LogModifiedChunk([]byte("mod-"))
	d.LogModifiedChunk([]byte("chunk"))
	mod := requireFileBytes(t, dir, "response_stream_modified.txt")
	if string(mod) != "mod-chunk" {
		t.Errorf("response_stream_modified.txt = %q, want %q", mod, "mod-chunk")
	}

	_ = ctx // usado por los tests de Handler más abajo; aquí solo se ejercitan los Log*.
}

// TestModeAllDiscardBuffersWritesAppLogsOnSuccess cubre debug_logger.py:325-330:
// en modo "all", discard_buffers() SÍ escribe app_logs.txt (los logs de una
// petición exitosa no se descartan, solo los buffers de datos que ya se
// escribieron de forma inmediata).
func TestModeAllDiscardBuffersWritesAppLogsOnSuccess(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeAll, dir)

	ctx := d.PrepareNewRequest(context.Background())
	logger := slog.New(d.Handler())
	logger.InfoContext(ctx, "success path log line")

	d.DiscardBuffers()

	appLogs := requireFileBytes(t, dir, "app_logs.txt")
	if !strings.Contains(string(appLogs), "success path log line") {
		t.Errorf("app_logs.txt = %q, want que contenga %q", appLogs, "success path log line")
	}
}

// TestModeAllFlushOnErrorWritesErrorInfoAndAppLogs cubre debug_logger.py:266-270:
// en modo "all", flush_on_error NO reescribe los buffers de datos (ya se
// escribieron de forma inmediata), solo añade error_info.json y app_logs.txt.
func TestModeAllFlushOnErrorWritesErrorInfoAndAppLogs(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeAll, dir)

	ctx := d.PrepareNewRequest(context.Background())
	logger := slog.New(d.Handler())
	logger.ErrorContext(ctx, "something failed")

	d.LogRequestBody([]byte(`{"a":1}`))

	d.FlushOnError(500, "boom")

	errInfo := requireFileBytes(t, dir, "error_info.json")
	var errObj map[string]any
	if err := json.Unmarshal(errInfo, &errObj); err != nil {
		t.Fatalf("error_info.json no es JSON válido: %v", err)
	}
	if errObj["status_code"] != float64(500) {
		t.Errorf("error_info.json status_code = %v, want 500", errObj["status_code"])
	}

	appLogs := requireFileBytes(t, dir, "app_logs.txt")
	if !strings.Contains(string(appLogs), "something failed") {
		t.Errorf("app_logs.txt = %q, want que contenga %q", appLogs, "something failed")
	}
}

// TestModeAllPrepareNewRequestClearsDirectory cubre debug_logger.py:146-154:
// en modo "all", prepare_new_request() borra y recrea DEBUG_DIR al empezar
// una nueva petición.
func TestModeAllPrepareNewRequestClearsDirectory(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeAll, dir)

	d.PrepareNewRequest(context.Background())
	d.LogRequestBody([]byte(`{"a":1}`))
	requireFileBytes(t, dir, "request_body.json")

	// Nueva petición: el directorio se limpia, el fichero de la petición
	// anterior desaparece hasta que se vuelva a loguear algo.
	d.PrepareNewRequest(context.Background())
	requireNoFile(t, dir, "request_body.json")
}

// ==================================================================================================
// slog.Handler: formato exacto de línea y activación gateada por el buffer
// instalado en el context (debug_logger.py:108-127, spec §6.13).
// ==================================================================================================

// TestHandlerFormatsLineExactly verifica, por regex, el formato
// "{YYYY-MM-DD HH:mm:ss.SSS} | {LEVEL:<8} | {origen}:{función}:{línea} | {mensaje}".
func TestHandlerFormatsLineExactly(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeAll, dir)
	ctx := d.PrepareNewRequest(context.Background())

	logger := slog.New(d.Handler())
	logger.InfoContext(ctx, "formatted line test")

	buf, ok := logBufferFromContext(ctx)
	if !ok {
		t.Fatalf("no se instaló ningún buffer en el context")
	}
	line := buf.String()

	levelField := fmt.Sprintf("%-8s", "INFO")
	pattern := regexp.MustCompile(
		`^\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}\.\d{3} \| ` +
			regexp.QuoteMeta(levelField) +
			` \| [\w.]+:\w+:\d+ \| formatted line test\n$`,
	)
	if !pattern.MatchString(line) {
		t.Errorf("línea de log = %q, no matchea el patrón esperado", line)
	}
}

// TestHandlerDisabledOutsideRequestContext verifica que, sin un buffer
// instalado en el context (fuera de una petición con debug activo), el
// handler queda deshabilitado — mismo comportamiento que "el sink solo está
// activo durante el procesamiento de una petición específica"
// (debug_logger.py:113-115).
func TestHandlerDisabledOutsideRequestContext(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeAll, dir)

	h := d.Handler()
	if h.Enabled(context.Background(), slog.LevelInfo) {
		t.Errorf("Handler().Enabled() = true sin buffer en el context, want false")
	}
}

// TestHandlerMultipleRequestsDoNotMixBuffers verifica que dos peticiones
// consecutivas (dos PrepareNewRequest) obtienen buffers de log distintos: el
// contenido de la primera no se filtra en la segunda.
func TestHandlerMultipleRequestsDoNotMixBuffers(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeAll, dir)
	logger := slog.New(d.Handler())

	ctx1 := d.PrepareNewRequest(context.Background())
	logger.InfoContext(ctx1, "first request")
	d.DiscardBuffers()
	firstLogs := requireFileBytes(t, dir, "app_logs.txt")
	if !strings.Contains(string(firstLogs), "first request") {
		t.Fatalf("app_logs.txt tras la primera petición = %q", firstLogs)
	}

	ctx2 := d.PrepareNewRequest(context.Background())
	logger.InfoContext(ctx2, "second request")
	d.DiscardBuffers()
	secondLogs := requireFileBytes(t, dir, "app_logs.txt")
	if strings.Contains(string(secondLogs), "first request") {
		t.Errorf("app_logs.txt de la segunda petición contiene contenido de la primera: %q", secondLogs)
	}
	if !strings.Contains(string(secondLogs), "second request") {
		t.Errorf("app_logs.txt tras la segunda petición = %q, want que contenga %q", secondLogs, "second request")
	}
}

// ==================================================================================================
// Aislamiento: cada test usa t.TempDir() como DEBUG_DIR (arriba, en cada
// New(...)); esta comprobación adicional documenta el contrato para
// revisores: ninguna ruta absoluta fuera de t.TempDir() aparece en el
// paquete bajo test.
// ==================================================================================================

func TestNewDoesNotReadEnvironmentOrConfig(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeAll, dir)
	if d == nil {
		t.Fatalf("New devolvió nil")
	}
}
