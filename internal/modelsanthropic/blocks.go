// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package modelsanthropic

import "encoding/json"

// Base64ImageSource es una fuente de imagen codificada en base64.
type Base64ImageSource struct {
	Type      string `json:"type"` // siempre "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// URLImageSource es una fuente de imagen referenciada por URL.
//
// Nota del original: las imágenes por URL requieren descargarlas y
// convertirlas a base64 para la API de Kiro; en el original se registran
// como warning y se omiten. Esa decisión es lógica de converter, no de tipo,
// así que no se replica aquí.
type URLImageSource struct {
	Type string `json:"type"` // siempre "url"
	URL  string `json:"url"`
}

// ImageSource es la unión discriminada de las dos codificaciones de fuente de
// imagen que acepta un ImageContentBlock (Base64ImageSource | URLImageSource
// en el original, como Union de Pydantic). Igual que ContentBlock, conserva
// los bytes originales en Raw y decodifica según el campo "type": "url" pasa
// a URL, cualquier otro valor (en la práctica solo "base64") pasa a Base64,
// replicando el orden del Union del original (Base64ImageSource primero).
type ImageSource struct {
	Type   string
	Base64 *Base64ImageSource
	URL    *URLImageSource
	Raw    json.RawMessage
}

// UnmarshalJSON decodifica un ImageSource preservando los bytes originales en
// Raw y poblando Base64 o URL según el discriminador "type".
func (s *ImageSource) UnmarshalJSON(data []byte) error {
	s.Raw = append(json.RawMessage(nil), data...)

	var probe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	s.Type = probe.Type

	if probe.Type == "url" {
		var u URLImageSource
		if err := json.Unmarshal(data, &u); err != nil {
			return err
		}
		s.URL = &u
		return nil
	}

	var b Base64ImageSource
	if err := json.Unmarshal(data, &b); err != nil {
		return err
	}
	s.Base64 = &b
	return nil
}

// ImageContentBlock es un bloque de contenido de imagen en formato
// Anthropic: siempre lleva type="image" y una fuente (base64 o URL).
type ImageContentBlock struct {
	Type   string      `json:"type"` // siempre "image"
	Source ImageSource `json:"source"`
}

// contentBlockAux es la forma auxiliar sobre la que ContentBlock.UnmarshalJSON
// decodifica para leer "type" y todos los campos opcionales del original,
// incluidos los que un ContentBlock unificado consolida en un único campo Go:
// "name" (ToolUseContentBlock) y "tool_name" (ToolReferenceContentBlock) se
// consolidan ambos en el campo Name del ContentBlock público, porque la
// interfaz de este task no reserva un campo ToolName aparte.
type contentBlockAux struct {
	Type      string          `json:"type"`
	Text      *string         `json:"text,omitempty"`
	Thinking  *string         `json:"thinking,omitempty"`
	Signature *string         `json:"signature,omitempty"`
	Name      *string         `json:"name,omitempty"`
	ToolName  *string         `json:"tool_name,omitempty"` // ToolReferenceContentBlock
	Input     json.RawMessage `json:"input,omitempty"`
	ID        *string         `json:"id,omitempty"`
	ToolUseID *string         `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   *bool           `json:"is_error,omitempty"`
	Source    *ImageSource    `json:"source,omitempty"`
}

// ContentBlock es la unión polimórfica de todos los bloques de contenido de
// la API Anthropic (TextContentBlock, ThinkingContentBlock, ToolUseContentBlock,
// ToolResultContentBlock, ImageContentBlock, ToolReferenceContentBlock),
// discriminada por Type. Es la pieza D5 del plan de fase 3: en vez de un tipo
// Go distinto por variante (que obligaría a los converters a hacer un type
// switch sobre una interfaz), se aplana en un único struct con todos los
// campos opcionales de todas las variantes, más Raw para no perder bytes que
// el struct no modele explícitamente.
type ContentBlock struct {
	Type      string
	Text      *string
	Thinking  *string
	Signature *string
	Name      *string
	Input     json.RawMessage
	ID        *string
	ToolUseID *string
	Content   json.RawMessage
	IsError   *bool
	Source    *ImageSource

	// Raw conserva los bytes JSON originales del bloque, sin modificar. Sirve
	// para reemitir un bloque que el struct aplanado no representa
	// completamente (p.ej. campos desconocidos que "extra": "allow" acepta en
	// el original) y para que los tests de discriminación puedan verificar
	// que no se pierde ni un byte de la entrada.
	Raw json.RawMessage
}

// UnmarshalJSON decodifica un ContentBlock:
//  1. Guarda los bytes de entrada en Raw, sin modificar.
//  2. Decodifica en la struct auxiliar contentBlockAux para leer "type" y
//     todos los campos opcionales conocidos.
//  3. Consolida esos campos en el ContentBlock público, incluida la fusión de
//     "name"/"tool_name" en Name.
//
// No falla si aparecen campos desconocidos: encoding/json los ignora al
// decodificar en un struct por defecto, y quedan preservados en Raw para la
// reemisión.
func (b *ContentBlock) UnmarshalJSON(data []byte) error {
	b.Raw = append(json.RawMessage(nil), data...)

	var aux contentBlockAux
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}

	b.Type = aux.Type
	b.Text = aux.Text
	b.Thinking = aux.Thinking
	b.Signature = aux.Signature
	if aux.Name != nil {
		b.Name = aux.Name
	} else {
		b.Name = aux.ToolName
	}
	b.Input = aux.Input
	b.ID = aux.ID
	b.ToolUseID = aux.ToolUseID
	b.Content = aux.Content
	b.IsError = aux.IsError
	b.Source = aux.Source
	return nil
}
