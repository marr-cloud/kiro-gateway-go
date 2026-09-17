// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package mcptools

import (
	"encoding/json"
	"testing"
)

func raw(t *testing.T, s string) json.RawMessage {
	t.Helper()
	return json.RawMessage(s)
}

// TestExtractQueryFromMessages_OpenAIStringContent: formato OpenAI típico,
// "content" es un string plano (mcp_tools.py:562-563).
func TestExtractQueryFromMessages_OpenAIStringContent(t *testing.T) {
	msgs := []json.RawMessage{raw(t, `{"role":"user","content":"search for golang tutorials"}`)}
	got, ok := ExtractQueryFromMessages(msgs, "openai")
	if !ok {
		t.Fatal("want ok=true")
	}
	if got != "search for golang tutorials" {
		t.Errorf("got %q", got)
	}
}

// TestExtractQueryFromMessages_AnthropicBlockContent: formato Anthropic,
// "content" es una lista de bloques {"type":"text","text":...}
// (mcp_tools.py:564-576).
func TestExtractQueryFromMessages_AnthropicBlockContent(t *testing.T) {
	msgs := []json.RawMessage{raw(t, `{"role":"user","content":[{"type":"text","text":"search for golang tutorials"}]}`)}
	got, ok := ExtractQueryFromMessages(msgs, "anthropic")
	if !ok {
		t.Fatal("want ok=true")
	}
	if got != "search for golang tutorials" {
		t.Errorf("got %q", got)
	}
}

// TestExtractQueryFromMessages_MultipleTextBlocksConcatenate: varios
// bloques de texto se concatenan (mcp_tools.py:576: "".join(text_parts)).
func TestExtractQueryFromMessages_MultipleTextBlocksConcatenate(t *testing.T) {
	msgs := []json.RawMessage{raw(t, `{"role":"user","content":[{"type":"text","text":"golang "},{"type":"image","text":"ignored"},{"type":"text","text":"tutorials"}]}`)}
	got, ok := ExtractQueryFromMessages(msgs, "anthropic")
	if !ok {
		t.Fatal("want ok=true")
	}
	if got != "golang tutorials" {
		t.Errorf("got %q, want %q", got, "golang tutorials")
	}
}

// TestExtractQueryFromMessages_StripsPrefix: mcp_tools.py:580-585.
func TestExtractQueryFromMessages_StripsPrefix(t *testing.T) {
	msgs := []json.RawMessage{raw(t, `{"role":"user","content":"Perform a web search for the query: golang tips"}`)}
	got, ok := ExtractQueryFromMessages(msgs, "anthropic")
	if !ok {
		t.Fatal("want ok=true")
	}
	if got != "golang tips" {
		t.Errorf("got %q, want %q", got, "golang tips")
	}
}

func TestExtractQueryFromMessages_EmptyMessages(t *testing.T) {
	if _, ok := ExtractQueryFromMessages(nil, "anthropic"); ok {
		t.Error("want ok=false para messages vacío")
	}
	if _, ok := ExtractQueryFromMessages([]json.RawMessage{}, "openai"); ok {
		t.Error("want ok=false para messages vacío")
	}
}

func TestExtractQueryFromMessages_MissingContent(t *testing.T) {
	msgs := []json.RawMessage{raw(t, `{"role":"user"}`)}
	if _, ok := ExtractQueryFromMessages(msgs, "anthropic"); ok {
		t.Error("want ok=false cuando falta content")
	}
}

func TestExtractQueryFromMessages_NullContent(t *testing.T) {
	msgs := []json.RawMessage{raw(t, `{"role":"user","content":null}`)}
	if _, ok := ExtractQueryFromMessages(msgs, "anthropic"); ok {
		t.Error("want ok=false cuando content es null")
	}
}

func TestExtractQueryFromMessages_WhitespaceOnlyIsEmpty(t *testing.T) {
	msgs := []json.RawMessage{raw(t, `{"role":"user","content":"   "}`)}
	if _, ok := ExtractQueryFromMessages(msgs, "anthropic"); ok {
		t.Error("want ok=false cuando el texto es solo espacios")
	}
}

func TestExtractQueryFromMessages_NoTextBlocks(t *testing.T) {
	msgs := []json.RawMessage{raw(t, `{"role":"user","content":[{"type":"image","source":{}}]}`)}
	if _, ok := ExtractQueryFromMessages(msgs, "anthropic"); ok {
		t.Error("want ok=false cuando no hay bloques de texto")
	}
}
