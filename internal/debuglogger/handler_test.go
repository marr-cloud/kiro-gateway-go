// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package debuglogger

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// slog.Handler: formato exacto de línea, activación gateada por el buffer
// instalado en el context (debug_logger.py:108-127, spec §6.13), y
// seguridad ante invocación concurrente de Handle (contrato de
// slog.Handler; FIX 1 de la review de Task 4 — bytes.Buffer sin lock).

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

// TestHandlerSequentialRequestsDoNotLeakBuffers verifica aislamiento
// SECUENCIAL: dos PrepareNewRequest consecutivos (sin solape, sin
// goroutines) obtienen buffers de log distintos, así que el contenido de la
// primera "petición" no se filtra en la segunda. Esto NO prueba nada sobre
// concurrencia — DebugLogger es single-flight por diseño (una petición
// activa a la vez, igual que el singleton del original); ver
// TestHandlerConcurrentWritesToSameBufferAreSafe para la garantía de
// concurrencia real que SÍ ofrece el paquete (Handle es seguro para
// invocación concurrente dentro de UNA petición).
func TestHandlerSequentialRequestsDoNotLeakBuffers(t *testing.T) {
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

// TestHandlerConcurrentWritesToSameBufferAreSafe cubre FIX 1 (review de
// Task 4): Handle debe ser seguro para invocación concurrente dentro de UNA
// petición (contrato de slog.Handler), ya que appLogBuffer serializa el
// acceso con su propio mutex. N goroutines escriben contra el MISMO buffer
// (un único ctx/PrepareNewRequest); se verifica que las N líneas llegan
// completas y sin corrupción — sin esto, bytes.Buffer sin lock puede
// entrelazar escrituras o hacer panic bajo -race.
func TestHandlerConcurrentWritesToSameBufferAreSafe(t *testing.T) {
	dir := t.TempDir()
	d := New(ModeAll, dir)
	ctx := d.PrepareNewRequest(context.Background())
	logger := slog.New(d.Handler())

	const n = 50
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			logger.InfoContext(ctx, fmt.Sprintf("concurrent line %d", i))
		}(i)
	}
	wg.Wait()

	buf, ok := logBufferFromContext(ctx)
	if !ok {
		t.Fatalf("no se instaló ningún buffer en el context")
	}
	content := buf.String()

	if got := strings.Count(content, "\n"); got != n {
		t.Errorf("líneas escritas = %d, want %d (posible corrupción/pérdida): %q", got, n, content)
	}
	for i := 0; i < n; i++ {
		want := fmt.Sprintf("concurrent line %d\n", i)
		if !strings.Contains(content, want) {
			t.Errorf("falta la línea %q en el buffer final", want)
		}
	}
}
