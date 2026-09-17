// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesanthropic

import (
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/converterscore"
	"github.com/marr-cloud/kiro-gateway-go/internal/truncationstate"
)

// TestTruncationRecoverySaveStateIntegration verifies that the shared
// truncationstate.State is wired correctly for the 8b (SAVE) + 8a (INJECT)
// cycle. This test demonstrates that the Handler's truncation field is
// properly initialized and can be used for persistence.
func TestTruncationRecoverySaveStateIntegration(t *testing.T) {
	// Enable truncation recovery for this test
	origEnabled := converterscore.TruncationRecoveryEnabled
	t.Cleanup(func() { converterscore.TruncationRecoveryEnabled = origEnabled })
	converterscore.TruncationRecoveryEnabled = true

	// Create a shared truncationstate.State (same instance that routes/8a/8b share)
	state := truncationstate.New()

	cfg := testConfig()
	manager := newTestManager(t, cfg, []string{"tok-test"})

	// Create handler with the shared state
	h := newTestHandler(t, manager, cfg, "http://localhost:8000")
	h.truncation = state

	// Verify the handler has the state
	if h.truncation != state {
		t.Fatalf("handler.truncation not wired correctly")
	}

	// Simulate Task 8b's save hook: persist a truncated tool
	toolID := "tc_test_001"
	toolName := "test_tool"
	truncInfo := map[string]any{
		"size_bytes": 5000,
		"reason":     "max_tokens",
	}

	h.truncation.SetTool(toolID, truncationstate.ToolRecord{
		ToolName:       toolName,
		TruncationInfo: truncInfo,
	})

	// Simulate Task 8a's inject hook: retrieve the persisted tool
	retrieved, ok := h.truncation.GetTool(toolID)
	if !ok {
		t.Fatalf("tool not found in state after SetTool")
	}

	record, ok := retrieved.(truncationstate.ToolRecord)
	if !ok {
		t.Fatalf("retrieved record is not ToolRecord type")
	}

	if record.ToolName != toolName {
		t.Errorf("ToolName mismatch: got %q, want %q", record.ToolName, toolName)
	}
	if record.TruncationInfo["size_bytes"] != truncInfo["size_bytes"] {
		t.Errorf("TruncationInfo mismatch")
	}
}
