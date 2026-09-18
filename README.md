# 👻 kiro-gateway-go

**Español** • [🇬🇧 English](README.en.md)

[![Licencia: AGPL v3](https://img.shields.io/badge/Licencia-AGPL%20v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8.svg)](https://go.dev/)
[![Port de](https://img.shields.io/badge/port%20de-jwadow%2Fkiro--gateway-black.svg)](https://github.com/jwadow/kiro-gateway)

Port a Go de [kiro-gateway](https://github.com/jwadow/kiro-gateway) como **binario único**, sin
dependencias de runtime.

Un proxy local que expone las APIs de **OpenAI** y **Anthropic** y traduce las peticiones a la
API de **Kiro** (Amazon Q Developer / AWS CodeWhisperer), de forma que cualquier cliente
compatible con esas APIs —Claude Code, Cursor, Cline, Roo Code, el SDK de OpenAI, LangChain,
Continue, etc.— pueda usar los modelos de Kiro.

> **Objetivo del port:** paridad byte a byte con el original en las fronteras de red (los bytes
> SSE/JSON que ven los clientes). Los conversores, el tokenizer, el streaming y los formatters se
> validan contra un corpus golden grabado del upstream fijado (commit `a5292ca`, v2.4.dev.13).
> Ver [docs/CORPUS.md](docs/CORPUS.md) y [docs/DIFFERENCES.md](docs/DIFFERENCES.md).

## Índice

- [Modelos](#modelos) • [Características](#características) • [Inicio rápido](#inicio-rápido)
- [Configuración](#configuración) • [Referencia de API](#referencia-de-api) • [Ejemplos](#ejemplos-de-uso)
- [Depuración](#depuración) • [Diferencias con el original](#diferencias-con-el-original) • [Licencia](#licencia-y-atribución)

---

## Modelos

La disponibilidad de modelos depende de tu plan de Kiro (gratuito o de pago): el gateway da
acceso a los modelos disponibles en tu IDE o CLI según tu suscripción. Consulta la lista real en
tiempo de ejecución con `GET /v1/models`.

Modelos habituales en el plan gratuito: **Claude Sonnet 4.5**, **Claude Haiku 4.5**,
**Claude Sonnet 4**, y varios modelos abiertos (GLM, DeepSeek, MiniMax, Qwen).

> 💡 **Resolución flexible de nombres:** puedes usar cualquier formato —`claude-sonnet-4-5`,
> `claude-sonnet-4.5` o nombres versionados como `claude-sonnet-4-5-20250929`—; el gateway los
> normaliza automáticamente.

---

## Características

| Característica | Descripción |
|---|---|
| 🔌 **API compatible con OpenAI** | `/v1/chat/completions` para cualquier herramienta compatible |
| 🔌 **API compatible con Anthropic** | Endpoint nativo `/v1/messages` |
| 🔀 **Multi-cuenta con failover** | Conmutación automática entre varias cuentas de Kiro |
| 🌐 **Soporte VPN/proxy** | Proxy HTTP/SOCKS5 para redes restringidas |
| 🧠 **Extended thinking** | Razonamiento (reasoning) en respuestas |
| 👁️ **Visión** | Envío de imágenes al modelo |
| 🔍 **Búsqueda web** | Herramienta `web_search` vía MCP de Kiro |
| 🛠️ **Tool calling** | Function calling en ambos dialectos |
| 💬 **Historial completo** | Se pasa todo el contexto de la conversación |
| 📡 **Streaming** | SSE completo en ambos dialectos |
| 🔄 **Reintentos** | Reintentos automáticos ante errores (403, 429, 5xx) |
| 🔐 **Gestión de tokens** | Refresco automático antes de expirar |
| 🧮 **Conteo de tokens fiel** | tiktoken `cl100k_base` embebido; `/v1/messages/count_tokens` |

---

## Inicio rápido

### Requisitos

- **Go 1.27+** (solo para compilar; el binario resultante no necesita nada instalado).
- Una de estas fuentes de credenciales de Kiro:
  - [Kiro IDE](https://kiro.dev/) con la sesión iniciada, o
  - [Kiro CLI](https://kiro.dev/cli/) con AWS SSO (Builder ID gratuito o cuenta corporativa).

### Compilar y ejecutar

```bash
# Clona el repositorio
git clone https://github.com/marr-cloud/kiro-gateway-go.git
cd kiro-gateway-go

# Compila el binario (usa Taskfile; equivale a `go build ./cmd/kiro-gateway`)
task build
# o directamente:
go build -o kiro-gateway ./cmd/kiro-gateway

# Configura las credenciales: un credentials.json con tus cuentas y un .env con
# PROXY_API_KEY (ver sección Configuración)

# Arranca el servidor
./kiro-gateway

# Con host/puerto personalizados (si el 8000 está ocupado)
./kiro-gateway --port 9000
```

El servidor queda disponible en `http://localhost:8000`.

### Con Docker

```bash
# Prepara credentials.json + un .env con PROXY_API_KEY (ver Configuración)
docker compose up -d
docker compose logs -f
curl http://localhost:8000/health
```

La imagen es multi-stage sobre `distroless/static:nonroot` (~27 MB, sin shell, usuario no-root) y
su healthcheck usa el propio binario (`--health`). Ver [`Dockerfile`](Dockerfile) y
[`docker-compose.yml`](docker-compose.yml).

> Los binarios de release cross-platform están planificados pero **aún no disponibles**; por ahora
> se compila desde el código o se usa Docker.

### Flags de línea de comandos

| Flag | Abreviatura | Descripción |
|---|---|---|
| `--host` | `-H` | Interfaz de escucha (por defecto `SERVER_HOST` o `0.0.0.0`) |
| `--port` | `-p` | Puerto de escucha (por defecto `SERVER_PORT` o `8000`) |
| `--version` | `-v` | Imprime la versión y sale |
| `--help` | `-h` | Imprime la ayuda y sale |
| `--health` | | Consulta `GET /health` del servidor en marcha y sale con 0 (sano) o 1 |

---

## Configuración

Dos piezas: **`credentials.json`** define las cuentas de Kiro; el **`.env`** guarda la clave del
proxy y los ajustes de comportamiento. Protege **siempre** tu proxy con `PROXY_API_KEY`: es la
clave que usarán los clientes al conectarse.

### Cuentas: `credentials.json`

El gateway carga las cuentas de un array JSON. Por defecto busca `credentials.json` en el
directorio de trabajo; puedes apuntar a otra ruta con `ACCOUNTS_CONFIG_FILE`. Cada entrada lleva un
`type` (`json`, `sqlite` o `refresh_token`), `enabled: true`, y la ruta o el token correspondiente.
Ver [`credentials.json.example`](credentials.json.example).

```json
[
  { "type": "json",   "enabled": true, "path": "C:/Users/tu-usuario/.aws/sso/cache/kiro-auth-token.json" },
  { "type": "sqlite", "enabled": true, "path": "C:/Users/tu-usuario/AppData/Local/kiro-cli/data.sqlite3" }
]
```

- `json` — token de Kiro IDE / Enterprise (válido si contiene `refreshToken` o `clientId`).
- `sqlite` — base de datos de kiro-cli (válida si tiene la tabla `auth_kv`).
- `refresh_token` — un token de refresco directo (`"refresh_token": "eyJ..."`, opcional `profile_arn`).

Con varias entradas el gateway hace **failover** automático: cuando una devuelve un error (429, 402)
pasa a la siguiente; si una falla varias veces seguidas la aparta y la reintenta periódicamente. Con
una sola cuenta no hay conmutación (se devuelve el error real de Kiro).

> El port **no** implementa el modo de "cuenta única" del original (`REFRESH_TOKEN` /
> `KIRO_CREDS_FILE` / `KIRO_CLI_DB_FILE` sueltos en el `.env` como fuente de cuenta): las cuentas se
> definen siempre en `credentials.json`. Ver [docs/DIFFERENCES.md](docs/DIFFERENCES.md).

### `.env`

```env
# Contraseña para proteger TU proxy (inventa una cadena segura)
PROXY_API_KEY="mi-contraseña-super-secreta-123"

# Opcional: ruta al fichero de cuentas (por defecto: credentials.json en el cwd)
ACCOUNTS_CONFIG_FILE=C:/Users/tu-usuario/kiro/kiro-gateway/credentials.json
```

### VPN / proxy

Para redes restringidas o problemas de conectividad con AWS:

```env
VPN_PROXY_URL=http://127.0.0.1:7890     # HTTP
# VPN_PROXY_URL=socks5://127.0.0.1:1080 # SOCKS5
```

### Otras variables útiles

| Variable | Por defecto | Descripción |
|---|---|---|
| `ACCOUNTS_CONFIG_FILE` | `credentials.json` | Ruta al array de cuentas |
| `SERVER_HOST` / `SERVER_PORT` | `0.0.0.0` / `8000` | Interfaz y puerto de escucha |
| `KIRO_REGION` / `KIRO_API_REGION` | `us-east-1` | Región de OIDC / de la API de Kiro |
| `WEB_SEARCH_ENABLED` | `true` | Habilita la herramienta de búsqueda web |
| `TRUNCATION_RECOVERY` | `true` | Recuperación ante respuestas truncadas |
| `DEBUG_MODE` | `off` | Modo de log de depuración (`off` / `errors` / `all`) |

---

## Referencia de API

| Endpoint | Método | Descripción |
|---|---|---|
| `/` | GET | Estado básico (JSON) |
| `/health` | GET | Estado detallado (JSON) |
| `/v1/models` | GET | Lista de modelos disponibles |
| `/v1/chat/completions` | POST | API Chat Completions de OpenAI |
| `/v1/messages` | POST | API Messages de Anthropic |
| `/v1/messages/count_tokens` | POST | Conteo de tokens (Anthropic) |

Autenticación: `Authorization: Bearer <PROXY_API_KEY>` (dialecto OpenAI) o
`x-api-key: <PROXY_API_KEY>` (dialecto Anthropic).

---

## Ejemplos de uso

### OpenAI (cURL)

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Authorization: Bearer mi-contraseña-super-secreta-123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-5",
    "messages": [{"role": "user", "content": "¡Hola!"}],
    "stream": true
  }'
```

### Anthropic (cURL)

```bash
curl http://localhost:8000/v1/messages \
  -H "x-api-key: mi-contraseña-super-secreta-123" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "¡Hola!"}]
  }'
```

### SDK de OpenAI (Python)

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8000/v1",
    api_key="mi-contraseña-super-secreta-123",  # tu PROXY_API_KEY
)

response = client.chat.completions.create(
    model="claude-sonnet-4-5",
    messages=[{"role": "user", "content": "¡Hola!"}],
    stream=True,
)
for chunk in response:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

---

## Depuración

El log de depuración está **desactivado por defecto**. Para activarlo:

```env
# off:    desactivado (por defecto)
# errors: guarda logs solo de peticiones fallidas (4xx, 5xx) — recomendado
# all:    guarda logs de todas las peticiones (se sobrescriben en cada una)
DEBUG_MODE=errors
```

Los ficheros se escriben en `debug_logs/` (`request_body.json`, `kiro_request_body.json`,
`response_stream_raw.txt`, `response_stream_modified.txt`, etc.).

---

## Diferencias con el original

Este port replica el comportamiento del upstream en las fronteras de red, con desviaciones
deliberadas y acotadas (p. ej. no expone `/docs`, `/redoc` ni `/openapi.json` de FastAPI; los
endpoints `/` y `/health` devuelven JSON con campos extra; añade el flag `--health`). La lista
completa y el porqué de cada una está en **[docs/DIFFERENCES.md](docs/DIFFERENCES.md)**.

## Documentación

- [Diseño del port](docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md)
- [El corpus golden: generación, formato y garantías](docs/CORPUS.md)
- [Correspondencia de módulos](docs/MAPPING.md)
- [Diferencias con el original](docs/DIFFERENCES.md)

## Licencia y atribución

**AGPL-3.0.** Este proyecto es obra derivada de
[`jwadow/kiro-gateway`](https://github.com/jwadow/kiro-gateway) (v2.4.dev.13, commit `a5292ca`),
distribuido bajo la misma licencia. Ver [LICENSE](LICENSE) y [NOTICE](NOTICE) para la atribución
completa y la lista de cambios. Si este proyecto te resulta útil, considera apoyar el
[proyecto original](https://github.com/jwadow/kiro-gateway#-support-the-project).

## Aviso

Este proyecto no está afiliado ni respaldado por AWS, Anthropic ni Kiro IDE. Úsalo bajo tu propia
responsabilidad y respetando los términos de servicio de las APIs subyacentes.
