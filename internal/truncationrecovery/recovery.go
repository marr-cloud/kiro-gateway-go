// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package truncationrecovery

// GenerateTruncationToolResult generates a synthetic tool_result message for a truncated tool call.
//
// The message is carefully worded to:
// - Acknowledge API limitation (not model's fault)
// - Warn against repeating same operation
// - NOT give specific instructions (avoid micro-steps)
//
// Args:
//   - toolName: Name of the truncated tool
//   - toolUseID: ID of the truncated tool call
//   - truncationInfo: Diagnostic information about truncation (e.g., {"size_bytes": 5000, "reason": "..."})
//
// Returns:
//   - Synthetic tool_result in unified format
func GenerateTruncationToolResult(toolName string, toolUseID string, truncationInfo map[string]any) map[string]any {
	content := "[API Limitation] Your tool call was truncated by the upstream API due to output size limits.\n\n" +
		"If the tool result below shows an error or unexpected behavior, this is likely a CONSEQUENCE of the truncation, " +
		"not the root cause. The tool call itself was cut off before it could be fully transmitted.\n\n" +
		"Repeating the exact same operation will be truncated again. Consider adapting your approach."

	return map[string]any{
		"type":        "tool_result",
		"tool_use_id": toolUseID,
		"content":     content,
		"is_error":    true,
	}
}

// GenerateTruncationUserMessage generates a synthetic user message for content truncation.
//
// The message is carefully worded to:
// - Acknowledge it's not model's fault
// - Suggest adaptation without specific instructions
// - NOT tell model to "break into steps" (causes micro-steps)
//
// Returns:
//   - Synthetic user message text
func GenerateTruncationUserMessage() string {
	return "[System Notice] Your previous response was truncated by the API due to " +
		"output size limitations. This is not an error on your part. " +
		"If you need to continue, please adapt your approach rather than repeating the same output."
}

// ShouldInjectRecovery returns true if truncation recovery messages should be injected.
// This checks the TRUNCATION_RECOVERY configuration flag via converterscore.TruncationRecoveryEnabled.
func ShouldInjectRecovery() bool {
	// Import converterscore to access TruncationRecoveryEnabled
	// This would need to be imported at the module level
	// For now, we'll use a package-level variable that can be set by tests
	return truncationRecoveryEnabled
}

// truncationRecoveryEnabled is a package-level variable that mirrors converterscore.TruncationRecoveryEnabled.
// This is set to true by default, matching the upstream behavior and converterscore initialization.
// Tests can override this as needed.
var truncationRecoveryEnabled = true
