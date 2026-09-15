// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

// emptyPlaceholder es el contenido sintético que usan
// EnsureFirstMessageIsUser y EnsureAlternatingRoles para los mensajes que
// insertan. Port literal del literal "(empty placeholder)" de
// kiro.converters_core (líneas 1196 y 1303): NO es "Continue." pese a lo que
// sugiere el brief de la tarea — ver el informe de la tarea para la
// verificación contra el original y el corpus.
const emptyPlaceholder = "(empty placeholder)"

// EnsureAssistantBeforeToolResults asegura que todo mensaje con
// tool_results venga precedido de un mensaje assistant con tool_calls. Port
// literal de kiro.converters_core.ensure_assistant_before_tool_results
// (.upstream/kiro/converters_core.py:995-1068).
//
// La API de Kiro exige que un toolResult venga después de un
// assistantResponseMessage con toolUses. Cuando ese mensaje assistant falta
// (conversaciones truncadas de algunos clientes), no se puede reconstruir
// sintéticamente porque no se conoce el nombre ni los argumentos originales
// de la herramienta: en su lugar, el/los tool_results huérfanos se
// convierten a texto y se anexan al content existente del mensaje,
// preservando el contexto para el modelo. modified indica si se convirtió
// algún tool_results huérfano; el resto del pipeline lo usa para decidir si
// se salta la inyección de las etiquetas de "thinking" falso (fuera del
// alcance de esta tarea).
func EnsureAssistantBeforeToolResults(msgs []UnifiedMessage) (out []UnifiedMessage, modified bool) {
	if len(msgs) == 0 {
		return []UnifiedMessage{}, false
	}

	result := make([]UnifiedMessage, 0, len(msgs))
	convertedAny := false

	for _, msg := range msgs {
		if len(msg.ToolResults) > 0 {
			hasPrecedingAssistant := len(result) > 0 &&
				result[len(result)-1].Role == "assistant" &&
				len(result[len(result)-1].ToolCalls) > 0

			if !hasPrecedingAssistant {
				toolResultsText := ToolResultsToText(msg.ToolResults)
				originalContent := ExtractTextContent(msg.Content)

				var newContent string
				switch {
				case originalContent != "" && toolResultsText != "":
					newContent = originalContent + "\n\n" + toolResultsText
				case toolResultsText != "":
					newContent = toolResultsText
				default:
					newContent = originalContent
				}

				result = append(result, UnifiedMessage{
					Role:      msg.Role,
					Content:   newContent,
					ToolCalls: msg.ToolCalls,
					Images:    msg.Images,
					// ToolResults se omite a propósito: los huérfanos ya
					// están en el texto, y el original pasa None.
				})
				convertedAny = true
				continue
			}
		}
		result = append(result, msg)
	}

	return result, convertedAny
}

// MergeAdjacentMessages fusiona mensajes consecutivos con el mismo role. Port
// literal de kiro.converters_core.merge_adjacent_messages
// (.upstream/kiro/converters_core.py:1071-1152).
//
// La API de Kiro no acepta mensajes consecutivos del mismo role. Al
// fusionar dos mensajes:
//   - content: si ambos son listas de bloques, se concatenan; si solo uno lo
//     es, el texto del otro se envuelve en un bloque {"type":"text","text":...}
//     y se antepone o pospone según cuál de los dos sea la lista; si ninguno
//     lo es, se concatenan como texto con "\n" en medio (ExtractTextContent
//     de cada lado, no el content crudo).
//   - tool_calls: se concatena SIEMPRE que el mensaje entrante (no el
//     acumulado) sea de role "assistant" y traiga tool_calls no vacío. No
//     hay resolución de conflictos ni deduplicación: dos tool_calls con el
//     mismo id se concatenan igual (ver el informe de la tarea sobre el
//     corpus con casos que NO se pueden portar fielmente por un defecto del
//     grabador, no de esta función).
//   - tool_results: mismo criterio que tool_calls, pero condicionado a role
//     "user".
//   - images: el original NO las toca en absoluto durante la fusión; el
//     mensaje acumulado conserva sus propias images (las del primer mensaje
//     de la racha) y las del mensaje fusionado se descartan en silencio.
func MergeAdjacentMessages(msgs []UnifiedMessage) []UnifiedMessage {
	if len(msgs) == 0 {
		return []UnifiedMessage{}
	}

	merged := make([]UnifiedMessage, 0, len(msgs))

	for _, msg := range msgs {
		if len(merged) == 0 {
			merged = append(merged, msg)
			continue
		}

		idx := len(merged) - 1
		if msg.Role != merged[idx].Role {
			merged = append(merged, msg)
			continue
		}

		merged[idx].Content = mergeContent(merged[idx].Content, msg.Content)

		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			merged[idx].ToolCalls = append(append([]map[string]any{}, merged[idx].ToolCalls...), msg.ToolCalls...)
		}

		if msg.Role == "user" && len(msg.ToolResults) > 0 {
			merged[idx].ToolResults = append(append([]map[string]any{}, merged[idx].ToolResults...), msg.ToolResults...)
		}
	}

	return merged
}

// mergeContent fusiona el content de dos mensajes adyacentes con el mismo
// role, replicando las cuatro ramas de merge_adjacent_messages
// (.upstream/kiro/converters_core.py:1101-1110).
func mergeContent(lastContent, msgContent any) any {
	lastList, lastIsList := lastContent.([]any)
	msgList, msgIsList := msgContent.([]any)

	switch {
	case lastIsList && msgIsList:
		out := make([]any, 0, len(lastList)+len(msgList))
		out = append(out, lastList...)
		out = append(out, msgList...)
		return out
	case lastIsList:
		out := make([]any, 0, len(lastList)+1)
		out = append(out, lastList...)
		out = append(out, map[string]any{"type": "text", "text": ExtractTextContent(msgContent)})
		return out
	case msgIsList:
		out := make([]any, 0, len(msgList)+1)
		out = append(out, map[string]any{"type": "text", "text": ExtractTextContent(lastContent)})
		out = append(out, msgList...)
		return out
	default:
		return ExtractTextContent(lastContent) + "\n" + ExtractTextContent(msgContent)
	}
}

// EnsureFirstMessageIsUser asegura que el primer mensaje de la conversación
// sea de role user. Port literal de
// kiro.converters_core.ensure_first_message_is_user
// (.upstream/kiro/converters_core.py:1155-1201).
//
// La API de Kiro exige que la conversación empiece con user. Si el primer
// mensaje no lo es, se antepone un mensaje user sintético mínimo con content
// emptyPlaceholder — el mismo placeholder que build_kiro_history usa en el
// original, no una cadena vacía.
func EnsureFirstMessageIsUser(msgs []UnifiedMessage) []UnifiedMessage {
	if len(msgs) == 0 {
		return msgs
	}

	if msgs[0].Role != "user" {
		synthetic := UnifiedMessage{Role: "user", Content: emptyPlaceholder}
		out := make([]UnifiedMessage, 0, len(msgs)+1)
		out = append(out, synthetic)
		out = append(out, msgs...)
		return out
	}

	return msgs
}

// NormalizeMessageRoles normaliza a "user" cualquier role que no sea "user"
// ni "assistant". Port literal de
// kiro.converters_core.normalize_message_roles
// (.upstream/kiro/converters_core.py:1204-1256).
//
// La API de Kiro solo admite "user" y "assistant" en el historial. Esta
// función, aislada, convierte también "system" a "user" — no lo conserva:
// el pipeline completo (build_kiro_payload) extrae los mensajes system
// ANTES de llegar a esta cadena, así que en el uso real no le llegan
// mensajes "system", pero la función en sí misma no distingue "system" de
// cualquier otro role desconocido (verificado contra el corpus: hay casos
// con role "system" en la entrada que salen como "user").
func NormalizeMessageRoles(msgs []UnifiedMessage) []UnifiedMessage {
	if len(msgs) == 0 {
		return msgs
	}

	normalized := make([]UnifiedMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Role != "user" && msg.Role != "assistant" {
			normalized = append(normalized, UnifiedMessage{
				Role:        "user",
				Content:     msg.Content,
				ToolCalls:   msg.ToolCalls,
				ToolResults: msg.ToolResults,
				Images:      msg.Images,
			})
			continue
		}
		normalized = append(normalized, msg)
	}

	return normalized
}

// EnsureAlternatingRoles asegura la alternancia user/assistant insertando
// mensajes assistant sintéticos. Port literal de
// kiro.converters_core.ensure_alternating_roles
// (.upstream/kiro/converters_core.py:1259-1313).
//
// Solo comprueba la dirección user-tras-user: cuando ve dos mensajes user
// consecutivos, inserta entre ambos un mensaje assistant sintético con
// content emptyPlaceholder. NO hay una rama simétrica que inserte un user
// sintético entre dos assistant consecutivos — pese a lo que sugiere el
// brief de la tarea, el original no la tiene (ver el informe de la tarea).
// Tiene sentido dado el orden de la cadena: para cuando esta función se
// ejecuta, MergeAdjacentMessages ya fusionó cualquier racha de assistant
// consecutivos en uno solo, y NormalizeMessageRoles (que corre justo antes)
// solo puede convertir roles desconocidos A "user", nunca a "assistant", así
// que la única racha de mismo role que puede sobrevivir hasta aquí es de
// user.
func EnsureAlternatingRoles(msgs []UnifiedMessage) []UnifiedMessage {
	if len(msgs) < 2 {
		return msgs
	}

	result := make([]UnifiedMessage, 0, len(msgs))
	result = append(result, msgs[0])

	for _, msg := range msgs[1:] {
		prevRole := result[len(result)-1].Role
		if msg.Role == "user" && prevRole == "user" {
			result = append(result, UnifiedMessage{Role: "assistant", Content: emptyPlaceholder})
		}
		result = append(result, msg)
	}

	return result
}
