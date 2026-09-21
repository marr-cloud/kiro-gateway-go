# 👻 kiro-gateway-go

[🇪🇸 Español](README.md) • **English**

[![License: AGPL v3](https://img.shields.io/badge/License-AGPL%20v3-blue.svg)](https://www.gnu.org/licenses/agpl-3.0)
[![Go 1.27](https://img.shields.io/badge/Go-1.27-00ADD8.svg)](https://go.dev/)
[![port of](https://img.shields.io/badge/port%20of-jwadow%2Fkiro--gateway-black.svg)](https://github.com/jwadow/kiro-gateway)

A Go port of [kiro-gateway](https://github.com/jwadow/kiro-gateway) as a **single binary**, with
no runtime dependencies.

A local proxy that exposes the **OpenAI** and **Anthropic** APIs and translates requests to the
**Kiro** API (Amazon Q Developer / AWS CodeWhisperer), so that any tool compatible with those
APIs — Claude Code, Cursor, Cline, Roo Code, the OpenAI SDK, LangChain, Continue, and so on — can
use Kiro's models.

> **Port goal:** byte-for-byte parity with the original at the network boundaries (the SSE/JSON
> bytes clients see). Converters, the tokenizer, streaming and the formatters are validated
> against a golden corpus recorded from the pinned upstream (commit `a5292ca`, v2.4.dev.13). See
> [docs/CORPUS.md](docs/CORPUS.md) and [docs/DIFFERENCES.md](docs/DIFFERENCES.md).

## Contents

- [Models](#models) • [Features](#features) • [Quick start](#quick-start)
- [Configuration](#configuration) • [API reference](#api-reference) • [Examples](#usage-examples)
- [Debugging](#debugging) • [Differences from the original](#differences-from-the-original) • [License](#license-and-attribution)

---

## Models

Model availability depends on your Kiro tier (free or paid): the gateway grants access to whatever
models are available in your IDE or CLI based on your subscription. Query the live list at runtime
with `GET /v1/models`.

Models commonly available on the free tier: **Claude Sonnet 4.5**, **Claude Haiku 4.5**,
**Claude Sonnet 4**, and several open models (GLM, DeepSeek, MiniMax, Qwen).

> 💡 **Smart model resolution:** use any name format — `claude-sonnet-4-5`, `claude-sonnet-4.5`,
> or versioned names like `claude-sonnet-4-5-20250929`; the gateway normalizes them automatically.

---

## Features

| Feature | Description |
|---|---|
| 🔌 **OpenAI-compatible API** | `/v1/chat/completions` for any compatible tool |
| 🔌 **Anthropic-compatible API** | Native `/v1/messages` endpoint |
| 🔀 **Multi-account failover** | Automatic switching between multiple Kiro accounts |
| 🌐 **VPN/proxy support** | HTTP/SOCKS5 proxy for restricted networks |
| 🧠 **Extended thinking** | Reasoning in responses |
| 👁️ **Vision** | Send images to the model |
| 🔍 **Web search** | `web_search` tool via Kiro's MCP |
| 🛠️ **Tool calling** | Function calling in both dialects |
| 💬 **Full history** | The complete conversation context is passed through |
| 📡 **Streaming** | Full SSE in both dialects |
| 🔄 **Retries** | Automatic retries on errors (403, 429, 5xx) |
| 🔐 **Token management** | Automatic refresh before expiration |
| 🧮 **Faithful token counting** | Embedded tiktoken `cl100k_base`; `/v1/messages/count_tokens` |

---

## Quick start

### Requirements

- **Go 1.27+** (to build only; the resulting binary needs nothing installed).
- One of these Kiro credential sources:
  - [Kiro IDE](https://kiro.dev/) with an active session, or
  - [Kiro CLI](https://kiro.dev/cli/) with AWS SSO (free Builder ID or corporate account).

### Build and run

```bash
# Clone the repository
git clone https://github.com/marr-cloud/kiro-gateway-go.git
cd kiro-gateway-go

# Build the binary (uses the Taskfile; equivalent to `go build ./cmd/kiro-gateway`)
task build
# or directly:
go build -o kiro-gateway ./cmd/kiro-gateway

# Configure credentials: a credentials.json with your accounts and a .env with
# PROXY_API_KEY (see the Configuration section)

# Start the server
./kiro-gateway

# With a custom host/port (if 8000 is busy)
./kiro-gateway --port 9000
```

The server is available at `http://localhost:8000`.

### With Docker

```bash
# Prepare credentials.json + a .env with PROXY_API_KEY (see Configuration)
docker compose up -d
docker compose logs -f
curl http://localhost:8000/health
```

The image is multi-stage on `distroless/static:nonroot` (~27 MB, no shell, non-root user) and its
healthcheck uses the binary itself (`--health`). See [`Dockerfile`](Dockerfile) and
[`docker-compose.yml`](docker-compose.yml).

A multi-arch image (linux amd64/arm64) is also published with every release:

```bash
docker pull ghcr.io/marr-cloud/kiro-gateway-go:latest
```

> **Release binaries** for all five platforms (Windows/Linux/macOS × amd64/arm64), with
> `SHA256SUMS`, are available on the
> [Releases page](https://github.com/marr-cloud/kiro-gateway-go/releases). Download the binary for
> your platform instead of building, if you prefer.

### Command-line flags

| Flag | Short | Description |
|---|---|---|
| `--host` | `-H` | Listen interface (defaults to `SERVER_HOST` or `0.0.0.0`) |
| `--port` | `-p` | Listen port (defaults to `SERVER_PORT` or `8000`) |
| `--version` | `-v` | Print the version and exit |
| `--help` | `-h` | Print help and exit |
| `--health` | | Query `GET /health` on the running server and exit with 0 (healthy) or 1 |

---

## Configuration

Two pieces: **`credentials.json`** defines the Kiro accounts; the **`.env`** holds the proxy key
and behavior settings. **Always** protect your proxy with `PROXY_API_KEY`: it is the key clients
use to connect.

### Accounts: `credentials.json`

The gateway loads accounts from a JSON array. By default it looks for `credentials.json` in the
working directory; point elsewhere with `ACCOUNTS_CONFIG_FILE`. Each entry has a `type` (`json`,
`sqlite` or `refresh_token`), `enabled: true`, and the matching path or token. See
[`credentials.json.example`](credentials.json.example).

```json
[
  { "type": "json",   "enabled": true, "path": "C:/Users/your-user/.aws/sso/cache/kiro-auth-token.json" },
  { "type": "sqlite", "enabled": true, "path": "C:/Users/your-user/AppData/Local/kiro-cli/data.sqlite3" }
]
```

- `json` — Kiro IDE / Enterprise token (valid if it contains `refreshToken` or `clientId`).
- `sqlite` — kiro-cli database (valid if it has an `auth_kv` table).
- `refresh_token` — a direct refresh token (`"refresh_token": "eyJ..."`, optional `profile_arn`).

With multiple entries the gateway does automatic **failover**: when one returns an error (429, 402)
it moves to the next; if one fails several times in a row it is set aside and periodically retried.
With a single account there is no switching (the real Kiro error is returned).

> The port does **not** implement the original's "single-account" mode (loose `REFRESH_TOKEN` /
> `KIRO_CREDS_FILE` / `KIRO_CLI_DB_FILE` in `.env` as the account source): accounts are always
> defined in `credentials.json`. See [docs/DIFFERENCES.md](docs/DIFFERENCES.md).

### `.env`

Copy [`.env.example`](.env.example) to `.env` and adjust it. The minimum is `PROXY_API_KEY`:

```env
# Password to protect YOUR proxy (make up a secure string)
PROXY_API_KEY="my-super-secret-password-123"

# Optional: path to the accounts file (default: credentials.json in the cwd)
ACCOUNTS_CONFIG_FILE=C:/Users/your-user/kiro/kiro-gateway/credentials.json
```

### VPN / proxy

For restricted networks or connectivity issues with AWS:

```env
VPN_PROXY_URL=http://127.0.0.1:7890     # HTTP
# VPN_PROXY_URL=socks5://127.0.0.1:1080 # SOCKS5
```

### Other useful variables

| Variable | Default | Description |
|---|---|---|
| `ACCOUNTS_CONFIG_FILE` | `credentials.json` | Path to the accounts array |
| `SERVER_HOST` / `SERVER_PORT` | `0.0.0.0` / `8000` | Listen interface and port |
| `KIRO_REGION` / `KIRO_API_REGION` | `us-east-1` | OIDC / Kiro API region |
| `WEB_SEARCH_ENABLED` | `true` | Enables the web search tool |
| `TRUNCATION_RECOVERY` | `true` | Recovery from truncated responses |
| `DEBUG_MODE` | `off` | Debug log mode (`off` / `errors` / `all`) |

---

## API reference

| Endpoint | Method | Description |
|---|---|---|
| `/` | GET | Basic status (JSON) |
| `/health` | GET | Detailed status (JSON) |
| `/v1/models` | GET | List of available models |
| `/v1/chat/completions` | POST | OpenAI Chat Completions API |
| `/v1/messages` | POST | Anthropic Messages API |
| `/v1/messages/count_tokens` | POST | Token counting (Anthropic) |

Authentication: `Authorization: Bearer <PROXY_API_KEY>` (OpenAI dialect) or
`x-api-key: <PROXY_API_KEY>` (Anthropic dialect).

---

## Usage examples

### OpenAI (cURL)

```bash
curl http://localhost:8000/v1/chat/completions \
  -H "Authorization: Bearer my-super-secret-password-123" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-5",
    "messages": [{"role": "user", "content": "Hello!"}],
    "stream": true
  }'
```

### Anthropic (cURL)

```bash
curl http://localhost:8000/v1/messages \
  -H "x-api-key: my-super-secret-password-123" \
  -H "anthropic-version: 2023-06-01" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "claude-sonnet-4-5",
    "max_tokens": 1024,
    "messages": [{"role": "user", "content": "Hello!"}]
  }'
```

### OpenAI SDK (Python)

```python
from openai import OpenAI

client = OpenAI(
    base_url="http://localhost:8000/v1",
    api_key="my-super-secret-password-123",  # your PROXY_API_KEY
)

response = client.chat.completions.create(
    model="claude-sonnet-4-5",
    messages=[{"role": "user", "content": "Hello!"}],
    stream=True,
)
for chunk in response:
    if chunk.choices[0].delta.content:
        print(chunk.choices[0].delta.content, end="")
```

---

## Debugging

Debug logging is **disabled by default**. To enable it:

```env
# off:    disabled (default)
# errors: save logs only for failed requests (4xx, 5xx) — recommended
# all:    save logs for every request (overwritten on each one)
DEBUG_MODE=errors
```

Files are written to `debug_logs/` (`request_body.json`, `kiro_request_body.json`,
`response_stream_raw.txt`, `response_stream_modified.txt`, etc.).

---

## Differences from the original

This port replicates the upstream's behavior at the network boundaries, with deliberate, bounded
deviations (e.g. it does not expose FastAPI's `/docs`, `/redoc` or `/openapi.json`; the `/` and
`/health` endpoints return JSON with extra fields; it adds the `--health` flag). The full list and
the rationale for each is in **[docs/DIFFERENCES.md](docs/DIFFERENCES.md)**.

## Documentation

- [Port design](docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md)
- [The golden corpus: generation, format and guarantees](docs/CORPUS.md)
- [Module mapping](docs/MAPPING.md)
- [Differences from the original](docs/DIFFERENCES.md)

## License and attribution

**AGPL-3.0.** This project is a derivative work of
[`jwadow/kiro-gateway`](https://github.com/jwadow/kiro-gateway) (v2.4.dev.13, commit `a5292ca`),
distributed under the same license. See [LICENSE](LICENSE) and [NOTICE](NOTICE) for the full
attribution and the list of changes. If you find this project useful, consider supporting the
[original project](https://github.com/jwadow/kiro-gateway#-support-the-project).

## Disclaimer

This project is not affiliated with or endorsed by AWS, Anthropic or Kiro IDE. Use it at your own
risk and in compliance with the terms of service of the underlying APIs.
