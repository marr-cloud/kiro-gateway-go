# Fase 4: Transporte — Plan de implementación

> **Para trabajadores agénticos:** SUB-SKILL OBLIGATORIA: usa
> `superpowers:subagent-driven-development` (recomendado) o `superpowers:executing-plans` para
> ejecutar este plan tarea por tarea. Los pasos usan sintaxis de casilla (`- [ ]`).

**Objetivo:** portar los tres paquetes de transporte que llevan la petición al backend real:
`httpclient` (cliente HTTP con reintentos, proxy y timeouts en tres capas), `auth` (tres fuentes
de credenciales, refresco proactivo con `singleflight`), y `accountmanager` (multicuenta con
circuit breaker, sticky index y `state.json` persistente).

**Arquitectura:** tres paquetes bajo `internal/`, cero corpus (spec §8.2 los coloca en la capa 2
de tests — a mano contra `httptest` y un Kiro falso). Cada test aísla la red con `httptest.Server`;
**ningún test toca red real**. Al cerrar la fase, un test de integración lanza el `accountmanager`
+ `auth` + `httpclient` juntos contra un servidor de mentira que devuelve chunks grabados.

**Decisiones de arquitectura confirmadas por el spec:**
- **D6 (empaquetado):** `CGO_ENABLED=0`. El driver de SQLite es `modernc.org/sqlite` (Go puro).
- **§5.6 (concurrencia):** el refresco de token va envuelto en `golang.org/x/sync/singleflight`;
  el account manager usa `sync.RWMutex`; el guardado de `state.json` es una goroutine con `context`
  cada `STATE_SAVE_INTERVAL_SECONDS`.
- **§5.5 (timeouts):** tres capas — conexión 30 s en `Transport.DialContext`, primer token 15 s
  con reintentos completos, lectura entre chunks 300 s por deadline renovado. **Sin `Client.Timeout`
  global.**
- **§6.9 (HTTP/2 y proxy):** `ForceAttemptHTTP2=false` y `TLSNextProto={}` para desactivar HTTP/2
  de verdad; `golang.org/x/net/proxy` para SOCKS5.

**Stack:** Go 1.27, `CGO_ENABLED=0`. **Tres dependencias externas nuevas**, todas Go puras:
- `modernc.org/sqlite` — driver SQLite sin cgo.
- `golang.org/x/sync/singleflight` — coalescing de refresco.
- `golang.org/x/net/proxy` — soporte SOCKS5.

**Spec:** `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md` (§5.5 timeouts,
§5.6 concurrencia, §6.9 httpclient, §6.10 auth, §6.11 accountmanager, §8.2 tests capa 2, D6).
**Fase anterior:** `docs/superpowers/plans/2026-09-14-fase-3-modelos-y-converters.md`.
**Fuentes a portar:**
- `.upstream/kiro/http_client.py` (349 líneas)
- `.upstream/kiro/auth.py` (976 líneas)
- `.upstream/kiro/account_manager.py` (897 líneas)

**Deuda técnica heredada de fase 3 que esta fase resuelve o hereda a fase 5:**

1. **Wire de package vars al arranque.** Las 7 vars exportadas en `converterscore`
   (`FakeReasoningEnabled`, `FakeReasoningMaxTokens`, `FakeReasoningBudgetCap`,
   `TruncationRecoveryEnabled`, `ToolDescriptionMaxLength`, `AutoTrimPayload`,
   `KiroMaxPayloadBytes`) deben asignarse desde `internal/config.Config` una vez al arranque.
   El punto de wire NO vive en `internal/` — vive en `cmd/kiro-gateway/main.go` y se toca en la
   fase 5 (server + lifecycle). Esta fase **no** las toca.
2. **Recuperación de panics.** `converterscore` panica en tres sitios (`ValidateToolNames` fallo,
   `mergedMessages` vacío, `ReasoningEffortToBudget` clave desconocida). El middleware HTTP que
   `recover()` vive en la fase 5. Esta fase **no** lo añade.
3. **`internal/modelresolver` y limpieza del subset duplicado.** Se duplicó una copia mínima en
   `convertersanthropic` y `convertersopenai`. El paquete propio se porta en fase 6 según spec §6.12.
4. **Corpus regeneration.** Los 21 skips documentados en las fases 3 (recorder captura args tras
   mutación in-place; snapshot de flags en namespace equivocado) se resuelven con un fix en
   `tools/corpus/recorder.py` + regeneración. **NO es tarea de esta fase.**

## Restricciones globales

- Module path `github.com/marr-cloud/kiro-gateway-go`, Go 1.27, `CGO_ENABLED=0`.
- Todo fichero `.go`, incluidos los `_test.go`, lleva como dos primeras líneas:
  ```go
  // SPDX-License-Identifier: AGPL-3.0-or-later
  // Port a Go de jwadow/kiro-gateway. Ver NOTICE.
  ```
- **Tres dependencias externas nuevas permitidas** (y sólo estas): `modernc.org/sqlite`,
  `golang.org/x/sync/singleflight`, `golang.org/x/net/proxy`. Cualquier otra dependencia — párate y
  reporta.
- Ningún test toca la red real. Todo con `httptest.Server` y tmp files.
- Nombres de paquete: `httpclient`, `auth`, `accountmanager`. Al terminar cada tarea se rellena la
  fila correspondiente de `docs/MAPPING.md`.
- Al cerrar la fase: `task lint` y `task test` en verde.
- Mensajes de commit en inglés, prefijos convencionales.
- **Nada de globals mutables nuevos.** El patrón de fase 3 (package vars leídos desde tests con
  `t.Cleanup`) se usó porque replicaba el comportamiento observable de módulos globales Python. Los
  paquetes de fase 4 son objetos con estado explícito (`*Manager`, `*Client`, `*Accounts`) — todo
  el estado va en structs, tests inyectan el struct.
- **BINDING CONTRACT: los detalles de comportamiento vienen del upstream, no del brief.** Fases 2–3
  encontraron desviaciones brief-vs-upstream en TODAS las tareas. Verifica cada función contra
  `.upstream/kiro/*.py` antes de escribir tests. Si el brief y el upstream discrepan, upstream gana.

## Estructura de ficheros

Ficheros que crea esta fase, con su responsabilidad. Un fichero por preocupación, siguiendo el
estilo de fase 3 (no monster files).

| Fichero | Responsabilidad |
|---|---|
| `internal/httpclient/transport.go` | Construcción del `*http.Transport` con proxy + HTTP/2-off + keepalives |
| `internal/httpclient/proxy.go` | Resolución de proxy (HTTP_PROXY/HTTPS_PROXY/ALL_PROXY/NO_PROXY), SOCKS5 |
| `internal/httpclient/client.go` | `Client` con `RequestWithRetry`; reintentos 403/429/5xx; timeouts de primer token |
| `internal/httpclient/client_test.go` | Tests contra `httptest.Server`; casos de retry + backoff + timeout |
| `internal/auth/types.go` | `AuthType` enum + tipos de credencial (`credsJSON`, `credsSQLite`, `credsRefreshOnly`) |
| `internal/auth/manager.go` | `Manager` struct + `AccessToken`, `ProfileARN`, `Region`, `Fingerprint` |
| `internal/auth/json_source.go` | Fuente Kiro Desktop: load, refresh, save |
| `internal/auth/oidc.go` | AWS SSO OIDC: refresh normal + Enterprise `clientIdHash` |
| `internal/auth/sqlite.go` | SQLite kiro-cli: keys `kirocli:*` / `codewhisperer:*`, read-merge-write, `SQLITE_READONLY` |
| `internal/auth/refresh.go` | Refresco proactivo (`TOKEN_REFRESH_THRESHOLD` = 600 s), `singleflight`, degradación grácil |
| `internal/auth/manager_test.go` | Tests `Manager` + JSON source + region resolution + fingerprint |
| `internal/auth/oidc_test.go` | Tests OIDC contra `httptest.Server` de mentira |
| `internal/auth/sqlite_test.go` | Tests SQLite con `modernc.org/sqlite` en tmp file |
| `internal/auth/refresh_test.go` | Tests singleflight (N goroutines → 1 refresco) + degradación grácil |
| `internal/accountmanager/types.go` | `Account`, `AccountStats`, `ModelAccountList` |
| `internal/accountmanager/discovery.go` | Descubrimiento desde `credentials.json` (JSON+SQLite+refresh_token) |
| `internal/accountmanager/state.go` | `state.json` load/save con `os.Rename` atómico + reintento corto |
| `internal/accountmanager/breaker.go` | Circuit breaker, backoff exponencial capado, reintento probabilístico |
| `internal/accountmanager/selection.go` | Índice sticky global + `GetNextAccount` |
| `internal/accountmanager/failover.go` | `ReportSuccess`/`ReportFailure` + clasificación de errores para failover |
| `internal/accountmanager/manager.go` | `Manager` struct + `LoadCredentials`/`LoadState`/`SaveStatePeriodically` |
| `internal/accountmanager/*_test.go` | Tests (un fichero por concepto) |

**Nuevas dependencias en `go.mod`** (por orden de Task):
- Task 1: `golang.org/x/net/proxy`
- Task 2/3/4: (ninguna nueva)
- Task 5: `modernc.org/sqlite`
- Task 6: `golang.org/x/sync/singleflight`

## Cómo se testea sin corpus

Fase 3 dependía del corpus golden. Fase 4 **no tiene corpus** — la lista de casos se deriva de los
nombres de los tests Python (`.upstream/tests/unit/test_http_client.py`,
`.upstream/tests/unit/test_auth.py`, `.upstream/tests/unit/test_account_manager.py`), que están
escritos de forma descriptiva. Reglas:

- Un test Go por escenario descrito en los tests Python. No se traducen 1:1, se traduce la
  intención.
- `httptest.NewServer` para todo lo que sea red.
- Tmp files (`t.TempDir()`) para credenciales/state.json/SQLite.
- Tiempo congelado con una interfaz `Clock` inyectable (`Now() time.Time`) en cada tarea que
  compare instantes — igual que hace el original con `time.time()` mockeado.
- Concurrencia real: los tests de singleflight y sticky-index usan `sync.WaitGroup` y `t.Parallel`
  sólo cuando el paquete tiene aislamiento por instancia (no hay globals mutables — véase
  restricción global).
- **Convención de fallos:** cada `t.Fatalf` incluye contexto suficiente para reproducir sin
  releer el test.

---

## Task 1: `httpclient` — cliente HTTP con reintentos y proxy

**Files:**
- Create: `internal/httpclient/transport.go`
- Create: `internal/httpclient/proxy.go`
- Create: `internal/httpclient/client.go`
- Create: `internal/httpclient/client_test.go`
- Modify: `go.mod`, `go.sum` (añade `golang.org/x/net/proxy`)
- Modify: `docs/MAPPING.md` (fila `http_client.py`)

**Interfaces:**
- Consumes: `internal/config` (para `VPN_PROXY_URL`, `FIRST_TOKEN_TIMEOUT`, `FIRST_TOKEN_MAX_RETRIES`,
  `STREAMING_READ_TIMEOUT`), `internal/networkerrors` (clasificación de errores), `internal/utils`
  (headers salientes vía `GetKiroHeaders`).
- Produces:
  ```go
  type Client struct { /* transport, cfg, base URL */ }

  func New(cfg *config.Config) (*Client, error)

  // RequestWithRetry hace la petición con reintentos idénticos al original:
  //   - 403 fuerza refresco de token (via TokenProvider) y repite 1 vez.
  //   - 429 y 5xx: 3 reintentos con backoff 1, 2, 4 s (BASE_RETRY_DELAY × 2^intento).
  //   - Primer token: FIRST_TOKEN_TIMEOUT sobre la primera lectura del body,
  //     hasta FIRST_TOKEN_MAX_RETRIES si vence.
  //   - Lectura entre chunks: deadline renovado STREAMING_READ_TIMEOUT.
  // Devuelve el *http.Response con el body abierto listo para streaming.
  func (c *Client) RequestWithRetry(ctx context.Context, req *http.Request, tp utils.TokenProvider) (*http.Response, error)

  // Close libera el Transport.
  func (c *Client) Close() error
  ```

**Ajustes del Transport** (§6.9, verifica los valores exactos en `.upstream/kiro/http_client.py:79-150`):

| Ajuste | Valor | Motivo |
|---|---|---|
| `MaxIdleConns` | 100 | Equivale a `max_connections` de httpx |
| `MaxIdleConnsPerHost` | 20 | Equivale a `max_keepalive_connections` |
| `IdleConnTimeout` | 30 s | Equivale a `keepalive_expiry` |
| `ForceAttemptHTTP2` | `false` | httpx no activa HTTP/2 |
| `TLSNextProto` | `map[string]func(...){}` | Necesario para desactivar HTTP/2 de verdad |
| `DialContext` timeout | 30 s | Timeout de conexión |

**Proxy** (§6.9):
- `VPN_PROXY_URL` de config (añade `http://` si falta esquema).
- Respetar `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY`, `NO_PROXY`; añadir `127.0.0.1,localhost` a `NO_PROXY`.
- SOCKS5 vía `golang.org/x/net/proxy`.

**Cabecera `Connection: close`** en peticiones de streaming (mitigación CLOSE_WAIT).

- [ ] **Paso 1:** Tests que fallan en `client_test.go`. Casos mínimos derivados de
  `.upstream/tests/unit/test_http_client.py`:
  - 200 OK simple contra `httptest.Server`.
  - 403 → llama a `TokenProvider.Refresh()` y repite; segundo intento devuelve 200.
  - 429 → espera 1 s (mockable via `Clock`) y repite; comprueba tres intentos.
  - 5xx → mismo backoff 1-2-4.
  - Timeout de primer token: `httptest.Server` que retarda el primer byte más allá de
    `FIRST_TOKEN_TIMEOUT` → error, reintento hasta `FIRST_TOKEN_MAX_RETRIES`, entonces error final.
  - Cabeceras: verifica que la request final tiene los headers exactos del original (User-Agent,
    x-amz-target, etc.), producidos por `utils.GetKiroHeaders`.
  - Proxy: `httptest` como proxy HTTP; verifica que la request pasa por él cuando `VPN_PROXY_URL`
    está seteado.
- [ ] **Paso 2:** Correr, ver fallar (`go test ./internal/httpclient`).
- [ ] **Paso 3:** Implementar `transport.go`, `proxy.go`, `client.go` traduciendo
  `.upstream/kiro/http_client.py:47-348`. Ojo: httpx usa `AsyncClient` — en Go es síncrono, el
  cliente devuelve el body como `io.ReadCloser` y el llamador drena.
- [ ] **Paso 4:** Correr, ver pasar.
- [ ] **Paso 5:** `task lint` y `task test` verdes; `docs/MAPPING.md` actualizado.
- [ ] **Paso 6:** Commit `feat(httpclient): HTTP client with retries, proxy, and streaming timeouts`.

---

## Task 2: `auth` — tipos, Manager, fingerprint, fuente Kiro Desktop JSON

**Files:**
- Create: `internal/auth/types.go`
- Create: `internal/auth/manager.go`
- Create: `internal/auth/json_source.go`
- Create: `internal/auth/manager_test.go`
- Modify: `docs/MAPPING.md` (fila `auth.py`, parcial)

**Interfaces:**
- Consumes: `internal/config`, `internal/utils` (fingerprint, `TokenProvider`), `internal/httpclient`.
- Produces:
  ```go
  type AuthType int
  const (
      AuthTypeUnknown AuthType = iota
      AuthTypeKiroDesktop
      AuthTypeAWSSSO
      AuthTypeKiroCLI
      AuthTypeRefreshOnly
  )

  type Manager struct { /* creds, refresh_token, tokens, cfg, mu */ }

  func NewManager(cfg *config.Config) (*Manager, error)

  // Implementa utils.TokenProvider.
  func (m *Manager) AccessToken(ctx context.Context) (string, error)
  func (m *Manager) ProfileARN() string
  func (m *Manager) Region() string
  func (m *Manager) APIHost() string        // p.ej. runtime.us-east-1.kiro.dev
  func (m *Manager) QHost() string          // p.ej. q.us-east-1.amazonaws.com
  func (m *Manager) Fingerprint() string
  func (m *Manager) Type() AuthType
  ```

**Detalles del original (verifica en `.upstream/kiro/auth.py:68-233`):**
- `AuthType` = `KIRO_DESKTOP | AWS_SSO_OIDC | KIRO_CLI | REFRESH_ONLY | UNKNOWN`.
- `Fingerprint()` = `sha256("{hostname}-{username}-kiro-gateway")` hex (ya lo hace
  `internal/utils.Fingerprint`).
- Precedencia de región de API: valor por cuenta > `KIRO_API_REGION` > detectado del ARN >
  `KIRO_REGION`.
- Detección de tipo: si viene por SQLite → `KIRO_CLI`; si tiene `clientId` en JSON → `AWS_SSO_OIDC`;
  si sólo hay `refreshToken` → `REFRESH_ONLY`; si hay JSON completo → `KIRO_DESKTOP`.

**Fuente Kiro Desktop** (`.upstream/kiro/auth.py:384-457`):
- Lee `{refreshToken, accessToken, profileArn, region, expiresAt}` del JSON.
- `expiresAt` en RFC3339 nano.
- Al refrescar, persiste el nuevo token de vuelta al fichero (write-through).

- [ ] **Paso 1:** Tests hand-written en `manager_test.go`:
  - Carga desde tmp JSON: verifica todos los campos poblados.
  - Región precedence: 4 subtests, uno por nivel.
  - `Fingerprint()` estable: llamar dos veces devuelve la misma cadena.
  - `AccessToken()` devuelve el token si `!isExpiringSoon`; si expira pronto llama a
    refresh (stub para esta Task — el refresco real está en Task 6).
  - `AuthType` correcto para JSON kiro-desktop.
- [ ] **Paso 2:** Correr, fallar.
- [ ] **Paso 3:** Implementar `types.go`, `manager.go`, `json_source.go`.
- [ ] **Paso 4:** Correr, pasar.
- [ ] **Paso 5:** Commit `feat(auth): manager, types, and Kiro Desktop JSON credentials`.

---

## Task 3: `auth` — AWS SSO OIDC (normal + Enterprise)

**Files:**
- Create: `internal/auth/oidc.go`
- Create: `internal/auth/oidc_test.go`

**Interfaces:**
- Consumes: `internal/httpclient` para el POST OIDC, `internal/auth` types.
- Produces (métodos privados sobre `*Manager`):
  ```go
  func (m *Manager) refreshAWSSSO(ctx context.Context) error
  func (m *Manager) loadEnterpriseDeviceRegistration(clientIDHash string) error
  ```

**Detalles del original** (`.upstream/kiro/auth.py:743-869`):
- Endpoint: `POST https://oidc.{region}.amazonaws.com/token`.
- Body **camelCase**: `{"grantType":"refresh_token","clientId":...,"clientSecret":...,"refreshToken":...}`.
- Enterprise: si el JSON de creds sólo trae `clientIdHash`, resuelve `clientId`/`clientSecret`
  desde `~/.aws/sso/cache/{clientIdHash}.json`.
- Ante un 400 con `error: "invalid_client"` en la respuesta: recarga la SQLite (si el auth type es
  KIRO_CLI) y reintenta UNA vez.

- [ ] **Paso 1:** Tests con `httptest.Server` simulando el endpoint OIDC:
  - Refresh exitoso: comprueba que el body es JSON camelCase con los cuatro campos.
  - Refresh 400 invalid_client: recarga y reintenta.
  - Enterprise: tmp dir con `~/.aws/sso/cache/{hash}.json` y verifica que se leen los campos.
  - Refresh error irrecuperable → propaga error, no toca `m.tokens`.
- [ ] **Paso 2-4:** Fallar, implementar, pasar.
- [ ] **Paso 5:** Commit `feat(auth): AWS SSO OIDC refresh (regular and Enterprise)`.

---

## Task 4: `auth` — SQLite kiro-cli source (read-merge-write, `SQLITE_READONLY`)

**Files:**
- Create: `internal/auth/sqlite.go`
- Create: `internal/auth/sqlite_test.go`
- Modify: `go.mod`, `go.sum` (añade `modernc.org/sqlite`)

**Interfaces:**
- Produces (métodos privados sobre `*Manager`):
  ```go
  func (m *Manager) loadFromSQLite(dbPath string) error
  func (m *Manager) saveToSQLite() error   // no-op si SQLITE_READONLY
  ```

**Detalles del original** (`.upstream/kiro/auth.py:248-633`):
- Tabla: `auth_kv(key TEXT PRIMARY KEY, value TEXT)`.
- Claves por prioridad al leer: `kirocli:social:token` → `kirocli:odic:token` → `codewhisperer:odic:token`.
- Registro de dispositivo: `kirocli:odic:device-registration` o `codewhisperer:odic:device-registration`.
- Campos en snake_case en el valor JSON: `access_token`, `refresh_token`, `profile_arn`,
  `region`, `scopes`, `expires_at`.
- Región de API detectada del ARN de tabla `state`, clave `api.codewhisperer.profile`.
- `expires_at` en RFC3339 nano (9 dígitos); fallback a regex si el formato varía.
- **Write:** read-merge-write — lee el registro actual, fusiona campos conocidos, reescribe
  preservando desconocidos. Salta si `SQLITE_READONLY`. Guarda en la MISMA clave desde la que
  se leyó.

**Driver:** `modernc.org/sqlite` (Go puro, cumple `CGO_ENABLED=0`). Importa como
`_ "modernc.org/sqlite"` y abre con `sql.Open("sqlite", dbPath)`.

- [ ] **Paso 1:** Tests con `modernc.org/sqlite` sobre tmp dir:
  - Load: crea tabla `auth_kv`, inserta bajo cada una de las 3 claves, verifica precedencia.
  - Region detection: inserta ARN en tabla `state`, verifica que se extrae la región.
  - Save read-merge-write: pre-carga un JSON con un campo extra desconocido, guarda, verifica que el
    extra sigue ahí.
  - `SQLITE_READONLY=true`: llama a save, verifica que la fila no cambia.
- [ ] **Paso 2-4:** Fallar, implementar, pasar.
- [ ] **Paso 5:** Commit `feat(auth): SQLite kiro-cli credentials source with read-merge-write`.

---

## Task 5: `auth` — refresco proactivo, `singleflight`, degradación grácil

**Files:**
- Create: `internal/auth/refresh.go`
- Create: `internal/auth/refresh_test.go`
- Modify: `go.mod`, `go.sum` (añade `golang.org/x/sync/singleflight`)

**Interfaces:**
- Produces:
  ```go
  // isExpiringSoon devuelve true si el token vence en < TOKEN_REFRESH_THRESHOLD (600 s).
  func (m *Manager) isExpiringSoon() bool

  // Refresh dispara un refresco. Si N goroutines lo llaman a la vez, singleflight coalesce a 1
  // llamada al servidor, todas comparten el resultado.
  func (m *Manager) Refresh(ctx context.Context) (string, error)

  // ForceRefresh siempre refresca, ignorando isExpiringSoon.
  func (m *Manager) ForceRefresh(ctx context.Context) (string, error)
  ```

**Detalles del original** (`.upstream/kiro/auth.py:634-949`):
- `TOKEN_REFRESH_THRESHOLD = 600` s.
- El refresco se dispara desde `get_access_token()` si el token expira pronto.
- Coalescing: en el original, un `asyncio.Lock` protege; en Go usamos
  `golang.org/x/sync/singleflight` con clave fija (una por instancia de `Manager`).
- **Degradación grácil:** si el refresco falla PERO el token todavía no ha vencido, se sigue
  usando. Sólo si ha vencido de verdad se devuelve error.

- [ ] **Paso 1:** Tests:
  - `isExpiringSoon`: token expira en 601 s → false; en 599 s → true; ya expirado → true.
  - Singleflight: lanza 20 goroutines llamando `Refresh` a la vez con un servidor OIDC de mentira
    que cuenta hits; verifica hits == 1 y todas reciben el mismo token.
  - Degradación: servidor OIDC devuelve 500; token todavía válido 100 s → `Refresh` devuelve el
    token viejo sin error.
  - Degradación fallada: token ya expirado + OIDC 500 → `Refresh` devuelve error.
  - `AccessToken()` de Task 2 llama a `Refresh` sólo cuando expira pronto, no en cada llamada.
- [ ] **Paso 2-4:** Fallar, implementar, pasar.
- [ ] **Paso 5:** Commit `feat(auth): singleflight refresh and graceful degradation`.

---

## Task 6: `accountmanager` — tipos, descubrimiento, `state.json`

**Files:**
- Create: `internal/accountmanager/types.go`
- Create: `internal/accountmanager/discovery.go`
- Create: `internal/accountmanager/state.go`
- Create: `internal/accountmanager/manager.go`
- Create: `internal/accountmanager/manager_test.go`
- Modify: `docs/MAPPING.md` (fila `account_manager.py`, parcial)

**Interfaces:**
- Consumes: `internal/auth` (para arrancar un `*auth.Manager` por cuenta), `internal/config`.
- Produces:
  ```go
  type Account struct {
      ID          string             // ruta absoluta resuelta, o "refresh_token_{sha256(token)[:16]}"
      Type        string             // "json" | "sqlite" | "refresh_token"
      Path        string             // vacío si Type=="refresh_token"
      Enabled     bool
      ProfileARN  string             // opcional
      Region      string             // opcional
      APIRegion   string             // opcional, fuerza runtime.{region}
      Auth        *auth.Manager
      Stats       AccountStats
      Models      ModelAccountList
  }

  type AccountStats struct {
      ConsecutiveFailures int
      LastFailure         time.Time
      LastFailureMsg      string
  }

  type ModelAccountList struct {
      Models     []string
      LoadedAt   time.Time
      TTL        time.Duration    // ACCOUNT_CACHE_TTL
  }

  type Manager struct { /* accounts, stickyIdx, mu, stateFile, saveInterval */ }

  func NewManager(cfg *config.Config) (*Manager, error)
  func (m *Manager) LoadCredentials(ctx context.Context) error
  func (m *Manager) LoadState() error
  func (m *Manager) SaveStatePeriodically(ctx context.Context)  // goroutine loop
  ```

**Detalles del original** (`.upstream/kiro/account_manager.py:127-431`):
- `credentials.json` es array de objetos con `type` (`json|sqlite|refresh_token`), `enabled`,
  opcionalmente `path`, `profile_arn`, `region`, `api_region`.
- Si `path` apunta a un directorio, escanea sin recursión.
- Un JSON es válido si contiene `refreshToken` **o** `clientId`.
- Un SQLite es válido si tiene la tabla `auth_kv`.
- `state.json` escritura atómica: tmp file + `os.Rename`. En Go `os.Rename` sobre Windows usa
  `MoveFileEx` con reemplazo — funciona. Si otro proceso tiene el fichero abierto (antivirus), un
  reintento corto (3 intentos, 100 ms cada uno). Sin dependencias adicionales.
- `SaveStatePeriodically` corre `STATE_SAVE_INTERVAL_SECONDS` (10 s por defecto) y guarda si el
  estado en memoria difiere del último guardado.

- [ ] **Paso 1:** Tests:
  - Discovery: tmp dir con 3 fuentes (JSON, SQLite, refresh_token), verifica IDs correctos y
    validez.
  - Discovery de directorio: verifica no-recursivo.
  - `state.json`: guardar, matar el manager, cargar nuevo, verifica que se recuperan
    `ConsecutiveFailures`/`LastFailure`.
  - `SaveStatePeriodically`: goroutine con `context` cancelable; verifica que se detiene limpiamente.
  - Rename atómico: usa `t.TempDir()`, guarda 100 veces en rápido bucle, verifica que
    ningún `state.json` queda corrupto (sin JSON parcial).
- [ ] **Paso 2-4:** Fallar, implementar, pasar.
- [ ] **Paso 5:** Commit `feat(accountmanager): types, discovery, and state persistence`.

---

## Task 7: `accountmanager` — circuit breaker, sticky, failover

**Files:**
- Create: `internal/accountmanager/breaker.go`
- Create: `internal/accountmanager/selection.go`
- Create: `internal/accountmanager/failover.go`
- Create: `internal/accountmanager/failover_test.go`

**Interfaces:**
- Produces (métodos sobre `*Manager`):
  ```go
  // GetNextAccount devuelve la siguiente cuenta habilitada, saltando las que están en cuarentena.
  // El índice sticky global se actualiza en ReportSuccess con el índice de la cuenta que tuvo éxito (upstream auth.py:801-804).
  // ReportFailure NO lo mueve (auth.py:864-865).
  func (m *Manager) GetNextAccount(model string, exclude map[string]struct{}) (*Account, error)

  // ReportSuccess resetea el contador de fallos de la cuenta.
  func (m *Manager) ReportSuccess(accountID, model string)

  // ReportFailure clasifica el error y decide si la cuenta se abre en cuarentena o si el error
  // sube al cliente. Devuelve accounterrors.Fatal o accounterrors.Recoverable.
  func (m *Manager) ReportFailure(accountID, model string, statusCode int, reason string, msg string) accounterrors.Kind

  // isInQuarantine devuelve true si la cuenta está en cuarentena en este instante,
  // salvo que se dispare el reintento probabilístico (ACCOUNT_PROBABILISTIC_RETRY_CHANCE).
  func (m *Manager) isInQuarantine(a *Account, now time.Time) bool
  ```

**Detalles del original** (`.upstream/kiro/account_manager.py:645-867`):
- Cuarentena: `ACCOUNT_RECOVERY_TIMEOUT × 2^(fails-1)`, cap a `ACCOUNT_MAX_BACKOFF_MULTIPLIER = 1440`
  (60 s × 1440 = un día).
- Reintento probabilístico: con `p = ACCOUNT_PROBABILISTIC_RETRY_CHANCE` (0.1) reintenta una cuenta
  en cuarentena.
- Índice sticky global: sólo avanza en `ReportFailure`; `ReportSuccess` no lo mueve.
- Con **una sola cuenta** configurada: devuelve el error real de Kiro tal cual.
- Con **varias cuentas**, tras completar el círculo sin éxito: devuelve 503 con el mensaje del
  último error.
- Clasificación (delegada a `internal/accounterrors`, ya existe): 402/403/429/`INVALID_MODEL_ID` son
  Recoverable; 400 con `CONTENT_LENGTH_EXCEEDS_THRESHOLD`/otros 400/422/5xx son Fatal.

- [ ] **Paso 1:** Tests:
  - Sticky: 3 cuentas, 5 llamadas seguidas exitosas → todas van a la misma cuenta.
  - Failover: la cuenta 1 falla, siguiente llamada va a la cuenta 2, luego a la 2 otra vez, etc.
  - Circuit breaker: cuenta falla 3 veces con backoff 60/120/240 s; a los 100 s del último fallo
    sigue en cuarentena; a los 300 s → recuperada.
  - Backoff cap: 1500 fallos → cap a 60 × 1440 s.
  - Reintento probabilístico: con `p=1.0` (mock) siempre reintenta una cuenta en cuarentena.
  - Clasificación: 402 → Recoverable, 5xx → Fatal, 400+CONTENT_LENGTH_EXCEEDS_THRESHOLD → Fatal.
  - Single-account 503: si sólo hay una cuenta y su error es Fatal → devuelve el error real, no 503.
  - Multi-account 503: 2 cuentas, ambas Fatal → 503 con el mensaje del último.
- [ ] **Paso 2-4:** Fallar, implementar, pasar.
- [ ] **Paso 5:** Commit `feat(accountmanager): circuit breaker, sticky selection, and failover`.

---

## Task 8: `accountmanager` — inicialización y caché de modelos por cuenta

**Files:**
- Create: `internal/accountmanager/init.go`
- Modify: `internal/accountmanager/manager.go` (integrar `Initialize` en `LoadCredentials`)
- Create: `internal/accountmanager/init_test.go`

**Interfaces:**
- Produces:
  ```go
  // Initialize arranca cada cuenta: instancia su *auth.Manager, valida credenciales,
  // pobla ModelAccountList (dinámico si endpoint antiguo, estático si runtime).
  // Corre en paralelo con limit de concurrencia razonable.
  func (m *Manager) Initialize(ctx context.Context) error

  // refreshAccountModels re-consulta la lista de modelos para una cuenta si su TTL expiró.
  // No hace nada contra endpoint runtime.*.kiro.dev.
  func (m *Manager) refreshAccountModels(ctx context.Context, accountID string) error

  // GetAllAvailableModels devuelve la unión de modelos de todas las cuentas habilitadas.
  func (m *Manager) GetAllAvailableModels() []string

  // GetFirstAccount devuelve la primera cuenta descubierta (para caminos legacy que no piden failover).
  func (m *Manager) GetFirstAccount() *Account
  ```

**Detalles del original** (`.upstream/kiro/account_manager.py:432-644, 868-897`):
- Endpoint runtime: catálogo estático `modelresolver.FALLBACK_MODELS` (ver spec §6.12; en fase 4
  hard-codea la lista en `accountmanager` con nota "temporal, replazar cuando modelresolver
  exista").
- Endpoint antiguo `q.*.amazonaws.com`: consulta `ListAvailableModels` con 3 reintentos y fallback
  al catálogo estático.
- Inicialización paralela pero con límite (~4 goroutines) para no reventar OIDC.

- [ ] **Paso 1:** Tests:
  - Init: 2 cuentas mock, tras `Initialize` cada una tiene su `Auth` no nil y modelos poblados.
  - Init con error en una cuenta: la marca como disabled y sigue con el resto.
  - Catálogo estático para endpoint runtime: verifica que la lista sale sin llamar a red.
  - Endpoint antiguo: `httptest` mockeando `ListAvailableModels`; verifica reintentos ante 5xx.
  - `GetAllAvailableModels`: unión de dos cuentas con modelos parcialmente solapados.
- [ ] **Paso 2-4:** Fallar, implementar, pasar.
- [ ] **Paso 5:** Commit `feat(accountmanager): account initialization and model catalog`.

---

## Task 9: Test de integración transporte + un solo commit de closure

**Files:**
- Create: `internal/accountmanager/integration_test.go`
- Modify: `docs/MAPPING.md` (columnas restantes)
- Modify: `README.md` (sección Estado)

**Alcance:** un test end-to-end que arranca `Manager` + `auth.Manager` + `httpclient.Client`
juntos, con un `httptest.Server` haciendo de Kiro falso que:
- Devuelve 200 en el primer intento.
- Devuelve 429 en el segundo, verifica reintento.
- Devuelve 403 en el tercero, verifica que se llama a `Manager.Refresh` de auth y se reintenta.
- Devuelve 500 tres veces desde una cuenta, verifica failover a la siguiente cuenta.

Sin corpus, sin red real. Todo con `httptest`.

- [ ] **Paso 1:** Escribir el test como un solo `TestPhase4Integration` con subtests para cada
  escenario. Usa dos cuentas (un JSON tmp file cada una) y un `httptest.Server` que cambia
  comportamiento por `X-Test-Scenario` header o similar.
- [ ] **Paso 2:** Ejecutar y ver pasar (implementación ya está de Tasks 1-8).
- [ ] **Paso 3:** Actualizar `docs/MAPPING.md` con las tres filas completas (`http_client.py`,
  `auth.py`, `account_manager.py`).
- [ ] **Paso 4:** Actualizar `README.md` sección Estado: fase 4 completa, transporte con tres
  fuentes de credenciales y multi-cuenta. Próxima fase: streaming y rutas (fase 5).
- [ ] **Paso 5:** `task lint` y `task test` verdes.
- [ ] **Paso 6:** Commit `test(accountmanager): end-to-end phase 4 integration` seguido de un
  commit final `docs: close phase 4 — transport`.

---

## Criterio de terminado

1. Los tres paquetes (`httpclient`, `auth`, `accountmanager`) compilan y sus tests pasan.
2. Test de integración de Task 9 en verde: dos cuentas, `httptest.Server`, escenarios 200/429/403/failover.
3. `task lint`, `task test` en verde.
4. `docs/MAPPING.md` refleja los tres paquetes nuevos.
5. Nuevas dependencias en `go.mod` son sólo las tres autorizadas.
6. Ningún test toca red real ni el filesystem del usuario (todo bajo `t.TempDir()`).
