// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package modelsopenai define los tipos que representan las peticiones y
// respuestas de la API compatible con OpenAI Chat Completions. Es un port
// literal de kiro/models_openai.py (jwadow/kiro-gateway, fijado en el commit
// a5292ca): los modelos Pydantic del original se convierten en structs Go sin
// añadir lógica de validación ni de negocio, que corresponde a fases
// posteriores (converters, validationerrors).
//
// Opcionalidad. Todo campo `Optional[T]` del original se representa como
// puntero o slice nil-able en Go, para distinguir "ausente" de "presente con
// el valor cero". Los campos requeridos, o los que llevan un valor por
// defecto no-None (p.ej. `prompt_tokens: int = 0`), se representan como el
// tipo base: el cero de Go coincide con el default de Python en esos casos.
//
// Contenido polimórfico. `content` en ChatMessage puede ser una cadena, una
// lista de bloques, o cualquier otra cosa según el cliente
// (`Optional[Union[str, List[Any], Any]]`); igual que la interfaz de este
// task exige, se deja como json.RawMessage: quien lo desempaqueta es el
// converter (fase 2 de esta feature), no el modelo. Lo mismo aplica a
// `tool_calls`, `stop`, `tool_choice` y a cualquier `Dict[str, Any]` que
// llegue en una petición y deba reenviarse sin perder bytes.
//
// Extra="allow". Los modelos Pydantic del original declaran
// `model_config = {"extra": "allow"}`: aceptan campos desconocidos sin fallar
// la validación. encoding/json hace lo mismo por defecto al decodificar en un
// struct (los campos que no tienen un tag correspondiente se ignoran sin
// error), así que no hace falta ningún mecanismo adicional para igualar ese
// comportamiento en la decodificación. Lo que el original SÍ conserva y este
// port no es la re-emisión de esos campos desconocidos al volver a
// serializar: no hay ningún caso del corpus que dependa de eso para estos
// tipos (a diferencia de modelsanthropic.ContentBlock, que sí lo necesita y
// por eso lleva un campo Raw).
package modelsopenai

import "encoding/json"

// ==================================================================================================
// Modelos para el endpoint /v1/models
// ==================================================================================================

// OpenAIModel describe un modelo de IA en formato OpenAI. Se usa en la
// respuesta del endpoint /v1/models.
//
// El original rellena Created con Field(default_factory=lambda:
// int(time.time())) al construir el objeto; este port no reproduce ese
// default automático porque es lógica de construcción, no de tipo — la fase
// que construya la respuesta debe fijar Created explícitamente.
type OpenAIModel struct {
	ID          string  `json:"id"`
	Object      string  `json:"object"` // por defecto en el original: "model"
	Created     int64   `json:"created"`
	OwnedBy     string  `json:"owned_by"` // por defecto en el original: "anthropic"
	Description *string `json:"description,omitempty"`
}

// ModelList es la lista de modelos en formato OpenAI: la respuesta completa
// de GET /v1/models.
type ModelList struct {
	Object string        `json:"object"` // por defecto en el original: "list"
	Data   []OpenAIModel `json:"data"`
}

// ==================================================================================================
// Modelos para el endpoint /v1/chat/completions
// ==================================================================================================

// ChatMessage es un mensaje de chat en formato OpenAI. Soporta varios roles
// (user, assistant, system, tool) y varias formas de content (cadena, lista,
// objeto), por lo que Content se deja sin desempaquetar: el converter decide
// su forma real.
type ChatMessage struct {
	Role       string            `json:"role"`
	Content    json.RawMessage   `json:"content,omitempty"`
	Name       *string           `json:"name,omitempty"`
	ToolCalls  []json.RawMessage `json:"tool_calls,omitempty"`
	ToolCallID *string           `json:"tool_call_id,omitempty"`
}

// ToolFunction describe la función de una tool: nombre, descripción y el
// JSON Schema de sus parámetros. Parameters se conserva como JSON crudo para
// no perder fidelidad de bytes frente al Dict[str, Any] del original.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description *string         `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// Tool es una tool en formato OpenAI. Soporta dos formas:
//  1. Formato estándar OpenAI: {"type": "function", "function": {...}}
//  2. Formato plano (Cursor-style): {"name": "...", "description": "...", "input_schema": {...}}
type Tool struct {
	// Campos del formato estándar OpenAI.
	Type     string        `json:"type"` // por defecto en el original: "function"
	Function *ToolFunction `json:"function,omitempty"`

	// Campos del formato plano (Cursor-style).
	Name        *string         `json:"name,omitempty"`
	Description *string         `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// ChatCompletionRequest es una petición de generación en formato OpenAI Chat
// Completions API. Soporta todos los campos estándar de la API OpenAI,
// incluidos parámetros de generación, tools (function calling) y parámetros
// adicionales que se aceptan pero se ignoran, por compatibilidad.
type ChatCompletionRequest struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`

	// Parámetros de generación.
	Temperature         *float64        `json:"temperature,omitempty"`
	TopP                *float64        `json:"top_p,omitempty"`
	N                   *int            `json:"n,omitempty"` // por defecto en el original: 1
	MaxTokens           *int            `json:"max_tokens,omitempty"`
	MaxCompletionTokens *int            `json:"max_completion_tokens,omitempty"`
	Stop                json.RawMessage `json:"stop,omitempty"` // string o []string
	PresencePenalty     *float64        `json:"presence_penalty,omitempty"`
	FrequencyPenalty    *float64        `json:"frequency_penalty,omitempty"`

	// Reasoning (modelos de razonamiento OpenAI). Los valores válidos son
	// "none", "minimal", "low", "medium", "high", "xhigh"; el tipo no los
	// impone, eso es validación, no forma.
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`

	// Tools (function calling).
	Tools      []Tool          `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"` // string o dict

	// Campos de compatibilidad (ignorados por la lógica de negocio, pero
	// aceptados para no romper clientes que los envían).
	StreamOptions     json.RawMessage    `json:"stream_options,omitempty"`
	LogitBias         map[string]float64 `json:"logit_bias,omitempty"`
	LogProbs          *bool              `json:"logprobs,omitempty"`
	TopLogProbs       *int               `json:"top_logprobs,omitempty"`
	User              *string            `json:"user,omitempty"`
	Seed              *int               `json:"seed,omitempty"`
	ParallelToolCalls *bool              `json:"parallel_tool_calls,omitempty"`
}

// ==================================================================================================
// Modelos de respuesta
// ==================================================================================================

// ChatCompletionChoice es una variante de respuesta en un Chat Completion.
// Message es Dict[str, Any] en el original: lo construye el converter al
// producir la respuesta, así que aquí se representa como mapa en vez de como
// JSON crudo.
type ChatCompletionChoice struct {
	Index        int            `json:"index"`
	Message      map[string]any `json:"message"`
	FinishReason *string        `json:"finish_reason,omitempty"`
}

// ChatCompletionUsage es la información de uso de tokens.
type ChatCompletionUsage struct {
	PromptTokens     int      `json:"prompt_tokens"`
	CompletionTokens int      `json:"completion_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	CreditsUsed      *float64 `json:"credits_used,omitempty"` // específico de Kiro
}

// ChatCompletionResponse es la respuesta completa de Chat Completion
// (no-streaming).
type ChatCompletionResponse struct {
	ID      string                 `json:"id"`
	Object  string                 `json:"object"` // por defecto en el original: "chat.completion"
	Created int64                  `json:"created"`
	Model   string                 `json:"model"`
	Choices []ChatCompletionChoice `json:"choices"`
	Usage   ChatCompletionUsage    `json:"usage"`
}

// ChatCompletionChunkDelta es el delta de cambios en un chunk de streaming.
type ChatCompletionChunkDelta struct {
	Role      *string          `json:"role,omitempty"` // solo en el primer chunk
	Content   *string          `json:"content,omitempty"`
	ToolCalls []map[string]any `json:"tool_calls,omitempty"`
}

// ChatCompletionChunkChoice es una variante en un chunk de streaming.
type ChatCompletionChunkChoice struct {
	Index        int                      `json:"index"`
	Delta        ChatCompletionChunkDelta `json:"delta"`
	FinishReason *string                  `json:"finish_reason,omitempty"` // solo en el último chunk
}

// ChatCompletionChunk es un chunk de streaming en formato OpenAI.
type ChatCompletionChunk struct {
	ID      string                      `json:"id"`
	Object  string                      `json:"object"` // por defecto en el original: "chat.completion.chunk"
	Created int64                       `json:"created"`
	Model   string                      `json:"model"`
	Choices []ChatCompletionChunkChoice `json:"choices"`
	Usage   *ChatCompletionUsage        `json:"usage,omitempty"` // solo en el último chunk
}
