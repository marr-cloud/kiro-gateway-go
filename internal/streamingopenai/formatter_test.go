// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingopenai

import (
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestFormatterCorpus tests the Formatter against corpus fixtures.
func TestFormatterCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "streaming_openai/stream_kiro_to_openai_internal")

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			testFormatterCase(t, c)
		})
	}
}
