// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"strings"

	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// BuildKiroHistory construye el array de historial que espera la API de
// Kiro a partir de mensajes en formato unificado. Port literal de
// kiro.converters_core.build_kiro_history
// (.upstream/kiro/converters_core.py:1320-1398).
//
// La API de Kiro espera userInputMessage/assistantResponseMessage
// alternados. Para cuando esta función se ejecuta, todos los mensajes
// deberían tener role "user" o "assistant" — los roles desconocidos ya se
// normalizaron antes, en normalize_message_roles() (Task 6). Un mensaje con
// cualquier otro role se descarta en silencio, igual que el if/elif del
// original sin rama else.
//
// Verificado ejecutando el upstream real (commit a5292ca) contra los 37
// casos del corpus: los 37 coinciden byte a byte, sin ningún caso
// defectuoso — a diferencia de BuildKiroPayload (ver su comentario y
// knownDefectiveBuildKiroPayloadCases en payload_test.go), esta función no
// necesita exclusiones.
func BuildKiroHistory(msgs []UnifiedMessage, modelID string) []map[string]any {
	history := []map[string]any{}

	for _, msg := range msgs {
		switch msg.Role {
		case "user":
			content := ExtractTextContent(msg.Content)
			if content == "" {
				content = emptyPlaceholder
			}

			userInput := map[string]any{
				"content": content,
				"modelId": modelID,
				"origin":  "AI_EDITOR",
			}

			// Las imágenes van directamente en userInputMessage, NUNCA en
			// userInputMessageContext — coincide con el formato nativo del
			// IDE de Kiro.
			images := msg.Images
			if len(images) == 0 {
				images = ExtractImagesFromContent(msg.Content)
			}
			if len(images) > 0 {
				if kiroImages := ConvertImagesToKiroFormat(images); len(kiroImages) > 0 {
					userInput["images"] = kiroImages
				}
			}

			// userInputMessageContext solo lleva tools y toolResults.
			userInputContext := map[string]any{}
			if len(msg.ToolResults) > 0 {
				if kiroToolResults := ConvertToolResultsToKiroFormat(msg.ToolResults); len(kiroToolResults) > 0 {
					userInputContext["toolResults"] = kiroToolResults
				}
			} else if toolResults := ExtractToolResultsFromContent(msg.Content); len(toolResults) > 0 {
				userInputContext["toolResults"] = toolResults
			}
			if len(userInputContext) > 0 {
				userInput["userInputMessageContext"] = userInputContext
			}

			history = append(history, map[string]any{"userInputMessage": userInput})

		case "assistant":
			content := ExtractTextContent(msg.Content)
			if content == "" {
				content = emptyPlaceholder
			}

			assistantResponse := map[string]any{"content": content}
			if toolUses := ExtractToolUsesFromMessage(msg); len(toolUses) > 0 {
				assistantResponse["toolUses"] = toolUses
			}

			history = append(history, map[string]any{"assistantResponseMessage": assistantResponse})
		}
	}

	return history
}

// BuildKiroPayload ensambla el payload completo de la API de Kiro a partir
// de los datos unificados de mensajes y tools. Port literal de
// kiro.converters_core.build_kiro_payload
// (.upstream/kiro/converters_core.py:1405-1597).
//
// # Firma
//
// SIETE argumentos, en el mismo orden que el original: messages,
// systemPrompt, modelID, tools, conversationID, profileArn, thinkingCfg. El
// plan de fase 3 describía una firma distinta (ocho argumentos, sin
// profileArn, con truncationRecovery y toolDescriptionMaxLength como
// parámetros propios) que no sobrevive la verificación contra
// .upstream/kiro/converters_core.py ni contra los 83 casos del corpus — ver
// el comentario de TestBuildKiroPayload (payload_test.go) para el detalle
// caso por caso de por qué cada parámetro del brief se descartó o se
// mantuvo. Resumen:
//
//   - conversation_id y profile_arn YA son parámetros normales del
//     original (quinto y sexto); no hay nada que "añadir". profileArn no
//     aparecía en absoluto en la firma que proponía el brief.
//   - truncation_recovery y tool_description_max_length NO son parámetros
//     del original: son TRUNCATION_RECOVERY y TOOL_DESCRIPTION_MAX_LENGTH,
//     globals de kiro.config que build_kiro_payload consulta indirectamente
//     (el primero a través de GetTruncationRecoverySystemAddition, Task 7;
//     el segundo pasándolo a ProcessToolsWithLongDescriptions, Task 4, que
//     sí lo recibe como argumento explícito por decisión de ESA tarea). Este
//     port reproduce ese mecanismo con dos package vars en thinking.go
//     (TruncationRecoveryEnabled, ya existente desde Task 7, y
//     ToolDescriptionMaxLength, añadida en esta tarea) en vez de aceptarlos
//     como parámetros de BuildKiroPayload.
//
// conversationID sí conserva el único añadido real del brief sobre la
// paridad estricta con upstream: si el llamador pasa "", se genera un
// UUIDv4 con crypto/rand (vía internal/utils.NewUUID) en vez de propagar una
// cadena vacía al payload — conveniencia para producción que ningún caso
// del corpus ejercita (conversation_id nunca es "" en los 83 casos).
//
// # Orden de la cadena (verificado línea a línea contra el original)
//
//  1. ProcessToolsWithLongDescriptions(tools, ToolDescriptionMaxLength) →
//     processedTools, toolDocumentation.
//  2. ValidateToolNames(processedTools) — si hay un nombre de tool que
//     excede el límite de 64 caracteres de Kiro, el original lanza
//     ValueError. Esta función no lo devuelve como error (ver "Decisión de
//     manejo de errores" más abajo), y ningún caso del corpus lo ejercita
//     (el nombre de tool más largo del corpus tiene 15 caracteres).
//  3. fullSystemPrompt = systemPrompt, con tres adiciones posibles en este
//     orden — toolDocumentation, GetThinkingSystemPromptAddition(),
//     GetTruncationRecoverySystemAddition() —, cada una aplicada con la
//     misma regla: si fullSystemPrompt ya tiene contenido, se concatena tal
//     cual; si estaba vacío, fullSystemPrompt pasa a ser la adición con
//     strings.TrimSpace (el .strip() de Python), no la adición cruda.
//  4. Si NO hay tools (tools vacío o nil, "not tools" de Python cubre
//     ambos), StripAllToolContent(messages) reemplaza tool_calls/tool_results
//     por su representación en texto — Kiro rechaza toolResults sin tools
//     definidas. Si SÍ hay tools, EnsureAssistantBeforeToolResults(messages)
//     en su lugar. Los dos devuelven un flag "modified"/"converted" que el
//     original asigna a converted_tool_results pero nunca vuelve a leer
//     dentro de build_kiro_payload (variable muerta en el original,
//     verificado con grep sobre el fichero completo) — este port no lo
//     conserva.
//  5. MergeAdjacentMessages → EnsureFirstMessageIsUser →
//     NormalizeMessageRoles → EnsureAlternatingRoles, en ese orden (spec
//     §6.7, ya verificado en Task 6).
//  6. historyMessages = todos los mensajes menos el último (o vacío si solo
//     hay uno). Si fullSystemPrompt no está vacío y historyMessages no está
//     vacío, se antepone fullSystemPrompt al primer mensaje de
//     historyMessages (mutando ese elemento del slice in-place, igual que
//     el original muta el objeto — ver la nota de aliasing más abajo).
//  7. history = BuildKiroHistory(historyMessages, modelID).
//  8. currentMessage = el último mensaje de la cadena normalizada.
//     currentContent = su texto extraído; si fullSystemPrompt no está vacío
//     y history SÍ está vacío (no hubo mensajes de historial), se antepone
//     fullSystemPrompt a currentContent en su lugar.
//  9. Si currentMessage es "assistant", se mueve a history como
//     assistantResponseMessage y currentContent se reinicia al placeholder
//     — el mensaje "actual" de Kiro siempre tiene que ser del usuario.
//  10. currentContent vacío → placeholder.
//  11. Imágenes de currentMessage (mensaje o content) → userInputMessage.images
//     directamente, nunca en userInputMessageContext.
//  12. userInputContext con tools (ConvertToolsToKiroFormat(processedTools))
//     y toolResults de currentMessage (mensaje o content, mismo fallback
//     que BuildKiroHistory).
//  13. Si currentMessage es "user", InjectThinkingTags(currentContent,
//     thinkingCfg) — DESPUÉS de todo lo anterior, así que el prefijo de
//     thinking queda por delante de fullSystemPrompt cuando ambos aplican
//     (verificado contra el corpus, p. ej. caso 10e918939ed7e3aa).
//  14. Ensamblado final: conversationState.history solo si no está vacío;
//     payload.profileArn solo si profileArn no es "".
//
// AUTO_TRIM_PAYLOAD / KIRO_MAX_PAYLOAD_BYTES (líneas 1587-1596 del
// original): build_kiro_payload SÍ las consulta directamente (no a través
// de otra función), para recortar el payload con
// check_payload_size/trim_payload_to_limit de kiro.payload_guards cuando
// AUTO_TRIM_PAYLOAD está activo y el payload excede el límite. Ese módulo
// (internal/payloadguards en docs/MAPPING.md) está fuera del alcance de esta
// tarea y no existe todavía en el port — ver el comentario de
// AutoTrimPayload/KiroMaxPayloadBytes en thinking.go. Los 83 casos del
// corpus fijan AUTO_TRIM_PAYLOAD=false (nunca ejercitan el recorte), así que
// esta rama se deja documentada pero sin implementar.
//
// # Nota de aliasing (paso 6)
//
// El original muta el mensaje in-place (first_msg.content = ...); este
// port hace lo mismo indexando historyMessages[0] directamente — como
// historyMessages es un slice de mergedMessages (misma cabecera, mismo
// array subyacente cuando len(mergedMessages) > 1), la mutación es visible
// también en mergedMessages[0], igual que en Python mutar el objeto
// first_msg es mutar el mismo objeto que ya vive en merged_messages[0] (las
// listas de Python solo copian referencias, no los objetos). Esto reproduce
// fielmente el original, PERO también reproduce el defecto de grabación del
// corpus que documenta knownDefectiveBuildKiroPayloadCases en
// payload_test.go: cuando ese primer mensaje conserva la misma identidad
// que el messages[0] que pasó el llamador (sin fusionar, sin normalizar),
// tools/corpus/recorder.py graba el "output" de esos 15 casos SIN el
// prefijo que el upstream real sí produce — verificado ejecutando el
// upstream real contra esos 15 inputs exactos.
//
// # Decisión de manejo de errores
//
// El original lanza ValueError en dos sitios: nombres de tool demasiado
// largos (validate_tool_names) y mergedMessages vacío ("No messages to
// send"). Ninguno de los 83 casos del corpus ejercita ninguna de las dos
// rutas (el nombre de tool más largo tiene 15 caracteres; messages siempre
// trae al menos 1 entrada, y ninguna función de la cadena de normalización
// puede reducir una lista no vacía a cero elementos — todas o preservan la
// longitud o la aumentan, ver normalize.go). La firma de BuildKiroPayload,
// fijada por el plan de fase 3, devuelve solo KiroPayloadResult, sin error
// — así que este port hace panic() en ambos casos, igual que
// internal/utils.NewUUID hace panic() ante un fallo de crypto/rand
// (internal/utils/ids.go): son condiciones que el resto del pipeline (capas
// de fase 4/5, todavía sin construir) tendrá que decidir cómo convertir en
// una respuesta HTTP 400, pero que no tiene sentido tragar en silencio
// aquí.
func BuildKiroPayload(
	messages []UnifiedMessage,
	systemPrompt string,
	modelID string,
	tools []UnifiedTool,
	conversationID string,
	profileArn string,
	thinkingCfg ThinkingConfig,
) KiroPayloadResult {
	processedTools, toolDocumentation := ProcessToolsWithLongDescriptions(tools, ToolDescriptionMaxLength)

	if err := ValidateToolNames(processedTools); err != nil {
		panic(err)
	}

	fullSystemPrompt := systemPrompt
	fullSystemPrompt = appendSystemPromptAddition(fullSystemPrompt, toolDocumentation)
	fullSystemPrompt = appendSystemPromptAddition(fullSystemPrompt, GetThinkingSystemPromptAddition())
	fullSystemPrompt = appendSystemPromptAddition(fullSystemPrompt, GetTruncationRecoverySystemAddition())

	var messagesWithAssistants []UnifiedMessage
	if len(tools) == 0 {
		messagesWithAssistants, _ = StripAllToolContent(messages)
	} else {
		messagesWithAssistants, _ = EnsureAssistantBeforeToolResults(messages)
	}

	mergedMessages := MergeAdjacentMessages(messagesWithAssistants)
	mergedMessages = EnsureFirstMessageIsUser(mergedMessages)
	mergedMessages = NormalizeMessageRoles(mergedMessages)
	mergedMessages = EnsureAlternatingRoles(mergedMessages)

	if len(mergedMessages) == 0 {
		panic("converterscore: BuildKiroPayload: no messages to send")
	}

	var historyMessages []UnifiedMessage
	if len(mergedMessages) > 1 {
		historyMessages = mergedMessages[:len(mergedMessages)-1]
	} else {
		historyMessages = []UnifiedMessage{}
	}

	// Si hay system prompt, se antepone al primer mensaje del historial
	// (solo si ese mensaje es de role "user"). Ver la nota de aliasing en
	// el docstring de esta función.
	if fullSystemPrompt != "" && len(historyMessages) > 0 {
		if historyMessages[0].Role == "user" {
			originalContent := ExtractTextContent(historyMessages[0].Content)
			historyMessages[0].Content = fullSystemPrompt + "\n\n" + originalContent
		}
	}

	history := BuildKiroHistory(historyMessages, modelID)

	currentMessage := mergedMessages[len(mergedMessages)-1]
	currentContent := ExtractTextContent(currentMessage.Content)

	// Si hay system prompt pero el historial quedó vacío, se antepone al
	// mensaje actual en su lugar.
	if fullSystemPrompt != "" && len(history) == 0 {
		currentContent = fullSystemPrompt + "\n\n" + currentContent
	}

	// El mensaje "actual" que espera Kiro siempre es del usuario: si el
	// último mensaje de la cadena es de assistant, se mueve al historial y
	// se sustituye por un placeholder de usuario.
	if currentMessage.Role == "assistant" {
		history = append(history, map[string]any{
			"assistantResponseMessage": map[string]any{
				"content": currentContent,
			},
		})
		currentContent = emptyPlaceholder
	}

	if currentContent == "" {
		currentContent = emptyPlaceholder
	}

	// Las imágenes van directamente en userInputMessage, nunca en
	// userInputMessageContext.
	images := currentMessage.Images
	if len(images) == 0 {
		images = ExtractImagesFromContent(currentMessage.Content)
	}
	var kiroImages []map[string]any
	if len(images) > 0 {
		kiroImages = ConvertImagesToKiroFormat(images)
	}

	userInputContext := map[string]any{}

	if kiroTools := ConvertToolsToKiroFormat(processedTools); len(kiroTools) > 0 {
		userInputContext["tools"] = kiroTools
	}

	if len(currentMessage.ToolResults) > 0 {
		if kiroToolResults := ConvertToolResultsToKiroFormat(currentMessage.ToolResults); len(kiroToolResults) > 0 {
			userInputContext["toolResults"] = kiroToolResults
		}
	} else if toolResults := ExtractToolResultsFromContent(currentMessage.Content); len(toolResults) > 0 {
		userInputContext["toolResults"] = toolResults
	}

	// La inyección de thinking tags ocurre DESPUÉS de todo lo anterior, y
	// solo para el mensaje actual cuando es de usuario.
	if currentMessage.Role == "user" {
		currentContent = InjectThinkingTags(currentContent, thinkingCfg)
	}

	userInputMessage := map[string]any{
		"content": currentContent,
		"modelId": modelID,
		"origin":  "AI_EDITOR",
	}
	if len(kiroImages) > 0 {
		userInputMessage["images"] = kiroImages
	}
	if len(userInputContext) > 0 {
		userInputMessage["userInputMessageContext"] = userInputContext
	}

	if conversationID == "" {
		conversationID = utils.NewUUID()
	}

	conversationState := map[string]any{
		"chatTriggerType": "MANUAL",
		"conversationId":  conversationID,
		"currentMessage": map[string]any{
			"userInputMessage": userInputMessage,
		},
	}
	if len(history) > 0 {
		conversationState["history"] = history
	}

	payload := map[string]any{
		"conversationState": conversationState,
	}
	if profileArn != "" {
		payload["profileArn"] = profileArn
	}

	// Guarda de tamaño de payload (AUTO_TRIM_PAYLOAD / KIRO_MAX_PAYLOAD_BYTES):
	// ver el docstring de esta función y el comentario de estas dos
	// variables en thinking.go. Sin implementar a propósito — fuera del
	// alcance de esta tarea, y nunca ejercitado por el corpus.
	if AutoTrimPayload {
		_ = KiroMaxPayloadBytes
	}

	return KiroPayloadResult{Payload: payload, ToolDocumentation: toolDocumentation}
}

// appendSystemPromptAddition aplica una adición al system prompt con la
// misma regla que el original repite tres veces (para toolDocumentation, la
// adición de thinking y la de truncation recovery):
//
//	full_system_prompt = full_system_prompt + addition if full_system_prompt else addition.strip()
//
// Si addition está vacía, no hace nada. Si prompt ya tiene contenido, se
// concatena tal cual (sin separador — las adiciones ya empiezan por
// "\n\n---\n"). Si prompt estaba vacío, el resultado es
// strings.TrimSpace(addition), no la adición cruda.
func appendSystemPromptAddition(prompt, addition string) string {
	if addition == "" {
		return prompt
	}
	if prompt != "" {
		return prompt + addition
	}
	return strings.TrimSpace(addition)
}
