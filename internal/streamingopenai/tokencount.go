// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package streamingopenai

import (
	"bytes"
	"encoding/json"
)

// countTokens, countMessageTokens and countToolsTokens used to live in this
// file as package-private functions. Task 8 (streaminganthropic) needs the
// identical primitives for estimate_request_tokens
// (.upstream/kiro/streaming_anthropic.py:177-183), so they were relocated to
// internal/tokenizer as exported functions (CountTokens, CountMessageTokens,
// CountToolsTokens, CountSystemTokens) — see
// internal/tokenizer/requesttokens.go and docs/MAPPING.md's task-8 ruling.
// This package now calls them via thin wrappers so formatter.go's call
// sites, and this package's own tests, don't need to change.
//
// isJSONTruthy stays here: it gates the OpenAI-dialect "usage" event
// truthiness check (.upstream/kiro/streaming_core.py:320), which is
// specific to this package and not part of the shared token-count surface.

// isJSONTruthy reports whether raw JSON bytes represent a Python-truthy
// value: present, and not one of None/False/0/0.0/""/{}/[] . Mirrors the
// `if event.usage:` / `if metering_data:` checks
// (.upstream/kiro/streaming_openai.py:265-266,405 and
// .upstream/kiro/streaming_core.py:320) that gate whether a "usage" event
// counts as a received completion signal and whether metering_data gets
// echoed back as credits_used.
func isJSONTruthy(raw json.RawMessage) bool {
	switch string(bytes.TrimSpace(raw)) {
	case "", "null", "false", "0", "0.0", "{}", "[]", `""`:
		return false
	default:
		return true
	}
}
