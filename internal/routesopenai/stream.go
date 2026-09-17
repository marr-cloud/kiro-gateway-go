// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
	"github.com/marr-cloud/kiro-gateway-go/internal/streamingopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationrecovery"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// newPipeline crea el streamingcore.Pipeline con la configuración de
// thinking de cfg (FAKE_REASONING_HANDLING, FAKE_REASONING_INITIAL_BUFFER_SIZE).
func (h *Handler) newPipeline() *streamingcore.Pipeline {
	return streamingcore.NewPipeline(h.cfg.FakeReasoningHandling, h.cfg.FakeReasoningInitialBufferSize)
}

// newFormatter crea el streamingopenai.Formatter para req y le inyecta el
// contexto de la petición original (mensajes/tools), que streaming_openai.py:317-323
// usa como fallback para prompt_tokens cuando Kiro no manda
// context_usage_percentage.
func (h *Handler) newFormatter(req *modelsopenai.ChatCompletionRequest) *streamingopenai.Formatter {
	f := streamingopenai.New(req.Model, h.thinkingHandling())
	f.SetRequestContext(marshalMessages(req.Messages), marshalTools(req.Tools))
	return f
}

// marshalMessages serializa cada ChatMessage a JSON crudo, preservando el
// orden de claves del struct (equivalente a msg.model_dump() del original,
// usado como entrada de tokenizer.CountMessageTokens).
func marshalMessages(msgs []modelsopenai.ChatMessage) []json.RawMessage {
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

// marshalTools serializa cada Tool a JSON crudo. nil si no hay tools —
// tokenizer.CountToolsTokens(nil, ...) devuelve 0, igual que el
// `if request_data.tools else None` del original.
func marshalTools(tools []modelsopenai.Tool) []json.RawMessage {
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

// drivePipeline es el bucle explícito D4 (spec §5.4, sin goroutines ni
// canales por petición): lee body en un bucle plano, alimenta cada chunk al
// pipeline y cada streamingcore.KiroEvent resultante al formatter, que
// escribe los bytes SSE en w. Si flusher no es nil, hace flush tras cada
// lectura (streaming en vivo); nil se usa para la agregación no-streaming
// (bytes.Buffer no necesita flush).
//
// Al EOF, drena streamingcore.Pipeline.Finish() por el mismo camino, y
// termina con formatter.Finish(w), que YA escribe el chunk final (con
// usage) y el terminador "data: [DONE]\n\n"
// (internal/streamingopenai/formatter.go:112-153) — no se vuelve a escribir
// sse.FormatDone() aparte, a diferencia de lo que sugiere la prosa del
// brief de esta tarea (que describe un tercer paso separado); hacerlo
// duplicaría el terminador SSE que el cliente recibe.
func drivePipeline(body io.Reader, pipeline *streamingcore.Pipeline, formatter *streamingopenai.Formatter, w io.Writer, flusher http.Flusher) error {
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

// serveStreaming atiende el modo streaming: escribe chat.completion.chunk
// SSE directamente en w a medida que llegan. Port de
// routes_openai.py:373-421 (rama account-system).
//
// Cabeceras: routes_openai.py:421 solo fija media_type="text/event-stream"
// en el StreamingResponse; Starlette añade "; charset=utf-8" porque el
// media_type empieza por "text/". A diferencia de routes_anthropic.py:493
// (que sí fija Cache-Control/Connection), routes_openai.py NO los fija —
// deliberadamente fiel al original, sin añadir cabeceras extra.
// Task 8b (SAVE side): después de cerrar el stream (formatter.Finish),
// persiste la información de truncación en truncationstate si
// ShouldInjectRecovery es true (routes_openai.py:266-285).
func (h *Handler) serveStreaming(w http.ResponseWriter, req *modelsopenai.ChatCompletionRequest, resp *http.Response) {
	defer resp.Body.Close()

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")

	pipeline := h.newPipeline()
	formatter := h.newFormatter(req)

	flusher, _ := w.(http.Flusher)
	// La desconexión del cliente (D4, spec §5.4) se detecta por el error
	// que devuelve w.Write dentro de drivePipeline/formatter.Handle; no hay
	// nada útil que responder al cliente en ese punto (la conexión ya se
	// perdió). El error SÍ se captura (en lugar de descartarse con `_ =`)
	// porque gatea el bloque de SAVE de abajo: en un fallo real a mitad de
	// stream, Finish() nunca corre pero formatter.TruncatedTools() ya puede
	// tener herramientas acumuladas de eventos previos, y guardarlas
	// persistiría datos parciales que upstream nunca persiste (su generador
	// sale por GeneratorExit y la sección de guardado -
	// streaming_openai.py:266-285 - nunca se alcanza en ese camino). Solo se
	// sigue sin propagar el error al cliente (la respuesta ya está
	// parcialmente escrita); lo único que cambia es que el SAVE se salta.
	err := drivePipeline(resp.Body, pipeline, formatter, w, flusher)

	// Task 8b (SAVE side): persist truncation info after stream closes
	// (routes_openai.py:266-285). The gate is checked here, not in
	// the formatter, because this is the save side — on the inject side
	// (truncationinject.go) the gate check is unconditional.
	if err == nil && truncationrecovery.ShouldInjectRecovery(converterscore.TruncationRecoveryEnabled) {
		// Save each truncated tool call (streaming_openai.py:366-383)
		for _, truncTool := range formatter.TruncatedTools() {
			h.truncation.SetTool(truncTool.ID, truncationstate.ToolRecord{
				ToolName:       truncTool.Name,
				TruncationInfo: truncTool.TruncationInfo,
			})
		}

		// Save content truncation if the stream ended without usage signal
		// and contained text (streaming_openai.py:271-274,266-285)
		if formatter.ContentWasTruncated() {
			h.truncation.SetContent(formatter.FullContent(), truncationstate.ContentRecord{
				MessageHash: computeMessageHash(formatter.FullContent()),
			})
		}
	}
}

// computeMessageHash computes the SHA256 hash of the first 500 characters
// of content, matching internal/truncationstate/cache.go's hashContentKey.
// This is purely for logging parity with upstream (routes_openai.py:227);
// the truncationstate.State.SetContent call computes the hash internally.
func computeMessageHash(content string) string {
	if len(content) > 500 {
		content = content[:500]
	}
	return truncationstate.ComputeContentHash(content)
}

// serveNonStreaming atiende el modo no-streaming: acumula los mismos
// eventos que produciría el modo streaming en un buffer en memoria, y
// reconstruye una única respuesta chat.completion a partir de esos bytes
// SSE — ver collectResponse (collect.go) para el porqué de este diseño
// (mismo que collect_stream_response del original). Port de
// routes_openai.py:423-441 (rama account-system).
func (h *Handler) serveNonStreaming(w http.ResponseWriter, req *modelsopenai.ChatCompletionRequest, resp *http.Response) {
	defer resp.Body.Close()

	pipeline := h.newPipeline()
	formatter := h.newFormatter(req)

	var buf bytes.Buffer
	if err := drivePipeline(resp.Body, pipeline, formatter, &buf, nil); err != nil {
		writeOpenAIError(w, http.StatusInternalServerError, "error reading Kiro stream: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, collectResponse(buf.Bytes(), req.Model))
}
