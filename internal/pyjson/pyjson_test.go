// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package pyjson

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestDumps(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"objeto simple", `{"a":1,"b":2}`, `{"a": 1, "b": 2}`},
		{"objeto anidado", `{"a":{"b":[1,2]}}`, `{"a": {"b": [1, 2]}}`},
		{"objeto vacio", `{}`, `{}`},
		{"array vacio", `[]`, `[]`},
		{"orden preservado", `{"z":1,"a":2}`, `{"z": 1, "a": 2}`},
		{"float entero lleva .0", `{"x":1.0}`, `{"x": 1.0}`},
		{"float no entero", `{"x":1.5}`, `{"x": 1.5}`},
		{"entero no lleva .0", `{"x":1}`, `{"x": 1}`},
		{"no ascii sin escapar", `{"k":"Moscú"}`, `{"k": "Moscú"}`},
		{"html sin escapar", `{"k":"a<b>c&d"}`, `{"k": "a<b>c&d"}`},
		{"comilla escapada", `{"k":"a\"b"}`, `{"k": "a\"b"}`},
		{"barra invertida", `{"k":"a\\b"}`, `{"k": "a\\b"}`},
		{"salto de linea", `{"k":"a\nb"}`, `{"k": "a\nb"}`},
		{"null", `{"k":null}`, `{"k": null}`},
		{"booleanos", `{"t":true,"f":false}`, `{"t": true, "f": false}`},
		{"cadena suelta", `"hola"`, `"hola"`},
		{"numero suelto", `42`, `42`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Dumps(json.RawMessage(c.in))
			if err != nil {
				t.Fatalf("Dumps(%s): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("Dumps(%s) = %s, quiero %s", c.in, got, c.want)
			}
		})
	}
}

// backslash es el caracter de escape JSON (0x5c), construido a partir de su
// code point en vez de tecleado literalmente. Evita que una secuencia
// similar a un escape \uXXXX tecleada a mano en el propio codigo fuente de
// este test se transforme en el caracter Unicode correspondiente antes de
// que el test llegue a compilarse -- justo el bug que uEscape existe para
// probar.
var backslash = string(rune(0x5c))

// uEscape reproduce como escribe CPython un code point bajo
// json.dumps(x) (ensure_ascii=True, el default): una sola secuencia de
// escape para el BMP (r <= 0xffff), o el par subrogado UTF-16 que usa
// CPython para code points por encima del BMP -- misma formula que
// writeASCIIString en dumps.go.
func uEscape(r rune) string {
	if r > 0xffff {
		r -= 0x10000
		hi := 0xd800 + (r >> 10)
		lo := 0xdc00 + (r & 0x3ff)
		return backslash + "u" + fmt.Sprintf("%04x", hi) + backslash + "u" + fmt.Sprintf("%04x", lo)
	}
	return backslash + "u" + fmt.Sprintf("%04x", r)
}

// TestDumpsASCII valida DumpsASCII contra json.dumps(x) real de Python (sin
// ensure_ascii=False, es decir con el default ensure_ascii=True) -- el
// comportamiento de los json.dumps(...) de .upstream/kiro/parsers.py, que no
// pasan ese argumento. Los "want" se construyen con uEscape a partir del
// code point (no tecleando la secuencia de escape a mano -- ver el
// comentario de uEscape sobre por que), verificado ejecutando python3
// directamente sobre cada code point de la tabla de casos: json.dumps de
// Python 3.13 produce, para cada uno, exactamente los 4 digitos hex en
// minuscula que calcula uEscape (par subrogado UTF-16 para el unico code
// point fuera del BMP, U+1F600).
func TestDumpsASCII(t *testing.T) {
	eacute := string(rune(0x00e9))                       // e con acento agudo
	nihon := string(rune(0x65e5)) + string(rune(0x672c)) // dos ideogramas CJK
	coffee := string(rune(0x2615))                       // taza de cafe (BMP)
	grin := string(rune(0x1f600))                        // cara sonriente (fuera del BMP)
	del := string(rune(0x7f))

	cases := []struct{ name, in, want string }{
		{"objeto ascii-only coincide con Dumps", `{"a":1,"b":2}`, `{"a": 1, "b": 2}`},
		{"latin-1: U+00E9", `"caf` + eacute + `"`, `"caf` + uEscape(0x00e9) + `"`},
		{"cjk: U+65E5 U+672C", `"` + nihon + `"`, `"` + uEscape(0x65e5) + uEscape(0x672c) + `"`},
		{"emoji BMP: U+2615", `"` + coffee + `"`, `"` + uEscape(0x2615) + `"`},
		{"emoji astral con par subrogado: U+1F600", `"` + grin + `"`, `"` + uEscape(0x1f600) + `"`},
		{
			"objeto anidado con valores no ascii",
			`{"path":"/tmp/caf` + eacute + `","emoji":"` + grin + coffee + `"}`,
			`{"path": "/tmp/caf` + uEscape(0x00e9) + `", "emoji": "` + uEscape(0x1f600) + uEscape(0x2615) + `"}`,
		},
		{"0x7f (DEL) se escapa, a diferencia de Dumps", `"` + del + `"`, `"` + uEscape(0x7f) + `"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := DumpsASCII(json.RawMessage(c.in))
			if err != nil {
				t.Fatalf("DumpsASCII(%s): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("DumpsASCII(%s) = %s, quiero %s", c.in, got, c.want)
			}
		})
	}
}

// TestDumpsASCIIMatchesDumpsForASCIIOnlyInput comprueba explicitamente que
// para entrada puramente ASCII (sin ningun code point fuera de 0x20-0x7e ni
// controles), Dumps y DumpsASCII producen exactamente la misma salida --
// solo divergen cuando hay algo que escapar.
func TestDumpsASCIIMatchesDumpsForASCIIOnlyInput(t *testing.T) {
	in := json.RawMessage(`{"a":1,"b":[true,false,null,"hola mundo"],"c":1.5}`)
	want, err := Dumps(in)
	if err != nil {
		t.Fatalf("Dumps: %v", err)
	}
	got, err := DumpsASCII(in)
	if err != nil {
		t.Fatalf("DumpsASCII: %v", err)
	}
	if got != want {
		t.Errorf("DumpsASCII(%s) = %s, quiero que coincida con Dumps = %s", in, got, want)
	}
}

func TestStr(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"true en mayuscula", `true`, "True"},
		{"false en mayuscula", `false`, "False"},
		{"null es None", `null`, "None"},
		{"entero", `42`, "42"},
		{"float entero", `1.0`, "1.0"},
		{"float", `1.5`, "1.5"},
		{"cadena sin comillas", `"hola"`, "hola"},
		{"cadena vacia", `""`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Str(json.RawMessage(c.in)); got != c.want {
				t.Errorf("Str(%s) = %q, quiero %q", c.in, got, c.want)
			}
		})
	}
}

func TestDumpsRejectsInvalidJSON(t *testing.T) {
	if _, err := Dumps(json.RawMessage(`{"a":`)); err == nil {
		t.Fatal("quiero error con JSON invalido, obtuve nil")
	}
}
