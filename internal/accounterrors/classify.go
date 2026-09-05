// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package accounterrors clasifica los errores de la API de Kiro para decidir
// si el gestor de cuentas del gateway debe hacer failover a otra cuenta o
// devolver el error al cliente inmediatamente.
//
// Es un port literal de kiro/account_errors.py (jwadow/kiro-gateway, fijado
// en el commit a5292ca). El corpus golden testdata/account_errors/classify_error
// verifica la paridad contra los 27 casos grabados de la suite del original.
//
// La tabla de decisión (spec §6.11):
//
//	Recuperable (siguiente cuenta):  402, 403, 429, 400 + INVALID_MODEL_ID
//	Fatal (al cliente):              400 + CONTENT_LENGTH_EXCEEDS_THRESHOLD,
//	                                 otros 400, 422, 5xx, resto
//
// Que un contexto demasiado grande sea FATAL es deliberado: no se arregla
// cambiando de cuenta. Ver la fuente para los comentarios completos.
package accounterrors

// Type es la categoría de un error de la API de Kiro. El valor subyacente es
// la cadena que emitía el ErrorType.value del enum de Python del original, que
// es lo que el corpus grabó como salida ("fatal" / "recoverable").
type Type string

const (
	// Fatal indica un error en la propia petición: se devuelve al cliente sin
	// probar otras cuentas porque va a fallar en todas.
	Fatal Type = "fatal"

	// Recoverable indica un error específico de la cuenta: se debería probar la
	// siguiente cuenta disponible.
	Recoverable Type = "recoverable"
)

// Identificadores de `reason` que Classify inspecciona. El original los
// compara como literales; se recogen aquí como constantes con nombre para
// evitar el error de escritura silencioso y para dejar explícito el vocabulario.
const (
	// ReasonInvalidModelID: modelo no disponible para la suscripción de la
	// cuenta actual. Otras cuentas pueden tener acceso, así que es recuperable.
	ReasonInvalidModelID = "INVALID_MODEL_ID"

	// ReasonContentLengthExceedsThreshold: contexto demasiado grande. Va a
	// fallar en cualquier cuenta, así que es fatal.
	ReasonContentLengthExceedsThreshold = "CONTENT_LENGTH_EXCEEDS_THRESHOLD"
)

// Classify decide la categoría de un error de la API de Kiro a partir del
// código HTTP y del `reason` que devuelve el servidor. reason == "" representa
// el None del original (Optional[str] sin valor).
//
// Es una traducción literal de kiro.account_errors.classify_error; el orden
// de las ramas y el fallback a Fatal siguen a la fuente.
func Classify(statusCode int, reason string) Type {
	switch statusCode {
	case 402, 403, 429:
		return Recoverable
	case 400:
		switch reason {
		case ReasonInvalidModelID:
			return Recoverable
		case ReasonContentLengthExceedsThreshold:
			return Fatal
		default:
			return Fatal
		}
	case 422:
		return Fatal
	}
	if statusCode >= 500 && statusCode < 600 {
		return Fatal
	}
	// Default: cualquier código desconocido es Fatal para no gastar reintentos.
	return Fatal
}
