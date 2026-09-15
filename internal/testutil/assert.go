// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package testutil

import (
	"encoding/json"
	"reflect"
	"testing"
)

// AssertJSONEqual compara got (un valor Go cualquiera, normalmente
// []map[string]any o map[string]any) contra want (la salida grabada del
// corpus, ya en JSON) tras canonicalizarlos a través de un ciclo
// Marshal/Unmarshal a `any`. Compara valores, no representaciones: el orden
// de las claves de un mapa o el formato exacto del JSON no importan, pero sí
// importan los tipos que sobreviven a esa decodificación (una cadena "1"
// nunca es igual al número 1).
//
// Falla el test citando caseName, así los tests que iteran el corpus con
// t.Run(c.Name, ...) identifican el caso exacto sin tener que buscarlo por
// contenido.
func AssertJSONEqual(tb testing.TB, got any, want json.RawMessage, caseName string) {
	tb.Helper()
	gotBytes, err := json.Marshal(got)
	if err != nil {
		tb.Fatalf("case %s: marshal got: %v", caseName, err)
		return
	}
	var gotN, wantN any
	_ = json.Unmarshal(gotBytes, &gotN)
	_ = json.Unmarshal(want, &wantN)
	if !reflect.DeepEqual(gotN, wantN) {
		tb.Fatalf("case %s:\n got: %s\nwant: %s", caseName, string(gotBytes), string(want))
	}
}
