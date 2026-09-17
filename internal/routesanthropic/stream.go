// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/streaminganthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationrecovery"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
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
// Task 8b (SAVE side): después de cerrar el stream (formatter.Finish),
// persiste la información de truncación en truncationstate si
// ShouldInjectRecovery es true (routes_anthropic.py:665-687).
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
	// desconexión del cliente. El error SÍ se captura (en lugar de
	// descartarse con `_ =`) porque gatea el bloque de SAVE de abajo: en un
	// fallo real a mitad de stream (p.ej. w.Write falla porque el cliente se
	// desconectó), Finish() nunca corre pero formatter.TruncatedTools() ya
	// puede tener herramientas acumuladas de eventos previos, y guardarlas
	// persistiría datos parciales que upstream nunca persiste (su generador
	// sale por GeneratorExit y la sección de guardado -
	// streaming_anthropic.py:665-687 - nunca se alcanza en ese camino). Solo
	// se sigue sin propagar el error al cliente (la respuesta ya está
	// parcialmente escrita); lo único que cambia es que el SAVE se salta.
	err := drivePipeline(resp.Body, pipeline, formatter, w, flusher)

	// Task 8b (SAVE side): persist truncation info after stream closes
	// (streaming_anthropic.py:665-687). The gate is checked here, not in
	// the formatter, because this is the save side — on the inject side
	// (truncationinject.go) the gate check is unconditional
	// (routes_anthropic.py:156-244 doesn't call should_inject_recovery()).
	if err == nil && truncationrecovery.ShouldInjectRecovery(converterscore.TruncationRecoveryEnabled) {
		// Save each truncated tool call (streaming_anthropic.py:467-472)
		for _, truncTool := range formatter.TruncatedTools() {
			h.truncation.SetTool(truncTool.ID, truncationstate.ToolRecord{
				ToolName:       truncTool.Name,
				TruncationInfo: truncTool.TruncationInfo,
			})
		}

		// Save content truncation if the stream ended without context_usage
		// signal and contained text (streaming_anthropic.py:609-614,673-687)
		if formatter.ContentWasTruncated() {
			h.truncation.SetContent(formatter.FullContent(), truncationstate.ContentRecord{
				MessageHash: computeMessageHash(formatter.FullContent()),
			})
		}
	}
}

// computeMessageHash computes the SHA256 hash of the first 500 characters
// of content, matching internal/truncationstate/cache.go's hashContentKey.
// This is purely for logging parity with upstream (routes_anthropic.py:242);
// the truncationstate.State.SetContent call computes the hash internally.
func computeMessageHash(content string) string {
	if len(content) > 500 {
		content = content[:500]
	}
	return truncationstate.ComputeContentHash(content)
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

	out := collectResponse(buf.Bytes(), req.Model)
	// Override de input_tokens con el valor derivado de context_usage cuando
	// Kiro lo reportó (el caso común): collect_anthropic_response usa el número
	// informado por Kiro, no la estimación pre-petición del message_start
	// (streaming_anthropic.py:809-816). Solo aplica en no-streaming.
	if v, ok := formatter.ContextCorrectedInputTokens(); ok {
		out.Usage.InputTokens = v
	}
	writeJSON(w, http.StatusOK, out)
}
