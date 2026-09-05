// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package tokenizer

import "testing"

// TestVocabLoads confirma que el paquete se inicializa sin panic: si
// `mustLoadRanks` hubiera saltado, la propia carga del binario de test habría
// abortado antes de este punto, así que basta con comprobar que el mapa está
// poblado.
func TestVocabLoads(t *testing.T) {
	if ranks == nil {
		t.Fatal("ranks es nil tras la inicialización del paquete")
	}
}

// TestVocabSize fija el tamaño exacto del vocabulario. Cambia SIEMPRE junto
// con el fichero embebido, nunca por separado.
func TestVocabSize(t *testing.T) {
	if got := len(ranks); got != expectedVocabSize {
		t.Fatalf("len(ranks) = %d, quiero %d", got, expectedVocabSize)
	}
	if expectedVocabSize != 100256 {
		t.Fatalf("expectedVocabSize = %d, quiero 100256 (ground truth de cl100k_base)", expectedVocabSize)
	}
}

// TestVocabSpotChecks fija rangos conocidos para tokens concretos. Los valores
// se derivaron del venv fijado ejecutando
//
//	.upstream/.venv/Scripts/python.exe -c \
//	  "import tiktoken; mr = tiktoken.get_encoding('cl100k_base')._mergeable_ranks; \
//	   print(mr[b' world'], mr[b'the'], mr[b' hello'], mr[b'{'], mr[b'A'])"
//
// contra `tiktoken==0.14.0` de `tools/corpus/requirements.lock`. Sirven como
// sanity check del parseo: si el orden de las líneas, la base64 o el número
// se leyeran mal, alguno de estos rangos cambiaría.
func TestVocabSpotChecks(t *testing.T) {
	cases := []struct {
		token string
		want  int
	}{
		{" world", 1917},
		{"the", 1820},
		{" hello", 24748},
		{"{", 90},
		{"A", 32},
	}
	for _, c := range cases {
		got, ok := ranks[c.token]
		if !ok {
			t.Errorf("ranks[%q] no está en el vocabulario", c.token)
			continue
		}
		if got != c.want {
			t.Errorf("ranks[%q] = %d, quiero %d", c.token, got, c.want)
		}
	}
}

// TestVocabHasAllSingleBytes asegura que cada byte 0..255 es un token base
// en cl100k_base. El BPE de tiktoken fusiona sobre bytes crudos, así que la
// existencia de todos los tokens de un byte es un invariante del que depende
// el codificador de la tarea 2.
func TestVocabHasAllSingleBytes(t *testing.T) {
	for b := 0; b < 256; b++ {
		key := string([]byte{byte(b)})
		if _, ok := ranks[key]; !ok {
			t.Errorf("byte 0x%02x no está en el vocabulario", b)
		}
	}
}

// TestVocabSingleByteSpotChecks fija los rangos de algunos tokens de un byte
// para atrapar reordenaciones de líneas o pérdidas de bytes concretos en el
// dumper. Valores derivados del mismo venv.
func TestVocabSingleByteSpotChecks(t *testing.T) {
	cases := []struct {
		b    byte
		want int
	}{
		{0x00, 188}, // el byte nulo tiene rango 188 en cl100k_base
		{'\n', 198}, // salto de línea
		{0xff, 187}, // el último byte
	}
	for _, c := range cases {
		key := string([]byte{c.b})
		got, ok := ranks[key]
		if !ok {
			t.Errorf("byte 0x%02x no está en el vocabulario", c.b)
			continue
		}
		if got != c.want {
			t.Errorf("ranks[byte 0x%02x] = %d, quiero %d", c.b, got, c.want)
		}
	}
}
