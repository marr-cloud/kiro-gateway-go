// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package modelresolver

// fallbackModelEntry refleja la forma de una entrada de FALLBACK_MODELS
// (.upstream/kiro/config.py:276-290): el original es una lista de objetos
// {"modelId": "..."}, NO de strings sueltas — VALID_RUNTIME_MODEL_IDS
// (model_resolver.py:48) se deriva de ese mismo catálogo con
// `{model["modelId"] for model in FALLBACK_MODELS}`. Se replica aquí la
// forma de objeto como fuente de verdad, y FallbackModels (abajo) expone
// solo los ids, que es lo único que este paquete necesita — el propio
// VALID_RUNTIME_MODEL_IDS del original no se usa en ningún otro punto de
// model_resolver.py ni del resto de .upstream/kiro (verificado por grep), así
// que no se expone un equivalente Go: sería código muerto sin llamador.
type fallbackModelEntry struct {
	ModelID string
}

// fallbackModelCatalog es el catálogo literal de 13 modelos de
// config.py:276-290, usado cuando /ListAvailableModels no está disponible
// (fallo DNS/red). Es la fuente de verdad de FallbackModels.
var fallbackModelCatalog = []fallbackModelEntry{
	{ModelID: "auto"},
	{ModelID: "claude-sonnet-4"},
	{ModelID: "claude-sonnet-4.5"},
	{ModelID: "claude-sonnet-4.6"},
	{ModelID: "claude-haiku-4.5"},
	{ModelID: "claude-opus-4.5"},
	{ModelID: "claude-opus-4.6"},
	{ModelID: "claude-opus-4.7"},
	{ModelID: "deepseek-3.2"},
	{ModelID: "glm-5"},
	{ModelID: "minimax-m2.1"},
	{ModelID: "minimax-m2.5"},
	{ModelID: "qwen3-coder-next"},
}

// FallbackModels es la vista de solo-ids de fallbackModelCatalog, en el
// mismo orden que config.py:276-290. Mismo catálogo que
// internal/accountmanager.fallbackModels (duplicación temporal documentada
// en docs/MAPPING.md: ese paquete lo copió literalmente porque este paquete
// todavía no existía; una tarea futura puede sustituirlo por
// modelresolver.FallbackModels).
var FallbackModels = fallbackModelIDs()

func fallbackModelIDs() []string {
	ids := make([]string, len(fallbackModelCatalog))
	for i, m := range fallbackModelCatalog {
		ids[i] = m.ModelID
	}
	return ids
}

// HiddenModels es el catálogo de modelos ocultos (§6.12, config.py:219,
// HIDDEN_MODELS): un mapa display-name→internal-id de modelos que Kiro no
// devuelve en /ListAvailableModels pero que siguen funcionando y se AÑADEN al
// listado /v1/models. Vacío por defecto (el único ejemplo del original está
// comentado). Es la fuente única que sustituye los `HiddenModels` locales
// duplicados de convertersopenai/convertersanthropic (dedup pendiente, ver
// ledger fase 6a Task 3).
var HiddenModels = map[string]string{}

// Aliases es el catálogo de alias de nombre de modelo (§6.12,
// config.py:249-251, MODEL_ALIASES): nombres personalizados que mapean a
// IDs de modelo reales. El default de "auto-kiro"->"auto" evita el
// conflicto con el modelo "auto" propio de Cursor IDE.
var Aliases = map[string]string{
	"auto-kiro": "auto",
}

// HiddenFromList es el catálogo de modelos ocultos del listado /v1/models
// (§6.12, config.py:263, HIDDEN_FROM_LIST): siguen funcionando si se piden
// directamente, pero no aparecen en la lista — así solo se muestra el alias
// "auto-kiro", evitando confusión con "auto".
var HiddenFromList = []string{"auto"}
