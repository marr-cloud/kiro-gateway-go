// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/streaminganthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
)

// newPipeline crea el streamingcore.Pipeline con la config de thinking de cfg.
func (h *Handler) newPipeline() *streamingcore.Pipeline {
	return streamingcore.NewPipeline(h.cfg.FakeReasoningHandling, h.cfg.FakeReasoningInitialBufferSize)
}

// newFormatter crea el streaminganthropic.Formatter para req y le inyecta el
// contexto de la petición original (mensajes/tools/system), que
// EmitMessageStart usa para estimar input_tokens vía
// tokenizer.Count{Message,Tools,System}Tokens (streaming_anthropic.py:176-183).
// El system prompt llega en su campo propio (req.System), no dentro de los
// mensajes — la diferencia clave con el dialecto OpenAI.
func (h *Handler) newFormatter(req *modelsanthropic.AnthropicMessagesRequest) *streaminganthropic.Formatter {
	f := streaminganthropic.New(req.Model, h.thinkingHandling())
	f.SetRequestContext(marshalMessages(req.Messages), marshalTools(req.Tools), req.System)
	return f
}

// marshalMessages serializa cada AnthropicMessage a JSON crudo (equivalente a
// msg.model_dump() del original), preservando el orden de claves para el
// tokenizer.
func marshalMessages(msgs []modelsanthropic.AnthropicMessage) []json.RawMessage {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]json.RawMessage, len(msgs))
	for i, m := range msgs {
		b, err := json.Marshal(m)
		if err != nil {
			b = []byte("{}")
		}
		out[i] = b
	}
	return out
}

// marshalTools serializa cada AnthropicTool a JSON crudo. nil si no hay tools
// (`if request_data.tools else None`, routes_anthropic.py:943).
func marshalTools(tools []modelsanthropic.AnthropicTool) []json.RawMessage {
	if len(tools) == 0 {
		return nil
	}
	out := make([]json.RawMessage, len(tools))
	for i, t := range tools {
		b, err := json.Marshal(t)
		if err != nil {
			b = []byte("{}")
		}
		out[i] = b
	}
	return out
}

// drivePipeline es el bucle explícito D4 (spec §5.4, sin goroutines por
// petición) para el dialecto Anthropic. A diferencia de routesopenai, emite
// primero el evento message_start (streaming_anthropic.py:206-223, el primer
// yield del generador, ANTES del bucle de eventos), luego alimenta cada chunk
// del cuerpo de Kiro al pipeline y cada streamingcore.KiroEvent al formatter, y
// al EOF drena Pipeline.Finish() y llama formatter.Finish(w) — que escribe
// message_delta + message_stop (el stream Anthropic NO termina con
// "data: [DONE]", a diferencia de OpenAI). flusher!=nil hace flush en vivo;
// nil se usa para la agregación no-streaming en un bytes.Buffer.
func drivePipeline(body io.Reader, pipeline *streamingcore.Pipeline, formatter *streaminganthropic.Formatter, w io.Writer, flusher http.Flusher) error {
	if err := formatter.EmitMessageStart(w); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}

	buf := make([]byte, 32*1024)
	for {
		n, readErr := body.Read(buf)
		if n > 0 {
			for _, ev := range pipeline.Feed(buf[:n]) {
				if err := formatter.Handle(ev, w); err != nil {
					return err
				}
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			return readErr
		}
	}

	for _, ev := range pipeline.Finish() {
		if err := formatter.Handle(ev, w); err != nil {
			return err
		}
	}
	if err := formatter.Finish(w); err != nil {
		return err
	}
	if flusher != nil {
		flusher.Flush()
	}
	return nil
}

// serveStreaming atiende el modo streaming: escribe los eventos SSE Anthropic
// (message_start, content_block_*, message_delta, message_stop) directamente en
// w. Cabeceras: routes_anthropic.py:489-496 fija media_type
// "text/event-stream" (Starlette añade "; charset=utf-8") más
// Cache-Control: no-cache y Connection: keep-alive.
func (h *Handler) serveStreaming(w http.ResponseWriter, req *modelsanthropic.AnthropicMessagesRequest, resp *http.Response) {
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	pipeline := h.newPipeline()
	formatter := h.newFormatter(req)

	flusher, _ := w.(http.Flusher)
	// Error a mitad de stream descartado a propósito (punto 4 de la cabecera
	// de handler.go): la conexión ya está arrancada y el fallo probable es la
	// desconexión del cliente.
	_ = drivePipeline(resp.Body, pipeline, formatter, w, flusher)
}

// serveNonStreaming atiende el modo no-streaming: acumula los mismos eventos
// SSE que produciría el modo streaming en un buffer, y reconstruye una única
// respuesta Anthropic message a partir de esos bytes (collectResponse,
// collect.go) — mismo diseño que routesopenai y que
// collect_anthropic_response (streaming_anthropic.py:721-863).
func (h *Handler) serveNonStreaming(w http.ResponseWriter, req *modelsanthropic.AnthropicMessagesRequest, resp *http.Response) {
	defer resp.Body.Close()

	pipeline := h.newPipeline()
	formatter := h.newFormatter(req)

	var buf bytes.Buffer
	if err := drivePipeline(resp.Body, pipeline, formatter, &buf, nil); err != nil {
		writeAnthropicError(w, http.StatusInternalServerError, "api_error", "error reading Kiro stream: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, collectResponse(buf.Bytes(), req.Model))
}
