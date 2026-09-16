// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package thinkingparser

import (
	"encoding/json"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/testutil"
)

// TestCorpus validates the Parser against the 49 recorded test fixtures
// from .upstream/kiro/thinking_parser.py.
//
// The corpus uses sequence-style test cases where each step is a call to
// __init__, feed, or finalize on the same parser instance.
func TestCorpus(t *testing.T) {
	cases := testutil.LoadCorpus(t, "thinking_parser/ThinkingParser")
	if len(cases) != 49 {
		t.Fatalf("se esperaban 49 casos de ThinkingParser, hay %d", len(cases))
	}

	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			var p *Parser

			for i, step := range c.Steps {
				switch step.Method {
				case "__init__":
					// Extract handling_mode and open_tags from kwargs if provided
					handling := HandlingAsReasoningContent // default
					var openTags []string
					kwargs := testutil.Kwargs(t, step.Input)

					if hm, ok := kwargs["handling_mode"]; ok {
						if err := json.Unmarshal(hm, &handling); err != nil {
							t.Fatalf("case %s paso %d: no se pudo parsear handling_mode: %v", c.Name, i, err)
						}
					}

					if ot, ok := kwargs["open_tags"]; ok {
						if err := json.Unmarshal(ot, &openTags); err != nil {
							t.Fatalf("case %s paso %d: no se pudo parsear open_tags: %v", c.Name, i, err)
						}
					}

					if len(openTags) > 0 {
						p = NewParserWithTags(handling, 20, openTags)
					} else {
						p = NewParser(handling, 20)
					}

				case "feed":
					if p == nil {
						t.Fatalf("case %s paso %d: feed llamado sin __init__", c.Name, i)
					}
					argBytes := testutil.Arg(t, step.Input, 0)
					var text string
					if err := json.Unmarshal(argBytes, &text); err != nil {
						t.Fatalf("case %s paso %d: no se pudo parsear argumento text: %v", c.Name, i, err)
					}

					thinking, content := p.Feed(text)

					// Parse expected output
					var expected map[string]interface{}
					if err := json.Unmarshal(step.Output, &expected); err != nil {
						t.Fatalf("case %s paso %d: no se pudo parsear salida esperada: %v", c.Name, i, err)
					}

					assertFeedOutput(t, c.Name, i, thinking, content, expected)

				case "finalize":
					if p == nil {
						t.Fatalf("case %s paso %d: finalize llamado sin __init__", c.Name, i)
					}

					thinking, content := p.Finish()

					// Parse expected output
					var expected map[string]interface{}
					if err := json.Unmarshal(step.Output, &expected); err != nil {
						t.Fatalf("case %s paso %d: no se pudo parsear salida esperada: %v", c.Name, i, err)
					}

					assertFeedOutput(t, c.Name, i, thinking, content, expected)

				case "process_for_output":
					// Skip process_for_output tests for now - we handle this internally
					continue

				case "reset":
					if p == nil {
						t.Fatalf("case %s paso %d: reset llamado sin __init__", c.Name, i)
					}
					p.Reset()
					// No output expected from reset

				default:
					t.Fatalf("case %s paso %d: método desconocido %q", c.Name, i, step.Method)
				}
			}
		})
	}
}

func assertFeedOutput(t *testing.T, caseName string, stepIdx int, thinking, content string, expected map[string]interface{}) {
	t.Helper()

	// Extract expected values
	expectedThinking, _ := expected["thinking_content"]
	expectedContent, _ := expected["regular_content"]

	// Handle nil cases
	if expectedThinking == nil && thinking != "" {
		t.Errorf("case %s paso %d: thinking_content = %q, want nil/empty", caseName, stepIdx, thinking)
	}
	if expectedContent == nil && content != "" {
		t.Errorf("case %s paso %d: regular_content = %q, want nil/empty", caseName, stepIdx, content)
	}

	if exp, ok := expectedThinking.(string); ok && exp != thinking {
		t.Errorf("case %s paso %d: thinking_content = %q, want %q", caseName, stepIdx, thinking, exp)
	}
	if exp, ok := expectedContent.(string); ok && exp != content {
		t.Errorf("case %s paso %d: regular_content = %q, want %q", caseName, stepIdx, content, exp)
	}
}
