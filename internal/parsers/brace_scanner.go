// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package parsers

// findMatchingBrace localiza la posición de la llave de cierre que empareja
// con la de apertura en buf[start], contando anidamiento y respetando
// cadenas entre comillas dobles y sus escapes. Port literal de
// kiro.parsers::find_matching_brace (.upstream/kiro/parsers.py:39-89).
//
// El original opera sobre str (posiciones en puntos de código Unicode); esta
// versión opera sobre bytes. Los cuatro caracteres estructurales que el
// escáner reconoce ('"', '\\', '{', '}') son ASCII, y ASCII nunca aparece
// como byte de continuación ni como parte de una secuencia UTF-8
// multibyte (propiedad de autosincronización de UTF-8) — así que el
// recorrido byte a byte encuentra exactamente la misma subcadena que el
// recorrido carácter a carácter de Python, aunque el índice entero
// devuelto sea un offset de bytes en vez de un offset de puntos de código.
// El corpus find_matching_brace (39 casos) es enteramente ASCII, así que
// para esos casos los dos offsets además coinciden numéricamente.
//
// A diferencia del original, un start negativo devuelve -1 en vez de
// indexar desde el final como haría Python (text[-1]). Ningún caso del
// corpus pasa un start negativo — todas las llamadas reales vienen de
// buffer.find()/strings.Index(), que nunca devuelven un valor negativo
// salvo -1, ya filtrado antes de llegar aquí.
func findMatchingBrace(buf []byte, start int) int {
	if start < 0 || start >= len(buf) || buf[start] != '{' {
		return -1
	}

	braceCount := 0
	inString := false
	escapeNext := false

	for i := start; i < len(buf); i++ {
		c := buf[i]

		if escapeNext {
			escapeNext = false
			continue
		}

		if c == '\\' && inString {
			escapeNext = true
			continue
		}

		if c == '"' {
			inString = !inString
			continue
		}

		if !inString {
			switch c {
			case '{':
				braceCount++
			case '}':
				braceCount--
				if braceCount == 0 {
					return i
				}
			}
		}
	}

	return -1
}
