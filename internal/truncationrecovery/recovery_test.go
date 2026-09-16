// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package truncationrecovery

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestGenerateTruncationToolResult validates GenerateTruncationToolResult against the
// 13 corpus fixtures from kiro.truncation_recovery:generate_truncation_tool_result
// (.upstream/kiro/truncation_recovery.py:47-89).
func TestGenerateTruncationToolResult(t *testing.T) {
	cases := testutil.LoadCorpus(t, "truncation_recovery/generate_truncation_tool_result")
	if len(cases) != 13 {
		t.Fatalf("expected 13 corpus cases for generate_truncation_tool_result, got %d", len(cases))
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			// Unpack input: parameters can be passed as args or kwargs
			args := testutil.Args(t, c.Input)
			var toolName, toolUseID string
			var truncationInfo map[string]any

			if len(args) >= 3 {
				// Parameters passed as args
				if err := json.Unmarshal(args[0], &toolName); err != nil {
					t.Fatalf("case %s: decoding tool_name from args[0]: %v", c.Name, err)
				}
				if err := json.Unmarshal(args[1], &toolUseID); err != nil {
					t.Fatalf("case %s: decoding tool_use_id from args[1]: %v", c.Name, err)
				}
				if err := json.Unmarshal(args[2], &truncationInfo); err != nil {
					t.Fatalf("case %s: decoding truncation_info from args[2]: %v", c.Name, err)
				}
			} else {
				// Parameters passed as kwargs
				kwargs := testutil.Kwargs(t, c.Input)
				if err := json.Unmarshal(kwargs["tool_name"], &toolName); err != nil {
					t.Fatalf("case %s: decoding tool_name from kwargs: %v", c.Name, err)
				}
				if err := json.Unmarshal(kwargs["tool_use_id"], &toolUseID); err != nil {
					t.Fatalf("case %s: decoding tool_use_id from kwargs: %v", c.Name, err)
				}
				if err := json.Unmarshal(kwargs["truncation_info"], &truncationInfo); err != nil {
					t.Fatalf("case %s: decoding truncation_info from kwargs: %v", c.Name, err)
				}
			}

			// Call the function
			got := GenerateTruncationToolResult(toolName, toolUseID, truncationInfo)

			// Compare output
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// TestGenerateTruncationUserMessage validates GenerateTruncationUserMessage against the
// 2 corpus fixtures from kiro.truncation_recovery:generate_truncation_user_message
// (.upstream/kiro/truncation_recovery.py:92-112).
func TestGenerateTruncationUserMessage(t *testing.T) {
	cases := testutil.LoadCorpus(t, "truncation_recovery/generate_truncation_user_message")
	if len(cases) != 2 {
		t.Fatalf("expected 2 corpus cases for generate_truncation_user_message, got %d", len(cases))
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			// This function takes no arguments
			args := testutil.Args(t, c.Input)
			if len(args) != 0 {
				t.Fatalf("case %s: expected 0 args, got %d", c.Name, len(args))
			}

			// Call the function
			got := GenerateTruncationUserMessage()

			// Compare output
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}

// TestShouldInjectRecovery validates ShouldInjectRecovery against the
// 2 corpus fixtures from kiro.truncation_recovery:should_inject_recovery
// (.upstream/kiro/truncation_recovery.py:36-44).
func TestShouldInjectRecovery(t *testing.T) {
	cases := testutil.LoadCorpus(t, "truncation_recovery/should_inject_recovery")
	if len(cases) != 2 {
		t.Fatalf("expected 2 corpus cases for should_inject_recovery, got %d", len(cases))
	}

	// Save and restore the original value
	origEnabled := truncationRecoveryEnabled
	t.Cleanup(func() { truncationRecoveryEnabled = origEnabled })

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			// This function takes no arguments
			args := testutil.Args(t, c.Input)
			if len(args) != 0 {
				t.Fatalf("case %s: expected 0 args, got %d", c.Name, len(args))
			}

			// Read TRUNCATION_RECOVERY from config
			config := testutil.Config(c.Input)
			if config != nil {
				if truncationRecoveryRaw, ok := config["TRUNCATION_RECOVERY"]; ok {
					var enabled bool
					if err := json.Unmarshal(truncationRecoveryRaw, &enabled); err != nil {
						t.Fatalf("case %s: decoding TRUNCATION_RECOVERY: %v", c.Name, err)
					}
					truncationRecoveryEnabled = enabled
				}
			}

			// Call the function
			got := ShouldInjectRecovery()

			// Compare output
			testutil.AssertJSONEqual(t, got, c.Output, c.Name)
		})
	}
}
