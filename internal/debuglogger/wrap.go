// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package debuglogger

import (
	"io"
	"net/http"
)

// RawBody envuelve el cuerpo de la respuesta de Kiro para que cada lectura
// pase por LogRawChunk, como el bucle de streaming_core.py:171,181. Con el
// logger apagado (o nil) devuelve body tal cual.
func (d *DebugLogger) RawBody(body io.ReadCloser) io.ReadCloser {
	if !d.isEnabled() {
		return body
	}
	return &rawBody{ReadCloser: body, d: d}
}

type rawBody struct {
	io.ReadCloser
	d *DebugLogger
}

func (b *rawBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.d.LogRawChunk(p[:n])
	}
	return n, err
}

// ModifiedWriter envuelve la respuesta al cliente para que cada escritura pase
// por LogModifiedChunk. El original solo registra los deltas de texto de la
// ruta OpenAI (streaming_openai.py:154,183,252); aquí se registra todo lo que
// sale, en ambas rutas (DIFFERENCES §19). Con el logger apagado (o nil)
// devuelve w tal cual.
func (d *DebugLogger) ModifiedWriter(w http.ResponseWriter) http.ResponseWriter {
	if !d.isEnabled() {
		return w
	}
	return &modifiedWriter{ResponseWriter: w, d: d}
}

type modifiedWriter struct {
	http.ResponseWriter
	d *DebugLogger
}

func (w *modifiedWriter) Write(p []byte) (int, error) {
	n, err := w.ResponseWriter.Write(p)
	if n > 0 {
		w.d.LogModifiedChunk(p[:n])
	}
	return n, err
}

// Flush mantiene el streaming en vivo: los handlers hacen w.(http.Flusher).
func (w *modifiedWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap deja que http.ResponseController llegue al writer real.
func (w *modifiedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
