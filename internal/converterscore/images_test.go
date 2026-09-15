// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestConvertImagesToKiroFormatAgainstCorpus valida ConvertImagesToKiroFormat
// contra los 25 casos grabados de kiro.converters_core:convert_images_to_kiro_format.
func TestConvertImagesToKiroFormatAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/convert_images_to_kiro_format") {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			var input []map[string]any
			if err := json.Unmarshal(raw, &input); err != nil {
				t.Fatalf("case %s: decode input: %v", c.Name, err)
			}
			got := ConvertImagesToKiroFormat(input)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// TestConvertToolResultsToKiroFormatAgainstCorpus valida
// ConvertToolResultsToKiroFormat contra los 17 casos grabados de
// kiro.converters_core:convert_tool_results_to_kiro_format.
func TestConvertToolResultsToKiroFormatAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "converters_core/convert_tool_results_to_kiro_format") {
		t.Run(c.Name, func(t *testing.T) {
			raw := testutil.Arg(t, c.Input, 0)
			var input []map[string]any
			if err := json.Unmarshal(raw, &input); err != nil {
				t.Fatalf("case %s: decode input: %v", c.Name, err)
			}
			got := ConvertToolResultsToKiroFormat(input)
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}
