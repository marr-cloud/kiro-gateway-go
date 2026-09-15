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
//
// Base64ImageSource y URLImageSource se embeben SIN NOMBRE (campo anónimo) a
// propósito, no solo por comodidad: es lo que hace que encoding/json
// APLANE sus campos (media_type, data, url) directamente en el objeto JSON
// de ImageSource al serializar, en vez de anidarlos bajo una clave
// "Base64ImageSource"/"URLImageSource" que el wire de Anthropic no tiene. El
// campo Type explícito, declarado a menos profundidad que los dos
// embebidos, es lo que gana la resolución de "type" sin ambigüedad (los dos
// tipos embebidos también declaran su propio "type", pero al estar más
// profundos quedan ocultos por completo, no producen conflicto). Verificado
// con un round-trip Unmarshal→Marshal→Unmarshal→Marshal que produce bytes
// idénticos en las dos serializaciones (TestRoundTripImageSource).
//
// El precio de este truco: el campo ya no se llama Source.Base64 sino
// Source.Base64ImageSource (el nombre de un campo anónimo es el nombre de su
// tipo), aunque sus propios campos (MediaType, Data, URL) también quedan
// promovidos y accesibles directamente como Source.MediaType, Source.Data,
// Source.URL.
type ImageSource struct {
	Type               string `json:"type"`
	*Base64ImageSource `json:",omitempty"`
	*URLImageSource    `json:",omitempty"`
	Raw                json.RawMessage `json:"-"`
}

// UnmarshalJSON decodifica un ImageSource preservando los bytes originales en
// Raw y poblando Base64ImageSource o URLImageSource según el discriminador
// "type".
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
		s.URLImageSource = &u
		return nil
	}

	var b Base64ImageSource
	if err := json.Unmarshal(data, &b); err != nil {
		return err
	}
	s.Base64ImageSource = &b
	return nil
}

// ImageContentBlock es un bloque de contenido de imagen en formato
// Anthropic: siempre lleva type="image" y una fuente (base64 o URL).
type ImageContentBlock struct {
	Type   string      `json:"type"` // siempre "image"
	Source ImageSource `json:"source"`
}

// contentBlockAux es la forma auxiliar sobre la que ContentBlock.UnmarshalJSON
// decodifica para leer "type" y todos los campos opcionales del original. Sus
// tags son la única fuente de verdad de las claves del wire al DEcodificar;
// contentBlockAux nunca se serializa (solo se usa dentro de UnmarshalJSON),
// así que no necesita objetear un ContentBlock: existe para que el
// ContentBlock público no tenga que exponer una struct distinta por variante.
type contentBlockAux struct {
	Type      string          `json:"type"`
	Text      *string         `json:"text,omitempty"`
	Thinking  *string         `json:"thinking,omitempty"`
	Signature *string         `json:"signature,omitempty"`
	Name      *string         `json:"name,omitempty"`      // ToolUseContentBlock
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
//
// Las tags json de cada campo son las claves reales del wire de Anthropic
// (ver .upstream/kiro/models_anthropic.py) y son las que usa
// encoding/json.Marshal para reemitir el bloque: sin ellas, Marshal produce
// claves con el nombre Go capitalizado (Type, Text, ...) y además emite Raw
// completo bajo una clave "Raw", duplicando el bloque. Name (de
// ToolUseContentBlock) y ToolName (de ToolReferenceContentBlock) se dejan
// como DOS campos separados, cada uno con su propia clave, en vez de
// consolidarse en uno solo: un único campo Go no puede llevar dos tags json
// distintas a la vez, y consolidarlos habría roto la reemisión de
// tool_reference (habría reemitido "name" en vez de "tool_name"). Esto es un
// cambio respecto a la primera versión de este fichero, que sí los
// consolidaba; ver el informe de fix del task 1, ronda 1, para el porqué.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      *string         `json:"text,omitempty"`
	Thinking  *string         `json:"thinking,omitempty"`
	Signature *string         `json:"signature,omitempty"`
	Name      *string         `json:"name,omitempty"`      // ToolUseContentBlock
	ToolName  *string         `json:"tool_name,omitempty"` // ToolReferenceContentBlock
	Input     json.RawMessage `json:"input,omitempty"`
	ID        *string         `json:"id,omitempty"`
	ToolUseID *string         `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
	IsError   *bool           `json:"is_error,omitempty"`
	Source    *ImageSource    `json:"source,omitempty"`

	// Raw conserva los bytes JSON originales del bloque, sin modificar. Sirve
	// para que un consumidor que necesite fidelidad byte a byte (p.ej. un
	// campo desconocido que "extra": "allow" acepta en el original y que el
	// struct aplanado no modela) pueda reemitir el bloque original en vez del
	// que reconstruye Marshal a partir de los campos tipados. Lleva
	// json:"-": NUNCA se serializa como parte del bloque — si lo hiciera,
	// duplicaría el bloque entero bajo una clave "Raw" en la salida.
	Raw json.RawMessage `json:"-"`
}

// UnmarshalJSON decodifica un ContentBlock:
//  1. Guarda los bytes de entrada en Raw, sin modificar.
//  2. Decodifica en la struct auxiliar contentBlockAux para leer "type" y
//     todos los campos opcionales conocidos.
//  3. Copia esos campos al ContentBlock público uno a uno.
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
	b.Name = aux.Name
	b.ToolName = aux.ToolName
	b.Input = aux.Input
	b.ID = aux.ID
	b.ToolUseID = aux.ToolUseID
	b.Content = aux.Content
	b.IsError = aux.IsError
	b.Source = aux.Source
	return nil
}
