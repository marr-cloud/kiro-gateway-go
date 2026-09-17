# Fase 6a — Extras (componentes Go) + limpiezas aparcadas — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Portar los componentes Go que quedaban de la fase 6 (`cache`, `modelresolver`, `debuglogger`, `debugmiddleware`, `mcptools`) y cerrar las deudas aparcadas de fases 3-5, dejando el binario funcionalmente completo salvo conformance/empaquetado/docs (que van en fase 6b).

**Architecture:** Cada componente es un port literal de su módulo Python homónimo (`kiro/*.py`, commit fijado `a5292ca`). `cache` y `modelresolver` sustituyen los subconjuntos duplicados que fases 3-5 dejaron en `convertersopenai`/`convertersanthropic`/`streaming*` (el `200000` hardcodeado y `getModelIDForKiro`+`HiddenModels`). `debuglogger`/`debugmiddleware` introducen el logging por-petición que fase 4/5 aplazaron. `mcptools` implementa web_search y se cablea en las rutas (Path A/B) que Task 10 dejó sin portar. Las limpiezas cierran deudas concretas ya ledgereadas.

**Tech Stack:** Go 1.27, `CGO_ENABLED=0`, `log/slog`, `net/http`, `encoding/json`. Sin dependencias externas nuevas (solo lo ya en `go.mod`).

**Spec:** `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md` (§6.12 modelresolver+cache, §6.13 debuglogger+debugmiddleware, §6.14 mcptools; §7.1 endpoints; §7.2 env vars). Upstream: `.upstream/kiro/{model_resolver,cache,debug_logger,mcp_tools}.py` + los sitios de truncation en `routes_*.py`/`streaming_*.py`.

## Global Constraints

- Go 1.27, `CGO_ENABLED=0`. Sin nuevas dependencias externas (solo `go.mod` actual).
- Cabecera SPDX de 2 líneas en todo `.go` nuevo (`// SPDX-License-Identifier: AGPL-3.0-or-later` / `// Port a Go de jwadow/kiro-gateway. Ver NOTICE.`).
- Todo fichero < 400 líneas (incluidos los de test — precedente Task 9).
- Sin globales mutables de paquete nuevas fuera del patrón ya aceptado de `converterscore` (config de módulo). `os.Getenv` solo en `config` (nunca en los paquetes nuevos: la config llega por parámetro/`*config.Config`).
- Contrato vinculante: upstream Python + spec + corpus > prosa del brief > prosa de review. Byte-parity en las fronteras de wire (§D1). Ledgear cada desviación.
- IDs derivados de contadores de sesión se comparan por FORMA (regex), no por valor (precedente Tasks 1/7/8): `srvtoolu_[0-9a-f]{32}`, `msg_[0-9a-f]{24}`, `web_search_tooluse_...`.
- `go build ./...`, `go vet ./...`, `gofmt -l .` limpios; `go test ./...` verde antes de cada commit.

---

## File Structure

- `internal/cache/cache.go` — `ModelInfoCache` (update, GetMaxInputTokens). Port de `cache.py`.
- `internal/modelresolver/{normalize.go,resolver.go,catalog.go}` — normalización, `ModelResolver.Resolve`, `GetModelIDForKiro`, catálogo `FallbackModels`/aliases/hidden. Port de `model_resolver.py`.
- `internal/debuglogger/debuglogger.go` — `DebugLogger` (3 modos, dumps, slog handler a buffer en context). Port de `debug_logger.py`.
- `internal/debugmiddleware/middleware.go` — middleware que prepara el logger antes de validar el cuerpo. Se monta en `internal/server`.
- `internal/mcptools/{client.go,websearch.go,ids.go,sse.go}` — `CallKiroMCPAPI`, `HandleNativeWebSearch`, generadores de id, SSE de web_search. Port de `mcp_tools.py`.
- Modificados: `internal/streamingopenai/formatter.go` + `internal/streaminganthropic/finish.go` (usar `cache.GetMaxInputTokens` en vez de `200000`), `internal/convertersopenai/converters.go` + `internal/convertersanthropic/converters.go` (usar `modelresolver` en vez del subconjunto duplicado), `internal/routesopenai/*` + `internal/routesanthropic/*` (web_search + truncation-recovery), `internal/converterscore/payload.go` (trim real), `internal/streamingcore/{events,pipeline}.go` (borrar `Usage` muerto), `internal/server/server.go` (montar debugmiddleware), `docs/MAPPING.md`.

---

## Task 1: `internal/cache` — ModelInfoCache

**Files:**
- Create: `internal/cache/cache.go`
- Create: `internal/cache/cache_test.go`
- Modify: `docs/MAPPING.md` (fila `cache.py`)

**Interfaces:**
- Consumes: nada (paquete hoja).
- Produces:
  ```go
  type ModelInfoCache struct { /* mu sync.RWMutex; maxInputTokens map[string]int; ... */ }
  func New() *ModelInfoCache
  func (c *ModelInfoCache) Update(modelsData []map[string]any)          // cache.py:65
  func (c *ModelInfoCache) GetMaxInputTokens(modelID string) int        // cache.py:129, fallback 200000
  ```

**Detalles del original (`.upstream/kiro/cache.py:36-160`):** cache dinámica protegida con lock; `get_max_input_tokens` devuelve el valor cacheado del modelo o el default `200000` si no está. `update` puebla desde la respuesta de `ListAvailableModels` (lista de dicts con `modelId`/límites).

**Test scenarios:**
- [ ] `GetMaxInputTokens("desconocido")` → `200000` (default).
- [ ] `Update([...])` con un modelo y su límite → `GetMaxInputTokens(ese)` devuelve el límite.
- [ ] Concurrencia: N goroutines Update + N Get sin race (`-race` opcional; el proyecto corre sin `-race` por defecto, así que test de dos fases como Task 3).

- [ ] Paso 1: Test hand-written (default + update + concurrencia). Paso 2: correr, fallar. Paso 3: implementar. Paso 4: correr, pasar. Paso 5: commit `feat(cache): ModelInfoCache with max-input-tokens lookup`.

---

## Task 2: `internal/modelresolver` — normalización y resolución

**Files:**
- Create: `internal/modelresolver/normalize.go` (normalize_model_name, to_runtime_model_id, extract_model_family)
- Create: `internal/modelresolver/catalog.go` (FallbackModels, Aliases, Hidden)
- Create: `internal/modelresolver/resolver.go` (ModelResolution, ModelResolver, Resolve, GetModelIDForKiro)
- Create: `internal/modelresolver/resolver_test.go`
- Modify: `docs/MAPPING.md` (fila `model_resolver.py`)

**Interfaces:**
- Consumes: `internal/cache` (para el descubrimiento dinámico; el resolver guarda modelos vistos).
- Produces:
  ```go
  type ModelResolution struct { RuntimeModelID string; Normalized string; Source string; /* ... */ }
  func NormalizeModelName(name string) string                          // model_resolver.py:87
  func ToRuntimeModelID(normalized string) string                      // model_resolver.py:51
  func ExtractModelFamily(name string) (string, bool)                  // model_resolver.py:222
  func GetModelIDForKiro(modelName string, hiddenModels map[string]string) string // model_resolver.py:192
  type ModelResolver struct { /* ... */ }
  func NewModelResolver(/* cache, endpointKind */) *ModelResolver
  func (r *ModelResolver) Resolve(externalModel string) ModelResolution // model_resolver.py:301
  var FallbackModels []string   // catálogo estático (§6.12)
  var Aliases map[string]string // {"auto-kiro":"auto"}
  var Hidden map[string]struct{} // {"auto"}
  ```

**Detalles del original (`.upstream/kiro/model_resolver.py:1-434`):** resolución en 4 capas (alias → normalización → caché dinámica → ocultos → passthrough). Normalización (§6.12): guiones→puntos, quitar fechas `YYYYMMDD`, quitar `-latest`, formato invertido (`claude-4.5-opus-high`→`claude-opus-4.5`), sufijos de ventana (`[1m]`,`[200k]`), formato antiguo (`claude-3-7-sonnet`→`claude-3.7-sonnet`). Catálogo `FALLBACK_MODELS` (§6.12, lista exacta de 13 modelos, en el original objetos `{"modelId":...}`). Alias `auto-kiro`→`auto`. Oculto en listado: `auto`. Descubrimiento dinámico: contra `runtime.*.kiro.dev` NO llama a `ListAvailableModels` (usa catálogo estático); contra `q.*.amazonaws.com` sí, con 3 reintentos + fallback.

**Test scenarios (corpus si existe en `testdata/model_resolver/`, si no hand-written derivados del original):**
- [ ] `NormalizeModelName` para cada regla: `claude-sonnet-4-20250101`→`claude.sonnet.4`... (verificar contra el original ejecutado, como Task 1 hizo con `diagnoseJSONTruncation`).
- [ ] `claude-4.5-opus-high` (invertido) → `claude-opus-4.5`.
- [ ] `claude-3-7-sonnet` (antiguo) → `claude-3.7-sonnet`.
- [ ] `[1m]`/`[200k]` se eliminan.
- [ ] `Resolve("auto-kiro")` → alias a `auto`.
- [ ] `GetModelIDForKiro` reproduce lo que hoy hace el subconjunto duplicado en converters (comparar salida byte a byte para los modelos del catálogo).

- [ ] Paso 1-5 (TDD). Commit `feat(modelresolver): four-layer model resolution and normalization`.

---

## Task 3: cablear `cache` + `modelresolver` en converters/streaming/rutas (elimina duplicación + HIDDEN_MODELS)

**Files:**
- Modify: `internal/convertersopenai/converters.go` (usar `modelresolver.GetModelIDForKiro`, borrar `HiddenModels`/`getModelIDForKiro` locales)
- Modify: `internal/convertersanthropic/converters.go` (idem)
- Modify: `internal/streamingopenai/formatter.go:333-339` (usar `cache.GetMaxInputTokens(model)` en vez de `const maxInputTokens = 200000`)
- Modify: `internal/streaminganthropic/finish.go` (`ContextCorrectedInputTokens`: idem)
- Modify: `internal/accountmanager/init.go` (aplicar HIDDEN_MODELS a `GetAllAvailableModels` — quita el `TODO(fase-5)` stale) O en el listado de `/v1/models` de `routesopenai` (elegir el sitio que reproduzca §6.11 del spec; ledgear cuál)
- Test: ampliar los tests existentes de esos paquetes.

**Interfaces:**
- Consumes: `modelresolver` (Task 2), `cache` (Task 1).
- Produces: nada nuevo; sustituye llamadas internas.

**Detalles:** El `getModelIDForKiro(req.Model, HiddenModels)` de `convertersopenai.BuildKiroPayload` y su gemelo en `convertersanthropic.AnthropicToKiro` pasan a `modelresolver.GetModelIDForKiro`. El `200000` hardcodeado (con `TODO(fase-6)`) de ambos formatters pasa a `cache.GetMaxInputTokens(model)` — requiere que el formatter/ruta reciba el `*cache.ModelInfoCache` (inyección vía constructor o vía la cuenta activa, que ya tiene `model_cache` en el original; `accountmanager.Account` debería exponerlo). HIDDEN_MODELS (§6.11): los modelos ocultos NO aparecen en `/v1/models` — resuelve el `TODO(fase-5): apply HIDDEN_MODELS` de `accountmanager/init.go` (F1 del preflight de fase 5).

**Test scenarios:**
- [ ] `/v1/models` NO lista los modelos de HIDDEN_MODELS (test en `routesopenai` o `accountmanager`).
- [ ] Los formatters siguen pasando su corpus (39/40 openai, etc.) con `cache.GetMaxInputTokens` devolviendo 200000 por defecto (equivalente al hardcode anterior mientras la cache esté vacía).
- [ ] Converters siguen pasando su corpus con `modelresolver.GetModelIDForKiro` (byte-idéntico al subconjunto que sustituye).

- [ ] Paso 1-5 (TDD). Commit `refactor(converters,streaming): use modelresolver+cache, apply HIDDEN_MODELS`.

---

## Task 4: `internal/debuglogger` — DebugLogger de 3 modos

**Files:**
- Create: `internal/debuglogger/debuglogger.go`
- Create: `internal/debuglogger/debuglogger_test.go`
- Modify: `docs/MAPPING.md` (fila `debug_logger.py`)

**Interfaces:**
- Consumes: `internal/config` (DEBUG_MODE, DEBUG_DIR).
- Produces:
  ```go
  type Mode string // "off" | "errors" | "all"
  type DebugLogger struct { /* ... */ }
  func New(mode Mode, dir string) *DebugLogger
  func (d *DebugLogger) LogRequestBody(body []byte)       // debug_logger.py:156
  func (d *DebugLogger) LogKiroRequestBody(body []byte)   // :172
  func (d *DebugLogger) LogRawChunk(chunk []byte)         // :188
  func (d *DebugLogger) LogModifiedChunk(chunk []byte)    // :204
  func (d *DebugLogger) LogErrorInfo(statusCode int, msg string) // :220
  func (d *DebugLogger) FlushOnError(statusCode int, msg string) // :251 (escribe a DEBUG_DIR)
  func (d *DebugLogger) DiscardBuffers()                  // :318
  // slog.Handler que escribe el formato del original a un buffer en context:
  // "{YYYY-MM-DD HH:mm:ss.SSS} | {LEVEL:<8} | {origen}:{función}:{línea} | {mensaje}"
  ```

**Detalles del original (`.upstream/kiro/debug_logger.py:45-330`):** 3 modos — `off` no hace nada; `errors` acumula en memoria y solo vuelca a disco si la petición falla (`FlushOnError`); `all` escribe siempre. Vuelca a `DEBUG_DIR`: cuerpo de petición, payload a Kiro, chunks crudos, chunks modificados, logs de app. El handler `slog` replica el formato del original (§6.13).

**Test scenarios:**
- [ ] Modo `off`: ningún fichero escrito tras Flush.
- [ ] Modo `errors`: nada en éxito (DiscardBuffers), ficheros en `FlushOnError`.
- [ ] Modo `all`: ficheros siempre.
- [ ] El `slog.Handler` produce una línea con el formato exacto (regex sobre timestamp + `LEVEL:<8` + origen).
- [ ] Aislamiento: usar `t.TempDir()` como DEBUG_DIR; ninguna prueba escribe fuera.

- [ ] Paso 1-5 (TDD). Commit `feat(debuglogger): three-mode per-request debug logger`.

---

## Task 5: `internal/debugmiddleware` + montaje en server

**Files:**
- Create: `internal/debugmiddleware/middleware.go`
- Create: `internal/debugmiddleware/middleware_test.go`
- Modify: `internal/server/server.go` (montar el middleware alrededor de `/v1/chat/completions` y `/v1/messages`, DENTRO del recover pero preparando el logger ANTES de auth/validación)
- Modify: `docs/MAPPING.md`

**Interfaces:**
- Consumes: `internal/debuglogger` (Task 4), `internal/config`.
- Produces:
  ```go
  func New(cfg *config.Config) func(http.Handler) http.Handler
  // Inyecta un *debuglogger.DebugLogger en el context de la petición (clave no exportada);
  // los handlers/formatters lo recuperan con debugmiddleware.FromContext(ctx).
  func FromContext(ctx context.Context) *debuglogger.DebugLogger
  ```

**Detalles del original (§6.13):** el middleware envuelve las dos rutas de generación y prepara el logger **antes** de validar el cuerpo, para que un 422 también quede registrado. En el orden de middleware del server (CORS→auth→recover→mux), el debug logger se prepara para esas dos rutas; decidir y ledgear el punto exacto (probablemente un wrapper por-ruta dentro del mux, no global, para no tocar `/`,`/health`,`/v1/models`).

**Test scenarios:**
- [ ] Petición a `/v1/chat/completions` con `DEBUG_MODE=all` → hay un `*DebugLogger` en el context del handler.
- [ ] Un cuerpo inválido (422) con modo `all` igualmente prepara el logger (antes de validación).
- [ ] `/health` no prepara logger (fuera del scope del middleware).

- [ ] Paso 1-5 (TDD). Commit `feat(debugmiddleware): per-request debug logger wiring`.

---

## Task 6: `internal/mcptools` — cliente MCP + web_search

**Files:**
- Create: `internal/mcptools/ids.go` (generate_random_id + los 3 patrones de id)
- Create: `internal/mcptools/client.go` (CallKiroMCPAPI: POST `{host}/mcp`, JSON-RPC 2.0, timeout 60s, doble deserialización)
- Create: `internal/mcptools/summary.go` (generate_search_summary: troceo en 100 chars, envoltura `<web_search>`)
- Create: `internal/mcptools/websearch.go` (extract_query_from_messages, handle_native_web_search)
- Create: `internal/mcptools/sse.go` (generate_anthropic_web_search_sse, generate_openai_web_search_sse)
- Create: `internal/mcptools/mcptools_test.go`
- Modify: `docs/MAPPING.md` (fila `mcp_tools.py`)

**Interfaces:**
- Consumes: `internal/utils` (TokenProvider/headers para el POST autenticado), `internal/httpclient` o `net/http`, `internal/sse`.
- Produces:
  ```go
  func CallKiroMCPAPI(ctx context.Context, query string, tp utils.TokenProvider) (toolUseID string, results map[string]any, err error) // mcp_tools.py:77
  func GenerateSearchSummary(query string, results map[string]any) string // :205
  func ExtractQueryFromMessages(messages []json.RawMessage, apiFormat string) (string, bool) // :534
  func HandleNativeWebSearch(ctx context.Context, ... , apiFormat string) (/* SSE bytes o handler */) // :590
  // IDs (comparados por forma en tests): srvtoolu_<32hex>, msg_<24hex>,
  // web_search_tooluse_<22>_<timestampMs>_<8>
  ```

**Detalles del original (`.upstream/kiro/mcp_tools.py:1-753`, §6.14):** `CallKiroMCPAPI` POST JSON-RPC (`{"method":"tools/call","params":{"name":"web_search",...}}`), timeout 60s; la respuesta trae `result.content[0].text` como cadena con JSON dentro (deserializar dos veces). Path A = tools nativas de servidor con `type` que empieza por `web_search`; Path B = inyección automática si `WEB_SEARCH_ENABLED`. Resumen troceado en 100 chars, envuelto en `<web_search>...</web_search>`. IDs con patrones exactos.

**Test scenarios (fake Kiro MCP con `httptest`, ninguna red real):**
- [ ] `CallKiroMCPAPI` contra fake que devuelve `result.content[0].text` con JSON → doble-deserializa correctamente; `tool_use_id` matchea `srvtoolu_[0-9a-f]{32}`.
- [ ] `GenerateSearchSummary` trocea en 100 chars y envuelve en `<web_search>`.
- [ ] `ExtractQueryFromMessages` para formato OpenAI y Anthropic.
- [ ] IDs matchean sus patrones (comparación por forma).

- [ ] Paso 1-5 (TDD). Commit `feat(mcptools): Kiro MCP web_search client and SSE`.

---

## Task 7: cablear web_search en las rutas (Path A/B — deferral de Task 10)

**Files:**
- Modify: `internal/routesanthropic/failover.go` (Path A: early return si una tool tiene `type` que empieza por `web_search`, ANTES del bucle de failover — `routes_anthropic.py:262-310`; Path B: inyección si `WEB_SEARCH_ENABLED` — `:280`)
- Modify: `internal/routesopenai/*` (el equivalente OpenAI de web_search si el original lo tiene en `routes_openai.py`; verificar y portar o ledgear ausencia)
- Modify: `internal/routesanthropic/handler.go` (quitar el punto 5 de la cabecera "web_search no portado" una vez portado)
- Test: escenarios en `handler_test.go` de ambas rutas.

**Interfaces:**
- Consumes: `internal/mcptools` (Task 6), `internal/config` (WebSearchEnabled).
- Produces: nada nuevo.

**Detalles:** Task 8 y Task 10 dejaron web_search Path A/B sin portar (llamada MCP de red real, fuera del alcance del formatter/ruta en su momento). Ahora que `mcptools` existe, se cablea: Path A intercepta y responde con el SSE de web_search vía `mcptools.HandleNativeWebSearch`; Path B inyecta la tool `web_search` en el payload cuando `WEB_SEARCH_ENABLED` está activo (`routes_anthropic.py:262-310`).

**Test scenarios (fake Kiro MCP):**
- [ ] `/v1/messages` con una tool `web_search_20250305` → Path A: respuesta SSE de web_search (no pasa por el failover normal).
- [ ] `WEB_SEARCH_ENABLED=true` + petición sin tool web_search → Path B: la tool se inyecta.
- [ ] `WEB_SEARCH_ENABLED=false` sin tool nativa → comportamiento normal (sin web_search).

- [ ] Paso 1-5 (TDD). Commit `feat(routes): wire web_search Path A/B via mcptools`.

---

## Task 8: cablear recuperación de truncación end-to-end (deferral Task 8→10)

**Files:**
- Modify: `internal/routesanthropic/*` + `internal/routesopenai/*` (tras cerrar el stream, si `truncationrecovery.ShouldInjectRecovery(converterscore.TruncationRecoveryEnabled)`: persistir con `truncationstate.SaveToolTruncation`/`SaveContentTruncation` — `streaming_anthropic.py:665-686`, y el equivalente OpenAI si existe)
- Modify: el sitio de construcción de payload (`convertersopenai`/`convertersanthropic` o las rutas) para LEER `truncationstate` en la SIGUIENTE petición e inyectar los mensajes de recovery (`truncationrecovery.GenerateTruncationToolResult`/`GenerateTruncationUserMessage`) — localizar el sitio de inyección en el original.
- Test: escenarios end-to-end (una petición trunca → persiste; la siguiente inyecta).

**Interfaces:**
- Consumes: `truncationstate` (Task 3 fase 5), `truncationrecovery` (Task 4 fase 5), `converterscore.TruncationRecoveryEnabled`.
- Produces: nada nuevo (cierra el efecto secundario documentado en `docs/MAPPING.md` fila `streaming_anthropic.py`).

**Detalles:** El save está documentado como DIFERIDO en la fila `streaming_anthropic.py` de `docs/MAPPING.md` (default-on, `should_inject_recovery()` devuelve `TRUNCATION_RECOVERY`=true por defecto). El READ/inject (siguiente petición) también hay que localizarlo y portarlo. **Antes de implementar, el implementador debe leer los sitios exactos del original** (`grep -n "save_tool_truncation\|save_content_truncation\|get_tool_truncation\|get_content_truncation\|generate_truncation" .upstream/kiro/*.py`) y ledgear el mapa save↔inject. Si el inject-side resulta más grande de lo previsto, partir en 8a (persistencia) + 8b (inyección).

**Test scenarios:**
- [ ] Un stream que termina truncado con `TRUNCATION_RECOVERY=true` persiste en `truncationstate`.
- [ ] La siguiente petición con el mismo contenido/tool inyecta el mensaje de recovery.
- [ ] Con `TRUNCATION_RECOVERY=false`: no persiste, no inyecta.

- [ ] Paso 1-5 (TDD). Commit `feat(routes): wire truncation-recovery persistence and injection`.

---

## Task 9: implementar el recorte real de payload (AUTO_TRIM_PAYLOAD)

**Files:**
- Modify: `internal/converterscore/payload.go:371-376` (sustituir el stub `if AutoTrimPayload { _ = KiroMaxPayloadBytes }` por el recorte real)
- Test: `internal/converterscore/payload_test.go` (o el fichero de test existente).

**Interfaces:**
- Consumes: `converterscore.AutoTrimPayload`, `converterscore.KiroMaxPayloadBytes` (ya cableados desde `cfg` en `main.go`).
- Produces: nada nuevo.

**Detalles:** El original recorta el payload cuando supera `KIRO_MAX_PAYLOAD_BYTES` y `AUTO_TRIM_PAYLOAD` está activo (localizar la lógica exacta en `.upstream/kiro/payload_guards.py` o donde el original la tenga — el spec §5.4 la menciona como `payloadguards.Check/Trim`, plegado en `converterscore` por ruling de fase 3). El implementador lee el original y porta el algoritmo de recorte (qué se recorta primero, cómo se mide el tamaño). **Verificar que el corpus de converters sigue pasando** (el recorte solo actúa sobre payloads que superan el umbral; ningún fixture del corpus debería superarlo con el default 600000).

**Test scenarios:**
- [ ] Payload bajo el umbral: sin cambios.
- [ ] Payload sobre el umbral con `AutoTrimPayload=true`: recortado según el algoritmo del original.
- [ ] Payload sobre el umbral con `AutoTrimPayload=false`: sin cambios (solo se recorta con el flag).

- [ ] Paso 1-5 (TDD). Commit `feat(converterscore): real AUTO_TRIM_PAYLOAD trimming`.

---

## Task 10: limpiezas aparcadas (código muerto, tamaño de fichero, doc drift)

**Files:**
- Modify: `internal/streamingcore/events.go` + `internal/streamingcore/pipeline.go` (borrar `Usage *UsageData`, `UsageData` y `extractUsageData` — código muerto confirmado por la review whole-branch de fase 5; ambos formatters usan solo `UsageRaw`)
- Modify: `internal/streamingcore/pipeline_test.go` (quitar/ajustar cualquier assert sobre `Usage`)
- Modify: `internal/streamingopenai/formatter_test.go` (428 líneas → split; mover helpers a `internal/streamingopenai/helpers_test.go` para dejar ambos < 400)
- Modify: `docs/MAPPING.md` fila `truncation_recovery.py` (corregir la descripción del viejo package-var `truncationRecoveryEnabled` → ahora `ShouldInjectRecovery(enabled bool)`; doc drift señalado en el ledger de fase 5)

**Interfaces:**
- Consumes/Produces: nada (limpieza interna).

**Detalles:** Cada ítem está ledgereado en `.superpowers/sdd/2026-09-16-fase-5-streaming-y-rutas/progress.md` como parked/observación. El borrado de `Usage` es seguro (0 consumidores, verificado por la review whole-branch). El split de `formatter_test.go` sigue el precedente Task 9 (helpers → `helpers_test.go`).

**Test scenarios:**
- [ ] `go build ./...` + `go test ./...` verde tras borrar `Usage` (0 consumidores → no rompe nada).
- [ ] `wc -l` de todo `.go` tocado < 400.
- [ ] `grep -rn "UsageData\|extractUsageData" internal/streamingcore` → 0 tras el borrado.

- [ ] Paso 1-5 (TDD). Commit `chore(streamingcore,streamingopenai): drop dead Usage field, split oversized test, fix MAPPING drift`.

---

## Self-Review

**1. Cobertura de spec (§6.12-6.14 + deudas):**
- §6.12 modelresolver+cache → Tasks 1+2+3 ✓
- §6.13 debuglogger+debugmiddleware → Tasks 4+5 ✓
- §6.14 mcptools → Task 6, cableado en Task 7 ✓
- HIDDEN_MODELS (F1 preflight fase 5) → Task 3 ✓
- Logging (F2 preflight fase 5) → Tasks 4+5 ✓
- web_search Path A/B (deferral Task 8/10) → Task 7 ✓
- truncation-recovery persistencia+inyección (deferral Task 8→10) → Task 8 ✓
- AUTO_TRIM_PAYLOAD (stub fase 3) → Task 9 ✓
- `Usage` muerto / formatter_test.go 428 / MAPPING drift (parked fase 5) → Task 10 ✓
- NO en 6a (van en 6b): conformance de doble binario, Taskfile/CI/Docker/release, README ES/EN, DIFFERENCES.md.

**2. Placeholders:** este plan usa el estilo de los planes de fase 1-5 del port (refs a upstream + interfaces + comportamientos + escenarios de test + pasos TDD), no transcripción literal de Go — el implementador traduce del upstream fijado, igual que en cada fase previa. Los "Detalles del original" citan fichero+líneas exactas. No hay "TBD"/"implementar luego" sin ancla.

**3. Consistencia de tipos:** `cache.ModelInfoCache`/`GetMaxInputTokens` (Task 1) se consume igual en Tasks 2 y 3. `modelresolver.GetModelIDForKiro(model, hidden)` (Task 2) coincide con la firma que Task 3 sustituye. `debuglogger.DebugLogger` (Task 4) es lo que `debugmiddleware.FromContext` (Task 5) devuelve. `mcptools.HandleNativeWebSearch` (Task 6) es lo que Task 7 invoca. `truncationstate`/`truncationrecovery` (Task 8) ya existen de fase 5 con las firmas ledgereadas.

**Riesgos:** Task 8 (truncation end-to-end) es el más incierto — el sitio de inyección no está confirmado; el implementador lo mapea primero y parte en 8a/8b si excede. Task 7 (web_search) depende de que el original OpenAI tenga o no web_search (verificar). Task 3 requiere exponer `*cache.ModelInfoCache` desde `accountmanager.Account` (como el original) — confirmar el seam.
