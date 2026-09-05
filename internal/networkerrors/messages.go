// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package networkerrors

import "strings"

// FormatForUser formatea Info para una respuesta a la API. Es un port literal
// de kiro.network_errors.format_error_for_user: acepta el tipo de formato
// ("openai", "anthropic" o cualquier otro = genérico) y una bandera para
// incluir o no los pasos de resolución. Devuelve un valor cuyo JSON coincide
// con el que grababa el corpus.
//
// El valor devuelto es un map[string]any en lugar de una struct porque las tres
// ramas producen JSON con esquemas distintos: la de OpenAI lleva "code" y
// "param":null; la de Anthropic solo "type" y "message"; la genérica lleva
// "category" y "technical_details". Un tipo union en Go es más ruido que
// beneficio para un valor que solo se serializa a JSON y no se compara con ==.
//
// El mensaje se construye igual que en el original: se parte de UserMessage y,
// si include_troubleshooting y hay pasos, se le concatena
// "\n\nTroubleshooting steps:\n1. paso1\n2. paso2\n..." y se hace strip() al
// resultado (quita el "\n" final y cualquier espacio en los extremos).
func FormatForUser(info Info, formatType string, includeTroubleshooting bool) any {
	message := info.UserMessage
	if includeTroubleshooting && len(info.TroubleshootingSteps) > 0 {
		var b strings.Builder
		b.WriteString(message)
		b.WriteString("\n\nTroubleshooting steps:\n")
		for i, step := range info.TroubleshootingSteps {
			// El original usa enumerate(..., 1): la numeración empieza en 1.
			// Aquí evitamos fmt.Fprintf para no depender de la localización de
			// %d y para dejar la salida byte-exacta.
			b.WriteString(itoa(i + 1))
			b.WriteString(". ")
			b.WriteString(step)
			b.WriteString("\n")
		}
		message = b.String()
	}
	message = strings.TrimSpace(message)

	switch formatType {
	case "openai":
		return map[string]any{
			"error": map[string]any{
				"message": message,
				"type":    "connectivity_error",
				"code":    string(info.Category),
				// param es None en el original: se serializa como JSON null.
				"param": nil,
			},
		}
	case "anthropic":
		return map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "connectivity_error",
				"message": message,
			},
		}
	default:
		return map[string]any{
			"error": map[string]any{
				"type":              "connectivity_error",
				"category":          string(info.Category),
				"message":           message,
				"technical_details": info.TechnicalDetails,
			},
		}
	}
}

// ShortMessage devuelve la versión corta del error para logs: el user_message
// tal cual. Es un port literal de get_short_error_message.
func ShortMessage(info Info) string {
	return info.UserMessage
}

// itoa convierte un entero positivo pequeño en su representación decimal, sin
// pasar por fmt para dejar explícito que la salida no depende de la
// localización. Solo se usa para numerar los pasos de resolución.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	// Los pasos son pocos (máximo 5 en el corpus); un buffer fijo basta.
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
