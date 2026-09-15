// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import "strings"

// ToolCallsToText convierte tool_calls a su representación textual.
// Port literal de kiro.converters_core.tool_calls_to_text.
func ToolCallsToText(toolCalls []map[string]any) string {
	if len(toolCalls) == 0 {
		return ""
	}

	var parts []string
	for _, tc := range toolCalls {
		funcObj, _ := tc["function"].(map[string]any)
		name, _ := funcObj["name"].(string)
		if name == "" {
			name = "unknown"
		}
		arguments, _ := funcObj["arguments"].(string)
		if arguments == "" {
			arguments = "{}"
		}
		toolID, _ := tc["id"].(string)

		// Format: [Tool: name] (id)\narguments
		var part string
		if toolID != "" {
			part = "[Tool: " + name + " (" + toolID + ")]\n" + arguments
		} else {
			part = "[Tool: " + name + "]\n" + arguments
		}
		parts = append(parts, part)
	}

	return strings.Join(parts, "\n\n")
}

// ToolResultsToText convierte tool_results a su representación textual.
// Port literal de kiro.converters_core.tool_results_to_text.
func ToolResultsToText(toolResults []map[string]any) string {
	if len(toolResults) == 0 {
		return ""
	}

	var parts []string
	for _, tr := range toolResults {
		content := tr["content"]
		toolUseID, _ := tr["tool_use_id"].(string)

		var contentText string
		switch c := content.(type) {
		case string:
			contentText = c
		default:
			contentText = ExtractTextContent(content)
		}

		// Use placeholder if content is empty
		if contentText == "" {
			contentText = "(empty result)"
		}

		// Format: [Tool Result] (id)\ncontent
		var part string
		if toolUseID != "" {
			part = "[Tool Result (" + toolUseID + ")]\n" + contentText
		} else {
			part = "[Tool Result]\n" + contentText
		}
		parts = append(parts, part)
	}

	return strings.Join(parts, "\n\n")
}
