// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package pyjson

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// Str emula str(x) de Python 3 para el valor decodificado de `raw`. El
// tokenizer del upstream envuelve algunos valores con str() antes de contar
// sus tokens, y las diferencias con Go son las esperables: str(True) es
// "True" con mayúscula, str(None) es "None", str("hola") es "hola" sin las
// comillas, y str(1.0) es "1.0" — no "1".
//
// Para los valores primitivos las reglas están cubiertas por los tests. Para
// contenedores (dict/list) devolvemos la forma repr() de Python con comillas
// simples; en el uso normal del tokenizer los contenedores no aparecen y su
// aparición se documenta en el informe.
func Str(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return ""
	}
	switch trimmed[0] {
	case 't':
		return "True"
	case 'f':
		return "False"
	case 'n':
		return "None"
	case '"':
		var s string
		if err := json.Unmarshal(trimmed, &s); err != nil {
			return string(trimmed)
		}
		return s
	case '{', '[':
		var v any
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return string(trimmed)
		}
		return pythonRepr(v)
	default:
		return formatNumberRepr(string(trimmed))
	}
}

// formatNumberRepr aplica las reglas de repr() de Python a un número escrito
// tal como aparece en JSON.
func formatNumberRepr(s string) string {
	if !strings.ContainsAny(s, ".eE") {
		return s
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return s
	}
	out := strconv.FormatFloat(f, 'g', -1, 64)
	if !strings.ContainsAny(out, ".eE") {
		out += ".0"
	}
	return out
}

// pythonRepr devuelve una forma parecida a repr() de Python para los tipos que
// json.Decode produce con UseNumber(). No es una emulación exacta: se usa como
// fallback en Str para contenedores, cuya aparición se anota en el informe
// porque significa que el uso del tokenizer es más amplio de lo previsto.
func pythonRepr(v any) string {
	var b strings.Builder
	writeRepr(&b, v)
	return b.String()
}

func writeRepr(b *strings.Builder, v any) {
	switch x := v.(type) {
	case nil:
		b.WriteString("None")
	case bool:
		if x {
			b.WriteString("True")
		} else {
			b.WriteString("False")
		}
	case string:
		writeReprString(b, x)
	case json.Number:
		b.WriteString(formatNumberRepr(string(x)))
	case []any:
		b.WriteByte('[')
		for i, e := range x {
			if i > 0 {
				b.WriteString(", ")
			}
			writeRepr(b, e)
		}
		b.WriteByte(']')
	case map[string]any:
		// json.Decode no preserva el orden de las claves. Este camino es un
		// fallback para uso no previsto, y aceptamos esa limitación.
		b.WriteByte('{')
		first := true
		for k, val := range x {
			if !first {
				b.WriteString(", ")
			}
			first = false
			writeReprString(b, k)
			b.WriteString(": ")
			writeRepr(b, val)
		}
		b.WriteByte('}')
	default:
		fmt.Fprintf(b, "%v", v)
	}
}

// writeReprString escribe s con las reglas de repr() de Python: comilla
// simple por defecto, comilla doble si la cadena contiene una comilla simple
// y ninguna doble; escapa `\`, la comilla activa y los controles.
func writeReprString(b *strings.Builder, s string) {
	quote := byte('\'')
	if strings.ContainsRune(s, '\'') && !strings.ContainsRune(s, '"') {
		quote = '"'
	}
	b.WriteByte(quote)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\':
			b.WriteString(`\\`)
		case c == quote:
			b.WriteByte('\\')
			b.WriteByte(quote)
		case c == '\n':
			b.WriteString(`\n`)
		case c == '\r':
			b.WriteString(`\r`)
		case c == '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(b, `\x%02x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	b.WriteByte(quote)
}
