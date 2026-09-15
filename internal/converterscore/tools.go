// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package converterscore

import (
	"fmt"
	"strings"
)

// SanitizeJSONSchema sanea un JSON Schema de los campos que la API de Kiro
// rechaza. Port literal de kiro.converters_core:sanitize_json_schema
// (.upstream/kiro/converters_core.py:439-491).
//
// Kiro devuelve 400 "Improperly formed request" si el schema lleva un
// `required` vacío ([]) o cualquier `additionalProperties`, así que la
// función recorre el schema recursivamente y elimina exactamente esas dos
// cosas — ninguna más: el original NO toca `$schema`, `$defs`, `$ref`,
// `definitions` ni `default: null`, pese a lo que pudiera sugerir cualquier
// lista de claves "razonable" para un saneador de JSON Schema. Se recursa en
// `properties` (tratando cada valor como sub-schema), en cualquier otro
// valor que sea un objeto anidado, y en listas (p. ej. anyOf/oneOf),
// procesando recursivamente los elementos que sean objetos y dejando el
// resto (strings, números, bools) tal cual.
func SanitizeJSONSchema(schema map[string]any) map[string]any {
	if len(schema) == 0 {
		return map[string]any{}
	}

	result := make(map[string]any, len(schema))
	for key, value := range schema {
		if key == "required" {
			if arr, ok := value.([]any); ok && len(arr) == 0 {
				continue
			}
		}
		if key == "additionalProperties" {
			continue
		}
		if key == "properties" {
			if props, ok := value.(map[string]any); ok {
				result[key] = sanitizeProperties(props)
				continue
			}
		}
		switch v := value.(type) {
		case map[string]any:
			result[key] = SanitizeJSONSchema(v)
		case []any:
			result[key] = sanitizeSchemaList(v)
		default:
			result[key] = value
		}
	}
	return result
}

// sanitizeProperties sanea cada sub-schema de un objeto `properties`. Un
// valor de propiedad que no sea un objeto (forma inválida de JSON Schema,
// pero el original no valida esto) se deja tal cual, igual que el original
// hace con su comprensión de diccionario condicional.
func sanitizeProperties(props map[string]any) map[string]any {
	result := make(map[string]any, len(props))
	for name, value := range props {
		if sub, ok := value.(map[string]any); ok {
			result[name] = SanitizeJSONSchema(sub)
		} else {
			result[name] = value
		}
	}
	return result
}

// sanitizeSchemaList sanea los elementos de una lista de schemas (p. ej.
// anyOf/oneOf): los elementos que son objetos se sanean recursivamente, el
// resto se deja tal cual.
func sanitizeSchemaList(items []any) []any {
	result := make([]any, len(items))
	for i, item := range items {
		if sub, ok := item.(map[string]any); ok {
			result[i] = SanitizeJSONSchema(sub)
		} else {
			result[i] = item
		}
	}
	return result
}

// toolDocumentationPreamble es el encabezado fijo que antecede a la
// documentación de tools movida al system prompt. Port literal del literal
// de kiro.converters_core:process_tools_with_long_descriptions.
const toolDocumentationPreamble = "\n\n---\n" +
	"# Tool Documentation\n" +
	"The following tools have detailed documentation that couldn't fit in the tool definition.\n\n"

// ProcessToolsWithLongDescriptions mueve al system prompt la descripción de
// cualquier tool que exceda maxLen caracteres Unicode, dejando en su lugar
// una referencia corta. Port literal de
// kiro.converters_core:process_tools_with_long_descriptions
// (.upstream/kiro/converters_core.py:493-558).
//
// El original lee TOOL_DESCRIPTION_MAX_LENGTH de kiro.config; este port lo
// recibe como argumento explícito (maxLen), tal y como fija el plan de fase
// 3 — la función no lee configuración global.
//
// La comparación de longitud es en caracteres Unicode (code points), no en
// bytes: len([]rune(description)), igual que el len() de Python 3 sobre
// str. maxLen <= 0 desactiva el límite (se devuelven las tools intactas, sin
// system prompt), igual que el original. Con tools vacío o nil, devuelve
// (nil, "") — el equivalente Go de (None, "").
func ProcessToolsWithLongDescriptions(tools []UnifiedTool, maxLen int) ([]UnifiedTool, string) {
	if len(tools) == 0 {
		return nil, ""
	}
	if maxLen <= 0 {
		return tools, ""
	}

	var docParts []string
	processed := make([]UnifiedTool, 0, len(tools))

	for _, tool := range tools {
		description := tool.Description
		if len([]rune(description)) <= maxLen {
			processed = append(processed, tool)
			continue
		}

		docParts = append(docParts, fmt.Sprintf("## Tool: %s\n\n%s", tool.Name, description))
		processed = append(processed, UnifiedTool{
			Name:        tool.Name,
			Description: fmt.Sprintf("[Full documentation in system prompt under '## Tool: %s']", tool.Name),
			InputSchema: tool.InputSchema,
		})
	}

	var addition string
	if len(docParts) > 0 {
		addition = toolDocumentationPreamble + strings.Join(docParts, "\n\n---\n\n")
	}

	if len(processed) == 0 {
		return nil, addition
	}
	return processed, addition
}

// ValidateToolNames comprueba que ningún nombre de tool exceda el límite de
// 64 caracteres de la API de Kiro. Port literal de
// kiro.converters_core:validate_tool_names
// (.upstream/kiro/converters_core.py:560-600).
//
// El original solo comprueba longitud — no valida un patrón de caracteres
// permitidos, pese a lo que pudiera parecer razonable para "nombres de
// tool". Devuelve un error (equivalente al ValueError del original, con el
// mismo mensaje) si hay uno o más nombres demasiado largos, o nil si no los
// hay o si tools está vacío o es nil.
//
// El original construye la excepción como *validationerrors.Error; ese tipo
// no existe en el port (validationerrors solo expone el envoltorio de
// errores 422 de FastAPI/Pydantic, un concepto distinto), así que esta
// función devuelve un error de stdlib con el mismo mensaje exacto. Ver el
// informe de la fase 3, tarea 4.
func ValidateToolNames(tools []UnifiedTool) error {
	if len(tools) == 0 {
		return nil
	}

	type problem struct {
		name   string
		length int
	}
	var problems []problem
	for _, tool := range tools {
		if n := len([]rune(tool.Name)); n > 64 {
			problems = append(problems, problem{tool.Name, n})
		}
	}
	if len(problems) == 0 {
		return nil
	}

	lines := make([]string, len(problems))
	for i, p := range problems {
		lines[i] = fmt.Sprintf("  - '%s' (%d characters)", p.name, p.length)
	}

	return fmt.Errorf(
		"Tool name(s) exceed Kiro API limit of 64 characters:\n%s\n\n"+
			"Solution: Use shorter tool names (max 64 characters).\n"+
			"Example: 'get_user_data' instead of 'get_authenticated_user_profile_data_with_extended_information_about_it'",
		strings.Join(lines, "\n"),
	)
}

// ConvertToolsToKiroFormat convierte tools del formato unificado al formato
// toolSpecification que espera la API de Kiro. Port literal de
// kiro.converters_core:convert_tools_to_kiro_format
// (.upstream/kiro/converters_core.py:602-634).
//
// Cada input_schema se sanea con SanitizeJSONSchema. La API de Kiro exige
// una descripción no vacía: si la descripción de la tool está vacía o solo
// tiene espacios en blanco (incluido el caso description="" al que colapsa
// cualquier description=None del original, ver UnifiedTool en types.go), se
// sustituye por el placeholder "Tool: {name}".
//
// Con tools vacío o nil, devuelve una lista vacía (no nil): el original
// devuelve [] en Python, que serializa distinto de null.
func ConvertToolsToKiroFormat(tools []UnifiedTool) []map[string]any {
	if len(tools) == 0 {
		return []map[string]any{}
	}

	kiroTools := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		sanitized := SanitizeJSONSchema(tool.InputSchema)

		description := tool.Description
		if strings.TrimSpace(description) == "" {
			description = fmt.Sprintf("Tool: %s", tool.Name)
		}

		kiroTools = append(kiroTools, map[string]any{
			"toolSpecification": map[string]any{
				"name":        tool.Name,
				"description": description,
				"inputSchema": map[string]any{"json": sanitized},
			},
		})
	}
	return kiroTools
}
