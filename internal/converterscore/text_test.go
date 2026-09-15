// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

func TestToolCallsToText(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/tool_calls_to_text") {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			var toolCalls []map[string]any
			if err := json.Unmarshal(raw, &toolCalls); err != nil {
				t.Fatalf("case %s: decode input: %v", c.Name, err)
			}
			got := ToolCallsToText(toolCalls)
			var want string
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decode want: %v", c.Name, err)
			}
			if got != want {
				t.Fatalf("case %s:\n got: %q\nwant: %q", c.Name, got, want)
			}
		})
	}
}

func TestToolResultsToText(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/tool_results_to_text") {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			var toolResults []map[string]any
			if err := json.Unmarshal(raw, &toolResults); err != nil {
				t.Fatalf("case %s: decode input: %v", c.Name, err)
			}
			got := ToolResultsToText(toolResults)
			var want string
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("case %s: decode want: %v", c.Name, err)
			}
			if got != want {
				t.Fatalf("case %s:\n got: %q\nwant: %q", c.Name, got, want)
			}
		})
	}
}
