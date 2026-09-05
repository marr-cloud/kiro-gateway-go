// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package validationerrors construye el envoltorio de la respuesta 422 que
// el gateway devuelve cuando las peticiones no pasan la validación de modelo.
//
// Forma. Deriva del original y del spec §4.3 (Fuera de alcance) y de
// docs/DIFFERENCES.md §2: el port mantiene el envoltorio (`detail` con una
// lista de fallos que llevan `loc`, `msg` y `type`, más `body` con el cuerpo
// de la petición truncado a 500 caracteres), pero no reproduce la forma
// literal de Pydantic v2 con sus campos `input` y `url`. Los clientes leen el
// mensaje y siguen; nadie consume la estructura interna de forma programática.
//
// Saneado. El original (kiro/exceptions.py :: sanitize_validation_errors)
// convierte los valores de tipo bytes a cadena porque Pydantic v2 puede
// inyectar bytes en la lista de errores y bytes no es serializable a JSON.
// Se replica aquí: aunque el port no usa Pydantic, es la razón por la que la
// función existe en el original y se conserva por consistencia y por si un
// consumidor futuro introduce valores no serializables en Loc.
//
// Truncado. `body[:500]` en Python 3 opera sobre str (code points). El port
// trunca por runas, no por bytes: 500 significa 500 caracteres Unicode. Ver
// el informe de la fase 2a para la traza de esta decisión.
package validationerrors

import (
	"strings"
	"unicode/utf8"
)

// Failure es un fallo de validación individual: la ruta del campo (`loc`),
// el mensaje humano (`msg`) y el identificador de la clase de error (`type`).
// Loc es []any porque puede mezclar strings (nombres de campo) e ints
// (índices en listas), y esa asimetría viene del original.
type Failure struct {
	Loc  []any  `json:"loc"`
	Msg  string `json:"msg"`
	Type string `json:"type"`
}

// Response es el envoltorio JSON del 422: la lista de fallos y el cuerpo
// truncado de la petición.
type Response struct {
	Detail []Failure `json:"detail"`
	Body   string    `json:"body"`
}

// maxBodyRunes es el límite del cuerpo truncado, expresado en code points
// Unicode para matchear el `body_str[:500]` del original.
const maxBodyRunes = 500

// New construye la Response a partir de los fallos y el cuerpo bruto. Trunca
// el cuerpo a 500 runas y convierte los valores []byte en Loc a string para
// garantizar que el resultado sea serializable a JSON.
func New(failures []Failure, body []byte) Response {
	sanitized := sanitizeFailures(failures)
	return Response{
		Detail: sanitized,
		Body:   truncateRunes(bodyToString(body), maxBodyRunes),
	}
}

// bodyToString convierte los bytes de la petición a string sustituyendo
// secuencias UTF-8 inválidas por U+FFFD, como hace `body.decode("utf-8",
// errors="replace")` en el original.
func bodyToString(body []byte) string {
	return strings.ToValidUTF8(string(body), string(utf8.RuneError))
}

// truncateRunes recorta s a como mucho n runas. Devuelve s tal cual si ya
// cabe.
func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	runes := []rune(s)
	return string(runes[:n])
}

// sanitizeFailures devuelve una copia de failures con los valores []byte de
// Loc convertidos a string mediante decodificación UTF-8 con reemplazo.
func sanitizeFailures(failures []Failure) []Failure {
	if failures == nil {
		return []Failure{}
	}
	out := make([]Failure, len(failures))
	for i, f := range failures {
		out[i] = Failure{
			Loc:  sanitizeLoc(f.Loc),
			Msg:  f.Msg,
			Type: f.Type,
		}
	}
	return out
}

func sanitizeLoc(loc []any) []any {
	if loc == nil {
		return nil
	}
	out := make([]any, len(loc))
	for i, v := range loc {
		if b, ok := v.([]byte); ok {
			out[i] = strings.ToValidUTF8(string(b), string(utf8.RuneError))
		} else {
			out[i] = v
		}
	}
	return out
}
