// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"strings"
)

// ConvertImagesToKiroFormat converts unified images to Kiro API format.
//
// Unified format: [{"media_type": "image/jpeg", "data": "base64..."}]
// Kiro format: [{"format": "jpeg", "source": {"bytes": "base64..."}}]
//
// Also handles the case where data contains a full data URL (data:image/jpeg;base64,...)
// by stripping the prefix and extracting pure base64.
func ConvertImagesToKiroFormat(images []map[string]any) []map[string]any {
	if len(images) == 0 {
		return []map[string]any{}
	}

	kiroImages := []map[string]any{}

	for _, img := range images {
		mediaType := "image/jpeg"
		if mt, ok := img["media_type"]; ok {
			if mtStr, isStr := mt.(string); isStr {
				mediaType = mtStr
			}
		}

		data := ""
		if d, ok := img["data"]; ok {
			if dStr, isStr := d.(string); isStr {
				data = dStr
			}
		}

		if data == "" {
			continue
		}

		// Strip data URL prefix if present (some clients send "data:image/jpeg;base64,..." in data field)
		// Kiro API expects pure base64 without the prefix
		if strings.HasPrefix(data, "data:") {
			parts := strings.SplitN(data, ",", 2)
			if len(parts) == 2 {
				header := parts[0] // "data:image/jpeg;base64"
				actualData := parts[1]

				// Extract media type from header if present
				mediaPart := strings.Split(header, ";")[0] // "data:image/jpeg"
				extractedMediaType := strings.TrimPrefix(mediaPart, "data:")
				if extractedMediaType != "" {
					mediaType = extractedMediaType
				}
				data = actualData
			}
		}

		// Extract format from media_type: "image/jpeg" -> "jpeg"
		formatStr := mediaType
		if idx := strings.Index(mediaType, "/"); idx >= 0 {
			formatStr = mediaType[idx+1:]
		}

		kiroImages = append(kiroImages, map[string]any{
			"format": formatStr,
			"source": map[string]any{
				"bytes": data,
			},
		})
	}

	return kiroImages
}

// ConvertToolResultsToKiroFormat converts unified tool results to Kiro API format.
//
// Unified format: {"type": "tool_result", "tool_use_id": "...", "content": "..."}
// Kiro format: {"content": [{"text": "..."}], "status": "success", "toolUseId": "..."}
func ConvertToolResultsToKiroFormat(toolResults []map[string]any) []map[string]any {
	kiroResults := []map[string]any{}

	for _, tr := range toolResults {
		contentValue := tr["content"]
		contentText := ExtractTextContent(contentValue)

		// Ensure content is not empty - Kiro API requires non-empty content
		if contentText == "" {
			contentText = "(empty result)"
		}

		toolUseID := ""
		if id, ok := tr["tool_use_id"]; ok {
			if idStr, isStr := id.(string); isStr {
				toolUseID = idStr
			}
		}

		kiroResults = append(kiroResults, map[string]any{
			"content": []map[string]any{
				{"text": contentText},
			},
			"status":    "success",
			"toolUseId": toolUseID,
		})
	}

	return kiroResults
}
