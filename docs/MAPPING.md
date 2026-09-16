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
| `parsers.py` | `internal/parsers` | |
| `thinking_parser.py` | `internal/thinkingparser` | |
| `truncation_state.py` | `internal/truncationstate` | |
| `truncation_recovery.py` | `internal/truncationrecovery` | |
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
| `streaming_core.py` | `internal/streamingcore` | |
| `streaming_openai.py` | `internal/streamingopenai` | |
| `streaming_anthropic.py` | `internal/streaminganthropic` | |
| `routes_openai.py` | `internal/routesopenai` | |
| `routes_anthropic.py` | `internal/routesanthropic` | |
| `http_client.py` | `internal/httpclient` | `transport.go`, `proxy.go`, `client.go`, `stream.go` |
| `auth.py` | `internal/auth` | `types.go`, `manager.go`, `json_source.go`, `oidc.go` (parcial — Task 3 añade el refresco AWS SSO OIDC completo, normal y Enterprise `clientIdHash`: `refreshAWSSSO`/`doAWSSSORefreshAttempt` port de `auth.py:743-869`, `loadEnterpriseDeviceRegistration` port de `auth.py:458-487`. `refreshLocked` ya rutea `AuthTypeAWSSSO` a `refreshAWSSSO`; la rama Kiro Desktop/RefreshOnly sigue en `ErrRefreshNotImplemented`, la fuente SQLite de kiro-cli (`loadFromSQLite`, hoy un stub no-op) y el envoltorio `singleflight` llegan en las Tasks 4-5) |
| `account_manager.py` | `internal/accountmanager` | |
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
| `internal/sse` | Formateo de eventos SSE | Rompe el ciclo `mcp_tools` ↔ `streaming_anthropic` |
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
