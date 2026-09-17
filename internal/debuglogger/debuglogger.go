// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package debuglogger implementa DebugLogger, port de
// .upstream/kiro/debug_logger.py: logger de depuración por petición con 3
// modos (off/errors/all). Paquete hoja: config por parámetro, sin os.Getenv.
package debuglogger

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// Mode es DEBUG_MODE (config.py); New no valida el valor recibido.
type Mode string

const (
	ModeOff    Mode = "off"    // deshabilita el logging por completo.
	ModeErrors Mode = "errors" // acumula en memoria; vuelca solo en FlushOnError.
	ModeAll    Mode = "all"    // escribe a disco de inmediato en cada llamada.
)

// DebugLogger es el port de DebugLogger (debug_logger.py:45-53), sin el
// singleton de proceso; mu protege solo los 5 buffers mutables.
type DebugLogger struct {
	mode Mode
	dir  string

	mu                 sync.Mutex
	requestBodyBuf     []byte
	kiroRequestBodyBuf []byte
	rawChunksBuf       bytes.Buffer
	modifiedChunksBuf  bytes.Buffer
	appLogsBuf         *bytes.Buffer
}

// New crea un DebugLogger. Port de __init__ (:62-76), sin el singleton __new__.
func New(mode Mode, dir string) *DebugLogger {
	return &DebugLogger{mode: mode, dir: dir, appLogsBuf: &bytes.Buffer{}}
}

func (d *DebugLogger) isEnabled() bool { // _is_enabled, debug_logger.py:78-80
	return d.mode == ModeErrors || d.mode == ModeAll
}

func (d *DebugLogger) isImmediateWrite() bool { // _is_immediate_write, :82-84
	return d.mode == ModeAll
}

// PrepareNewRequest limpia buffers, instala un buffer de logs fresco en el
// context devuelto (para Handler()) y, en modo "all", recrea DEBUG_DIR.
// Port de prepare_new_request (debug_logger.py:129-154). Adición sobre el
// brief de la Task 4 (no listada ahí), necesaria para que Task 5 instale el
// buffer en el context — ver ledger en el informe.
func (d *DebugLogger) PrepareNewRequest(ctx context.Context) context.Context {
	if !d.isEnabled() {
		return ctx
	}
	d.clearBuffers()

	d.mu.Lock()
	buf := d.appLogsBuf
	d.mu.Unlock()
	ctx = contextWithLogBuffer(ctx, buf)

	if d.isImmediateWrite() {
		if err := d.recreateDir(); err != nil {
			logErr("Error preparing directory", err)
		}
	}
	return ctx
}

// LogRequestBody: log_request_body (debug_logger.py:156-170).
func (d *DebugLogger) LogRequestBody(body []byte) {
	d.logBody(&d.requestBodyBuf, "request_body", "request_body.json", body)
}

// LogKiroRequestBody: log_kiro_request_body (debug_logger.py:172-186).
func (d *DebugLogger) LogKiroRequestBody(body []byte) {
	d.logBody(&d.kiroRequestBodyBuf, "kiro_request_body", "kiro_request_body.json", body)
}

// logBody: patrón write-inmediato/buffer común a LogRequestBody/LogKiroRequestBody.
func (d *DebugLogger) logBody(buf *[]byte, label, filename string, body []byte) {
	if !d.isEnabled() {
		return
	}
	if d.isImmediateWrite() {
		d.writeJSONOrRaw(label, filename, body)
		return
	}
	d.mu.Lock()
	*buf = append([]byte(nil), body...)
	d.mu.Unlock()
}

// LogRawChunk: log_raw_chunk (debug_logger.py:188-202).
func (d *DebugLogger) LogRawChunk(chunk []byte) {
	d.logChunk(&d.rawChunksBuf, "response_stream_raw.txt", chunk)
}

// LogModifiedChunk: log_modified_chunk (debug_logger.py:204-218).
func (d *DebugLogger) LogModifiedChunk(chunk []byte) {
	d.logChunk(&d.modifiedChunksBuf, "response_stream_modified.txt", chunk)
}

// logChunk: patrón write-inmediato/buffer común a LogRawChunk/LogModifiedChunk.
func (d *DebugLogger) logChunk(buf *bytes.Buffer, filename string, chunk []byte) {
	if !d.isEnabled() {
		return
	}
	if d.isImmediateWrite() {
		d.appendToFile(filename, chunk)
		return
	}
	d.mu.Lock()
	buf.Write(chunk)
	d.mu.Unlock()
}

// LogErrorInfo escribe error_info.json. Port de log_error_info (:220-249).
func (d *DebugLogger) LogErrorInfo(statusCode int, errorMessage string) {
	if !d.isEnabled() {
		return
	}
	if err := d.writeErrorInfo(statusCode, errorMessage); err != nil {
		logErr("Error writing error_info", err)
	}
}

// FlushOnError vuelca a DEBUG_DIR en caso de error. Port de flush_on_error
// (debug_logger.py:251-316). "all": ya se escribió todo, solo añade
// error_info+app_logs. "errors": no-op completo si NINGÚN buffer tiene
// contenido (:272-279, ANTES del try/finally); si hay algo, recrea
// DEBUG_DIR, escribe y limpia buffers al terminar (`finally`).
func (d *DebugLogger) FlushOnError(statusCode int, errorMessage string) {
	if !d.isEnabled() {
		return
	}
	if d.isImmediateWrite() {
		d.LogErrorInfo(statusCode, errorMessage)
		d.writeAppLogsToFile()
		d.clearAppLogsBuffer()
		return
	}

	d.mu.Lock()
	hasData := len(d.requestBodyBuf) > 0 || len(d.kiroRequestBodyBuf) > 0 ||
		d.rawChunksBuf.Len() > 0 || d.modifiedChunksBuf.Len() > 0
	d.mu.Unlock()
	if !hasData {
		return
	}
	defer d.clearBuffers()

	if err := d.recreateDir(); err != nil {
		logErr("Error flushing buffers", err)
		return
	}

	d.mu.Lock()
	reqBody := d.requestBodyBuf
	kiroBody := d.kiroRequestBodyBuf
	rawChunks := append([]byte(nil), d.rawChunksBuf.Bytes()...)
	modChunks := append([]byte(nil), d.modifiedChunksBuf.Bytes()...)
	d.mu.Unlock()

	if len(reqBody) > 0 {
		d.writeJSONOrRaw("request_body", "request_body.json", reqBody)
	}
	if len(kiroBody) > 0 {
		d.writeJSONOrRaw("kiro_request_body", "kiro_request_body.json", kiroBody)
	}
	if !d.writeIfNonEmpty("response_stream_raw.txt", rawChunks) {
		return
	}
	if !d.writeIfNonEmpty("response_stream_modified.txt", modChunks) {
		return
	}

	d.LogErrorInfo(statusCode, errorMessage)
	d.writeAppLogsToFile()
}

// writeIfNonEmpty escribe data si no está vacío; false (tras loguear) si
// falla, para que FlushOnError corte la secuencia (excepción sin capturar).
func (d *DebugLogger) writeIfNonEmpty(filename string, data []byte) bool {
	if len(data) == 0 {
		return true
	}
	if err := os.WriteFile(filepath.Join(d.dir, filename), data, 0o644); err != nil {
		logErr("Error flushing buffers", err)
		return false
	}
	return true
}

// DiscardBuffers: discard_buffers (:318-330). "errors" limpia todo; "all"
// ADEMÁS escribe app_logs.txt antes de limpiar ese buffer.
func (d *DebugLogger) DiscardBuffers() {
	switch d.mode {
	case ModeErrors:
		d.clearBuffers()
	case ModeAll:
		d.writeAppLogsToFile()
		d.clearAppLogsBuffer()
	}
}

// logErr reporta un fallo de depuración vía slog.Default(), con el mismo
// prefijo "[DebugLogger] " del original.
func logErr(prefix string, err error) {
	slog.Default().Error(fmt.Sprintf("[DebugLogger] %s: %v", prefix, err))
}

// clearBuffers: _clear_buffers (debug_logger.py:86-92).
func (d *DebugLogger) clearBuffers() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.requestBodyBuf = nil
	d.kiroRequestBodyBuf = nil
	d.rawChunksBuf.Reset()
	d.modifiedChunksBuf.Reset()
	d.appLogsBuf = &bytes.Buffer{}
}

// clearAppLogsBuffer: _clear_app_logs_buffer (debug_logger.py:94-106), sin
// el retiro de sink de loguru — Handler consulta el buffer vía context.
func (d *DebugLogger) clearAppLogsBuffer() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.appLogsBuf = &bytes.Buffer{}
}

// writeAppLogsToFile: _write_app_logs_to_file (:380-399); traga CUALQUIER
// error en silencio a propósito ("avoid recursion", comentario original).
func (d *DebugLogger) writeAppLogsToFile() {
	d.mu.Lock()
	var content []byte
	if d.appLogsBuf != nil {
		content = append([]byte(nil), d.appLogsBuf.Bytes()...)
	}
	d.mu.Unlock()

	if len(bytes.TrimSpace(content)) == 0 {
		return
	}
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(d.dir, "app_logs.txt"), content, 0o644)
}

// recreateDir borra y recrea DEBUG_DIR (:148-151, :283-285).
func (d *DebugLogger) recreateDir() error {
	if err := os.RemoveAll(d.dir); err != nil {
		return err
	}
	return os.MkdirAll(d.dir, 0o755)
}

// writeJSONOrRaw: si body es JSON válido lo reindenta a 2 espacios (sin
// decodificar, a diferencia del json.loads/json.dump original — preserva
// el orden de claves sin reconstruirlo a mano); si no, bytes crudos. Port
// de _write_request_body_to_file/_write_kiro_request_body_to_file (:334-360).
func (d *DebugLogger) writeJSONOrRaw(label, filename string, body []byte) {
	path := filepath.Join(d.dir, filename)
	data := body
	if json.Valid(body) {
		var buf bytes.Buffer
		if err := json.Indent(&buf, body, "", "  "); err == nil {
			data = buf.Bytes()
		}
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		logErr(fmt.Sprintf("Error writing %s", label), err)
	}
}

// appendToFile: _append_raw/_append_modified_chunk_to_file (:362-378).
func (d *DebugLogger) appendToFile(filename string, chunk []byte) {
	f, err := os.OpenFile(filepath.Join(d.dir, filename), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(chunk)
}

// errorInfo: forma del dict de log_error_info (:239-242); un struct
// preserva el orden de campos, a diferencia de map[string]any.
type errorInfo struct {
	StatusCode   int    `json:"status_code"`
	ErrorMessage string `json:"error_message"`
}

// writeErrorInfo: log_error_info (:235-247), reutilizado por LogErrorInfo.
func (d *DebugLogger) writeErrorInfo(statusCode int, errorMessage string) error {
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return err
	}
	data, err := marshalIndentNoEscape(errorInfo{StatusCode: statusCode, ErrorMessage: errorMessage})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(d.dir, "error_info.json"), data, 0o644)
}

// marshalIndentNoEscape: v indentado a 2 espacios, sin escapado HTML
// (Python ensure_ascii=False nunca escapa) ni salto de línea final.
func marshalIndentNoEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// logBufferCtxKey: clave de context no exportada bajo la que
// PrepareNewRequest instala el buffer de logs en curso (§6.13).
type logBufferCtxKey struct{}

func contextWithLogBuffer(ctx context.Context, buf *bytes.Buffer) context.Context {
	return context.WithValue(ctx, logBufferCtxKey{}, buf)
}

func logBufferFromContext(ctx context.Context) (*bytes.Buffer, bool) {
	buf, ok := ctx.Value(logBufferCtxKey{}).(*bytes.Buffer)
	return buf, ok && buf != nil
}

// Handler devuelve un slog.Handler que replica el formato de línea del sink
// de loguru original (debug_logger.py:121-127), activo solo si ctx lleva
// el buffer que PrepareNewRequest instaló (por-context, no por-sink-global
// como el original :113-115, así que peticiones concurrentes no se mezclan):
//
//	{YYYY-MM-DD HH:mm:ss.SSS} | {LEVEL:<8} | {origen}:{función}:{línea} | {mensaje}
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
	fmt.Fprintf(buf, "%s | %-8s | %s:%s:%d | %s\n",
		r.Time.Format("2006-01-02 15:04:05.000"), r.Level.String(),
		origen, función, línea, r.Message)
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
	if dot := strings.Index(full, "."); dot >= 0 {
		origen = full[:dot]
		función = full[dot+1:]
	}
	return origen, función, frame.Line
}
