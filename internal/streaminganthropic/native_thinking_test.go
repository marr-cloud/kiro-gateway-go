// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streaminganthropic

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
)

// sseData devuelve el data JSON de cada evento SSE, en orden.
func sseData(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(raw, "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m); err != nil {
			t.Fatalf("data no es JSON: %q", line)
		}
		out = append(out, m)
	}
	return out
}

// summary resume cada evento como "tipo[índice]:detalle".
func summary(events []map[string]any) []string {
	var out []string
	for _, e := range events {
		typ, _ := e["type"].(string)
		s := typ
		if idx, ok := e["index"].(float64); ok {
			s += "[" + string(rune('0'+int(idx))) + "]"
		}
		if cb, ok := e["content_block"].(map[string]any); ok {
			s += ":" + cb["type"].(string)
			if sig, ok := cb["signature"]; ok {
				s += ",sig=" + sig.(string)
			}
			if data, ok := cb["data"]; ok {
				s += ",data=" + data.(string)
			}
		}
		if d, ok := e["delta"].(map[string]any); ok {
			switch d["type"] {
			case "thinking_delta":
				s += ":thinking=" + d["thinking"].(string)
			case "signature_delta":
				s += ":signature=" + d["signature"].(string)
			case "text_delta":
				s += ":text=" + d["text"].(string)
			}
		}
		out = append(out, s)
	}
	return out
}

func run(t *testing.T, evs ...streamingcore.KiroEvent) []string {
	t.Helper()
	f := New("claude-sonnet-5.5")
	var w bytes.Buffer
	for _, ev := range evs {
		if err := f.Handle(ev, &w); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Finish(&w); err != nil {
		t.Fatal(err)
	}
	all := summary(sseData(t, w.String()))
	// Sin message_delta/message_stop finales, que no cambian.
	return all[:len(all)-2]
}

func assertSeq(t *testing.T, got, want []string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("eventos:\n%s\nquiero:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// Razonamiento nativo → bloque thinking con su firma real en signature_delta
// (DIFFERENCES §18), no la firma inventada del fake reasoning.
func TestNativeThinkingBlock(t *testing.T) {
	got := run(t,
		streamingcore.KiroEvent{Kind: "native_thinking", Thinking: "a"},
		streamingcore.KiroEvent{Kind: "native_thinking", Thinking: "b"},
		streamingcore.KiroEvent{Kind: "thinking_signature", Signature: "SIG"},
		streamingcore.KiroEvent{Kind: "content", Content: "Hola"},
	)
	assertSeq(t, got, []string{
		"content_block_start[0]:thinking,sig=",
		"content_block_delta[0]:thinking=a",
		"content_block_delta[0]:thinking=b",
		"content_block_delta[0]:signature=SIG",
		"content_block_stop[0]",
		"content_block_start[1]:text",
		"content_block_delta[1]:text=Hola",
		"content_block_stop[1]",
	})
}

// Razonamiento oculto (solo firma) y cifrado (redacted) también llegan como
// bloques, para que el cliente los devuelva en el historial.
func TestNativeThinkingSignatureOnlyAndRedacted(t *testing.T) {
	got := run(t,
		streamingcore.KiroEvent{Kind: "thinking_signature", Signature: "SIG"},
		streamingcore.KiroEvent{Kind: "redacted_thinking", RedactedData: "AAEC"},
		streamingcore.KiroEvent{Kind: "content", Content: "Hola"},
	)
	assertSeq(t, got, []string{
		"content_block_start[0]:thinking,sig=",
		"content_block_delta[0]:signature=SIG",
		"content_block_stop[0]",
		"content_block_start[1]:redacted_thinking,data=AAEC",
		"content_block_stop[1]",
		"content_block_start[2]:text",
		"content_block_delta[2]:text=Hola",
		"content_block_stop[2]",
	})
}

// Thinking intercalado después de texto: el texto se cierra antes de abrir
// el thinking, cada uno con su índice.
func TestNativeThinkingAfterText(t *testing.T) {
	got := run(t,
		streamingcore.KiroEvent{Kind: "content", Content: "x"},
		streamingcore.KiroEvent{Kind: "native_thinking", Thinking: "y"},
		streamingcore.KiroEvent{Kind: "thinking_signature", Signature: "SIG"},
	)
	assertSeq(t, got, []string{
		"content_block_start[0]:text",
		"content_block_delta[0]:text=x",
		"content_block_stop[0]",
		"content_block_start[1]:thinking,sig=",
		"content_block_delta[1]:thinking=y",
		"content_block_delta[1]:signature=SIG",
		"content_block_stop[1]",
	})
}

func TestNativeThinkingStripped(t *testing.T) {
	f := New("claude-sonnet-5.5", Strip)
	var w bytes.Buffer
	for _, ev := range []streamingcore.KiroEvent{
		{Kind: "native_thinking", Thinking: "a"},
		{Kind: "thinking_signature", Signature: "SIG"},
		{Kind: "redacted_thinking", RedactedData: "AAEC"},
	} {
		_ = f.Handle(ev, &w)
	}
	if strings.Contains(w.String(), "thinking") {
		t.Errorf("con Strip no debe salir ningún bloque de razonamiento:\n%s", w.String())
	}
}
