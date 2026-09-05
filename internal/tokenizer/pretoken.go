// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package tokenizer

import (
	"unicode"
	"unicode/utf8"
)

// splitPretokens reproduce el pre-tokenizador de `cl100k_base` de tiktoken.
// El patrón canónico es:
//
//	'(?i:[sdmt]|ll|ve|re)|[^\r\n\p{L}\p{N}]?+\p{L}++|\p{N}{1,3}+|
//	 ?[^\s\p{L}\p{N}]++[\r\n]*+|\s++$|\s*[\r\n]|\s+(?!\S)|\s
//
// El motor `regexp` de Go (RE2) no puede expresarlo: los cuantificadores
// posesivos y el `(?!\S)` de la alternativa 7 quedan fuera de su gramática. El
// scanner es a mano, recorriendo el texto por posición de byte y probando las
// 8 alternativas EN ORDEN; la primera que consume ≥1 byte gana. El orden es
// parte del contrato: 5/6/7/8 son todas de whitespace y sólo el orden explica
// por qué `"\n\n\n"` va por alt 5 y `"\n\nx"` va por alt 6, o por qué el
// último espacio de una carrera se retiene para arrancar la siguiente pieza.
//
// Puntos concretos que hay que respetar byte a byte contra
// `regex.findall(pat, text)`:
//
//   - alt 1 (`'(?i:[sdmt]|ll|ve|re)`): la alternación intenta primero
//     `[sdmt]` (una letra) y luego los dígrafos `ll|ve|re`, así que `'ss` se
//     parte en `'s` y `s`, no en `'ss`. La CI del `regex` de Python plega
//     `ſ` (U+017F) a `s`; el resto de las letras (`d m t l v r e`) no tienen
//     pliegue Unicode que las alcance, así que basta ASCII para ellas.
//
//   - alt 2 (`[^\r\n\p{L}\p{N}]?+\p{L}++`): el opcional inicial es posesivo,
//     de modo que si consume un carácter y luego no hay letras la alternativa
//     falla en bloque (no reintenta con 0). El excluido son CR/LF/letra/
//     número, no toda la clase `\s`, así que TAB, NBSP, `\u2028`, `\u2029`,
//     emojis, puntuación… pueden actuar como leading.
//
//   - alt 3 (`\p{N}{1,3}+`): posesivo, con lo que `1234` se parte en `123` y
//     `4`, y `12345` en `123` y `45`.
//
//   - alt 4 (` ?[^\s\p{L}\p{N}]++[\r\n]*+`): el leading es un ESPACIO literal
//     U+0020, no toda `\s`. En posesivo/greedy da el mismo resultado en la
//     práctica: si tras consumir el espacio no hay non-space, retroceder a 0
//     también fallaría porque el siguiente carácter sigue siendo espacio, así
//     que lo implemento como si fuese posesivo para evitar un backtrack
//     redundante.
//
//   - alt 5 (`\s++$`): whitespace hasta fin de cadena. Gana antes que alt 6
//     cuando toda la cola es whitespace: `"\n\n\n"` es una única pieza.
//
//   - alt 6 (`\s*[\r\n]`): cero o más `\s` seguidos de exactamente un CR/LF.
//     Con `\s*` greedy, el motor busca la coincidencia más larga; equivale a
//     "consume hasta e incluye el último CR/LF de la carrera de whitespace
//     que arranca en `i`". Se comprobó contra el venv en casos como
//     `"\r\n\r\nx"` → `["\r\n\r\n", "x"]` y `"\n \n \n x"` →
//     `["\n \n \n", " x"]`.
//
//   - alt 7 (`\s+(?!\S)`): whitespace `+` greedy con look-ahead negativo. El
//     brief lo formula como "retén el último whitespace de la carrera si el
//     siguiente carácter no es whitespace"; equivale exactamente a: una
//     carrera de N whitespaces (sin CR/LF; alt 6 los habría cazado antes)
//     seguida de non-whitespace produce una pieza de N-1 whitespaces si
//     N≥2, y falla si N=1 (entonces cae a alt 8). Verificado con
//     `"a  b"` → `["a", " ", " b"]` y `"\t\t\tx"` → `["\t\t", "\tx"]`.
//
//   - alt 8 (`\s`): un único whitespace. Fallback.
//
// Todos los offsets son de byte, pero los "caracteres" se cuentan como runes
// UTF-8: los predicados `\p{L}`, `\p{N}`, `\s` se aplican al rune decodificado.
// `unicode.IsLetter` = Python `\p{L}` (Lu|Ll|Lt|Lm|Lo); `unicode.IsNumber` =
// `\p{N}` (Nd|Nl|No); `unicode.IsSpace` coincide con `\s` del módulo `regex`
// en modo Unicode (comprobado directo contra el venv sobre `\u0009`-`\u000d`,
// U+0020, U+0085, U+00A0, U+1680, U+2007, U+2028, U+2029, U+202F, U+205F,
// U+3000, y contra los que están fuera: U+001C-U+001F, U+FEFF, U+180E, U+200B).
func splitPretokens(text string) []string {
	if text == "" {
		return nil
	}
	n := len(text)
	pieces := make([]string, 0, n/4+1)
	i := 0
	for i < n {
		end := nextPretokenEnd(text, i, n)
		if end <= i {
			// Invariante roto: nextPretokenEnd garantiza avance ≥1 byte. Si
			// no avanza, forzamos un rune para no colgar el proceso; llegar
			// aquí implica un bug en las 8 alternativas o input UTF-8 inválido
			// con size==0.
			_, size := utf8.DecodeRuneInString(text[i:])
			if size == 0 {
				size = 1
			}
			end = i + size
		}
		pieces = append(pieces, text[i:end])
		i = end
	}
	return pieces
}

// nextPretokenEnd devuelve el offset de byte donde termina la pieza que
// arranca en i. Prueba las alternativas en orden y devuelve el primer end
// mayor que i.
func nextPretokenEnd(text string, i, n int) int {
	if end, ok := altApostrophe(text, i, n); ok {
		return end
	}
	if end, ok := altLetters(text, i, n); ok {
		return end
	}
	if end, ok := altDigits(text, i, n); ok {
		return end
	}
	if end, ok := altOther(text, i, n); ok {
		return end
	}
	if end, ok := altWSEnd(text, i, n); ok {
		return end
	}
	if end, ok := altWSCRLF(text, i, n); ok {
		return end
	}
	if end, ok := altWSHoldback(text, i, n); ok {
		return end
	}
	if end, ok := altSingleWS(text, i, n); ok {
		return end
	}
	// No debería ocurrir con texto UTF-8 válido: alt 8 casa cualquier
	// whitespace y alt 2/3/4 cubren el resto de runes. La llamada externa
	// gestiona el fallback.
	return i
}

// Alt 1: '(?i:[sdmt]|ll|ve|re)
//
// La CI del `regex` de Python es Unicode y pliega `ſ` (U+017F) a `s`. Ese es
// el único carácter no-ASCII que puede alcanzar el conjunto `[sdmt]` por
// case-folding; se verificó contra `CaseFolding.txt` de Unicode (entrada
// `017F; C; 0073;`) y con el venv sobre `"'\u017fabc"`. Las letras `d m t l
// v r e` no tienen pliegue Unicode a estas letras ASCII, así que basta con
// case-insensitive ASCII para el resto.
func altApostrophe(text string, i, n int) (int, bool) {
	if i >= n || text[i] != '\'' {
		return 0, false
	}
	if i+1 >= n {
		return 0, false
	}
	r1, size1 := utf8.DecodeRuneInString(text[i+1:])
	// Alternación regex: prueba `[sdmt]` (una letra) antes que los dígrafos.
	if isApostropheSDMT(r1) {
		return i + 1 + size1, true
	}
	if i+1+size1 >= n {
		return 0, false
	}
	r2, size2 := utf8.DecodeRuneInString(text[i+1+size1:])
	if isApostropheDigraph(r1, r2) {
		return i + 1 + size1 + size2, true
	}
	return 0, false
}

func isApostropheSDMT(r rune) bool {
	switch r {
	case 's', 'S', 'd', 'D', 'm', 'M', 't', 'T':
		return true
	case 0x017F: // LATIN SMALL LETTER LONG S → s (Unicode simple case fold)
		return true
	}
	return false
}

func isApostropheDigraph(a, b rune) bool {
	la := asciiLower(a)
	lb := asciiLower(b)
	switch {
	case la == 'l' && lb == 'l':
		return true
	case la == 'v' && lb == 'e':
		return true
	case la == 'r' && lb == 'e':
		return true
	}
	return false
}

func asciiLower(r rune) rune {
	if r >= 'A' && r <= 'Z' {
		return r + 32
	}
	return r
}

// Alt 2: [^\r\n\p{L}\p{N}]?+\p{L}++
//
// El leading opcional consume 0 ó 1 rune que no sea CR/LF/letra/número. Es
// posesivo: si consume 1 y luego no hay letras, la alternativa falla sin
// intentar 0. En la práctica, si al no consumir el leading el rune actual no
// es letra, alt 2 falla igual: el efecto de la posesividad sólo se nota en la
// no-terminación anticipada.
func altLetters(text string, i, n int) (int, bool) {
	if i >= n {
		return 0, false
	}
	j := i
	r, size := utf8.DecodeRuneInString(text[j:])
	if r != '\r' && r != '\n' && !unicode.IsLetter(r) && !unicode.IsNumber(r) {
		j += size
	}
	// `\p{L}++`: al menos una letra.
	lettersStart := j
	for j < n {
		r, size := utf8.DecodeRuneInString(text[j:])
		if !unicode.IsLetter(r) {
			break
		}
		j += size
	}
	if j == lettersStart {
		return 0, false
	}
	return j, true
}

// Alt 3: \p{N}{1,3}+
//
// De 1 a 3 dígitos, posesivo. `12345` se parte en `123` y `45`; `1234abc` en
// `123`, `4`, `abc`.
func altDigits(text string, i, n int) (int, bool) {
	j := i
	count := 0
	for j < n && count < 3 {
		r, size := utf8.DecodeRuneInString(text[j:])
		if !unicode.IsNumber(r) {
			break
		}
		j += size
		count++
	}
	if count == 0 {
		return 0, false
	}
	return j, true
}

// Alt 4:  ?[^\s\p{L}\p{N}]++[\r\n]*+
//
// Espacio inicial LITERAL U+0020 (no toda `\s`); luego 1+ chars que no son
// whitespace/letra/número; luego 0+ CR/LF. Como el body `[^\s\p{L}\p{N}]++`
// es posesivo, la trampa clásica es: si consumes el espacio y no viene un
// non-\s, retroceder a 0 leading tampoco arregla nada porque el carácter en
// `i` sigue siendo espacio (∈ \s) y la clase excluida no lo admite. Así que
// lo trato como posesivo: si el espacio consumido no arranca un body válido,
// alt 4 falla directamente.
func altOther(text string, i, n int) (int, bool) {
	j := i
	if j < n && text[j] == ' ' {
		j++
	}
	bodyStart := j
	for j < n {
		r, size := utf8.DecodeRuneInString(text[j:])
		if unicode.IsSpace(r) || unicode.IsLetter(r) || unicode.IsNumber(r) {
			break
		}
		j += size
	}
	if j == bodyStart {
		return 0, false
	}
	for j < n && (text[j] == '\r' || text[j] == '\n') {
		j++
	}
	return j, true
}

// Alt 5: \s++$
//
// Toda la cola es whitespace. Gana a alt 6 cuando la carrera llega al fin de
// cadena: `"\n\n\n"` es UNA pieza, no tres alt-6.
func altWSEnd(text string, i, n int) (int, bool) {
	j := i
	for j < n {
		r, size := utf8.DecodeRuneInString(text[j:])
		if !unicode.IsSpace(r) {
			return 0, false
		}
		j += size
	}
	if j == i {
		return 0, false
	}
	return j, true
}

// Alt 6: \s*[\r\n]
//
// `\s*` greedy + un CR/LF. La búsqueda backtracking del motor equivale, para
// una carrera de whitespace que arranca en i, a "casa hasta e incluye el
// ÚLTIMO CR/LF de esa carrera". Ejemplos derivados del venv:
//
//	"\r\n"        → "\r\n"        (2 chars)
//	"\r\n\r\nx"   → "\r\n\r\n"    (backtrack para que el body termine en LF)
//	"\n \n \n x"  → "\n \n \n"    (último LF de la carrera)
//	"\n\r\r x"    → "\n\r\r"      (último CR)
//
// Si no hay CR/LF en la carrera, alt 6 falla y cae a 7 u 8.
func altWSCRLF(text string, i, n int) (int, bool) {
	j := i
	lastCRLFEnd := -1
	for j < n {
		r, size := utf8.DecodeRuneInString(text[j:])
		if !unicode.IsSpace(r) {
			break
		}
		if r == '\r' || r == '\n' {
			lastCRLFEnd = j + size
		}
		j += size
	}
	if lastCRLFEnd < 0 {
		return 0, false
	}
	return lastCRLFEnd, true
}

// Alt 7: \s+(?!\S)
//
// El brief describe la semántica como "consume una carrera de whitespace y,
// si le sigue un non-whitespace, retén el último whitespace" para que arranque
// la siguiente pieza vía alt 2/4. Puesta contra el motor: `\s+` greedy prueba
// la carrera completa; el lookahead negativo `(?!\S)` obliga a que el carácter
// tras el match no sea non-whitespace; si lo es, el motor hace backtrack un
// rune y comprueba de nuevo — el rune retenido, que sí es whitespace, satisface
// el lookahead. Por eso, una carrera de N whitespaces (sin CR/LF: alt 6 los
// caza antes) seguida de non-whitespace da una pieza de N-1 whitespaces cuando
// N≥2, o falla cuando N=1 (entonces cae a alt 8).
func altWSHoldback(text string, i, n int) (int, bool) {
	j := i
	count := 0
	lastStart := -1
	for j < n {
		r, size := utf8.DecodeRuneInString(text[j:])
		if !unicode.IsSpace(r) || r == '\r' || r == '\n' {
			break
		}
		lastStart = j
		count++
		j += size
	}
	if count < 2 {
		return 0, false
	}
	return lastStart, true
}

// Alt 8: \s
//
// Un único whitespace. Fallback final para carreras de un único whitespace no-
// CR/LF seguidas de non-whitespace, y para posiciones que las alternativas
// 5/6/7 dejaron pasar.
func altSingleWS(text string, i, n int) (int, bool) {
	if i >= n {
		return 0, false
	}
	r, size := utf8.DecodeRuneInString(text[i:])
	if !unicode.IsSpace(r) {
		return 0, false
	}
	return i + size, true
}
