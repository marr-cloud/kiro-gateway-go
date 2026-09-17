// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingopenai

import (
	"io"

	"github.com/marr-cloud/kiro-gateway-go/internal/parsers"
	"github.com/marr-cloud/kiro-gateway-go/internal/sse"
	"github.com/marr-cloud/kiro-gateway-go/internal/tokenizer"
)

// Finish completes the stream by emitting any pending tool calls, the final chunk with usage,
// and the [DONE] marker. Also stores the computed contentWasTruncated value for later
// access via the ContentWasTruncated() accessor (Task 8b).
func (f *Formatter) Finish(w io.Writer) error {
	// Bracket-style tool calls ("[Called fn with args: {...}]") detected
	// post-loop in the accumulated content, merged with the stream's tool
	// calls and deduplicated (streaming_openai.py:276-279). NOTE: the OpenAI
	// upstream applies deduplicate_tool_calls here; the streaminganthropic
	// twin does NOT (its upstream has no dedup step), so the two dialects
	// legitimately differ on this line. dedup runs unconditionally, matching
	// upstream, even when there are no bracket calls.
	bracketCalls := parsers.ParseBracketToolCalls(f.fullContent)
	merged := make([]map[string]any, 0, len(f.toolCallsFromStream)+len(bracketCalls))
	merged = append(merged, f.toolCallsFromStream...)
	merged = append(merged, bracketCalls...)
	allToolCalls := parsers.DeduplicateToolCalls(merged)

	// Emit tool calls if any (stream + bracket, deduplicated)
	if len(allToolCalls) > 0 {
		if err := f.emitToolCallsChunk(allToolCalls, w); err != nil {
			return err
		}
	}

	// completion_tokens uses the Claude correction factor by default, matching
	// `count_tokens(full_content + full_thinking_content)` (streaming_openai.py:305),
	// which calls count_tokens with apply_claude_correction defaulting to True.
	completionTokens := tokenizer.CountTokens(f.fullContent+f.fullThinkingContent, true)

	// stream_completed_normally = received_usage or received_context_usage
	// (streaming_openai.py:272-274).
	streamCompletedNormally := len(f.creditsUsedRaw) > 0 || f.receivedContextUsage
	// Task 8b: Use allToolCalls (stream + bracket, deduped), not just toolCallsFromStream.
	// This is the CORRECT computation that matches the Finish logic; store it for the
	// accessor to return (streaming_openai.py:282-286).
	contentTruncated := !streamCompletedNormally && len(f.fullContent) > 0 && len(allToolCalls) == 0
	f.contentWasTruncated = contentTruncated

	var finishReason string
	switch {
	case contentTruncated:
		finishReason = "length"
	case len(allToolCalls) > 0:
		finishReason = "tool_calls"
	default:
		finishReason = "stop"
	}

	promptTokens, totalTokens := f.calculateTokens(completionTokens)

	// Emit final chunk with usage
	if err := f.emitFinalChunk(finishReason, promptTokens, completionTokens, totalTokens, w); err != nil {
		return err
	}

	// Emit [DONE]
	if _, err := w.Write(sse.FormatDone()); err != nil {
		return err
	}

	return nil
}

// TruncatedTools devuelve la lista de tool calls que fueron truncados durante
// el stream, recolectados como un side-channel en Handle (Task 8b).
// Cada entrada contiene {ID, Name, TruncationInfo}. Espeja
// streaming_openai.py:366-383 (truncated_tools collection).
func (f *Formatter) TruncatedTools() []truncatedToolRecord {
	return f.truncatedTools
}

// FullContent devuelve el contenido completo acumulado durante el stream.
// Solo text content, no thinking (streaming_openai.py:304-305).
func (f *Formatter) FullContent() string {
	return f.fullContent
}

// ContentWasTruncated retorna true si la respuesta fue truncada por tamaño.
// Returns the value computed by Finish(), which uses allToolCalls (stream +
// bracket, deduped) instead of just toolCallsFromStream (Task 8b). Must be
// called AFTER Finish() (streaming_openai.py:282-286).
func (f *Formatter) ContentWasTruncated() bool {
	return f.contentWasTruncated
}
