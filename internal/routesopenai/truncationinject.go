// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import (
	"encoding/json"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsopenai"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationrecovery"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// truncationToolResultSeparator es el separador EXACTO que
// routes_openai.py:205 usa para unir el aviso sintético con el contenido
// original del tool_result: `f"{synthetic['content']}\n\n---\n\nOriginal tool result:\n{msg.content}"`.
// Idéntico al de routesanthropic (mismo literal en el original, verificado
// contra ambas rutas).
const truncationToolResultSeparator = "\n\n---\n\nOriginal tool result:\n"

// injectTruncationRecovery implementa el lado READ/inject de la recuperación
// de truncación (Task 8a), port literal de routes_openai.py:185-234. Se
// llama en ChatCompletions (failover.go) INCONDICIONALMENTE (el original NO
// llama a should_inject_recovery() en este bucle — ese gate solo existe del
// lado SAVE, streaming_openai.py:366-369, Task 8b; con el gate apagado la
// cache queda vacía y GetTool/GetContent simplemente no encuentran nada, así
// que ejecutar esta función siempre es correcto), ANTES de injectWebSearchTool,
// igual que el original (routes_openai.py:185-234 antes de :236-272).
//
// Recorre req.Messages y produce una lista reemplazada donde:
//  1. Un mensaje role="tool" cuyo tool_call_id tiene un
//     *truncationstate.ToolRecord pendiente en h.truncation ve su content
//     REEMPLAZADO por el aviso sintético PREPENDIDO al original
//     (truncationToolResultSeparator exacto). A diferencia de Anthropic
//     (lista de bloques), el content de un ChatMessage "tool" es una cadena
//     plana — routes_openai.py:207-208 hace lo mismo (model_copy con
//     content=cadena, no lista).
//  2. Un mensaje role="assistant" cuyo content es una cadena JSON plana (NO
//     una lista — routes_openai.py:215 exige isinstance(str)) tiene un
//     *truncationstate.ContentRecord pendiente se deja tal cual, seguido de
//     un NUEVO mensaje role="user" sintético cuyo content es la cadena
//     PLANA de truncationrecovery.GenerateTruncationUserMessage() (no una
//     lista de bloques — diferencia deliberada frente a routesanthropic,
//     que sí usa una lista; routes_openai.py:221-224 construye
//     `ChatMessage(role="user", content=generate_truncation_user_message())`
//     con content como string).
//  3. Cualquier otro mensaje pasa sin cambios.
//
// GetTool/GetContent son destructivos (truncationstate.State): cada record
// se consume como mucho una vez, igual que el original.
func (h *Handler) injectTruncationRecovery(req *modelsopenai.ChatCompletionRequest) {
	modified := make([]modelsopenai.ChatMessage, 0, len(req.Messages))

	for _, msg := range req.Messages {
		if msg.Role == "tool" && msg.ToolCallID != nil && *msg.ToolCallID != "" {
			if newMsg, ok := h.injectToolResultRecovery(msg); ok {
				modified = append(modified, newMsg)
				continue
			}
		}
		if msg.Role == "assistant" {
			if syntheticUser, ok := h.injectContentRecovery(msg); ok {
				modified = append(modified, msg, syntheticUser)
				continue
			}
		}
		modified = append(modified, msg)
	}

	req.Messages = modified
}

// injectToolResultRecovery implementa el paso 1 de injectTruncationRecovery
// para UN mensaje "tool". Devuelve ok=false si no había ToolRecord pendiente
// para *msg.ToolCallID (el llamador debe usar el mensaje original).
func (h *Handler) injectToolResultRecovery(msg modelsopenai.ChatMessage) (modelsopenai.ChatMessage, bool) {
	recordAny, found := h.truncation.GetTool(*msg.ToolCallID)
	if !found {
		return modelsopenai.ChatMessage{}, false
	}
	rec, ok := recordAny.(truncationstate.ToolRecord)
	if !ok {
		// Defensivo: ver el comentario equivalente en routesanthropic.
		return modelsopenai.ChatMessage{}, false
	}

	synthetic := truncationrecovery.GenerateTruncationToolResult(rec.ToolName, *msg.ToolCallID, rec.TruncationInfo)
	originalContent := converterscore.ExtractTextContent(decodeRawContent(msg.Content))
	modifiedContent := synthetic["content"].(string) + truncationToolResultSeparator + originalContent

	contentBytes, err := json.Marshal(modifiedContent)
	if err != nil {
		return modelsopenai.ChatMessage{}, false
	}

	newMsg := msg
	newMsg.Content = contentBytes
	return newMsg, true
}

// injectContentRecovery implementa el paso 2 de injectTruncationRecovery
// para UN mensaje "assistant". Devuelve el mensaje user sintético a insertar
// DESPUÉS de msg, y ok=true, si msg.Content es una cadena JSON plana no
// vacía con un ContentRecord pendiente. ok=false si Content no es una cadena
// (isinstance(str) del original), está vacío, o no hay match.
func (h *Handler) injectContentRecovery(msg modelsopenai.ChatMessage) (modelsopenai.ChatMessage, bool) {
	var textContent string
	if err := json.Unmarshal(msg.Content, &textContent); err != nil {
		// Content ausente, null, lista u objeto: no es el caso
		// isinstance(msg.content, str) que routes_openai.py:215 exige.
		return modelsopenai.ChatMessage{}, false
	}
	if textContent == "" {
		return modelsopenai.ChatMessage{}, false
	}

	recordAny, found := h.truncation.GetContent(textContent)
	if !found {
		return modelsopenai.ChatMessage{}, false
	}
	if _, ok := recordAny.(truncationstate.ContentRecord); !ok {
		return modelsopenai.ChatMessage{}, false
	}

	recoveryText := truncationrecovery.GenerateTruncationUserMessage()
	contentBytes, err := json.Marshal(recoveryText)
	if err != nil {
		return modelsopenai.ChatMessage{}, false
	}
	return modelsopenai.ChatMessage{Role: "user", Content: contentBytes}, true
}

// decodeRawContent decodifica el `content` crudo de un ChatMessage a `any`
// genérico, para pasarlo a converterscore.ExtractTextContent. Idéntico a
// decodeOpenAIContent (converters.go) pero definido aquí, sin exportar,
// porque truncationinject.go no debe depender de internal/convertersopenai
// (paquete de otra capa) solo por esta función de 8 líneas.
func decodeRawContent(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}
