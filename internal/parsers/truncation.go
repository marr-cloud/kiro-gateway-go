// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package parsers

import (
	"fmt"
	"strings"
)

// truncationInfo es el diagnóstico de un intento fallido de json.loads sobre
// los argumentos acumulados de un tool call. Port literal de la forma que
// devuelve kiro.parsers::AwsEventStreamParser._diagnose_json_truncation
// (.upstream/kiro/parsers.py:464-548).
type truncationInfo struct {
	isTruncated bool
	reason      string
	sizeBytes   int
}

// diagnoseJSONTruncation analiza una cadena JSON malformada para distinguir
// un truncamiento (la API de Kiro cortó la respuesta a mitad de los
// argumentos de un tool call) de un JSON simplemente inválido. Port literal
// de _diagnose_json_truncation (.upstream/kiro/parsers.py:464-548).
//
// Verificado ejecutando el método real del upstream (commit a5292ca, vía
// .upstream/kiro/parsers.py cargado directamente con los módulos loguru/
// kiro.utils/kiro.config sustituidos por stubs) contra 11 cadenas de prueba
// cubriendo cada rama: cadena vacía, solo espacios, llave/corchete sin
// cerrar, llaves desbalanceadas, corchetes desbalanceados, cadena sin
// cerrar (con y sin escape), y JSON malformado sin ninguna de esas señales.
// Las 11 salidas de ese run son los casos hardcodeados de
// TestDiagnoseJSONTruncation en parser_test.go. Este target no tiene
// fixtures propias en testdata/parsers/ — ninguno de los 22 casos de
// AwsEventStreamParser ejercita la rama de error de _finalize_tool_call —
// así que esta verificación directa contra upstream es la única cobertura
// disponible, tal como exige la Task 1 para funciones sin corpus.
func diagnoseJSONTruncation(jsonStr string) truncationInfo {
	sizeBytes := len(jsonStr) // len(s.encode('utf-8')) == longitud en bytes de un string Go ya UTF-8.
	stripped := strings.TrimSpace(jsonStr)

	if stripped == "" {
		return truncationInfo{isTruncated: false, reason: "empty string", sizeBytes: sizeBytes}
	}

	openBraces := strings.Count(stripped, "{")
	closeBraces := strings.Count(stripped, "}")
	openBrackets := strings.Count(stripped, "[")
	closeBrackets := strings.Count(stripped, "]")

	if strings.HasPrefix(stripped, "{") && !strings.HasSuffix(stripped, "}") {
		missing := openBraces - closeBraces
		return truncationInfo{
			isTruncated: true,
			reason:      fmt.Sprintf("missing %d closing brace(s)", missing),
			sizeBytes:   sizeBytes,
		}
	}

	if strings.HasPrefix(stripped, "[") && !strings.HasSuffix(stripped, "]") {
		missing := openBrackets - closeBrackets
		return truncationInfo{
			isTruncated: true,
			reason:      fmt.Sprintf("missing %d closing bracket(s)", missing),
			sizeBytes:   sizeBytes,
		}
	}

	if openBraces != closeBraces {
		return truncationInfo{
			isTruncated: true,
			reason:      fmt.Sprintf("unbalanced braces (%d open, %d close)", openBraces, closeBraces),
			sizeBytes:   sizeBytes,
		}
	}

	if openBrackets != closeBrackets {
		return truncationInfo{
			isTruncated: true,
			reason:      fmt.Sprintf("unbalanced brackets (%d open, %d close)", openBrackets, closeBrackets),
			sizeBytes:   sizeBytes,
		}
	}

	// Heurística de comillas sin cerrar: cuenta comillas no escapadas. Igual
	// que find_matching_brace, el recorrido es byte a byte en vez de
	// carácter a carácter, y es equivalente por la misma razón (ASCII no
	// aparece dentro de una secuencia UTF-8 multibyte).
	quoteCount := 0
	i := 0
	for i < len(stripped) {
		if stripped[i] == '\\' && i+1 < len(stripped) {
			i += 2
			continue
		}
		if stripped[i] == '"' {
			quoteCount++
		}
		i++
	}

	if quoteCount%2 != 0 {
		return truncationInfo{isTruncated: true, reason: "unclosed string literal", sizeBytes: sizeBytes}
	}

	return truncationInfo{isTruncated: false, reason: "malformed JSON", sizeBytes: sizeBytes}
}
