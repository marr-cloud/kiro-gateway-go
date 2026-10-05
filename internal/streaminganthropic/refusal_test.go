// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streaminganthropic

import (
	"bytes"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/streamingcore"
)

// Un corte de Kiro (CONTENT_FILTERED) termina con stop_reason "refusal" en
// vez de un end_turn silencioso (DIFFERENCES §17), aunque haya texto parcial
// y no haya llegado context_usage, y no se marca como truncado.
func TestFinishRefusalStopReason(t *testing.T) {
	f := New("claude-sonnet-5.5")
	var w bytes.Buffer
	for _, ev := range []streamingcore.KiroEvent{
		{Kind: "content", Content: "Hola, est"},
		{Kind: "refusal", Refusal: &streamingcore.RefusalInfo{Category: "REASONING_EXTRACTION"}},
	} {
		if err := f.Handle(ev, &w); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Finish(&w); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(w.String(), `"stop_reason": "refusal"`) {
		t.Errorf("falta stop_reason refusal en:\n%s", w.String())
	}
	if f.ContentWasTruncated() {
		t.Errorf("un corte no es una truncación: no debe activar la recuperación de truncado")
	}
}
