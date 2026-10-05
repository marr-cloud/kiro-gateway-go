// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingopenai

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
)

// El razonamiento nativo sale como reasoning_content; la firma y el
// razonamiento cifrado no tienen equivalente en OpenAI (DIFFERENCES §18).
func TestNativeThinkingAsReasoningContent(t *testing.T) {
	f := New("gpt-5.6-sol", AsReasoningContent)
	var w bytes.Buffer
	for _, ev := range []streamingcore.KiroEvent{
		{Kind: "native_thinking", Thinking: "Pienso"},
		{Kind: "thinking_signature", Signature: "SIG"},
		{Kind: "redacted_thinking", RedactedData: "AAEC"},
		{Kind: "content", Content: "Hola"},
	} {
		if err := f.Handle(ev, &w); err != nil {
			t.Fatal(err)
		}
	}
	out := w.String()
	if !strings.Contains(out, `"reasoning_content":"Pienso"`) {
		t.Errorf("falta reasoning_content:\n%s", out)
	}
	if strings.Contains(out, "SIG") || strings.Contains(out, "AAEC") {
		t.Errorf("la firma y el razonamiento cifrado no deben salir:\n%s", out)
	}
}
