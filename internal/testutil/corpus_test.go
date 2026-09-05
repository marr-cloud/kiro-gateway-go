// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package testutil

import (
	"encoding/json"
	"testing"
)

// Los tests de Go se ejecutan en el directorio del paquete, así que el
// testdata de ejemplo de este paquete está en "testdata" relativo al CWD.

func TestLoadCorpusFunction(t *testing.T) {
	cases, err := readCases("testdata", "sample/function")
	if err != nil {
		t.Fatalf("readCases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("cargados %d casos, quiero 1", len(cases))
	}
	c := cases[0]
	if c.Kind != KindFunction {
		t.Errorf("Kind = %q, quiero %q", c.Kind, KindFunction)
	}
	if c.Target != "kiro.sample:add" {
		t.Errorf("Target = %q", c.Target)
	}
	var out int
	if err := json.Unmarshal(c.Output, &out); err != nil {
		t.Fatalf("Output no deserializa: %v", err)
	}
	if out != 5 {
		t.Errorf("Output = %d, quiero 5", out)
	}
	if c.Name != "aaaa000000000001" {
		t.Errorf("Name = %q, quiero el hash del fichero", c.Name)
	}
}

func TestLoadCorpusSequence(t *testing.T) {
	cases, err := readCases("testdata", "sample/sequence")
	if err != nil {
		t.Fatalf("readCases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("cargados %d casos, quiero 1", len(cases))
	}
	steps := cases[0].Steps
	if len(steps) != 3 {
		t.Fatalf("%d pasos, quiero 3", len(steps))
	}
	// El paso 0 es siempre el constructor: es parte de la entrada, porque dos
	// instancias construidas distinto responden distinto a las mismas llamadas.
	if steps[0].Method != "__init__" {
		t.Errorf("primer paso = %q, quiero __init__", steps[0].Method)
	}
	if got := string(Kwarg(t, steps[0].Input, "sep")); got != `""` {
		t.Errorf("kwarg sep del constructor = %s", got)
	}
	if flags := Config(steps[0].Input); len(flags) != 1 {
		t.Errorf("config del constructor = %v, quiero una bandera", flags)
	}
	if steps[1].Method != "feed" || steps[2].Method != "finalize" {
		t.Errorf("métodos = %q, %q", steps[1].Method, steps[2].Method)
	}
	chunk, err := DecodeBytes(Arg(t, steps[1].Input, 0))
	if err != nil {
		t.Fatalf("DecodeBytes del primer argumento: %v", err)
	}
	if string(chunk) != "hi" {
		t.Errorf("primer chunk = %q, quiero %q", chunk, "hi")
	}
}

func TestDecodeBytes(t *testing.T) {
	got, err := DecodeBytes(json.RawMessage(`{"__bytes__":"aGk="}`))
	if err != nil {
		t.Fatalf("DecodeBytes: %v", err)
	}
	if string(got) != "hi" {
		t.Errorf("DecodeBytes = %q, quiero %q", got, "hi")
	}
}

func TestDecodeBytesRejectsPlainValue(t *testing.T) {
	if _, err := DecodeBytes(json.RawMessage(`"hola"`)); err == nil {
		t.Fatal("quiero error para un valor que no es bytes codificados, obtuve nil")
	}
}

func TestReadCasesMissingTargetFails(t *testing.T) {
	if _, err := readCases("testdata", "sample/noexiste"); err == nil {
		t.Fatal("quiero error para un objetivo inexistente, obtuve nil")
	}
}

func TestRepoRootFindsGoMod(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	if root == "" {
		t.Fatal("RepoRoot devolvió una cadena vacía")
	}
}

func TestDecodeSet(t *testing.T) {
	items, err := DecodeSet(json.RawMessage(`{"__set__":["a","b"]}`))
	if err != nil {
		t.Fatalf("DecodeSet: %v", err)
	}
	if len(items) != 2 || string(items[0]) != `"a"` {
		t.Errorf("DecodeSet = %v", items)
	}
	if _, err := DecodeSet(json.RawMessage(`["a"]`)); err == nil {
		t.Error("quiero error para un valor que no es un conjunto codificado")
	}
}

func TestDecodeException(t *testing.T) {
	raw := json.RawMessage(`{"__exception__":{"type":"ConnectError","module":"httpx",
	  "args":["Connection failed"],"str":"Connection failed",
	  "cause":{"type":"gaierror","module":"socket","args":[11001,"getaddrinfo failed"],"str":"[Errno 11001] getaddrinfo failed"}}}`)
	if !IsException(raw) {
		t.Fatal("IsException = false, quiero true")
	}
	exc, err := DecodeException(raw)
	if err != nil {
		t.Fatalf("DecodeException: %v", err)
	}
	if exc.Type != "ConnectError" || exc.Module != "httpx" {
		t.Errorf("tipo = %q del módulo %q", exc.Type, exc.Module)
	}
	if exc.Cause == nil {
		t.Fatal("Cause = nil, quiero la excepción encadenada")
	}
	if exc.Cause.Type != "gaierror" {
		t.Errorf("Cause.Type = %q, quiero gaierror", exc.Cause.Type)
	}
	if len(exc.Cause.Args) != 2 || string(exc.Cause.Args[0]) != "11001" {
		t.Errorf("Cause.Args = %v, quiero el errno como primer argumento", exc.Cause.Args)
	}
}

func TestExceptionHelpersRejectPlainValues(t *testing.T) {
	if IsException(json.RawMessage(`{"type":"ConnectError"}`)) {
		t.Error("IsException = true para un objeto sin marcador")
	}
	if _, err := DecodeException(json.RawMessage(`"boom"`)); err == nil {
		t.Error("quiero error para un valor que no es una excepción codificada")
	}
}

func TestInputAccessors(t *testing.T) {
	input := json.RawMessage(`{"args":[1,"dos"],"kwargs":{"flag":true},"config":{"TRUNCATION_RECOVERY":false}}`)

	if got := string(Arg(t, input, 1)); got != `"dos"` {
		t.Errorf("Arg(1) = %s", got)
	}
	if got := len(Args(t, input)); got != 2 {
		t.Errorf("Args = %d elementos, quiero 2", got)
	}
	if got := string(Kwarg(t, input, "flag")); got != "true" {
		t.Errorf("Kwarg(flag) = %s", got)
	}
	if got := len(Kwargs(t, input)); got != 1 {
		t.Errorf("Kwargs = %d elementos, quiero 1", got)
	}
	flags := Config(input)
	if string(flags["TRUNCATION_RECOVERY"]) != "false" {
		t.Errorf("Config = %v", flags)
	}
	if Config(json.RawMessage(`{"args":[]}`)) != nil {
		t.Error("Config de una entrada sin config debería ser nil")
	}
	if _, ok := OptionalField(input, "events"); ok {
		t.Error("OptionalField dice que existe un campo que no está")
	}
}

func TestEventsAccessor(t *testing.T) {
	input := json.RawMessage(`{"kwargs":{},"events":[{"type":"content"},{"type":"usage"}]}`)
	if got := len(Events(t, input)); got != 2 {
		t.Errorf("Events = %d elementos, quiero 2", got)
	}
}
