// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package pyjson

import (
	"encoding/json"
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
