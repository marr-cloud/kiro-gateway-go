// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"bytes"
	"encoding/json"
	"strings"
)

// --- forma de salida (respuesta Anthropic message, no-streaming) ---

type outThinkingBlock struct {
	Type      string `json:"type"`
	Thinking  string `json:"thinking"`
	Signature string `json:"signature"`
}

type outTextBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type outToolUseBlock struct {
	Type  string          `json:"type"`
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// anthropicUsage refleja usage_payload de collect_anthropic_response
// (streaming_anthropic.py:848-852): input_tokens/output_tokens siempre; los
// campos de caché solo si un evento "usage" los trajo (punteros omitempty,
// mismo criterio que streaminganthropic.messageDeltaUsage).
type anthropicUsage struct {
	InputTokens              int  `json:"input_tokens"`
	OutputTokens             int  `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
}

// messagesResponse es la respuesta única de /v1/messages en modo no-streaming
// (forma Anthropic message). Content es []any porque mezcla bloques thinking/
// text/tool_use con formas distintas; stop_sequence es siempre null
// (streaming_anthropic.py:861). Orden de campos fiel a
// streaming_anthropic.py:854-862.
type messagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"`
	Role         string         `json:"role"`
	Content      []any          `json:"content"`
	Model        string         `json:"model"`
	StopReason   string         `json:"stop_reason"`
	StopSequence *string        `json:"stop_sequence"`
	Usage        anthropicUsage `json:"usage"`
}

// --- acumulador de un bloque de contenido mientras se re-parsea el SSE ---

type blockAcc struct {
	index     int
	blockType string // "thinking" | "text" | "tool_use"
	id        string
	name      string
	signature string
	text      strings.Builder // text_delta / thinking_delta
	partial   strings.Builder // input_json_delta (JSON de input de tool_use)
}

// collectResponse reconstruye una única respuesta Anthropic message a partir
// de los bytes SSE que drivePipeline ya escribió — los MISMOS bytes que
// recibiría un cliente en modo streaming — replicando el diseño de
// routesopenai.collectResponse y de collect_anthropic_response
// (streaming_anthropic.py:721-863): streaming y no-streaming NUNCA pueden
// discrepar porque el segundo consume la salida del primero, no acumuladores
// internos del formatter.
func collectResponse(sseBytes []byte, model string) messagesResponse {
	var (
		messageID    string
		inputTokens  int
		outputTokens int
		stopReason   = "end_turn"
		cacheRead    *int
		cacheCreate  *int
	)

	var order []*blockAcc
	byIndex := map[int]*blockAcc{}

	for _, data := range splitSSEData(sseBytes) {
		var ev sseEventIn
		if err := json.Unmarshal(data, &ev); err != nil {
			continue
		}

		switch ev.Type {
		case "message_start":
			var ms messageStartIn
			if err := json.Unmarshal(ev.Message, &ms); err == nil {
				messageID = ms.ID
				inputTokens = ms.Usage.InputTokens
			}

		case "content_block_start":
			var cb contentBlockIn
			if err := json.Unmarshal(ev.ContentBlock, &cb); err != nil {
				continue
			}
			acc := &blockAcc{index: ev.Index, blockType: cb.Type, id: cb.ID, name: cb.Name, signature: cb.Signature}
			order = append(order, acc)
			byIndex[ev.Index] = acc

		case "content_block_delta":
			acc := byIndex[ev.Index]
			if acc == nil {
				continue
			}
			var d deltaIn
			if err := json.Unmarshal(ev.Delta, &d); err != nil {
				continue
			}
			switch d.Type {
			case "text_delta":
				acc.text.WriteString(d.Text)
			case "thinking_delta":
				acc.text.WriteString(d.Thinking)
			case "input_json_delta":
				acc.partial.WriteString(d.PartialJSON)
			}

		case "message_delta":
			var d messageDeltaIn
			if err := json.Unmarshal(ev.Delta, &d); err == nil && d.StopReason != "" {
				stopReason = d.StopReason
			}
			var u usageIn
			if err := json.Unmarshal(ev.Usage, &u); err == nil {
				outputTokens = u.OutputTokens
				cacheRead = u.CacheReadInputTokens
				cacheCreate = u.CacheCreationInputTokens
			}
		}
	}

	return messagesResponse{
		ID:           messageID,
		Type:         "message",
		Role:         "assistant",
		Content:      buildContentBlocks(order),
		Model:        model,
		StopReason:   stopReason,
		StopSequence: nil,
		Usage: anthropicUsage{
			InputTokens:              inputTokens,
			OutputTokens:             outputTokens,
			CacheReadInputTokens:     cacheRead,
			CacheCreationInputTokens: cacheCreate,
		},
	}
}

// buildContentBlocks convierte los acumuladores (en orden de apertura) en la
// lista de bloques de contenido de la respuesta. Los índices se asignan en
// orden creciente al abrir cada bloque, así que el orden de apertura ES el
// orden final (thinking, luego text, luego tool_use...).
func buildContentBlocks(order []*blockAcc) []any {
	blocks := make([]any, 0, len(order))
	for _, acc := range order {
		switch acc.blockType {
		case "thinking":
			blocks = append(blocks, outThinkingBlock{Type: "thinking", Thinking: acc.text.String(), Signature: acc.signature})
		case "text":
			blocks = append(blocks, outTextBlock{Type: "text", Text: acc.text.String()})
		case "tool_use":
			// input es el JSON acumulado de input_json_delta, ya un objeto
			// JSON válido (streaminganthropic lo emitió con pythonStyleDumps);
			// {} si vino vacío o no parsea (collect_anthropic_response:793-797).
			input := json.RawMessage("{}")
			if raw := strings.TrimSpace(acc.partial.String()); raw != "" && json.Valid([]byte(raw)) {
				input = json.RawMessage(raw)
			}
			blocks = append(blocks, outToolUseBlock{Type: "tool_use", ID: acc.id, Name: acc.name, Input: input})
		}
	}
	return blocks
}

// --- formas de entrada para re-parsear el SSE Anthropic ---

type sseEventIn struct {
	Type         string          `json:"type"`
	Index        int             `json:"index"`
	Message      json.RawMessage `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        json.RawMessage `json:"delta"`
	Usage        json.RawMessage `json:"usage"`
}

type messageStartIn struct {
	ID    string `json:"id"`
	Usage struct {
		InputTokens int `json:"input_tokens"`
	} `json:"usage"`
}

type contentBlockIn struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	Signature string `json:"signature"`
}

type deltaIn struct {
	Type        string `json:"type"`
	Text        string `json:"text"`
	Thinking    string `json:"thinking"`
	PartialJSON string `json:"partial_json"`
}

type messageDeltaIn struct {
	StopReason string `json:"stop_reason"`
}

type usageIn struct {
	OutputTokens             int  `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens"`
}

// splitSSEData extrae el payload JSON de la línea "data: ..." de cada evento
// SSE Anthropic ("event: <name>\ndata: <json>\n\n") separado por "\n\n". El
// tipo real se lee del campo "type" del propio JSON, así que la línea
// "event:" se ignora.
func splitSSEData(sseBytes []byte) [][]byte {
	var out [][]byte
	for _, event := range bytes.Split(sseBytes, []byte("\n\n")) {
		for _, line := range bytes.Split(event, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(bytes.TrimPrefix(line, []byte("data:")))
			if len(data) > 0 {
				out = append(out, data)
			}
		}
	}
	return out
}
