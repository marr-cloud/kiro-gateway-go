// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package modelresolver

import (
	"sort"

	"github.com/marr-cloud/kiro-gateway-go/internal/cache"
)

// ModelResolution es el resultado de resolver un nombre de modelo externo.
// Port de la dataclass ModelResolution (.upstream/kiro/model_resolver.py:68-84),
// con una desviación deliberada de forma: el original tiene un quinto campo,
// original_request (lo que mandó el cliente); se omite aquí porque el
// llamador ya tiene ese valor — es el mismo string que pasó a Resolve — y
// añadirlo solo duplicaría un dato que el propio caller posee. Firma exacta
// tomada del brief de la tarea, verificada contra los tres campos que sí
// hacen falta para el pipeline de conversión (Task 3): el id a mandar a
// Kiro, la forma normalizada (para mensajes de error/logging) y si la
// resolución fue verificada o es un passthrough optimista.
type ModelResolution struct {
	// RuntimeModelID es el id a mandar a la API de Kiro (internal_id del
	// original).
	RuntimeModelID string
	// Normalized es el nombre tras NormalizeModelName.
	Normalized string
	// Source indica la capa que resolvió el modelo: "cache", "hidden" o
	// "passthrough".
	Source string
	// IsVerified es true si el modelo se encontró en caché o en
	// hiddenModels; false si es un passthrough optimista sin verificar.
	IsVerified bool
}

// ModelResolver resuelve nombres de modelo externos a IDs internos de Kiro,
// con normalización y passthrough optimista. Port de la clase ModelResolver
// (.upstream/kiro/model_resolver.py:248-397).
//
// Principio clave (heredado del module docstring, model_resolver.py:29):
// somos un gateway, no un gatekeeper — Kiro es el árbitro final de qué
// modelos existen, así que Resolve nunca falla.
type ModelResolver struct {
	cache          *cache.ModelInfoCache
	hiddenModels   map[string]string
	aliases        map[string]string
	hiddenFromList map[string]struct{}
}

// NewModelResolver crea un ModelResolver. Port del constructor
// (.upstream/kiro/model_resolver.py:277-299): hiddenModels/aliases/
// hiddenFromList nil se tratan como vacíos, igual que los `Optional[...] =
// None` del original que caen a `{}`/`set()`.
func NewModelResolver(c *cache.ModelInfoCache, hiddenModels, aliases map[string]string, hiddenFromList []string) *ModelResolver {
	// Copia defensiva de los mapas del llamador: los catálogos que se pasan
	// aquí suelen ser los `var` de paquete (Aliases, etc.), y guardarlos por
	// referencia dejaría que una mutación posterior de esos globales alterase
	// resolvers ya construidos. Rangear un mapa nil da 0 iteraciones, así que
	// no hace falta el chequeo de nil.
	hiddenCopy := make(map[string]string, len(hiddenModels))
	for k, v := range hiddenModels {
		hiddenCopy[k] = v
	}
	aliasCopy := make(map[string]string, len(aliases))
	for k, v := range aliases {
		aliasCopy[k] = v
	}
	hiddenSet := make(map[string]struct{}, len(hiddenFromList))
	for _, id := range hiddenFromList {
		hiddenSet[id] = struct{}{}
	}
	return &ModelResolver{
		cache:          c,
		hiddenModels:   hiddenCopy,
		aliases:        aliasCopy,
		hiddenFromList: hiddenSet,
	}
}

// Resolve resuelve external_model a un ModelResolution. Port literal de
// ModelResolver.resolve (.upstream/kiro/model_resolver.py:301-368).
//
// Nunca falla — un modelo no encontrado en caché ni en hiddenModels se
// pasa tal cual (Source="passthrough", IsVerified=false) para que Kiro
// decida.
//
// Nota sobre las "4 capas": el docstring del módulo (model_resolver.py:23-27)
// las numera 1-4 (normalizar, caché, ocultos, passthrough) y el docstring de
// la clase las numera 0-4 añadiendo el alias como capa 0
// (model_resolver.py:255-260); en ambos casos hay UNA sola comprobación de
// caché, sobre el nombre YA normalizado — no hay una comprobación de caché
// separada sobre el nombre crudo antes de normalizar. Se implementa tal
// cual el cuerpo real del método (líneas 315-368), que es la autoridad
// vinculante sobre cualquier paráfrasis.
func (r *ModelResolver) Resolve(externalModel string) ModelResolution {
	// Capa 0: resolver alias.
	resolvedModel, ok := r.aliases[externalModel]
	if !ok {
		resolvedModel = externalModel
	}

	// Capa 1: normalizar (guiones→puntos, quitar fechas/latest/ventana).
	normalized := NormalizeModelName(resolvedModel)

	// Capa 2: caché dinámica (/ListAvailableModels).
	if r.cache.IsValidModel(normalized) {
		return ModelResolution{
			RuntimeModelID: ToRuntimeModelID(normalized),
			Normalized:     normalized,
			Source:         "cache",
			IsVerified:     true,
		}
	}

	// Capa 3: modelos ocultos (config manual).
	if internalID, hidden := r.hiddenModels[normalized]; hidden {
		return ModelResolution{
			RuntimeModelID: ToRuntimeModelID(internalID),
			Normalized:     normalized,
			Source:         "hidden",
			IsVerified:     true,
		}
	}

	// Capa 4: passthrough — deja que Kiro decida.
	return ModelResolution{
		RuntimeModelID: ToRuntimeModelID(normalized),
		Normalized:     normalized,
		Source:         "passthrough",
		IsVerified:     false,
	}
}

// GetAvailableModels devuelve la lista de IDs de modelo disponibles para el
// endpoint /v1/models. Port literal de ModelResolver.get_available_models
// (.upstream/kiro/model_resolver.py:370-397): unión de la caché dinámica y
// los nombres de modelos ocultos, MENOS hiddenFromList, MÁS los nombres de
// alias (en ese orden — un alias cuyo nombre coincidiera con
// hiddenFromList sobreviviría, igual que el original) — y ordenados, como
// hace `sorted(models)` sobre el set resultante.
func (r *ModelResolver) GetAvailableModels() []string {
	models := make(map[string]struct{})
	for _, id := range r.cache.GetAllModelIDs() {
		models[id] = struct{}{}
	}
	for displayName := range r.hiddenModels {
		models[displayName] = struct{}{}
	}
	for id := range r.hiddenFromList {
		delete(models, id)
	}
	for alias := range r.aliases {
		models[alias] = struct{}{}
	}

	result := make([]string, 0, len(models))
	for id := range models {
		result = append(result, id)
	}
	sort.Strings(result)
	return result
}

// GetModelIDForKiro resuelve el model ID a mandar a la API de Kiro. Port
// literal de kiro.model_resolver.get_model_id_for_kiro
// (.upstream/kiro/model_resolver.py:192-219): helper independiente para
// llamadores (como los converters) que no tienen acceso a un
// *ModelResolver completo — normaliza y comprueba hiddenModels, sin caché
// dinámica ni alias.
func GetModelIDForKiro(modelName string, hiddenModels map[string]string) string {
	normalized := NormalizeModelName(modelName)
	internal, ok := hiddenModels[normalized]
	if !ok {
		internal = normalized
	}
	return ToRuntimeModelID(internal)
}
