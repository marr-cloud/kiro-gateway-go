// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package cache implementa el cache de metadatos de modelos (ModelInfoCache),
// port de .upstream/kiro/cache.py. Es un paquete hoja: no depende de ningún
// otro paquete interno del proyecto.
package cache

import (
	"fmt"
	"sync"
	"time"
)

// DefaultMaxInputTokens es el límite de tokens de entrada usado cuando un
// modelo no tiene tokenLimits.maxInputTokens (o es "falsy": ausente, nil o
// cero), igual que DEFAULT_MAX_INPUT_TOKENS en .upstream/kiro/config.py:300.
const DefaultMaxInputTokens = 200000

// ModelInfoCache es un cache thread-safe de metadatos de modelos, poblado de
// forma perezosa (lazy loading): los datos solo se cargan en el primer
// acceso o cuando el cache está stale. Espeja ModelInfoCache de cache.py:36.
//
// El original usa asyncio.Lock (mutex cooperativo de un solo hilo); este
// port usa sync.RWMutex para permitir lecturas concurrentes reales.
type ModelInfoCache struct {
	mu sync.RWMutex

	// cache mapea modelId -> dict de metadatos del modelo. Los valores son
	// map[string]any deliberadamente (no se tipan los dicts de modelo:
	// cache.py:77 los acepta tal cual vienen de ListAvailableModels o de
	// AddHiddenModel).
	cache map[string]map[string]any

	// lastUpdate es el momento de la última llamada a Update. Cero
	// (time.Time{}) significa "nunca actualizado", equivalente al
	// _last_update = None inicial de cache.py:62.
	lastUpdate time.Time

	// ttl es la vida útil del cache (cache_ttl del original, cache.py:63).
	ttl time.Duration
}

// New crea un ModelInfoCache vacío con el TTL indicado en segundos.
// El original toma cache_ttl con default MODEL_CACHE_TTL (config.py:297,
// 3600s); este port no lee configuración por sí mismo (sin os.Getenv en este
// paquete), así que el llamador debe pasar el TTL resuelto.
func New(ttlSeconds int) *ModelInfoCache {
	return &ModelInfoCache{
		cache: make(map[string]map[string]any),
		ttl:   time.Duration(ttlSeconds) * time.Second,
	}
}

// Update reemplaza el contenido completo del cache con modelsData, indexado
// por la clave "modelId" de cada dict, y marca lastUpdate a ahora. Port de
// cache.py:65-78.
//
// Si un elemento de modelsData no tiene "modelId" (o no es string), se
// almacena bajo la clave vacía "" en vez de fallar; el original asume que
// siempre está presente (accede con model["modelId"], que lanzaría KeyError
// si faltara) y esta desviación es deliberada: un paquete hoja de Go no debe
// entrar en pánico por datos de entrada malformados.
func (c *ModelInfoCache) Update(modelsData []map[string]any) {
	newCache := make(map[string]map[string]any, len(modelsData))
	for _, model := range modelsData {
		id, _ := model["modelId"].(string)
		newCache[id] = model
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = newCache
	c.lastUpdate = time.Now()
}

// Get devuelve los metadatos del modelo indicado y true si existe en el
// cache, o nil y false si no. Port de cache.py:80-90.
func (c *ModelInfoCache) Get(modelID string) (map[string]any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	model, ok := c.cache[modelID]
	return model, ok
}

// IsValidModel indica si modelID existe en el cache dinámico. Usado por
// ModelResolver (Task 2) para verificar disponibilidad. Port de
// cache.py:92-104.
func (c *ModelInfoCache) IsValidModel(modelID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.cache[modelID]
	return ok
}

// AddHiddenModel añade un modelo oculto al cache: modelos que /ListAvailableModels
// de Kiro no devuelve pero que siguen siendo funcionales, para que aparezcan
// en /v1/models. Es un no-op si displayName ya existe. Port de
// cache.py:106-127, replicando la forma exacta del dict.
func (c *ModelInfoCache) AddHiddenModel(displayName, internalID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.cache[displayName]; exists {
		return
	}

	c.cache[displayName] = map[string]any{
		"modelId":      displayName,
		"modelName":    displayName,
		"description":  fmt.Sprintf("Hidden model (internal: %s)", internalID),
		"tokenLimits":  map[string]any{"maxInputTokens": DefaultMaxInputTokens},
		"_internal_id": internalID,
		"_is_hidden":   true,
	}
}

// GetMaxInputTokens devuelve tokenLimits.maxInputTokens para modelID, o
// DefaultMaxInputTokens si el modelo no existe, no tiene tokenLimits, o el
// valor de maxInputTokens es "falsy" (ausente, nil o cero) — mismo chequeo
// que `model["tokenLimits"].get("maxInputTokens") or DEFAULT_MAX_INPUT_TOKENS`
// en cache.py:139-142.
func (c *ModelInfoCache) GetMaxInputTokens(modelID string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	model, ok := c.cache[modelID]
	if !ok {
		return DefaultMaxInputTokens
	}

	tokenLimits, ok := model["tokenLimits"].(map[string]any)
	if !ok {
		return DefaultMaxInputTokens
	}

	if n, truthy := asTruthyInt(tokenLimits["maxInputTokens"]); truthy {
		return n
	}
	return DefaultMaxInputTokens
}

// IsEmpty indica si el cache no contiene ningún modelo. Port de
// cache.py:144-151.
func (c *ModelInfoCache) IsEmpty() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cache) == 0
}

// IsStale indica si el cache nunca se actualizó, o si han pasado más de ttl
// segundos desde la última actualización. Port de cache.py:153-163.
func (c *ModelInfoCache) IsStale() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if c.lastUpdate.IsZero() {
		return true
	}
	return time.Since(c.lastUpdate) > c.ttl
}

// asTruthyInt intenta interpretar v como un entero "truthy" al estilo
// Python: presente, numérico y distinto de cero. Soporta los tipos
// numéricos que puede traer un map[string]any poblado a mano o desde JSON
// (int, int64 y float64).
func asTruthyInt(v any) (n int, truthy bool) {
	switch x := v.(type) {
	case int:
		n = x
	case int64:
		n = int(x)
	case float64:
		n = int(x)
	default:
		return 0, false
	}
	return n, n != 0
}
