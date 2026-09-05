// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package pyjson emula el subconjunto de json.dumps de Python 3 que el
// tokenizer necesita para contar tokens con paridad con el upstream, y str()
// para los valores primitivos que ese mismo tokenizer envuelve con str(...)
// antes de contar.
//
// Motivación. json.dumps(x, ensure_ascii=False) en Python escribe
//
//	{"a": 1, "b": 2}
//
// con espacio tras la coma y tras los dos puntos, y no escapa `<`, `>` o `&`.
// encoding/json en Go escribe
//
//	{"a":1,"b":2}
//
// sin ese espacio y con las entidades HTML escapadas. Son cadenas distintas
// y cuentan distinto número de tokens. Ese conteo aparece en el campo `usage`
// que ven los clientes: si divergiera del original, el port dejaría de tener
// paridad en la contabilidad.
//
// Diseño. Dumps no serializa un mapa: reformatea los bytes JSON originales
// token a token con encoding/json.Decoder y UseNumber(). Como Python hace
// json.loads seguido de json.dumps y el diccionario conserva el orden del
// documento, trabajar sobre los bytes originales garantiza el orden correcto
// de claves sin necesidad de mapas ordenados.
package pyjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Dumps reformatea `raw` con las reglas de json.dumps(x, ensure_ascii=False):
// separadores `, ` y `: `, sin escape HTML, y garantía de que los floats
// enteros llevan `.0`. Devuelve error si `raw` no es JSON válido o contiene
// contenido extra tras el valor top-level.
func Dumps(raw json.RawMessage) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var b strings.Builder
	if err := writeValue(dec, &b); err != nil {
		return "", err
	}
	if dec.More() {
		return "", fmt.Errorf("pyjson: entrada con datos extra tras el valor top-level")
	}
	return b.String(), nil
}

func writeValue(dec *json.Decoder, b *strings.Builder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	return writeToken(tok, dec, b)
}

func writeToken(tok json.Token, dec *json.Decoder, b *strings.Builder) error {
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			return writeObject(dec, b)
		case '[':
			return writeArray(dec, b)
		default:
			return fmt.Errorf("pyjson: delimitador inesperado %q", v)
		}
	case string:
		writeString(b, v)
		return nil
	case json.Number:
		writeNumber(b, v)
		return nil
	case bool:
		if v {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
		return nil
	case nil:
		b.WriteString("null")
		return nil
	default:
		return fmt.Errorf("pyjson: tipo de token inesperado %T", tok)
	}
}

func writeObject(dec *json.Decoder, b *strings.Builder) error {
	b.WriteByte('{')
	first := true
	for dec.More() {
		if !first {
			b.WriteString(", ")
		}
		first = false
		key, err := dec.Token()
		if err != nil {
			return err
		}
		s, ok := key.(string)
		if !ok {
			return fmt.Errorf("pyjson: clave de objeto no es string: %T", key)
		}
		writeString(b, s)
		b.WriteString(": ")
		if err := writeValue(dec, b); err != nil {
			return err
		}
	}
	// Consumir el '}' de cierre.
	if _, err := dec.Token(); err != nil {
		return err
	}
	b.WriteByte('}')
	return nil
}

func writeArray(dec *json.Decoder, b *strings.Builder) error {
	b.WriteByte('[')
	first := true
	for dec.More() {
		if !first {
			b.WriteString(", ")
		}
		first = false
		if err := writeValue(dec, b); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	b.WriteByte(']')
	return nil
}

// writeString escribe s con las reglas de Python json.dumps(ensure_ascii=False):
// escapa `"`, `\` y los controles < 0x20 (con las formas cortas para \b, \f,
// \n, \r, \t; \u00XX para el resto). Nada más — `<`, `>`, `&` y las secuencias
// UTF-8 no ASCII van tal cual.
func writeString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(b, `\u%04x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte('"')
}

// writeNumber emite s tal como lo emitiría Python tras json.loads/json.dumps.
// Si s contiene `.eE` es un float y se reformatea para garantizar que lleva
// parte decimal (`1.0` en lugar del `1` que produce strconv.FormatFloat).
// Los enteros van tal cual.
func writeNumber(b *strings.Builder, n json.Number) {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		b.WriteString(s)
		return
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		// No debería pasar: encoding/json ya validó el número al tokenizar.
		b.WriteString(s)
		return
	}
	out := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(out, ".eE") {
		out += ".0"
	}
	b.WriteString(out)
}
