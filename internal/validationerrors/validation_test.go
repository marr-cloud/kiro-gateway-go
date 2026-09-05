// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package validationerrors

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"
)

// decode marshaliza `r` y lo vuelve a decodificar en un map[string]any para
// inspeccionar la forma que verá el cliente, no el struct interno.
func decode(t *testing.T, r Response) map[string]any {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json.Unmarshal(%s): %v", raw, err)
	}
	return got
}

func TestNewSingleFailure(t *testing.T) {
	r := New([]Failure{
		{Loc: []any{"body", "model"}, Msg: "field required", Type: "value_error.missing"},
	}, []byte(`{}`))
	got := decode(t, r)

	if got["body"] != "{}" {
		t.Errorf("body = %v, quiero %q", got["body"], "{}")
	}
	detail, ok := got["detail"].([]any)
	if !ok || len(detail) != 1 {
		t.Fatalf("detail = %v, quiero lista de longitud 1", got["detail"])
	}
	failure := detail[0].(map[string]any)
	if failure["msg"] != "field required" {
		t.Errorf("msg = %v, quiero %q", failure["msg"], "field required")
	}
	if failure["type"] != "value_error.missing" {
		t.Errorf("type = %v, quiero %q", failure["type"], "value_error.missing")
	}
	loc := failure["loc"].([]any)
	if len(loc) != 2 || loc[0] != "body" || loc[1] != "model" {
		t.Errorf("loc = %v, quiero [body model]", loc)
	}
}

func TestNewMultipleFailures(t *testing.T) {
	r := New([]Failure{
		{Loc: []any{"body", "model"}, Msg: "field required", Type: "value_error.missing"},
		{Loc: []any{"body", "messages"}, Msg: "field required", Type: "value_error.missing"},
	}, []byte(`{}`))
	got := decode(t, r)

	detail := got["detail"].([]any)
	if len(detail) != 2 {
		t.Fatalf("detail len = %d, quiero 2", len(detail))
	}
	first := detail[0].(map[string]any)["loc"].([]any)
	second := detail[1].(map[string]any)["loc"].([]any)
	if first[1] != "model" || second[1] != "messages" {
		t.Errorf("orden preservado: got %v, %v", first, second)
	}
}

func TestNewNestedLoc(t *testing.T) {
	r := New([]Failure{
		{
			Loc:  []any{"body", "messages", 0, "content", 2, "text"},
			Msg:  "field required",
			Type: "value_error.missing",
		},
	}, []byte(`{}`))
	got := decode(t, r)

	loc := got["detail"].([]any)[0].(map[string]any)["loc"].([]any)
	if len(loc) != 6 {
		t.Fatalf("loc len = %d, quiero 6", len(loc))
	}
	// Tras el round-trip por JSON, los enteros vuelven como float64.
	if loc[0] != "body" || loc[2].(float64) != 0 || loc[4].(float64) != 2 {
		t.Errorf("loc anidado = %v", loc)
	}
}

func TestBodyTruncatedTo500(t *testing.T) {
	body := strings.Repeat("a", 501)
	r := New(nil, []byte(body))
	got := decode(t, r)
	s := got["body"].(string)
	if utf8.RuneCountInString(s) != 500 {
		t.Errorf("longitud tras truncar = %d runas, quiero 500", utf8.RuneCountInString(s))
	}
	if s != strings.Repeat("a", 500) {
		t.Errorf("body truncado no coincide")
	}
}

func TestBodyShorterThan500NotTruncated(t *testing.T) {
	body := strings.Repeat("b", 42)
	r := New(nil, []byte(body))
	got := decode(t, r)
	if got["body"] != body {
		t.Errorf("body = %v, quiero intacto de 42 caracteres", got["body"])
	}
}

func TestBodyTruncatedByRunes(t *testing.T) {
	// 300 copias de "ñ" (2 bytes cada una en UTF-8) → 600 bytes, 300 runas.
	// Añadimos 250 ascii más para llegar a 550 runas.
	body := strings.Repeat("ñ", 300) + strings.Repeat("a", 250)
	r := New(nil, []byte(body))
	got := decode(t, r)
	s := got["body"].(string)
	if utf8.RuneCountInString(s) != 500 {
		t.Errorf("truncado por runas = %d, quiero 500", utf8.RuneCountInString(s))
	}
	// Las primeras 300 runas siguen siendo "ñ", las 200 siguientes "a".
	wantPrefix := strings.Repeat("ñ", 300) + strings.Repeat("a", 200)
	if s != wantPrefix {
		t.Errorf("prefijo tras truncar por runas no coincide")
	}
}

func TestBytesInLocConvertedToString(t *testing.T) {
	// En Pydantic, un valor bytes puede aparecer en loc: se sanea a string
	// para que la respuesta sea serializable a JSON.
	r := New([]Failure{
		{Loc: []any{"body", []byte("clave-binaria")}, Msg: "x", Type: "y"},
	}, []byte("{}"))
	got := decode(t, r)
	loc := got["detail"].([]any)[0].(map[string]any)["loc"].([]any)
	if loc[1] != "clave-binaria" {
		t.Errorf("loc[1] = %v (%T), quiero string \"clave-binaria\"", loc[1], loc[1])
	}
}
