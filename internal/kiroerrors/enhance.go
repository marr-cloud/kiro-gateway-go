// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package kiroerrors enriquece los errores crudos que devuelve la API de Kiro
// con mensajes accionables para el usuario final. Es un port literal de
// kiro/kiro_errors.py (jwadow/kiro-gateway, fijado en el commit a5292ca) y su
// paridad se verifica contra los 21 casos golden de
// testdata/kiro_errors/enhance_kiro_error.
//
// El original recibe el cuerpo del error ya parseado a diccionario y usa dos
// operaciones distintas sobre él:
//
//	original = error_json.get("message")         # ausente o null -> None -> "Unknown error"
//	reason   = error_json.get("reason")          # ausente o null -> None -> "UNKNOWN"
//	if "reason" in error_json and reason != "UNKNOWN":  # <-- PRESENCIA de la clave
//	    user_message = f"{original} (reason: {reason})"
//
// La comprobación de presencia significa que una firma (message, reason string)
// pierde información: no distingue "clave ausente" de "cadena vacía", y el corpus
// contiene ambos casos con salidas distintas ({"message":"Error 3"} sin clave
// reason -> "Error 3"; {"message":"","reason":"SOME_ERROR"} -> " (reason: SOME_ERROR)").
// Por eso Enhance toma el mapa ya parseado con json.RawMessage: preserva
// simultáneamente la presencia de cada clave y si su valor es null.
package kiroerrors

import (
	"encoding/json"
	"strings"
)

// Info es la información estructurada de un error de la API de Kiro después de
// enriquecerlo. Los nombres de los campos JSON son los que el corpus grabó y
// no se pueden cambiar. Todos los campos son cadenas, así que Info es
// comparable con ==, y eso es lo que usan los tests golden.
type Info struct {
	Reason          string `json:"reason"`
	UserMessage     string `json:"user_message"`
	OriginalMessage string `json:"original_message"`
}

// Constantes de vocabulario. El original las escribe como literales dentro de la
// función; extraerlas evita el error de escritura silencioso y deja explícito el
// contrato con el corpus (fijado en el commit a5292ca del upstream).
const (
	// unknownMessage es el fallback para original_message cuando la clave
	// "message" falta o es JSON null.
	unknownMessage = "Unknown error"

	// unknownReason es el fallback para reason cuando la clave "reason" falta
	// o es JSON null. El original lo compara además contra el literal "null"
	// en la rama especial de "Improperly formed request." (ver más abajo).
	unknownReason = "UNKNOWN"

	// reasonContentLengthExceedsThreshold, reasonMonthlyRequestCount,
	// reasonInvalidModelID son los tres `reason` cuya sustitución de mensaje
	// tiene texto propio en el original.
	reasonContentLengthExceedsThreshold = "CONTENT_LENGTH_EXCEEDS_THRESHOLD"
	reasonMonthlyRequestCount           = "MONTHLY_REQUEST_COUNT"
	reasonInvalidModelID                = "INVALID_MODEL_ID"

	// improperlyFormedOriginal es el mensaje literal que dispara la respuesta
	// especial con la URL de issues de GitHub cuando `reason` es desconocido.
	improperlyFormedOriginal = "Improperly formed request."

	// messageContentLengthExceedsThreshold, messageMonthlyRequestCount y
	// messageInvalidModelID son los tres mensajes literales que el usuario ve
	// cuando el `reason` es conocido. Copiados exactamente del original.
	messageContentLengthExceedsThreshold = "Model context limit reached. Conversation size exceeds model capacity."
	messageMonthlyRequestCount           = "Monthly request limit exceeded. Account has reached its monthly quota."
	messageInvalidModelID                = "Invalid model ID or insufficient subscription level to use it."

	// messageImproperlyFormed es la respuesta especial para
	// "Improperly formed request." con reason desconocido. En el original está
	// escrita como concatenación de dos literales sin espacio entre ellos, con
	// el resultado "...logs at:https://github.com/...". El corpus refleja esa
	// ausencia de espacio antes de "https://" y hay que reproducirla tal cual.
	messageImproperlyFormed = "Kiro API rejected the request. If problem persists, open issue with info and attached debug logs at:https://github.com/jwadow/kiro-gateway/issues"
)

// Enhance devuelve la información enriquecida para un error de la API de Kiro
// a partir del cuerpo JSON ya deserializado a map[string]json.RawMessage.
//
// La forma esperada del cuerpo es {"message": "...", "reason": "..."}. Ambos
// campos son opcionales. Otras claves (por ejemplo "extra_field") se ignoran,
// como hace el original: se recorre solo por get("message") y get("reason").
//
// Semántica reproducida de kiro.kiro_errors.enhance_kiro_error, en orden:
//
//  1. original_message = error_json.get("message"), o "Unknown error" si el
//     valor es None (clave ausente o JSON null).
//  2. reason = error_json.get("reason"), o "UNKNOWN" si el valor es None.
//  3. Si reason ∈ {CONTENT_LENGTH_EXCEEDS_THRESHOLD, MONTHLY_REQUEST_COUNT,
//     INVALID_MODEL_ID}, user_message es el literal correspondiente.
//  4. Si original_message == "Improperly formed request." y reason ∈
//     {"UNKNOWN", "null"} (tras la coerción del punto 2), user_message es la
//     respuesta especial con la URL de issues.
//  5. En otro caso, si la clave "reason" ESTÁ PRESENTE en el mapa y reason
//     != "UNKNOWN", user_message = original_message + " (reason: " + reason + ")".
//     En caso contrario, user_message = original_message.
func Enhance(errorJSON map[string]json.RawMessage) Info {
	// Punto 1: original_message.
	original := unknownMessage
	if raw, ok := errorJSON["message"]; ok && !isJSONNull(raw) {
		// Solo aceptamos cadenas: si el valor no es una cadena JSON, caemos al
		// fallback. El original hace lo equivalente al leer un tipo cualquiera,
		// pero el corpus solo contiene cadenas o null en este campo.
		var s string
		if err := json.Unmarshal(raw, &s); err == nil {
			original = s
		}
	}

	// Punto 2: reason y presencia de la clave.
	reason := unknownReason
	_, reasonKeyPresent := errorJSON["reason"]
	if reasonKeyPresent {
		if raw := errorJSON["reason"]; !isJSONNull(raw) {
			var s string
			if err := json.Unmarshal(raw, &s); err == nil {
				reason = s
			}
		}
	}

	// Puntos 3, 4 y 5: cadena de if/elif del original. El orden importa: si un
	// mensaje "Improperly formed request." llegase con reason INVALID_MODEL_ID
	// (no ocurre en el corpus, pero el original lo decidiría así), gana el
	// mensaje del reason conocido.
	var userMessage string
	switch reason {
	case reasonContentLengthExceedsThreshold:
		userMessage = messageContentLengthExceedsThreshold
	case reasonMonthlyRequestCount:
		userMessage = messageMonthlyRequestCount
	case reasonInvalidModelID:
		userMessage = messageInvalidModelID
	default:
		// La rama especial de "Improperly formed request." replica el elif del
		// original. `reason in (None, "UNKNOWN", "null")`: None ya se coerció a
		// "UNKNOWN" en el punto 2, así que aquí basta comparar contra los dos
		// literales que quedan.
		if original == improperlyFormedOriginal && (reason == unknownReason || reason == "null") {
			userMessage = messageImproperlyFormed
		} else if reasonKeyPresent && reason != unknownReason {
			// La rama genérica de "unknown reason con sufijo" del original.
			// AQUÍ es donde la presencia de la clave importa: {"message":"X"}
			// (sin reason) produce "X", pero {"message":"X","reason":""}
			// produciría "X (reason: )" con clave presente y reason coercido a
			// cadena vacía (el corpus no contiene ese caso pero el original lo
			// decide así por construcción).
			userMessage = original + " (reason: " + reason + ")"
		} else {
			userMessage = original
		}
	}

	return Info{
		Reason:          reason,
		UserMessage:     userMessage,
		OriginalMessage: original,
	}
}

// isJSONNull decide si un RawMessage es el literal JSON `null`. Los valores que
// salen de json.Unmarshal a un map suelen venir sin espacios, pero recortarlos
// hace el helper robusto ante entradas construidas a mano en tests.
func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}
