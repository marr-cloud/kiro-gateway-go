// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package modelsanthropic define los tipos que representan las peticiones y
// respuestas de la API Anthropic Messages (/v1/messages,
// /v1/messages/count_tokens), incluidos los eventos de streaming. Es un port
// literal de kiro/models_anthropic.py (jwadow/kiro-gateway, fijado en el
// commit a5292ca): los modelos Pydantic del original se convierten en
// structs Go sin añadir lógica de validación ni de negocio.
//
// Referencia: https://docs.anthropic.com/en/api/messages
//
// La única pieza con lógica real es ContentBlock (blocks.go), la unión
// polimórfica de bloques de contenido discriminada por "type" — ver el
// comentario de ese fichero. El resto de este fichero son structs planos con
// sus tags json, más AnthropicMessage.UnmarshalJSON, que replica el
// field_validator del original: content puede llegar como cadena o como
// lista de bloques.
//
// model_validator no replicado. AnthropicTool.validate_tool_consistency
// (kiro/models_anthropic.py) exige que las tools sin "type" (definidas por el
// usuario) traigan input_schema. Esa es una regla de VALIDACIÓN de negocio,
// no de forma: este task solo produce tipos, así que la regla queda para la
// fase de converters/validación que consuma AnthropicTool.
package modelsanthropic

import "encoding/json"

// ==================================================================================================
// Modelos de mensaje
// ==================================================================================================

// AnthropicMessage es un mensaje en formato Anthropic: role (user o
// assistant) y content (cadena o lista de bloques de contenido).
type AnthropicMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

// anthropicMessageAux es la forma auxiliar sobre la que se decodifica Role
// tal cual, dejando Content en bruto para decidir si es cadena o lista.
type anthropicMessageAux struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// UnmarshalJSON decodifica un AnthropicMessage replicando el field_validator
// del original: si content es una cadena JSON, se convierte en un único
// ContentBlock{Type: "text", Text: &s}; si es una lista, cada elemento se
// decodifica como ContentBlock (con toda su polimorfía).
func (m *AnthropicMessage) UnmarshalJSON(data []byte) error {
	var aux anthropicMessageAux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	m.Role = aux.Role

	var asString string
	if err := json.Unmarshal(aux.Content, &asString); err == nil {
		m.Content = []ContentBlock{{Type: "text", Text: &asString}}
		return nil
	}

	var blocks []ContentBlock
	if err := json.Unmarshal(aux.Content, &blocks); err != nil {
		return err
	}
	m.Content = blocks
	return nil
}

// ==================================================================================================
// Modelos de tool
// ==================================================================================================

// AnthropicTool es la definición de una tool en formato Anthropic. Soporta
// tanto tools definidas por el usuario (requieren InputSchema) como tools
// del lado del servidor de Anthropic (usan Type, p.ej. "web_search_20250305").
type AnthropicTool struct {
	// Campos de tools del lado del servidor (spec Anthropic).
	Type *string `json:"type,omitempty"`

	// Campos comunes.
	Name        string          `json:"name"`
	Description *string         `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"` // requerido para tools definidas por el usuario

	// Parámetros de tools del lado del servidor (spec Anthropic; se aceptan
	// pero no se imponen).
	MaxUses        *int            `json:"max_uses,omitempty"`
	AllowedDomains []string        `json:"allowed_domains,omitempty"`
	BlockedDomains []string        `json:"blocked_domains,omitempty"`
	UserLocation   json.RawMessage `json:"user_location,omitempty"`
}

// ToolChoiceAuto: el modelo decide si usar tools.
type ToolChoiceAuto struct {
	Type string `json:"type"` // siempre "auto"
}

// ToolChoiceAny: el modelo debe usar al menos una tool.
type ToolChoiceAny struct {
	Type string `json:"type"` // siempre "any"
}

// ToolChoiceTool: el modelo debe usar la tool especificada.
type ToolChoiceTool struct {
	Type string `json:"type"` // siempre "tool"
	Name string `json:"name"`
}

// ==================================================================================================
// Modelos de petición
// ==================================================================================================

// SystemContentBlock es un bloque de contenido de system prompt, usado para
// prompt caching: la API Anthropic soporta system como lista de bloques con
// cache_control opcional.
type SystemContentBlock struct {
	Type         string          `json:"type"` // siempre "text"
	Text         string          `json:"text"`
	CacheControl json.RawMessage `json:"cache_control,omitempty"`
}

// AnthropicMessagesRequest es una petición a la API Anthropic Messages
// (/v1/messages).
//
// System es Union[str, List[SystemContentBlock], List[Dict[str, Any]]] en el
// original: se deja como JSON crudo porque es el converter quien decide su
// forma real, igual que Content en ChatMessage (modelsopenai). Lo mismo
// aplica a Thinking, ToolChoice (Union[ToolChoice, Dict[str, Any]]) y
// Metadata.
type AnthropicMessagesRequest struct {
	Model     string             `json:"model"`
	Messages  []AnthropicMessage `json:"messages"`
	MaxTokens int                `json:"max_tokens"`

	// Parámetros opcionales.
	System json.RawMessage `json:"system,omitempty"`
	Stream bool            `json:"stream"`

	// Extended thinking (parámetro oficial de Anthropic).
	Thinking json.RawMessage `json:"thinking,omitempty"`

	// Tools.
	Tools      []AnthropicTool `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`

	// Parámetros de sampling.
	Temperature *float64 `json:"temperature,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	TopK        *int     `json:"top_k,omitempty"`

	// Otros parámetros.
	StopSequences []string        `json:"stop_sequences,omitempty"`
	Metadata      json.RawMessage `json:"metadata,omitempty"`
}

// AnthropicCountTokensRequest es una petición a la API Anthropic Count
// Tokens (/v1/messages/count_tokens). Similar a AnthropicMessagesRequest
// pero sin parámetros de generación: solo lo que afecta al recuento de
// tokens.
type AnthropicCountTokensRequest struct {
	Model    string             `json:"model"`
	Messages []AnthropicMessage `json:"messages"`

	System json.RawMessage `json:"system,omitempty"`
	Tools  []AnthropicTool `json:"tools,omitempty"`
}

// ==================================================================================================
// Modelos de respuesta
// ==================================================================================================

// AnthropicUsage es la información de uso de tokens en formato Anthropic.
// CacheReadInputTokens y CacheCreationInputTokens solo se reenvían cuando la
// API de Kiro los devuelve explícitamente.
type AnthropicUsage struct {
	InputTokens              int  `json:"input_tokens"`
	OutputTokens             int  `json:"output_tokens"`
	CacheReadInputTokens     *int `json:"cache_read_input_tokens,omitempty"`
	CacheCreationInputTokens *int `json:"cache_creation_input_tokens,omitempty"`
}

// AnthropicMessagesResponse es la respuesta de la API Anthropic Messages
// (no-streaming).
//
// Content es List[Union[ThinkingContentBlock, TextContentBlock,
// ToolUseContentBlock]] en el original: un subconjunto de los tipos de
// bloque que ContentBlock (blocks.go) ya cubre, así que se reutiliza el
// mismo tipo aplanado en vez de definir una unión más estrecha.
type AnthropicMessagesResponse struct {
	ID           string         `json:"id"`
	Type         string         `json:"type"` // siempre "message"
	Role         string         `json:"role"` // siempre "assistant"
	Content      []ContentBlock `json:"content"`
	Model        string         `json:"model"`
	StopReason   *string        `json:"stop_reason,omitempty"`
	StopSequence *string        `json:"stop_sequence,omitempty"`
	Usage        AnthropicUsage `json:"usage"`
}

// ==================================================================================================
// Modelos de eventos de streaming
// ==================================================================================================

// MessageStartEvent se envía al inicio de un stream de mensaje, con el
// objeto de mensaje inicial (content vacío).
type MessageStartEvent struct {
	Type    string         `json:"type"` // siempre "message_start"
	Message map[string]any `json:"message"`
}

// ContentBlockStartEvent se envía al inicio de un bloque de contenido.
type ContentBlockStartEvent struct {
	Type         string         `json:"type"` // siempre "content_block_start"
	Index        int            `json:"index"`
	ContentBlock map[string]any `json:"content_block"`
}

// TextDelta es un delta de contenido de texto.
type TextDelta struct {
	Type string `json:"type"` // siempre "text_delta"
	Text string `json:"text"`
}

// ThinkingDelta es un delta de contenido de thinking.
type ThinkingDelta struct {
	Type     string `json:"type"` // siempre "thinking_delta"
	Thinking string `json:"thinking"`
}

// InputJsonDelta es un delta del JSON de input de una tool.
type InputJsonDelta struct {
	Type        string `json:"type"` // siempre "input_json_delta"
	PartialJSON string `json:"partial_json"`
}

// ContentBlockDeltaEvent se envía cuando se actualiza un bloque de
// contenido. Delta es Union[TextDelta, ThinkingDelta, InputJsonDelta,
// Dict[str, Any]] en el original: en Go se representa como any porque quien
// construye el evento (fase de streaming) sabe qué variante concreta está
// emitiendo, y encoding/json serializa el valor concreto sin necesidad de un
// discriminador propio en este tipo.
type ContentBlockDeltaEvent struct {
	Type  string `json:"type"` // siempre "content_block_delta"
	Index int    `json:"index"`
	Delta any    `json:"delta"`
}

// ContentBlockStopEvent se envía cuando un bloque de contenido se completa.
type ContentBlockStopEvent struct {
	Type  string `json:"type"` // siempre "content_block_stop"
	Index int    `json:"index"`
}

// MessageDeltaUsage es la información de uso en un evento message_delta.
type MessageDeltaUsage struct {
	OutputTokens int `json:"output_tokens"`
}

// MessageDeltaEvent se envía cerca del final del stream con los datos
// finales del mensaje (stop_reason, stop_sequence) y el recuento de tokens
// de salida.
type MessageDeltaEvent struct {
	Type  string            `json:"type"` // siempre "message_delta"
	Delta map[string]any    `json:"delta"`
	Usage MessageDeltaUsage `json:"usage"`
}

// MessageStopEvent se envía al final del stream de mensaje.
type MessageStopEvent struct {
	Type string `json:"type"` // siempre "message_stop"
}

// PingEvent se envía periódicamente para mantener viva la conexión.
type PingEvent struct {
	Type string `json:"type"` // siempre "ping"
}

// ErrorEvent se envía cuando ocurre un error durante el streaming.
type ErrorEvent struct {
	Type  string         `json:"type"` // siempre "error"
	Error map[string]any `json:"error"`
}

// ==================================================================================================
// Modelos de error
// ==================================================================================================

// AnthropicErrorDetail es el detalle de un error en formato Anthropic.
type AnthropicErrorDetail struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// AnthropicErrorResponse es la respuesta de error en formato Anthropic.
type AnthropicErrorResponse struct {
	Type  string               `json:"type"` // siempre "error"
	Error AnthropicErrorDetail `json:"error"`
}
