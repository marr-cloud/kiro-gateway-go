// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streaminganthropic

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/marr-cloud/kiro-gateway-go/internal/parsers"
	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
	"github.com/marr-cloud/kiro-gateway-go/internal/tokenizer"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// This file holds Formatter.Finish, Formatter.EmitError, the bracket-style
// tool-call tail, the per-event-type emit helpers, and small stateless
// helpers shared across formatter.go and this file. Split out of
// formatter.go to stay under the 400-line-per-file budget; there's no
// behavioral boundary here beyond that.

// Finish completes the stream: detects bracket-style tool calls in the
// accumulated text, closes any still-open blocks, computes output_tokens
// and stop_reason, and emits message_delta then message_stop
// (streaming_anthropic.py:526-663).
func (f *Formatter) Finish(w io.Writer) error {
	// Bracket-style tool calls ("[Called func with args: {...}]") detected
	// post-loop in the full accumulated text (streaming_anthropic.py:530-592).
	bracketCalls := parsers.ParseBracketToolCalls(f.fullContent)
	if len(bracketCalls) > 0 {
		if idx, closed := f.blocks.CloseThinking(); closed {
			if err := f.emitBlockStop(idx, w); err != nil {
				return err
			}
		}
		if idx, closed := f.blocks.CloseText(); closed {
			if err := f.emitBlockStop(idx, w); err != nil {
				return err
			}
		}
		for _, tc := range bracketCalls {
			if err := f.emitBracketToolCall(tc, w); err != nil {
				return err
			}
		}
	}

	// Close any block still open after the loop (streaming_anthropic.py:594-608).
	if idx, closed := f.blocks.CloseThinking(); closed {
		if err := f.emitBlockStop(idx, w); err != nil {
			return err
		}
	}
	if idx, closed := f.blocks.CloseText(); closed {
		if err := f.emitBlockStop(idx, w); err != nil {
			return err
		}
	}

	// content_was_truncated: stream ended without a context_usage signal,
	// there's text content, and no tool call happened (a tool call ending
	// the stream is a normal completion signal, not truncation)
	// (streaming_anthropic.py:609-614).
	contentTruncated := !f.receivedContextUsage && len(f.fullContent) > 0 && f.toolBlockCount == 0

	// output_tokens applies the Claude correction factor: count_tokens
	// defaults apply_claude_correction=True, and this call site doesn't
	// override it (streaming_anthropic.py:625, tokenizer.py:77).
	outputTokens := tokenizer.CountTokens(f.fullContent+f.fullThinkingContent, true)

	var stopReason string
	switch {
	case contentTruncated:
		stopReason = "max_tokens"
	case f.toolBlockCount > 0:
		stopReason = "tool_use"
	default:
		stopReason = "end_turn"
	}

	if err := writeEvent(w, "message_delta", messageDeltaData{
		Type:  "message_delta",
		Delta: messageDeltaDelta{StopReason: stopReason, StopSequence: nil},
		Usage: messageDeltaUsage{
			OutputTokens:             outputTokens,
			CacheReadInputTokens:     f.cacheReadInputTokens,
			CacheCreationInputTokens: f.cacheCreationInputTokens,
		},
	}); err != nil {
		return err
	}

	return writeEvent(w, "message_stop", messageStopData{Type: "message_stop"})
}

// ContextCorrectedInputTokens devuelve el input_tokens corregido por
// context_usage para una respuesta NO-streaming, y si la corrección aplica.
// Espeja el override de collect_anthropic_response (streaming_anthropic.py:809-816):
// cuando llegó un evento context_usage con porcentaje > 0 (source "subtraction",
// no "unknown", streaming_core.py:356-362), input = max(0, int(pct/100 *
// maxInput) - output_tokens); si el porcentaje es 0/ausente (source "unknown")
// devuelve false y el llamador conserva la estimación pre-petición del
// message_start.
//
// Solo se usa en no-streaming: el SSE streaming fija input_tokens en
// message_start antes del stream y el protocolo Anthropic no tiene un campo
// posterior para reenviar el valor corregido (upstream también lo calcula en el
// generador de streaming, pero ahí es código muerto — nunca se reenvía).
func (f *Formatter) ContextCorrectedInputTokens() (int, bool) {
	if f.contextUsagePercentage <= 0 {
		return 0, false
	}
	// TODO(fase-6): max_input_tokens real del modelo vía model resolver/cache
	// (model_cache.get_max_input_tokens, streaming_core.py:357); 200000 es el
	// fallback de upstream, el mismo que usa streamingopenai.
	const maxInputTokens = 200000
	// output_tokens idéntico al de Finish (count_tokens con corrección Claude,
	// streaming_anthropic.py:807).
	outputTokens := tokenizer.CountTokens(f.fullContent+f.fullThinkingContent, true)
	total := int((f.contextUsagePercentage / 100.0) * float64(maxInputTokens))
	prompt := total - outputTokens
	if prompt < 0 {
		prompt = 0
	}
	return prompt, true
}

// TruncatedTools devuelve la lista de tool calls que fueron truncados durante
// el stream, recolectados como un side-channel en handleToolUse (Task 8b).
// Cada entrada contiene {ID, Name, TruncationInfo}. Espeja
// streaming_anthropic.py:470-476 (truncated_tools collection).
func (f *Formatter) TruncatedTools() []truncatedToolRecord {
	return f.truncatedTools
}

// FullContent devuelve el contenido completo acumulado durante el stream.
// Solo text content, no thinking (streaming_anthropic.py:609-614).
func (f *Formatter) FullContent() string {
	return f.fullContent
}

// ContentWasTruncated retorna true si la respuesta fue truncada por tamaño.
// Espeja la lógica de streaming_anthropic.py:609-614:
// content_was_truncated = not received_context_usage and len(full_content) > 0
// and len(tool_blocks) == 0.
func (f *Formatter) ContentWasTruncated() bool {
	return !f.receivedContextUsage && len(f.fullContent) > 0 && f.toolBlockCount == 0
}

// EmitError writes the error SSE event upstream emits when an exception
// propagates out of the Kiro stream mid-generation (streaming_anthropic.py:700-712).
// Callers driving the stream should call this — and then stop, without
// calling Finish — when the underlying event source fails with anything
// other than FirstTokenTimeoutError, which upstream re-raises silently with
// no error event (streaming_anthropic.py:695-696). message is the
// exception's str(); an empty string becomes "(empty message)"
// (streaming_anthropic.py:702).
func (f *Formatter) EmitError(w io.Writer, message string) error {
	if message == "" {
		message = "(empty message)"
	}
	return writeEvent(w, "error", errorEventData{
		Type:  "error",
		Error: errorEventInner{Type: "api_error", Message: "Internal error: " + message},
	})
}

// emitBracketToolCall mirrors one iteration of the bracket-tool-calls loop
// (streaming_anthropic.py:550-592). parsers.ParseBracketToolCalls already
// JSON-dumped the arguments once (DumpsASCII, matching parsers.py:142's bare
// json.dumps), so this round-trips through json.loads then re-dumps with
// ensure_ascii=False, exactly like upstream's tool_input handling
// (streaming_anthropic.py:555-559,572).
func (f *Formatter) emitBracketToolCall(tc map[string]any, w io.Writer) error {
	toolID, _ := tc["id"].(string)
	if toolID == "" {
		toolID = generateToolUseID()
	}

	var toolName string
	var argumentsRaw string
	if fn, ok := tc["function"].(map[string]any); ok {
		toolName, _ = fn["name"].(string)
		argumentsRaw, _ = fn["arguments"].(string)
	}

	var toolInput map[string]any
	if argumentsRaw != "" {
		_ = json.Unmarshal([]byte(argumentsRaw), &toolInput)
	}
	if toolInput == nil {
		toolInput = map[string]any{}
	}

	return f.emitToolUseBlock(toolID, toolName, toolInput, w)
}

// --- small emit helpers for individual events ---

func (f *Formatter) emitBlockStop(idx int, w io.Writer) error {
	return writeEvent(w, "content_block_stop", blockStopData{Type: "content_block_stop", Index: idx})
}

func (f *Formatter) emitTextStart(idx int, w io.Writer) error {
	inner, err := json.Marshal(textContentBlock{Type: "text", Text: ""})
	if err != nil {
		return err
	}
	return writeEvent(w, "content_block_start", contentBlockStartData{Type: "content_block_start", Index: idx, ContentBlock: inner})
}

func (f *Formatter) emitThinkingStart(idx int, w io.Writer) error {
	inner, err := json.Marshal(thinkingContentBlock{Type: "thinking", Thinking: "", Signature: f.thinkingSignature})
	if err != nil {
		return err
	}
	return writeEvent(w, "content_block_start", contentBlockStartData{Type: "content_block_start", Index: idx, ContentBlock: inner})
}

func (f *Formatter) emitToolUseStart(idx int, id, name string, w io.Writer) error {
	inner, err := json.Marshal(toolUseContentBlock{Type: "tool_use", ID: id, Name: name, Input: json.RawMessage("{}")})
	if err != nil {
		return err
	}
	return writeEvent(w, "content_block_start", contentBlockStartData{Type: "content_block_start", Index: idx, ContentBlock: inner})
}

func (f *Formatter) emitTextDelta(idx int, text string, w io.Writer) error {
	inner, err := json.Marshal(textDeltaInner{Type: "text_delta", Text: text})
	if err != nil {
		return err
	}
	return writeEvent(w, "content_block_delta", contentBlockDeltaData{Type: "content_block_delta", Index: idx, Delta: inner})
}

func (f *Formatter) emitThinkingDelta(idx int, thinking string, w io.Writer) error {
	inner, err := json.Marshal(thinkingDeltaInner{Type: "thinking_delta", Thinking: thinking})
	if err != nil {
		return err
	}
	return writeEvent(w, "content_block_delta", contentBlockDeltaData{Type: "content_block_delta", Index: idx, Delta: inner})
}

func (f *Formatter) emitInputJSONDelta(idx int, partialJSON string, w io.Writer) error {
	inner, err := json.Marshal(inputJSONDeltaInner{Type: "input_json_delta", PartialJSON: partialJSON})
	if err != nil {
		return err
	}
	return writeEvent(w, "content_block_delta", contentBlockDeltaData{Type: "content_block_delta", Index: idx, Delta: inner})
}

// --- shared helpers ---

// generateToolUseID returns "toolu_<24 hex>", the fallback used when a tool
// call arrives without an id, via the same NewHexToken seam as
// internal/utils's other id generators (streaming_anthropic.py:346,551,789).
func generateToolUseID() string {
	return "toolu_" + utils.NewHexToken()[:24]
}

// pythonStyleDumps mirrors json.dumps(v, ensure_ascii=False): marshal via
// encoding/json (key order doesn't matter here since Go maps have none to
// begin with — the ordering fidelity concern is for nested source JSON,
// not this already-parsed tool_input map), then reformat with pyjson.Dumps
// for Python separators and non-ASCII passthrough.
func pythonStyleDumps(v map[string]any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return pyjson.Dumps(raw)
}

// jsonTruthy reports whether raw JSON bytes represent a Python-truthy
// value, matching the `if request_system:` truthiness check
// (streaming_anthropic.py:176). Kept local rather than shared with
// internal/streamingopenai's identical isJSONTruthy: it's a few lines,
// and importing a sibling dialect package for it would be a stranger
// dependency than duplicating it — see docs/MAPPING.md's task-8 ruling.
func jsonTruthy(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", "false", "0", "0.0", "{}", "[]", `""`:
		return false
	default:
		return true
	}
}

// extractCacheUsageFields mirrors _extract_cache_usage_fields
// (streaming_anthropic.py:101-126): pulls cache_read_input_tokens/
// cacheReadInputTokens and cache_creation_input_tokens/cacheCreationInputTokens
// out of a "usage" event's raw payload. Within each pair, the camelCase
// variant is checked second and so wins if both are present, matching
// Python dict.update() overwrite order under the key_map iteration.
func extractCacheUsageFields(raw json.RawMessage) (cacheRead, cacheCreation *int) {
	if len(raw) == 0 {
		return nil, nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, nil
	}

	if v, ok := firstNumericField(m, "cache_read_input_tokens", "cacheReadInputTokens"); ok {
		cacheRead = &v
	}
	if v, ok := firstNumericField(m, "cache_creation_input_tokens", "cacheCreationInputTokens"); ok {
		cacheCreation = &v
	}
	return cacheRead, cacheCreation
}

// firstNumericField returns the int(value) of the last key in keys that's
// present in m and holds a JSON number, matching Python's
// `if isinstance(value, (int, float)): extracted[target_key] = int(value)`
// applied once per key in key_map order, where a later key for the same
// target overwrites an earlier one.
func firstNumericField(m map[string]json.RawMessage, keys ...string) (int, bool) {
	found := false
	var val int
	for _, k := range keys {
		raw, ok := m[k]
		if !ok {
			continue
		}
		var f float64
		if err := json.Unmarshal(raw, &f); err != nil {
			continue
		}
		val = int(f)
		found = true
	}
	return val, found
}
