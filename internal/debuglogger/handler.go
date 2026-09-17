// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package debuglogger

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
)

// appLogBuffer es un bytes.Buffer protegido por su propio mutex. Vive fuera
// de DebugLogger (viaja en context.Context, instalado por PrepareNewRequest)
// y dos llamadas independientes lo tocan: Handle (slog.Handler debe ser
// seguro para invocación concurrente, contrato de la interfaz) y el lado de
// flush (writeAppLogsToFile, vía snapshot). bytes.Buffer NO es seguro para
// uso concurrente por sí solo — de ahí el mutex propio, distinto del mu de
// DebugLogger (que solo protege el puntero appLogsBuf, no este contenido).
type appLogBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *appLogBuffer) write(p []byte) {
	b.mu.Lock()
	b.buf.Write(p)
	b.mu.Unlock()
}

// String devuelve el contenido acumulado, con locking — usado por tests.
func (b *appLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// snapshot devuelve una copia del contenido acumulado, con locking —
// writeAppLogsToFile la usa para no retener el lock durante la escritura a
// disco.
func (b *appLogBuffer) snapshot() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.buf.Bytes()...)
}

// logBufferCtxKey: clave de context no exportada bajo la que
// PrepareNewRequest instala el buffer de logs en curso (§6.13).
type logBufferCtxKey struct{}

func contextWithLogBuffer(ctx context.Context, buf *appLogBuffer) context.Context {
	return context.WithValue(ctx, logBufferCtxKey{}, buf)
}

func logBufferFromContext(ctx context.Context) (*appLogBuffer, bool) {
	buf, ok := ctx.Value(logBufferCtxKey{}).(*appLogBuffer)
	return buf, ok && buf != nil
}

// Handler devuelve un slog.Handler que replica el formato de línea del sink
// de loguru original (debug_logger.py:121-127), activo solo si ctx lleva
// el buffer que PrepareNewRequest instaló (por-context, no por-sink-global
// como el original :113-115):
//
//	{YYYY-MM-DD HH:mm:ss.SSS} | {LEVEL:<8} | {origen}:{función}:{línea} | {mensaje}
//
// Es seguro para invocación concurrente (contrato de slog.Handler): Handle
// serializa sus escrituras vía el mutex propio de appLogBuffer. Eso NO hace
// a DebugLogger seguro para peticiones solapadas en general — el lado de
// flush (FlushOnError/DiscardBuffers/writeAppLogsToFile) sigue leyendo el
// campo appLogsBuf vigente de la struct, un único buffer activo a la vez,
// igual que el singleton del original: modo debug no está pensado para
// tráfico de producción concurrente.
func (d *DebugLogger) Handler() slog.Handler {
	return appLogHandler{}
}

// appLogHandler: slog.Handler sin estado (el buffer viaja en ctx).
// WithAttrs/WithGroup son no-ops: loguru solo interpola {message}.
type appLogHandler struct{}

func (h appLogHandler) Enabled(ctx context.Context, _ slog.Level) bool {
	_, ok := logBufferFromContext(ctx)
	return ok
}

func (h appLogHandler) Handle(ctx context.Context, r slog.Record) error {
	buf, ok := logBufferFromContext(ctx)
	if !ok {
		return nil
	}
	origen, función, línea := sourceInfo(r.PC)
	line := fmt.Sprintf("%s | %-8s | %s:%s:%d | %s\n",
		r.Time.Format("2006-01-02 15:04:05.000"), r.Level.String(),
		origen, función, línea, r.Message)
	buf.write([]byte(line))
	return nil
}

func (h appLogHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h appLogHandler) WithGroup(name string) slog.Handler       { return h }

// sourceInfo: origen/función/línea del PC, análogo a {name}:{function}:{line}
// de loguru (:123) — solo la FORMA, no un replay de logs Python.
func sourceInfo(pc uintptr) (origen, función string, línea int) {
	if pc == 0 {
		return "unknown", "unknown", 0
	}
	frames := runtime.CallersFrames([]uintptr{pc})
	frame, _ := frames.Next()

	full := frame.Function
	if idx := strings.LastIndex(full, "/"); idx >= 0 {
		full = full[idx+1:]
	}
	origen = full
	if before, after, found := strings.Cut(full, "."); found {
		origen = before
		función = after
	}
	return origen, función, frame.Line
}
