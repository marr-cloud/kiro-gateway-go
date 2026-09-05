// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package tokenizer reproduce el conteo de tokens de `kiro.tokenizer` del
// upstream sobre el vocabulario `cl100k_base` de OpenAI, embebido en el
// binario.
//
// Estrategia (Opción A del plan de fase 2b): stdlib pura y el fichero de
// vocabulario `cl100k_base.tiktoken` embebido con `go:embed`. El fichero se
// genera desde el mismo `tiktoken` que grabó el corpus golden
// (`.upstream/.venv`, versión fijada en `tools/corpus/requirements.lock`),
// nunca se descarga en tiempo de ejecución ni en tiempo de build; de esta
// forma el vocabulario que el port carga es byte a byte el que produjo los
// recuentos de referencia. La regeneración se hace con `task tokenizer:vocab`;
// ver `NOTICE` para procedencia y licencia.
//
// Este fichero solo se ocupa de embeber y parsear el vocabulario a un mapa
// `bytes del token → rango`. El codificador (pre-tokenizador + BPE) se
// implementa en las tareas 2 y 3 del plan sobre esta misma estructura.
package tokenizer

import (
	_ "embed"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
)

// El fichero se genera desde el venv fijado con `task tokenizer:vocab`. Formato
// canónico de tiktoken: una línea por token, `base64(bytes del token) <espacio>
// rank`, ordenadas por rango ascendente y terminadas en LF.
//
//go:embed cl100k_base.tiktoken
var cl100kBaseVocab string

// expectedVocabSize es el número exacto de mergeable ranks en `cl100k_base`
// (rangos 0..100255). Un vocabulario de cualquier otro tamaño es corrupción,
// no un fallo blando: cargarlo con menos entradas produciría recuentos de
// tokens silenciosamente distintos de los del original.
const expectedVocabSize = 100256

// ranks mapea cada secuencia de bytes crudos del token (como `string`, que en
// Go admite bytes arbitrarios) a su rango BPE. Se rellena una sola vez, en la
// inicialización del paquete, a partir del vocabulario embebido. La clave se
// usa como `string([]byte{...})` desde el codificador de la tarea 2.
var ranks = mustLoadRanks(cl100kBaseVocab)

// mustLoadRanks parsea el vocabulario embebido en formato tiktoken. Hace panic
// si el fichero está truncado, mal formado o no contiene exactamente
// expectedVocabSize entradas. Un vocabulario incompleto es un bug del build,
// no una condición recuperable.
func mustLoadRanks(data string) map[string]int {
	m := make(map[string]int, expectedVocabSize)
	line := 0
	for len(data) > 0 {
		line++
		var raw string
		if i := strings.IndexByte(data, '\n'); i >= 0 {
			raw = data[:i]
			data = data[i+1:]
		} else {
			raw = data
			data = ""
		}
		if raw == "" {
			continue
		}
		sp := strings.IndexByte(raw, ' ')
		if sp < 0 {
			panic(fmt.Sprintf("tokenizer: cl100k_base.tiktoken línea %d sin separador de espacio", line))
		}
		tok, err := base64.StdEncoding.DecodeString(raw[:sp])
		if err != nil {
			panic(fmt.Sprintf("tokenizer: cl100k_base.tiktoken línea %d: base64: %v", line, err))
		}
		rank, err := strconv.Atoi(raw[sp+1:])
		if err != nil {
			panic(fmt.Sprintf("tokenizer: cl100k_base.tiktoken línea %d: rango: %v", line, err))
		}
		if _, dup := m[string(tok)]; dup {
			panic(fmt.Sprintf("tokenizer: cl100k_base.tiktoken línea %d: token duplicado", line))
		}
		m[string(tok)] = rank
	}
	if len(m) != expectedVocabSize {
		panic(fmt.Sprintf("tokenizer: se esperaban %d rangos en cl100k_base, hay %d", expectedVocabSize, len(m)))
	}
	return m
}
