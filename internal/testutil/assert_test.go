// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package testutil

import (
	"encoding/json"
	"testing"
)

// TestAssertJSONEqualPassesOnEqualMaps comprueba el caso feliz: dos valores
// que serializan al mismo JSON canónico, aunque sus tipos Go de origen sean
// distintos ([]map[string]any frente al json.RawMessage esperado).
func TestAssertJSONEqualPassesOnEqualMaps(t *testing.T) {
	got := []map[string]any{
		{"name": "get_weather", "input": map[string]any{"location": "Moscow"}, "toolUseId": "call_123"},
	}
	want := json.RawMessage(`[{"name": "get_weather", "input": {"location": "Moscow"}, "toolUseId": "call_123"}]`)
	AssertJSONEqual(t, got, want, "caso-feliz")
}

// TestAssertJSONEqualFailsOnUnequalMaps comprueba que un valor con una clave
// distinta falla, usando un *testing.T interno para capturar el fallo sin
// abortar este test (tb.Fatalf llamaría a runtime.Goexit en el t real).
func TestAssertJSONEqualFailsOnUnequalMaps(t *testing.T) {
	fake := &fakeTB{}
	got := map[string]any{"name": "get_weather"}
	want := json.RawMessage(`{"name": "get_time"}`)
	AssertJSONEqual(fake, got, want, "caso-distinto")
	if !fake.failed {
		t.Fatal("quiero que AssertJSONEqual falle para mapas distintos, no falló")
	}
}

// TestAssertJSONEqualFailsOnStringVsNumber comprueba que la comparación
// distingue tipos tras decodificar a `any`: "1" (string) no es igual a 1
// (número), aunque ambos aparezcan como el valor de la misma clave.
func TestAssertJSONEqualFailsOnStringVsNumber(t *testing.T) {
	fake := &fakeTB{}
	got := map[string]any{"count": "1"}
	want := json.RawMessage(`{"count": 1}`)
	AssertJSONEqual(fake, got, want, "string-vs-numero")
	if !fake.failed {
		t.Fatal("quiero que AssertJSONEqual falle para \"1\" frente a 1, no falló")
	}
}

// fakeTB es un testing.TB mínimo que registra si Fatalf se llamó, para poder
// probar el camino de fallo de AssertJSONEqual sin abortar el test real.
type fakeTB struct {
	testing.TB
	failed bool
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed = true
}
