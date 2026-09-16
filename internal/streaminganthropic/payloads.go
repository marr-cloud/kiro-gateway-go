// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streaminganthropic

import (
	"encoding/json"
	"io"

	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
	"github.com/marr-cloud/kiro-gateway-go/internal/sse"
)

// This file holds the Anthropic SSE event payload shapes and the shared
// write path. Field order in every struct below matches the corresponding
// Python dict literal's key order in .upstream/kiro/streaming_anthropic.py
// verbatim, because encoding/json.Marshal always emits struct fields in
// declaration order and pyjson.Dumps preserves whatever order the bytes it's
// given already have.

// writeEvent marshals payload, reformats it with Python json.dumps(...,
// ensure_ascii=False) separators/escaping parity (internal/pyjson.Dumps),
// and writes the framed SSE event to w. This is the Go equivalent of
// format_sse_event(event_type, data) (streaming_anthropic.py:70-85).
func writeEvent(w io.Writer, eventType string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	formatted, err := pyjson.Dumps(raw)
	if err != nil {
		return err
	}
	_, err = w.Write(sse.FormatEvent(eventType, []byte(formatted)))
	return err
}

// --- message_start (streaming_anthropic.py:206-221) ---

type messageStartData struct {
	Type    string              `json:"type"`
	Message messageStartMessage `json:"message"`
}

type messageStartMessage struct {
	ID           string            `json:"id"`
	Type         string            `json:"type"`
	Role         string            `json:"role"`
	Content      []json.RawMessage `json:"content"`
	Model        string            `json:"model"`
	StopReason   *string           `json:"stop_reason"`
	StopSequence *string           `json:"stop_sequence"`
	Usage        messageStartUsage `json:"usage"`
}

type messageStartUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// --- content_block_start (streaming_anthropic.py:240-247,270-278,386-395,486-495) ---

type contentBlockStartData struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	ContentBlock json.RawMessage `json:"content_block"`
}

type textContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type thinkingContentBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

type toolUseContentBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// --- content_block_delta (streaming_anthropic.py:252-259,282-289,316-323,398-405,499-506) ---

type contentBlockDeltaData struct {
	Type  string          `json:"type"`
	Index int             `json:"index"`
	Delta json.RawMessage `json:"delta"`
}

type textDeltaInner struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type thinkingDeltaInner struct {
	Type     string `json:"type"`
	Thinking string `json:"thinking"`
}

type inputJSONDeltaInner struct {
	Type        string `json:"type"`
	PartialJSON string `json:"partial_json"`
}

// --- content_block_stop (streaming_anthropic.py:230-233,etc.) ---

type blockStopData struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
}

// --- message_delta (streaming_anthropic.py:646-658) ---

type messageDeltaData struct {
	Type  string            `json:"type"`
	Delta messageDeltaDelta `json:"delta"`
	Usage messageDeltaUsage `json:"usage"`
}

type messageDeltaDelta struct {
	StopReason   string  `json:"stop_reason"`
	StopSequence *string `json:"stop_sequence"`
}

// messageDeltaUsage's cache fields are pointers so they're omitted entirely
// (matching Python's dict, which simply lacks the key) unless a "usage"
// KiroEvent supplied a value, mirroring
// usage_payload.update(upstream_cache_usage) (streaming_anthropic.py:646-649).
// Field order (cache_read before cache_creation) matches
// _extract_cache_usage_fields's key_map iteration order
// (streaming_anthropic.py:115-120).
type messageDeltaUsage struct {
	OutputTokens             int  `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
}

// --- message_stop (streaming_anthropic.py:661-663) ---

type messageStopData struct {
	Type string `json:"type"`
}

// --- error (streaming_anthropic.py:706-712) ---

type errorEventData struct {
	Type  string          `json:"type"`
	Error errorEventInner `json:"error"`
}

type errorEventInner struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}
