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
| `converters_core.py` | `internal/converterscore` | `types.go`, `extract.go`, `text.go`, `tools.go`, `images.go`, `normalize.go` |
| `converters_openai.py` | `internal/convertersopenai` | |
| `converters_anthropic.py` | `internal/convertersanthropic` | |
| `streaming_core.py` | `internal/streamingcore` | |
| `streaming_openai.py` | `internal/streamingopenai` | |
| `streaming_anthropic.py` | `internal/streaminganthropic` | |
| `routes_openai.py` | `internal/routesopenai` | |
| `routes_anthropic.py` | `internal/routesanthropic` | |
| `http_client.py` | `internal/httpclient` | |
| `auth.py` | `internal/auth` | |
| `account_manager.py` | `internal/accountmanager` | |
| `mcp_tools.py` | `internal/mcptools` | |
| `debug_logger.py` | `internal/debuglogger` | |
| `debug_middleware.py` | `internal/debugmiddleware` | |
| `__init__.py` | *(sin equivalente: solo reexporta)* | |

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
