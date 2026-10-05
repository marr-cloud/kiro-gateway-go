// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package routesopenai

import "strings"

// upperModelWords son los segmentos de id que se escriben como siglas.
var upperModelWords = map[string]bool{"gpt": true, "glm": true}

// modelDisplayName deriva un nombre legible del id de Kiro para el display_name
// de /v1/models (DIFFERENCES §15): "claude-sonnet-4.5" → "Claude Sonnet 4.5".
// Claude Code no reconoce los ids con punto de Kiro y, sin display_name, los
// muestra crudos en el picker /model.
func modelDisplayName(id string) string {
	words := strings.FieldsFunc(id, func(r rune) bool { return r == '-' })
	for i, word := range words {
		if upperModelWords[word] {
			words[i] = strings.ToUpper(word)
			continue
		}
		words[i] = strings.ToUpper(word[:1]) + word[1:]
	}
	return strings.Join(words, " ")
}
