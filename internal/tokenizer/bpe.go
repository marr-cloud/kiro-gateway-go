// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package tokenizer

import "math"

// maxRank es el valor centinela para "este par no está en el vocabulario". Se
// usa para inicializar y para el final del array de partes de BPE.
const maxRank = math.MaxInt

// bytePairEncode reproduce el algoritmo `byte_pair_encode` de tiktoken sobre
// una única pre-pieza. Trata la pieza como una secuencia de partes que empieza
// con un byte por parte, y fusiona repetidamente el par adyacente cuyo
// contenido (concatenación de sus bytes) tiene el rango MÁS BAJO en `ranks`,
// hasta que ningún par adyacente es un token conocido. El vocabulario de
// cl100k_base contiene los 256 bytes 0x00..0xff (verificado en `vocab_test.go`
// `TestVocabHasAllSingleBytes`), así que la recursión siempre termina: en el
// peor caso, cada byte de la pieza queda como su propio token.
//
// Implementación equivalente al `_byte_pair_merge` de referencia (Rust) que
// usa tiktoken:
//
//   - `parts[i].start` = offset de byte donde empieza la parte i dentro de la
//     pieza (parts[len-1] es el centinela con start = len(piece)).
//   - `parts[i].rank`  = rango del par (parts[i], parts[i+1]) = rango del
//     substring piece[parts[i].start:parts[i+2].start]. `maxRank` si el par no
//     está en el vocabulario o si i está en el centinela.
//
// Cada iteración: encuentra el `parts[i].rank` mínimo, fusiona parts[i+1] en
// parts[i], y recalcula los rangos de los dos pares afectados (el nuevo par
// que empieza en i y el que termina en i, es decir, i-1). El coste es
// O(N²) por pieza; las pre-piezas son cortas en la práctica (el token más
// largo del vocab tiene 128 bytes; una pre-pieza típica <20), así que no
// merece la pena la estructura de prioridad más sofisticada.
func bytePairEncode(piece []byte) []int {
	if len(piece) == 0 {
		// Invariante externo: `splitPretokens` no emite piezas vacías. Blindaje
		// contra futuras regresiones sin ocultar el bug (la lista vacía es
		// benigna aguas arriba, pero acredito con este comentario).
		return nil
	}
	if len(piece) == 1 {
		return []int{ranks[string(piece)]}
	}

	type part struct {
		start int
		rank  int
	}
	parts := make([]part, len(piece)+1)
	for i := range parts {
		parts[i] = part{start: i, rank: maxRank}
	}

	pairRank := func(idx int) int {
		if idx+2 >= len(parts) {
			return maxRank
		}
		key := string(piece[parts[idx].start:parts[idx+2].start])
		if r, ok := ranks[key]; ok {
			return r
		}
		return maxRank
	}

	// Rangos iniciales de cada par adyacente. Los últimos dos índices (el
	// centinela y el anterior) quedan en maxRank.
	for i := 0; i+2 < len(parts); i++ {
		parts[i].rank = pairRank(i)
	}

	for {
		minRank := maxRank
		minIdx := -1
		for i := 0; i+1 < len(parts); i++ {
			if parts[i].rank < minRank {
				minRank = parts[i].rank
				minIdx = i
			}
		}
		if minIdx < 0 {
			// Ningún par adyacente está en el vocabulario: hemos terminado.
			break
		}
		// Fusiona parts[minIdx+1] dentro de parts[minIdx] borrando el índice
		// minIdx+1. parts[minIdx].start no cambia; parts[minIdx].rank pasa a
		// ser el rango del NUEVO par (parts[minIdx], parts[minIdx+2_viejo]).
		parts = append(parts[:minIdx+1], parts[minIdx+2:]...)
		parts[minIdx].rank = pairRank(minIdx)
		if minIdx > 0 {
			parts[minIdx-1].rank = pairRank(minIdx - 1)
		}
	}

	out := make([]int, 0, len(parts)-1)
	for i := 0; i+1 < len(parts); i++ {
		out = append(out, ranks[string(piece[parts[i].start:parts[i+1].start])])
	}
	return out
}

// EncodeOrdinary devuelve la secuencia de token-ids que produciría
// `tiktoken.get_encoding("cl100k_base").encode_ordinary(text)` byte a byte.
// "Ordinary" implica que los literales de tokens especiales (`<|endoftext|>`,
// `<|fim_prefix|>`, `<|fim_middle|>`, `<|fim_suffix|>`, `<|endofprompt|>`) se
// codifican como texto corriente; no se detectan ni se sustituyen. Esta es la
// función que consumen las tareas 3-6 del plan de fase 2b (los cinco contadores
// del port), así que su fidelidad es el crux del corpus del tokenizer.
//
// Texto vacío devuelve una slice nil (equivalente a `[]int{}` para todos los
// callers actuales, que sólo miran `len(...)`).
func EncodeOrdinary(text string) []int {
	if text == "" {
		return nil
	}
	pieces := splitPretokens(text)
	if len(pieces) == 0 {
		return nil
	}
	out := make([]int, 0, len(pieces))
	for _, p := range pieces {
		out = append(out, bytePairEncode([]byte(p))...)
	}
	return out
}
