// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package modelresolver implementa la resolución de nombres de modelo de
// Kiro Gateway: normalización de formatos de cliente heterogéneos al formato
// canónico de Kiro, y la resolución en capas (alias → normalizar → caché
// dinámica → modelos ocultos → passthrough). Port de
// .upstream/kiro/model_resolver.py.
//
// Principio clave del original (model_resolver.py:29): "We are a gateway,
// not a gatekeeper. Kiro API is the final arbiter" — de ahí que Resolve
// nunca falle: un modelo desconocido se pasa tal cual (passthrough) y es
// Kiro quien decide si existe.
package modelresolver

import (
	"regexp"
	"strings"
)

// Los cinco patrones de kiro.model_resolver.normalize_model_name
// (.upstream/kiro/model_resolver.py:141,151,159,170,180), probados en orden;
// el primero que hace match decide el resultado. Mismos patrones ya
// verificados contra corpus en internal/convertersanthropic y
// internal/convertersopenai (duplicación temporal documentada en
// docs/MAPPING.md, "Duplicación temporal: model_resolver.py") — aquí se
// verifican de nuevo, independientemente, contra los 40 casos de
// testdata/model_resolver/normalize_model_name.
var (
	// contextWindowSuffixPattern quita el sufijo de ventana de contexto
	// (p.ej. "[1m]", "[200k]"): indicador de cliente, no parte del model ID.
	// model_resolver.py:132.
	contextWindowSuffixPattern = regexp.MustCompile(`(?i)\[\d+[mk]\]$`)

	// standardModelPattern: claude-{family}-{major}-{minor}(-{fecha|latest|n})?
	// model_resolver.py:141.
	standardModelPattern = regexp.MustCompile(`^(claude-(?:haiku|sonnet|opus)-\d+)-(\d{1,2})(?:-(?:\d{8}|latest|\d+))?$`)

	// noMinorModelPattern: claude-{family}-{major}(-{fecha de 8 dígitos})?
	// model_resolver.py:151.
	noMinorModelPattern = regexp.MustCompile(`^(claude-(?:haiku|sonnet|opus)-\d+)(?:-\d{8})?$`)

	// legacyModelPattern: claude-{major}-{minor}-{family}(-{fecha|latest|n})?
	// model_resolver.py:159.
	legacyModelPattern = regexp.MustCompile(`^(claude)-(\d+)-(\d+)-(haiku|sonnet|opus)(?:-(?:\d{8}|latest|\d+))?$`)

	// dotWithDateModelPattern: ya normalizado con punto, pero con fecha de 8
	// dígitos colgando. model_resolver.py:170.
	dotWithDateModelPattern = regexp.MustCompile(`^(claude-(?:\d+\.\d+-)?(?:haiku|sonnet|opus)(?:-\d+\.\d+)?)-\d{8}$`)

	// invertedWithSuffixPattern: formato invertido claude-{major}.{minor}-{family}-{sufijo}.
	// Requiere sufijo para no matchear formatos ya normalizados como
	// claude-3.7-sonnet. model_resolver.py:180.
	invertedWithSuffixPattern = regexp.MustCompile(`^claude-(\d+)\.(\d+)-(haiku|sonnet|opus)-(.+)$`)

	// modelFamilyPattern busca "haiku"/"sonnet"/"opus" en cualquier posición
	// del nombre, sin anclar. model_resolver.py:242.
	modelFamilyPattern = regexp.MustCompile(`(?i)(haiku|sonnet|opus)`)
)

// NormalizeModelName normaliza un nombre de modelo externo al formato que
// espera Kiro. Port literal de kiro.model_resolver.normalize_model_name
// (.upstream/kiro/model_resolver.py:87-190): nombre vacío se devuelve tal
// cual, se quita el sufijo de ventana de contexto, se prueban los cinco
// patrones en orden sobre la versión en minúsculas, y sin match se devuelve
// el nombre (con mayúsculas originales, sin la ventana de contexto) tal
// cual — pass-through.
func NormalizeModelName(name string) string {
	if name == "" {
		return name
	}

	name = contextWindowSuffixPattern.ReplaceAllString(name, "")
	nameLower := strings.ToLower(name)

	if m := standardModelPattern.FindStringSubmatch(nameLower); m != nil {
		return m[1] + "." + m[2]
	}
	if m := noMinorModelPattern.FindStringSubmatch(nameLower); m != nil {
		return m[1]
	}
	if m := legacyModelPattern.FindStringSubmatch(nameLower); m != nil {
		return m[1] + "-" + m[2] + "." + m[3] + "-" + m[4]
	}
	if m := dotWithDateModelPattern.FindStringSubmatch(nameLower); m != nil {
		return m[1]
	}
	if m := invertedWithSuffixPattern.FindStringSubmatch(nameLower); m != nil {
		return "claude-" + m[3] + "-" + m[1] + "." + m[2]
	}

	return name
}

// ToRuntimeModelID es un pass-through deliberado para el model ID de
// runtime.kiro.dev. Port literal de kiro.model_resolver.to_runtime_model_id
// (.upstream/kiro/model_resolver.py:51-65): antes hacía fallback a "auto"
// para modelos desconocidos, pero eso violaba el principio "gateway, not
// gatekeeper" — ahora devuelve el modelo tal cual y deja que la API de Kiro
// decida si existe.
func ToRuntimeModelID(normalized string) string {
	return normalized
}

// ExtractModelFamily extrae la familia de modelo Claude de un nombre. Port
// de kiro.model_resolver.extract_model_family
// (.upstream/kiro/model_resolver.py:222-245): busca "haiku"/"sonnet"/"opus"
// en cualquier posición (sin anclar, case-insensitive) y devuelve el primer
// match en minúsculas. El Optional[str] del original se traduce al idiom Go
// (string, bool): ok=false cuando no hay match, equivalente a None.
func ExtractModelFamily(name string) (string, bool) {
	m := modelFamilyPattern.FindStringSubmatch(name)
	if m == nil {
		return "", false
	}
	return strings.ToLower(m[1]), true
}
