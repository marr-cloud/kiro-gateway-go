# Correspondencia entre el original Python y este port

Origen: `jwadow/kiro-gateway`, v2.4.dev.13, commit `a5292ca`.

Regla: el nombre del paquete Go es el del módulo Python sin guiones bajos. La columna
`Ficheros Go` se rellena a medida que cada fase implementa su paquete.

| Módulo Python | Paquete Go | Ficheros Go |
|---|---|---|
| `main.py` | `cmd/kiro-gateway` + `internal/server` | `cmd/kiro-gateway/main.go` |
| `config.py` | `internal/config` | `config.go`, `parse.go`, `dotenv.go` |
| `utils.py` | `internal/utils` | `fingerprint.go`, `ids.go`, `headers.go` |
| `tokenizer.py` | `internal/tokenizer` | |
| `parsers.py` | `internal/parsers` | `parser.go`, `toolcalls.go`, `brace_scanner.go`, `dedup.go`, `truncation.go` (Task 1 porta find_matching_brace, deduplicate_tool_calls, parse_bracket_tool_calls y el state machine AwsEventStreamParser.feed/get_tool_calls/reset — ports de `parsers.py:39-569`. `Event.Kind` usa los 3 tipos que `_process_event` realmente emite (`content`/`usage`/`context_usage`, .upstream/kiro/parsers.py:308-332), no los 7 nombres de prefijo: `tool_start`/`tool_input`/`tool_stop` nunca producen evento en el original — solo mutan estado — y `followupPrompt` como prefijo más temprano no tiene rama en absoluto (cae al `return None` final). `Finish()` y el Kind `tool_call` son adiciones Go-only sin equivalente upstream (el original expone tool calls solo vía `get_tool_calls()`, que también se porta 1:1 como `Parser.GetToolCalls()`). Los ids de `parse_bracket_tool_calls` dependen de un contador de sesión no reproducible fuera de la suite completa de grabación (docs/CORPUS.md §"contadores globales de sesión"); el test de corpus compara forma (`call_<8 hex>`), no valor, igual que ya hace el port para chatcmpl-/msg_-hex. Test diferencial `bytes.ToValidUTF8` vs `decode('utf-8', errors='ignore')` (spec §6.4/§8.4, pendiente desde fase 2) añadido en `differential_test.go`, 1000 semillas fijas, verificado contra python3 real.) |
| `thinking_parser.py` | `internal/thinkingparser` | `state_machine.go`, `parser_test.go` (Task 2 porta el FSM de tres estados (PreContent/InThinking/Streaming) que detecta 4 tags de razonamiento (`<thinking>`, `<think>`, `<reasoning>`, `<thought>`) al inicio del stream. Implementa buffering prudente con `max_tag_length = max(len(tag)) * 2` para evitar partir tags entre chunks. Feed/Finish devuelven `(thinking, content)` en lugar del `ThinkingParseResult` completo del original, y aplican el handling mode internamente en lugar de dejar `process_for_output` como método separado.) |
| `truncation_state.py` | `internal/truncationstate` | `cache.go`, `cache_test.go` (Task 3 porta los dos `dict` protegidos con `threading.Lock` como caches de `any` con protección `sync.Mutex`, índice de contenido con SHA256 hash del primer 500 caracteres, Get destructivo — `map.pop(key)` ↔ `delete(map, key)` — retorna `false` si no existe. Tests hand-written: SetTool + GetTool retorna y elimina, SetContent + GetContent con SHA256, concurrencia con dos fases secuenciales: 10 goroutines para set, 10 para get.) |
| `truncation_recovery.py` | `internal/truncationrecovery` | `recovery.go`, `recovery_test.go` (Task 4 porta `generate_truncation_tool_result(tool_name, tool_use_id, truncation_info) -> dict` y `generate_truncation_user_message() -> string` y `should_inject_recovery() -> bool` del original. Implementa un package var `truncationRecoveryEnabled` que espeja la bandera de converterscore; los tests pueden sobrescribir localmente. 17 fixtures de corpus (13 para tool_result, 2 para user_message, 2 para should_inject, con algunos casos usando kwargs en lugar de args, y TRUNCATION_RECOVERY tanto true como false en config). Tests manejan ambas formas de input (args vs kwargs) y leen/aplican la config dinámicamente.) |
| `payload_guards.py` | `internal/payloadguards` | |
| `cache.py` | `internal/cache` | |
| `model_resolver.py` | `internal/modelresolver` | |
| `kiro_errors.py` | `internal/kiroerrors` | `enhance.go` |
| `network_errors.py` | `internal/networkerrors` | `types.go`, `classify.go`, `messages.go` |
| `account_errors.py` | `internal/accounterrors` | `classify.go` |
| `exceptions.py` | `internal/validationerrors` | `validation.go` |
| `models_openai.py` | `internal/modelsopenai` | `models.go` |
| `models_anthropic.py` | `internal/modelsanthropic` | `blocks.go`, `models.go` |
| `converters_core.py` | `internal/converterscore` | `types.go`, `extract.go`, `text.go`, `tools.go`, `images.go`, `normalize.go`, `thinking.go`, `payload.go` |
| `converters_openai.py` | `internal/convertersopenai` | `converters.go` |
| `converters_anthropic.py` | `internal/convertersanthropic` | `converters.go` |
| `streaming_core.py` | `internal/streamingcore` | `events.go`, `pipeline.go`, `pipeline_test.go` (Task 6 porta la clase KiroEvent y el pipeline que une parsers.Parser (Task 1) y thinkingparser.Parser (Task 2) en un stream de eventos unificado. El pipeline procesa eventos del parser y aplica thinking splitting: para cada evento "content", extrae el valor, lo pasa al thinking parser, y emite eventos "thinking" (si hay) y "content" (si hay) en ese orden. Maneja también "usage" → UsageData, "context_usage" → float, "tool_call" → ToolUseData. Si hay diagnóstico de truncamiento, emite un evento "error" con el mensaje del diagnóstico. Feed() procesa un chunk, Finish() vacía buffers pendientes.) |
| `streaming_openai.py` | `internal/streamingopenai` | `formatter.go`, `formatter_test.go` (Task 7 porta el formateador OpenAI SSE que convierte eventos KiroEvent en chunks chat.completion. Formatter struct mantiene estado: completionID (chatcmpl-<32hex>), model, created (timestamp unix), firstChunk (para agregar "role": "assistant"), acumuladores de content/thinking, tool calls, metering data (usage + credits_used), context_usage_percentage. Handle() procesa cada evento: content/thinking emiten chunks SSE, tool_use/usage/context_usage actualizan estado. Finish() emite tool calls (si hay), calcula completion_tokens via tokenizer, determina finish_reason (truncation vs tool_calls vs stop) basado en presencia de completion signals, emite final chunk con usage, emite [DONE]. Los primeros 17 fixtures pasan; los restantes requieren refinamiento en truncation detection y token counting (algunos off-by-one). Configurable thinking handling mode: AsReasoningContent vs AsContent para "reasoning_content" vs "content" delta key.) |
| `streaming_anthropic.py` | `internal/streaminganthropic` | |
| `routes_openai.py` | `internal/routesopenai` | |
| `routes_anthropic.py` | `internal/routesanthropic` | |
| `http_client.py` | `internal/httpclient` | `transport.go`, `proxy.go`, `client.go`, `stream.go` |
| `auth.py` | `internal/auth` | `types.go`, `manager.go`, `json_source.go`, `oidc.go`, `sqlite.go`, `refresh.go` (Task 1-2 porta `types.go`, `manager.go` (constructores + `AccessToken`/`ProfileARN`, stub de `ForceRefresh`), `json_source.go`, stubs de `oidc.go`/`sqlite.go`. Task 3 completa `oidc.go` con `refreshAWSSSO`/`doAWSSSORefreshAttempt` (ports de `auth.py:743-869`) y `loadEnterpriseDeviceRegistration` (port de `auth.py:458-487`). Task 4 completa `sqlite.go` (`loadFromSQLite`/`saveToSQLite` — ports de `auth.py:248-382` y `auth.py:388-633`). Task 5 implanta `refresh.go` con `Refresh()` singleflight (port de `auth.py:870-934` `get_access_token`), graceful degradation (SQLite+400, `auth.py:906-919`), y reestructura `AccessToken`/`ForceRefresh`) |
| `account_manager.py` | `internal/accountmanager` | `types.go`, `discovery.go`, `state.go`, `manager.go`, `manager_test.go`, `breaker.go`, `selection.go`, `failover.go`, `failover_test.go`, `init.go`, `init_test.go`, `integration_test.go` (Task 6 porta `types.go` (Account, AccountStats, ModelAccountList), `discovery.go` (loadCredentials, processRefreshTokenEntry, processFileEntry, scanDirectory, processFile, isValidJSONFile, isValidSQLiteFile — ports de `account_manager.py:127-326`), `state.go` (LoadState, SaveState, renameWithRetry — ports de `account_manager.py:328-416`), `manager.go` (Manager struct, NewManager, LoadCredentials, SaveStatePeriodically, hasStateChanged, Accounts — ports de `account_manager.py:179-214` y las rutas del state loop). Task 7 implanta circuit breaker (breaker.go — quarantineWindow, isInQuarantine), sticky selection (selection.go — nextEnabledIdx), failover (failover.go — GetNextAccount, ReportSuccess, ReportFailure — todos ports de `account_manager.py:645-867`). Task 8 implanta model catalog (init.go — Initialize, refreshAccountModels, GetAllAvailableModels, GetFirstAccount, fallbackModels — ports de `account_manager.py:432-644, 868-897` y config.py:276-290). Task 9 implanta integration test (`integration_test.go` — TestPhase4Integration que verifica wiring de Manager + auth.Manager + httpclient.Client contra fake Kiro server: 200 success, 429 retry, 500 failover).) |
| `mcp_tools.py` | `internal/mcptools` | |
| `debug_logger.py` | `internal/debuglogger` | |
| `debug_middleware.py` | `internal/debugmiddleware` | |
| `__init__.py` | *(sin equivalente: solo reexporta)* | |

## Duplicación temporal: `model_resolver.py`

`internal/convertersanthropic/converters.go` (Task 9) porta, como funciones NO
exportadas (`normalizeModelName`, `getModelIDForKiro`) más el package var
`HiddenModels`, el subconjunto mínimo de `model_resolver.py` que
`anthropic_to_kiro` necesita para calcular el `modelId` que manda a Kiro
(`normalize_model_name` + `get_model_id_for_kiro`, sin la clase
`ModelResolver` completa, sin caché dinámica ni alias — el propio original
describe `get_model_id_for_kiro` como "a simple helper for converters that
don't have access to the full ModelResolver"). Se hizo así porque
`internal/modelresolver` (fila de arriba) todavía no existe y Task 9 no puede
crear paquetes fuera de `internal/convertersanthropic/*`. Cuando una tarea
futura implemente `internal/modelresolver`, debería sustituir ese subconjunto
en `converters.go` por una llamada real a ese paquete en vez de mantener dos
copias del mismo algoritmo.

`internal/convertersopenai/converters.go` (Task 10) repite exactamente la
misma duplicación, por la misma razón y con la misma restricción de alcance
(Task 10 solo puede tocar `internal/convertersopenai/*` y este fichero):
`build_kiro_payload` del original también llama a
`get_model_id_for_kiro(request_data.model, HIDDEN_MODELS)`. En vez de
importar `convertersanthropic` (sus dos funciones no están exportadas, y
acoplar un adaptador a los internals de otro no reduce el radio de impacto),
este paquete lleva su propia copia de `normalizeModelName` +
`getModelIDForKiro` + `HiddenModels`. Cuando `internal/modelresolver` exista,
la tarea que lo cree debería sustituir las TRES copias (converterscore no
tiene una propia — solo los dos adaptadores) por una llamada real a ese
paquete.

## Paquetes que no existen en el original

| Paquete Go | Responsabilidad | Por qué existe |
|---|---|---|
| `internal/sse` | Formateo de eventos SSE (Task 5) | Rompe el ciclo `mcp_tools` ↔ `streaming_anthropic` (FormatEvent con dialecto OpenAI/Anthropic, FormatDone OpenAI-only) |
| `internal/pyjson` | Emula `json.dumps` y `str()` de Python | Aísla las rarezas de Python en un solo sitio |
| `internal/testutil` | Cargador del corpus y generadores de chunks | Equivalente de los helpers de `conftest.py` |
| `internal/server` | Router, middleware y ciclo de vida | Equivalente del `lifespan` de FastAPI |
| `internal/version` | Cadena de versión | En Python es una constante en `config.py` |

## Renombrados

| Python | Go | Motivo |
|---|---|---|
| `exceptions.py` | `internal/validationerrors` | «exceptions» no significa nada en Go |

## Ciclos de importación rotos

| Ciclo en Python | Cómo se rompe |
|---|---|
| `utils` ↔ `auth` | `utils` declara la interfaz `TokenProvider` y `auth.Manager` la satisface |
| `mcp_tools` ↔ `streaming_anthropic` | El formateo de SSE se extrae a `internal/sse` |

## Extensiones deliberadas respecto al original

| Dónde | Qué | Por qué |
|---|---|---|
| `internal/auth.AuthType` | 5 valores (`Unknown`, `KiroDesktop`, `AWSSSO`, `KiroCLI`, `RefreshOnly`) en vez de los 2 del `Enum` original (`KIRO_DESKTOP`, `AWS_SSO_OIDC`, `auth.py:68-83`) | Reflejan los tres `type` que ya distingue `credentials.json` (`json`, `sqlite`, `refresh_token`, spec §6.11) en vez de que kiro-cli y refresh-token-only colapsen en `KIRO_DESKTOP` como hoy hace el Python |
| `internal/auth` (JSON source) | La detección de región por ARN (`auth.py:357-366`, solo en el loader de SQLite en el original) se generaliza como fallback también para el JSON de Kiro Desktop | Pedido explícitamente por el plan de la Task 2 (4 niveles de precedencia de región, incluido "detectado del ARN") y reutilizable sin cambios por la Task 4 (SQLite) |
