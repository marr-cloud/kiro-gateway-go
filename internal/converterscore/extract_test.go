// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestExtractTextContentAgainstCorpus valida ExtractTextContent contra los
// 152 casos grabados de kiro.converters_core:extract_text_content.
func TestExtractTextContentAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/extract_text_content") {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			var input any
			if err := json.Unmarshal(raw, &input); err != nil {
				t.Fatalf("case %s: decode input: %v", c.Name, err)
			}
			got := ExtractTextContent(input)
			var want string
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decode want: %v", c.Name, err)
			}
			if got != want {
				t.Fatalf("case %s: got %q, want %q", c.Name, got, want)
			}
		})
	}
}

// TestExtractImagesFromContentAgainstCorpus valida ExtractImagesFromContent
// contra los 100 casos grabados de
// kiro.converters_core:extract_images_from_content.
func TestExtractImagesFromContentAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/extract_images_from_content") {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			var input any
			if err := json.Unmarshal(raw, &input); err != nil {
				t.Fatalf("case %s: decode input: %v", c.Name, err)
			}
			got := ExtractImagesFromContent(input)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// TestExtractToolResultsFromContentAgainstCorpus valida
// ExtractToolResultsFromContent contra los 53 casos grabados de
// kiro.converters_core:extract_tool_results_from_content.
func TestExtractToolResultsFromContentAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/extract_tool_results_from_content") {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			var input any
			if err := json.Unmarshal(raw, &input); err != nil {
				t.Fatalf("case %s: decode input: %v", c.Name, err)
			}
			got := ExtractToolResultsFromContent(input)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// TestExtractToolUsesFromMessageAgainstCorpus valida ExtractToolUsesFromMessage
// contra los 34 casos grabados de
// kiro.converters_core:extract_tool_uses_from_message, cuya firma Python es
// extract_tool_uses_from_message(content, tool_calls=None) — dos argumentos
// sueltos, no un UnifiedMessage. La interfaz de este port los agrupa en un
// UnifiedMessage porque así lo fija el plan de la fase 3.
//
// El recorder graba la llamada como posicional cuando el caller del upstream
// pasó ambos argumentos así (30 de los 34 casos), y como kwargs
// {"content": ..., "tool_calls": ...} cuando los pasó por nombre (los otros
// 4). El test normaliza ambas formas antes de construir el UnifiedMessage.
func TestExtractToolUsesFromMessageAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/extract_tool_uses_from_message") {
		t.Run(c.Name, func(t *testing.T) {
			args := testutil.Args(t, c.Input)

			var contentRaw json.RawMessage
			if len(args) > 0 {
				contentRaw = args[0]
			} else {
				contentRaw = testutil.Kwarg(t, c.Input, "content")
			}

			var toolCallsRaw json.RawMessage
			if len(args) > 1 {
				toolCallsRaw = args[1]
			} else {
				toolCallsRaw = testutil.Kwarg(t, c.Input, "tool_calls")
			}

			var content any
			if err := json.Unmarshal(contentRaw, &content); err != nil {
				t.Fatalf("case %s: decode content: %v", c.Name, err)
			}
			var toolCalls []map[string]any
			if err := json.Unmarshal(toolCallsRaw, &toolCalls); err != nil {
				t.Fatalf("case %s: decode tool_calls: %v", c.Name, err)
			}

			msg := UnifiedMessage{Content: content, ToolCalls: toolCalls}
			got := ExtractToolUsesFromMessage(msg)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}
