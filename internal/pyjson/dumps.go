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
//
// Usar Dumps solo para los call sites que en el original pasan
// ensure_ascii=False explícitamente (el tokenizer, kiro/tokenizer.py). Todo
// lo demás — en particular los json.dumps(...) de kiro/parsers.py, que NO
// pasan ensure_ascii — debe usar DumpsASCII: Python por defecto usa
// ensure_ascii=True.
func Dumps(raw json.RawMessage) (string, error) {
	return dumps(raw, false)
}

// DumpsASCII reformatea `raw` con las reglas de json.dumps(x) SIN
// ensure_ascii=False — es decir, con el default real de Python,
// ensure_ascii=True. Todo code point fuera del rango ASCII imprimible
// 0x20-0x7e (incluido 0x7f, DEL, que la regexp de CPython
// `ESCAPE_ASCII = re.compile(r'([\\"]|[^\ -~])')` también escapa) sale como
// `\uXXXX`; los code points por encima de U+FFFF salen como el par
// subrogado UTF-16 que produce CPython (`\uD800-\uDBFF` seguido de
// `\uDC00-\uDFFF`), NO como el escape `\U00nnnnnn` de 8 dígitos que usa
// strconv.Quote/AppendQuote de Go — esa forma no es JSON válido y Python
// nunca la produce.
//
// Corresponde a los json.dumps(...) de .upstream/kiro/parsers.py:142,361,389,
// 419,454, ninguno de los cuales pasa ensure_ascii. Devuelve error en las
// mismas condiciones que Dumps.
func DumpsASCII(raw json.RawMessage) (string, error) {
	return dumps(raw, true)
}

func dumps(raw json.RawMessage, ascii bool) (string, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var b strings.Builder
	if err := writeValue(dec, &b, ascii); err != nil {
		return "", err
	}
	if dec.More() {
		return "", fmt.Errorf("pyjson: entrada con datos extra tras el valor top-level")
	}
	return b.String(), nil
}

func writeValue(dec *json.Decoder, b *strings.Builder, ascii bool) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	return writeToken(tok, dec, b, ascii)
}

func writeToken(tok json.Token, dec *json.Decoder, b *strings.Builder, ascii bool) error {
	switch v := tok.(type) {
	case json.Delim:
		switch v {
		case '{':
			return writeObject(dec, b, ascii)
		case '[':
			return writeArray(dec, b, ascii)
		default:
			return fmt.Errorf("pyjson: delimitador inesperado %q", v)
		}
	case string:
		if ascii {
			writeASCIIString(b, v)
		} else {
			writeString(b, v)
		}
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

func writeObject(dec *json.Decoder, b *strings.Builder, ascii bool) error {
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
		if ascii {
			writeASCIIString(b, s)
		} else {
			writeString(b, s)
		}
		b.WriteString(": ")
		if err := writeValue(dec, b, ascii); err != nil {
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

func writeArray(dec *json.Decoder, b *strings.Builder, ascii bool) error {
	b.WriteByte('[')
	first := true
	for dec.More() {
		if !first {
			b.WriteString(", ")
		}
		first = false
		if err := writeValue(dec, b, ascii); err != nil {
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

// writeASCIIString escribe s con las reglas de Python
// json.dumps(x) (ensure_ascii=True, el default): escapa `"`, `\`, los
// controles < 0x20 y 0x7f (DEL) con las formas cortas donde existen (\b, \f,
// \n, \r, \t) o \u%04x, y además escapa TODO code point fuera del rango
// ASCII imprimible 0x20-0x7e como \uXXXX — un code point por encima de
// U+FFFF sale como el par subrogado UTF-16 que produce CPython
// (json.dumps(chr(0x1F600)) == '"\\ud83d\\ude00"'), no como el \U00nnnnnn de
// 8 dígitos de Go.
//
// Itera por runas, no por bytes: a diferencia de writeString (donde un byte
// no-ASCII se copia tal cual y el iterar byte a byte es correcto), aquí cada
// code point puede expandirse a una o dos secuencias \uXXXX, así que hace
// falta decodificar UTF-8 primero.
func writeASCIIString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
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
			switch {
			case r >= 0x20 && r <= 0x7e:
				b.WriteByte(byte(r))
			case r <= 0xffff:
				fmt.Fprintf(b, `\u%04x`, r)
			default:
				// Par subrogado UTF-16 para code points fuera del BMP,
				// igual que el codificador JSON de CPython.
				r -= 0x10000
				hi := 0xd800 + (r >> 10)
				lo := 0xdc00 + (r & 0x3ff)
				fmt.Fprintf(b, `\u%04x\u%04x`, hi, lo)
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
