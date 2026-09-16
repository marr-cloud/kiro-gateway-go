// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package parsers

import (
	"encoding/json"
	"fmt"

	"github.com/marr-cloud/kiro-gateway-go/internal/pyjson"
)

// DeduplicateToolCalls elimina tool calls duplicados por dos criterios
// sucesivos: primero por id (quedándose con el que tenga más argumentos, no
// "{}"), luego por name+arguments (duplicados exactos). Port literal de
// kiro.parsers::deduplicate_tool_calls (.upstream/kiro/parsers.py:151-208).
//
// El original construye by_id como un dict de Python: al reasignar
// by_id[tc_id] = tc para un id repetido, la posición en la iteración
// (insertion order, Python 3.7+) es la del PRIMER id visto, no la del
// último — solo cambia el valor. Aquí se replica con un slice "values" en
// orden de primera aparición más un índice by id -> posición en ese slice,
// para que una actualización in-place no reordene el resultado.
func DeduplicateToolCalls(toolCalls []map[string]any) []map[string]any {
	values := make([]map[string]any, 0, len(toolCalls))
	byID := make(map[string]int, len(toolCalls))

	for _, tc := range toolCalls {
		id, hasID := idOf(tc)
		if !hasID {
			continue
		}
		if pos, ok := byID[id]; ok {
			existingArgs := argumentsGetDefault(values[pos])
			currentArgs := argumentsGetDefault(tc)
			if currentArgs != "{}" && (existingArgs == "{}" || len(currentArgs) > len(existingArgs)) {
				values[pos] = tc
			}
		} else {
			byID[id] = len(values)
			values = append(values, tc)
		}
	}

	withoutID := make([]map[string]any, 0)
	for _, tc := range toolCalls {
		if _, hasID := idOf(tc); !hasID {
			withoutID = append(withoutID, tc)
		}
	}

	combined := make([]map[string]any, 0, len(values)+len(withoutID))
	combined = append(combined, values...)
	combined = append(combined, withoutID...)

	seen := make(map[string]bool, len(combined))
	unique := make([]map[string]any, 0, len(combined))
	for _, tc := range combined {
		// func = tc.get("function") or {} ; func_name = func.get("name") or ""
		// func_args = func.get("arguments") or "{}" — estilo OR: el default
		// entra tanto si falta la clave como si el valor es falsy (None, "").
		name := funcFieldOrDefault(tc, "name", "")
		args := funcFieldOrDefault(tc, "arguments", "{}")
		key := name + "-" + args
		if !seen[key] {
			seen[key] = true
			unique = append(unique, tc)
		}
	}
	return unique
}

// idOf devuelve el id de un tool call y si es "truthy" (no vacío, no None,
// no ausente) según las reglas de Python usadas por
// `if not tc.get("id"): ...` — misma regla en las dos apariciones del
// original (el bucle by_id y la list comprehension de result_without_id).
func idOf(tc map[string]any) (string, bool) {
	v, ok := tc["id"]
	if !ok || !isTruthy(v) {
		return "", false
	}
	return anyStr(v), true
}

// argumentsGetDefault replica
// `d.get("function", {}).get("arguments", "{}")`: el default solo entra si
// la clave está AUSENTE, no si el valor es falsy — a diferencia de
// funcFieldOrDefault, que usa el patrón `.get(x) or default` del bucle
// final. Son dos reglas de Python distintas dentro de la misma función
// upstream; el corpus no distingue entre ambas (ningún caso con id
// duplicado tiene "function" o "arguments" ausente/None), pero se
// mantienen separadas para fidelidad bug-a-bug.
//
// Si "function" está presente pero no es un objeto (o es None), el
// original haría `None.get(...)` y lanzaría AttributeError. Ese camino no
// está cubierto por el corpus; aquí se trata defensivamente como si
// "function" estuviera ausente, para no hacer panic en un servidor Go.
func argumentsGetDefault(tc map[string]any) string {
	fn, _ := tc["function"].(map[string]any)
	if fn == nil {
		return "{}"
	}
	v, hasArg := fn["arguments"]
	if !hasArg {
		return "{}"
	}
	return anyStr(v)
}

// funcFieldOrDefault replica `(tc.get("function") or {}).get(field) or def`:
// el default entra tanto si la clave falta como si el valor es falsy.
// Si "function" está presente pero no es un objeto, se trata como {} en vez
// de replicar el AttributeError de Python — mismo razonamiento defensivo
// que argumentsGetDefault, para el mismo tipo de entrada no cubierta por el
// corpus.
func funcFieldOrDefault(tc map[string]any, field, def string) string {
	fn, _ := tc["function"].(map[string]any)
	if fn == nil {
		return def
	}
	v, present := fn[field]
	if !present || !isTruthy(v) {
		return def
	}
	return anyStr(v)
}

// isTruthy replica la verdad/falsedad de Python para un valor ya decodificado
// de JSON: None, false, "", 0/0.0, [] y {} son falsy; cualquier otra cosa es
// truthy (incluida una cadena "0", que en Python es truthy porque no está
// vacía).
func isTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case float64:
		return x != 0
	case []any:
		return len(x) != 0
	case map[string]any:
		return len(x) != 0
	default:
		return true
	}
}

// anyStr aplica str(x) de Python a un valor JSON ya decodificado,
// reutilizando pyjson.Str (que ya conoce las reglas exactas: True/False/None
// con mayúscula inicial, floats enteros con ".0", contenedores en forma
// repr()). Las cadenas se devuelven tal cual, sin pasar por json.Marshal +
// pyjson.Str, porque json.Marshal las envolvería entre comillas antes de que
// pyjson.Str las desenvuelva — sería un viaje de ida y vuelta innecesario, no
// un cambio de comportamiento.
func anyStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		// v ya viene de json.Unmarshal, así que solo puede fallar aquí por un
		// tipo que este paquete no produce nunca (p.ej. un canal). Defensivo.
		return fmt.Sprint(v)
	}
	return pyjson.Str(b)
}
