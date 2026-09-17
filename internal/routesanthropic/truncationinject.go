// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"encoding/json"
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/modelsanthropic"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationrecovery"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// truncationToolResultSeparator es el separador EXACTO que
// routes_anthropic.py:195 usa para unir el aviso sintético con el contenido
// original del tool_result: `f"{synthetic['content']}\n\n---\n\nOriginal tool result:\n{original_content}"`.
const truncationToolResultSeparator = "\n\n---\n\nOriginal tool result:\n"

// injectTruncationRecovery implementa el lado READ/inject de la recuperación
// de truncación (Task 8a), port literal de routes_anthropic.py:156-244. Se
// llama en Messages (failover.go) INCONDICIONALMENTE (el original NO llama a
// should_inject_recovery() en este bucle — ese gate solo existe del lado
// SAVE, streaming_anthropic.py:666-669, Task 8b; con el gate apagado la
// cache queda vacía y GetTool/GetContent simplemente no encuentran nada, así
// que ejecutar esta función siempre es correcto), ANTES de injectWebSearchTool.
//
// Recorre req.Messages y produce una lista reemplazada donde:
//  1. Un mensaje "user" con un bloque tool_result cuyo tool_use_id tiene un
//     *truncationstate.ToolRecord pendiente en h.truncation ve su content
//     PREPENDIDO con el aviso sintético (truncationToolResultSeparator
//     exacto, incluidos los guiones). Los demás bloques del mensaje quedan
//     intactos.
//  2. Un mensaje "assistant" cuyo texto (concatenación de bloques "text")
//     tiene un *truncationstate.ContentRecord pendiente se deja tal cual,
//     seguido de un NUEVO mensaje "user" sintético con el aviso de
//     truncationrecovery.GenerateTruncationUserMessage().
//  3. Cualquier otro mensaje pasa sin cambios.
//
// GetTool/GetContent son destructivos (truncationstate.State): cada record
// se consume como mucho una vez, igual que el original.
func (h *Handler) injectTruncationRecovery(req *modelsanthropic.AnthropicMessagesRequest) {
	modified := make([]modelsanthropic.AnthropicMessage, 0, len(req.Messages))

	for _, msg := range req.Messages {
		if msg.Role == "user" {
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
// para UN mensaje user. Devuelve ok=false si ningún bloque tool_result del
// mensaje tenía un ToolRecord pendiente (el llamador debe usar el mensaje
// original sin cambios).
func (h *Handler) injectToolResultRecovery(msg modelsanthropic.AnthropicMessage) (modelsanthropic.AnthropicMessage, bool) {
	hasModifications := false
	blocks := make([]modelsanthropic.ContentBlock, len(msg.Content))
	copy(blocks, msg.Content)

	for i, block := range blocks {
		if block.Type != "tool_result" || block.ToolUseID == nil || *block.ToolUseID == "" {
			continue
		}

		recordAny, found := h.truncation.GetTool(*block.ToolUseID)
		if !found {
			continue
		}
		rec, ok := recordAny.(truncationstate.ToolRecord)
		if !ok {
			// Defensivo: un valor guardado por otro llamador con un tipo
			// distinto no debería ocurrir (el contrato es truncationstate.
			// ToolRecord), pero no hay equivalente en el original a fallar
			// aquí — se trata como "sin match".
			continue
		}

		synthetic := truncationrecovery.GenerateTruncationToolResult(rec.ToolName, *block.ToolUseID, rec.TruncationInfo)
		originalContent := converterscore.ExtractTextContent(decodeRawContent(block.Content))
		modifiedContent := synthetic["content"].(string) + truncationToolResultSeparator + originalContent

		newBlock := block
		contentBytes, err := json.Marshal(modifiedContent)
		if err != nil {
			continue
		}
		newBlock.Content = contentBytes
		blocks[i] = newBlock
		hasModifications = true
	}

	if !hasModifications {
		return modelsanthropic.AnthropicMessage{}, false
	}
	return modelsanthropic.AnthropicMessage{Role: msg.Role, Content: blocks}, true
}

// injectContentRecovery implementa el paso 2 de injectTruncationRecovery
// para UN mensaje assistant. Devuelve el mensaje user sintético a insertar
// DESPUÉS de msg, y ok=true, si el texto concatenado de msg tenía un
// ContentRecord pendiente. ok=false (mensaje sintético vacío, ignorar) si el
// texto está vacío o no hay match — igual que el `if text_content:` y el
// `if truncation_info:` del original.
func (h *Handler) injectContentRecovery(msg modelsanthropic.AnthropicMessage) (modelsanthropic.AnthropicMessage, bool) {
	var text strings.Builder
	for _, block := range msg.Content {
		if block.Type == "text" && block.Text != nil {
			text.WriteString(*block.Text)
		}
	}
	textContent := text.String()
	if textContent == "" {
		return modelsanthropic.AnthropicMessage{}, false
	}

	recordAny, found := h.truncation.GetContent(textContent)
	if !found {
		return modelsanthropic.AnthropicMessage{}, false
	}
	if _, ok := recordAny.(truncationstate.ContentRecord); !ok {
		return modelsanthropic.AnthropicMessage{}, false
	}

	recoveryText := truncationrecovery.GenerateTruncationUserMessage()
	return modelsanthropic.AnthropicMessage{
		Role:    "user",
		Content: []modelsanthropic.ContentBlock{{Type: "text", Text: &recoveryText}},
	}, true
}

// decodeRawContent decodifica el `content` crudo de un ContentBlock a `any`
// genérico, para pasarlo a converterscore.ExtractTextContent — mismo patrón
// que convertersopenai.decodeOpenAIContent (internal/convertersopenai/converters.go).
// Ausente (raw vacío) o JSON null decodifican los dos a nil, que
// ExtractTextContent reduce a "" — igual que el `block.get("content", "")`
// del original para el caso ausente.
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
