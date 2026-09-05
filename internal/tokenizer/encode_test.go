// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package tokenizer

import (
	"reflect"
	"testing"
)

// TestSplitPretokensAlternatives ejerce cada una de las 8 alternativas del
// patrón cl100k_base al menos una vez y varias interacciones sutiles entre
// ellas. Los splits se derivaron del venv fijado con
//
//	regex.findall(tiktoken.get_encoding("cl100k_base")._pat_str, text)
//
// contra `tiktoken==0.14.0`. Si algún split del port se desvía del venv, el
// encoder no puede reproducir `encode_ordinary` byte a byte, así que este
// test es la primera línea de defensa antes del test de IDs de referencia.
func TestSplitPretokensAlternatives(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		// Alt 1 — apóstrofe con letra CI y con dígrafo, formas cortas y largas.
		{"alt1: 's minúscula", "'s", []string{"'s"}},
		{"alt1: 'S mayúscula", "'S", []string{"'S"}},
		{"alt1: 'll dígrafo", "'ll", []string{"'ll"}},
		{"alt1: 'RE mayúsculas dígrafo", "'RE", []string{"'RE"}},
		{"alt1: 'ss se parte en 's + s", "'ss", []string{"'s", "s"}},
		{"alt1: 'l no matchea (falta segunda l)", "'l'll", []string{"'l", "'ll"}},
		{
			"alt1 no-arranque: apóstrofe sin letra cae a alt 4",
			"abc'z",
			[]string{"abc", "'z"},
		},
		{"alt1 mixto: 'S 'll 'RE", "'S 'll 'RE", []string{"'S", " '", "ll", " '", "RE"}},

		// Alt 2 — leading opcional (no CR/LF/letra/número) + letras.
		{"alt2: sin leading", "hello", []string{"hello"}},
		{"alt2: leading espacio", " hello", []string{" hello"}},
		{"alt2: leading TAB", "\ta", []string{"\ta"}},
		{"alt2: leading NBSP U+00A0", "\u00a0b", []string{"\u00a0b"}},
		{"alt2: leading LSEP U+2028", "\u2028b", []string{"\u2028b"}},
		{"alt2: leading emoji", "\U0001F98Ajumps", []string{"\U0001F98Ajumps"}},
		{"alt2: '{\"a\"' JSON", "{\"a\"", []string{"{\"", "a", "\""}},

		// Alt 3 — 1..3 dígitos.
		{"alt3: 12345 parte 123+45", "12345", []string{"123", "45"}},
		{"alt3: 1a", "1a", []string{"1", "a"}},
		{"alt3: 1234abc", "1234abc", []string{"123", "4", "abc"}},
		{"alt3: 1234567890", "1234567890", []string{"123", "456", "789", "0"}},

		// Alt 4 — espacio opcional + non-\s/letra/número + CR/LF opcionales.
		{"alt4: ...!!!", "...!!!", []string{"...!!!"}},
		{"alt4: !ab en dos piezas alt2", "!ab!cd", []string{"!ab", "!cd"}},
		{"alt4: ] final tras dígito", "]}", []string{"]}"}},
		{"alt4: trailing \\n", "!!!\n\n", []string{"!!!\n\n"}},
		{"alt4: trailing \\r\\n", " !\r\nx", []string{" !\r\n", "x"}},
		{"alt4: leading + CR/LF sin non-\\s falla", "'''abc", []string{"'''", "abc"}},

		// Alt 5 — whitespace hasta fin.
		{"alt5: sólo espacios", "   ", []string{"   "}},
		{"alt5: sólo saltos", "\n\n\n", []string{"\n\n\n"}},
		{"alt5: espacio + \\n + espacio", "  \n  ", []string{"  \n  "}},
		{"alt5: CRLF final", "\r\n", []string{"\r\n"}},
		{"alt5: mezcla al final", "\n \t \r\n", []string{"\n \t \r\n"}},

		// Alt 6 — \s*[\r\n] con backtrack.
		{"alt6: \\n solo", "\nabc", []string{"\n", "abc"}},
		{"alt6: \\n\\n bloque", "\n\nabc", []string{"\n\n", "abc"}},
		{"alt6: espacio+\\n", "  \nx", []string{"  \n", "x"}},
		{"alt6: \\r\\n\\r\\n bloque", "\r\n\r\nx", []string{"\r\n\r\n", "x"}},
		{"alt6: \\n \\n \\n último LF", "\n \n \n x", []string{"\n \n \n", " x"}},
		{"alt6: espacio+CR sin LF", " !\r  ", []string{" !\r", "  "}},

		// Alt 7 — hold-back del último whitespace de la carrera.
		{"alt7: 2 espacios + letra", "a  b", []string{"a", " ", " b"}},
		{"alt7: 3 espacios + letra", "   x", []string{"  ", " x"}},
		{"alt7: 6 espacios + letra", "a      b", []string{"a", "     ", " b"}},
		{"alt7: 3 tabs + letra", "\t\t\tx", []string{"\t\t", "\tx"}},
		{"alt7: mezcla tab+espacio", " \t!", []string{" ", "\t", "!"}},
		{"alt7: LSEP repetido", "\u2028\u2028\u2028b", []string{"\u2028\u2028", "\u2028b"}},

		// Alt 8 — un único whitespace.
		{"alt8: 1 espacio entre dígitos", "1 2", []string{"1", " ", "2"}},
		{"alt8: \\t + puntuación", "\t!\t!", []string{"\t", "!", "\t", "!"}},

		// Casos mixtos del brief.
		{
			"mix: don't  I'LL",
			"don't  I'LL",
			[]string{"don", "'t", " ", " I", "'LL"},
		},
		{"mix: leading whitespace largo", "  leading", []string{" ", " leading"}},
		{"mix: year 2024 now", "year 2024 now", []string{"year", " ", "202", "4", " now"}},
		{"mix: trailing spaces \"hi   \"", "hi   ", []string{"hi", "   "}},
		{
			"mix: JSON literal completo",
			`{"a": 1, "b": [1, 2]}`,
			[]string{"{\"", "a", "\":", " ", "1", ",", " \"", "b", "\":", " [", "1", ",", " ", "2", "]}"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := splitPretokens(c.in)
			if !equalStringSlice(got, c.want) {
				t.Errorf("splitPretokens(%q) = %#v, quiero %#v", c.in, got, c.want)
			}
		})
	}
}

// TestSplitPretokensEmpty comprueba el caso vacío. La convención del port es
// slice nil (`len == 0`), consistente con el retorno de EncodeOrdinary("").
func TestSplitPretokensEmpty(t *testing.T) {
	if got := splitPretokens(""); len(got) != 0 {
		t.Errorf("splitPretokens(\"\") = %#v, quiero longitud 0", got)
	}
}

// TestEncodeOrdinaryReference fija ids de tiktoken cl100k_base para un
// puñado de textos representativos. Los tres primeros son los que fija el
// brief (task-2-brief.md, §Goal); el resto se derivó del mismo venv fijado
// para cubrir non-ASCII + emoji, whitespace complejo, contracciones y una
// prosa un poco más larga:
//
//	.upstream/.venv/Scripts/python.exe -c "import tiktoken; \
//	  print(tiktoken.get_encoding('cl100k_base').encode_ordinary(TEXT))"
//
// tiktoken en el venv está fijado por tools/corpus/requirements.lock a la
// versión 0.14.0, la misma que grabó el corpus golden.
func TestEncodeOrdinaryReference(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []int
	}{
		{"brief: hello world", "hello world", []int{15339, 1917}},
		{"brief: JSON pequeño", `{"a": 1, "b": [1, 2]}`,
			[]int{5018, 64, 794, 220, 16, 11, 330, 65, 794, 510, 16, 11, 220, 17, 14316}},
		{"venv: non-ASCII + emoji Moscú café 😀", "Moscú café 😀",
			[]int{44, 24366, 6792, 53050, 91416}},
		{"venv: contracciones don't  I'LL", "don't  I'LL",
			[]int{15357, 956, 220, 358, 6, 4178}},
		{"venv: mezcla ASCII + emoji + dígitos", "The quick brown 🦊 jumps over 42 lazy 🐶s.",
			[]int{791, 4062, 14198, 11410, 99, 232, 35308, 927, 220, 2983, 16053, 11410, 238, 114, 82, 13}},
		{"venv: whitespace + CRLF + non-ASCII", "línea uno\n\tlínea dos\r\nfin",
			[]int{75, 2483, 33252, 24840, 198, 8986, 2483, 33252, 8924, 319, 5589}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := EncodeOrdinary(c.in)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("EncodeOrdinary(%q) = %v, quiero %v", c.in, got, c.want)
			}
		})
	}
}

// TestEncodeOrdinaryEmpty documenta que texto vacío devuelve una slice sin
// tokens. Elegimos nil por convención Go (evita una asignación gratis) y los
// callers de fase 2b sólo consultan `len(...)`. Este test evita que un futuro
// cambio a `make([]int, 0)` se cuele sin actualizar la convención.
func TestEncodeOrdinaryEmpty(t *testing.T) {
	if got := EncodeOrdinary(""); len(got) != 0 {
		t.Errorf("EncodeOrdinary(\"\") = %v, quiero longitud 0", got)
	}
}

// TestBytePairEncodeSingleByte fija el atajo del BPE para piezas de un solo
// byte: se resuelve directo por lookup en `ranks` sin correr el bucle de
// fusión. Los rangos vienen de vocab_test.go (TestVocabSingleByteSpotChecks).
func TestBytePairEncodeSingleByte(t *testing.T) {
	cases := []struct {
		b    byte
		want int
	}{
		{'\n', 198},
		{' ', 220},
		{'a', 64},
		{'{', 90},
	}
	for _, c := range cases {
		got := bytePairEncode([]byte{c.b})
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("bytePairEncode(%q) = %v, quiero [%d]", string([]byte{c.b}), got, c.want)
		}
	}
}

// TestBytePairEncodeKnownWholeToken cubre la ruta en la que la pieza entera
// es un token del vocabulario y colapsa a un único id. " world" tiene rango
// 1917 (fijado también en vocab_test.go TestVocabSpotChecks) y hello tiene
// 15339 según el reference test de encode_ordinary("hello world").
func TestBytePairEncodeKnownWholeToken(t *testing.T) {
	cases := []struct {
		piece string
		want  []int
	}{
		{" world", []int{1917}},
		{"hello", []int{15339}},
	}
	for _, c := range cases {
		got := bytePairEncode([]byte(c.piece))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("bytePairEncode(%q) = %v, quiero %v", c.piece, got, c.want)
		}
	}
}

func equalStringSlice(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
