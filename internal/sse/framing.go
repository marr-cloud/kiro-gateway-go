// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package sse

import (
	"bytes"
)

// FormatEvent returns the bytes of one SSE event in the appropriate format.
//
//   - If name == "" (OpenAI dialect):
//     Returns "data: {payload}\n\n"
//
//   - If name != "" (Anthropic dialect):
//     Returns "event: {name}\ndata: {payload}\n\n"
//
// The data parameter should contain the JSON payload as bytes (without the "data:" prefix).
// The function handles the SSE framing and line endings.
func FormatEvent(name string, data []byte) []byte {
	var buf bytes.Buffer

	if name != "" {
		// Anthropic dialect: include event type
		buf.WriteString("event: ")
		buf.WriteString(name)
		buf.WriteString("\n")
	}

	// OpenAI and Anthropic both have the data line
	buf.WriteString("data: ")
	buf.Write(data)
	buf.WriteString("\n\n")

	return buf.Bytes()
}

// FormatDone returns the bytes of the OpenAI-style [DONE] message: "data: [DONE]\n\n"
func FormatDone() []byte {
	return []byte("data: [DONE]\n\n")
}
