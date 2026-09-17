// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"bytes"
	"encoding/json"
	"strings"
)

// checkPayloadSize devuelve el tamaño en bytes de la serialización UTF-8 del
// payload como JSON compacto (sin espacios en separadores).
//
// Port de payload_guards.py:46-48. El tamaño se calcula con separadores
// compactos (",", ":"), igual que el original.
//
// NOTA CRÍTICA sobre HTML escaping: encoding/json.Marshal escapa los caracteres
// `&`, `<`, `>` como `&`, `<`, `>` (por seguridad en HTML) PERO
// la medición debe coincidir exactamente con el original Python que usa
// json.dumps(payload, separators=(",", ":")), que NO escapa esos caracteres.
// Para lograr parity, usamos json.Encoder con SetEscapeHTML(false) sobre un
// bytes.Buffer, luego restamos el newline que Encoder añade automáticamente.
func checkPayloadSize(payload map[string]any) int {
	buf := bytes.Buffer{}
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(payload); err != nil {
		return 0
	}
	// json.Encoder añade un newline al final; restar para coincidir con
	// json.dumps() de Python, que no lo añade.
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		return len(b) - 1
	}
	return len(b)
}

// stripEmptyToolUses elimina arrays vacíos de toolUses en los mensajes de
// respuesta del asistente (quirk de Kiro).
//
// Port de payload_guards.py:51-56. Muta el history in-place.
func stripEmptyToolUses(history []map[string]any) {
	for _, entry := range history {
		if assistant, ok := entry["assistantResponseMessage"].(map[string]any); ok {
			if toolUses, ok := assistant["toolUses"].([]map[string]any); ok && len(toolUses) == 0 {
				delete(assistant, "toolUses")
			}
		}
	}
}

// alignToUserMessage asegura que el historial comienza con una entrada
// userInputMessage, removiendo entradas del frente que no lo sean.
//
// Port de payload_guards.py:59-63. Muta el history in-place.
func alignToUserMessage(history []map[string]any) {
	for len(history) > 0 {
		if _, ok := history[0]["userInputMessage"]; !ok {
			history = history[1:]
		} else {
			break
		}
	}
}

// repairOrphanedToolResults elimina toolResults huérfanos (cuyo toolUseId no
// aparece en el mensaje de asistente anterior) y preserva su contenido de texto
// con un marcador.
//
// Port de payload_guards.py:66-119. Muta el history in-place.
func repairOrphanedToolResults(history []map[string]any) {
	for i, entry := range history {
		userMsg, ok := entry["userInputMessage"].(map[string]any)
		if !ok {
			continue
		}

		ctx, ok := userMsg["userInputMessageContext"].(map[string]any)
		if !ok {
			continue
		}

		toolResults, ok := ctx["toolResults"].([]map[string]any)
		if !ok {
			continue
		}

		// Recolectar toolUseIds válidos del mensaje de asistente anterior
		validIDs := make(map[string]bool)
		if i > 0 {
			if prevAssistant, ok := history[i-1]["assistantResponseMessage"].(map[string]any); ok {
				if prevToolUses, ok := prevAssistant["toolUses"].([]map[string]any); ok {
					for _, tu := range prevToolUses {
						if toolUseID, ok := tu["toolUseId"].(string); ok {
							validIDs[toolUseID] = true
						}
					}
				}
			}
		}

		kept := []map[string]any{}
		orphanedTextParts := []string{}

		for _, tr := range toolResults {
			toolUseID, _ := tr["toolUseId"].(string)
			if validIDs[toolUseID] {
				kept = append(kept, tr)
			} else {
				// Preservar contenido de texto de resultados huérfanos
				if content, ok := tr["content"]; ok {
					if contentList, ok := content.([]any); ok {
						// content es un array de partes
						for _, part := range contentList {
							if partMap, ok := part.(map[string]any); ok {
								if text, ok := partMap["text"].(string); ok && text != "" {
									orphanedTextParts = append(orphanedTextParts, text)
								}
							}
						}
					} else if contentStr, ok := content.(string); ok && contentStr != "" {
						// content es un string directo
						orphanedTextParts = append(orphanedTextParts, contentStr)
					}
				}
			}
		}

		// Si hubo cambios (algunos toolResults fueron huérfanos)
		if len(kept) != len(toolResults) {
			if len(kept) > 0 {
				ctx["toolResults"] = kept
			} else {
				delete(ctx, "toolResults")
				if len(ctx) == 0 {
					delete(userMsg, "userInputMessageContext")
				}
			}

			// Añadir contenido de huérfanos al mensaje del usuario con marcador
			if len(orphanedTextParts) > 0 {
				marker := "\n[trimmed tool result] " + strings.Join(orphanedTextParts, "; ")
				currentContent := ""
				if c, ok := userMsg["content"].(string); ok {
					currentContent = c
				}
				userMsg["content"] = currentContent + marker
			}
		}
	}
}

// TrimPayloadToLimit recorta las entradas de historial más antiguas para que el
// payload serializado quepa bajo maxBytes. Se recorta en pares (un mensaje de
// usuario + uno de asistente), se alinea al inicio en userInputMessage y se
// reparan los toolResults huérfanos.
//
// Port de payload_guards.py:121-164. PERO: en lugar de retornar PayloadTrimStats,
// solo muta el payload in-place. La decisión de si trimear está fuera (gateada
// por AutoTrimPayload en BuildKiroPayload).
//
// Muta el payload in-place, modificando payload["conversationState"]["history"]
// directamente.
func TrimPayloadToLimit(payload map[string]any, maxBytes int) {
	// NOTA: en BuildKiroPayload, esto se llama dentro de `if AutoTrimPayload { ... }`
	// así que ya sabemos que AutoTrimPayload es true. Si por algún motivo se llama
	// fuera de ese contexto, simplemente devolvemos sin hacer nada.
	if !AutoTrimPayload {
		return
	}

	conversationState, ok := payload["conversationState"].(map[string]any)
	if !ok {
		return
	}

	history, ok := conversationState["history"].([]map[string]any)
	if !ok || len(history) == 0 {
		return
	}

	// Paso 1: Eliminar toolUses vacíos antes de medir
	stripEmptyToolUses(history)

	// Paso 2: Recortar pares desde el frente mientras el tamaño exceda el máximo
	// (mantener al menos 2 entradas si las hay)
	for len(history) > 2 && checkPayloadSize(payload) > maxBytes {
		if len(history) >= 2 {
			// Remover dos entradas (par usuario/asistente)
			history = history[2:]
		} else {
			break
		}
	}

	// Actualizar la referencia en el payload después de posibles removals
	if len(history) > 0 {
		conversationState["history"] = history
	} else {
		delete(conversationState, "history")
		return
	}

	// Paso 3: Alinear al inicio de userInputMessage
	alignToUserMessage(history)

	// Paso 4: Reparar toolResults huérfanos
	repairOrphanedToolResults(history)
}
