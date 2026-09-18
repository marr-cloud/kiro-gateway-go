# Port de kiro-gateway a Go (single binary) — Documento de diseño

**Fecha:** 2026-09-04
**Estado:** aprobado
**Origen:** [jwadow/kiro-gateway](https://github.com/jwadow/kiro-gateway) v2.4.dev.13, commit `a5292ca`
**Destino:** `github.com/marr-cloud/kiro-gateway-go`

---

## 1. Objetivo

Reimplementar en Go el gateway `kiro-gateway`, hoy escrito en Python con FastAPI, de forma que
el resultado sea **un único binario autocontenido** sin dependencias de runtime, sin intérprete,
sin librerías dinámicas y sin descargas en el primer arranque.

El criterio de éxito ordenado por prioridad, tal como lo fijó el usuario:

1. **Pruebas y compatibilidad.** El port debe demostrar paridad con el original, no afirmarla.
2. **Documentación.** README exhaustivo, escrito para alguien que llega sin contexto previo.
3. **Publicación.** El repositorio se sube a `github.com/marr-cloud`.

### 1.1 Qué es el sistema que se porta

Un proxy HTTP local que expone dos dialectos de API de modelos de lenguaje —el de OpenAI
(`/v1/chat/completions`) y el de Anthropic (`/v1/messages`)— y traduce cada petición a la API
interna de Kiro (`runtime.{region}.kiro.dev/generateAssistantResponse`, servicio
`AmazonCodeWhispererStreamingService`), reenviando la respuesta en streaming de vuelta al
cliente en el dialecto que corresponda.

Volumen del original: 14.623 líneas de código repartidas en los 31 ficheros de `kiro/` más
`main.py`, y 43.374 líneas de tests en 34 ficheros con aproximadamente 1.662 funciones de prueba.

---

## 2. Licencia y atribución

El upstream está publicado bajo **GNU AGPL-3.0**. Un port a otro lenguaje es obra derivada, por
lo que este proyecto **debe** distribuirse también bajo AGPL-3.0. Consecuencias operativas:

- El fichero `LICENSE` es una copia literal de la AGPL-3.0.
- Se preserva el aviso de copyright del autor original y se añade el del port.
- El `README` y el `NOTICE` declaran de forma visible que esto es un port de
  `jwadow/kiro-gateway`, indican el commit de origen (`a5292ca`, v2.4.dev.13) y enumeran los
  cambios respecto al original (ver §11).
- La cláusula 13 de la AGPL obliga a ofrecer el código fuente a los usuarios si el servicio se
  expone por red a terceros. Para uso propio autohospedado no impone nada.

---

## 3. Decisiones de diseño

Las siete decisiones que sigue el resto del documento, todas validadas con el usuario.

### D1 — Nivel de compatibilidad: híbrido

Paridad **byte a byte** en las dos fronteras del wire, y paridad **semántica** en el resto.

| Frontera | Nivel | Motivo |
|---|---|---|
| Payload HTTP saliente hacia Kiro | byte a byte | El backend puede validar forma y cabeceras; el fingerprint del User-Agent es parte del contrato |
| Bytes SSE devueltos al cliente | byte a byte | Claude Code y los SDK oficiales son estrictos con la secuencia de eventos |
| Logs, errores internos, `state.json` | semántica | Nadie los consume programáticamente |

El marshaller que emula `json.dumps` de Python queda **acotado al tokenizer**, que es el único
sitio donde la forma exacta del JSON afecta a un valor observable (el conteo de tokens).

### D2 — Estrategia de tests: escalonada en tres capas

1. **Corpus golden extraído del Python.** Un plugin de pytest instrumenta las funciones puras y
   graba entrada y salida de cada llamada que hacen los 1.662 tests existentes. Los tests Go
   consumen ese corpus. Las expectativas no las escribe nadie a mano: las produce el Python real.
2. **Port a mano** de los módulos con estado, que no se pueden congelar como funciones puras.
3. **Suite de conformance** que lanza el gateway Python y el binario Go contra un Kiro falso y
   compara los bytes de respuesta.

Volumen estimado escrito a mano: 10.000–12.000 líneas, frente a las 25.000–30.000 de un port 1:1
de la suite.

### D3 — Estructura: espejo a nivel de paquete, idiomático dentro

El nombre de cada paquete Go es el del módulo Python sin guiones bajos. Los ficheros grandes se
subdividen por responsabilidad dentro del paquete. `docs/MAPPING.md` documenta la
correspondencia módulo a paquete y toda función renombrada. Motivo: poder seguir los cambios de
upstream de forma ocasional sin que el mapeo requiera criterio.

### D4 — Arquitectura del pipeline de streaming: bucle explícito

El parser es una máquina de estados `Feed(chunk []byte) []Event`, en correspondencia directa con
el `feed()` del Python, que ya funciona así. El handler es dueño del bucle de lectura. **Sin
goroutines ni canales por petición.** La desconexión del cliente se detecta por
`r.Context().Done()` o por el error del `Write`.

Descartado: canales con goroutine por stream (fuga si el consumidor sale sin drenar, y duplica la
lógica de timeout) e iteradores `iter.Seq2` (envolver el primer elemento en un timeout exige
`iter.Pull2` más una goroutine, justo lo que el bucle evita).

### D5 — Tipos JSON polimórficos: struct plano con bytes originales

Un `ContentBlock` único con los campos de todas las variantes como opcionales, un
`UnmarshalJSON` que discrimina por `type`, y un campo `Raw json.RawMessage` que conserva los
bytes tal como llegaron. Esto da control total del orden de reserialización (requisito de D1) y
permite reemitir campos no modelados en vez de perderlos.

Descartado: `json.RawMessage` con dispatch en cada punto de uso (esparce el `switch` y permite
decodificaciones divergentes) e interfaz con registro de constructores (degenera en type
assertions y complica la serialización ordenada).

### D6 — Empaquetado

Cinco objetivos con `CGO_ENABLED=0`: `windows/amd64`, `linux/amd64`, `linux/arm64`,
`darwin/arm64`, `darwin/amd64`. Automatización con **Taskfile** (`make` no está disponible en la
máquina de desarrollo). Vocabulario BPE del tokenizer embebido con `go:embed`.

### D7 — Fases

Un solo spec (este documento) y **seis planes de implementación**, en orden de abajo hacia
arriba. El orden lo dicta el grafo de dependencias y el hecho de que las fases tempranas quedan
verificadas contra el Python desde el primer día gracias al corpus. Ver §10.

---

## 4. Alcance

### 4.1 Dentro, con paridad 1:1

- Los 6 endpoints funcionales (§7.1).
- Las 35 variables de entorno con nombres, tipos y valores por defecto exactos (§7.2).
- El CLI con exactamente los flags del original: `-H/--host`, `-p/--port`, `-v/--version`,
  `-h/--help`.
- Sistema multi-cuenta completo: circuit breaker, backoff exponencial capado, índice sticky
  global, reintento probabilístico y persistencia en `state.json`.
- Las tres fuentes de credenciales: JSON de Kiro Desktop, AWS SSO OIDC (incluido el caso
  Enterprise que resuelve `clientId`/`clientSecret` a partir de `clientIdHash`), y SQLite de
  kiro-cli con read-merge-write y respeto de `SQLITE_READONLY`.
- `web_search` vía MCP por sus dos caminos: tools nativas de servidor (Path A) e inyección
  automática (Path B).
- Máquina de estados de `<thinking>` con sus cuatro modos de manejo.
- Recuperación de argumentos de tool truncados.
- Guardas de tamaño de payload y recorte automático.
- Proxy y VPN, incluido SOCKS5.
- Los tres modos de `DEBUG_MODE`.
- Carga de `.env`, incluida la lectura en crudo de `KIRO_CREDS_FILE` y `KIRO_CLI_DB_FILE` para
  no corromper rutas de Windows con barras invertidas.

### 4.2 Réplica bug-for-bug, deliberada

Estos comportamientos son defectos o atajos del original que **se replican a propósito** porque
cambiarlos altera comportamiento observable:

| Comportamiento | Descripción |
|---|---|
| Parser oportunista del stream | `parsers.py` no decodifica el framing binario de AWS event-stream: descarta bytes inválidos y rescata JSON buscando prefijos literales. Se replica tal cual, sin decoder real ni siquiera detrás de un flag |
| Decodificación por chunk independiente | Un carácter multibyte partido entre dos chunks se corrompe. Ver §6.4 |
| Factor 1.15 del tokenizer | Corrección aplicada a los conteos de `cl100k_base` para aproximar la tokenización de Claude |
| `Connection: close` en streams | Mitigación de fugas de CLOSE_WAIT del original |
| HTTP/1.1 forzado | `httpx` no activa HTTP/2; Go sí por defecto en HTTPS, y hay que desactivarlo |

### 4.3 Fuera de alcance

| Elemento | Motivo |
|---|---|
| `/docs`, `/redoc`, `/openapi.json` | FastAPI los genera gratis; en Go habría que escribir y mantener un OpenAPI a mano para un proxy que nadie explora por navegador |
| Paridad literal de los errores 422 | Reproducir la forma exacta de Pydantic v2 (con sus campos `input` y `url`) es trabajo real para algo que ningún cliente consume. Se mantiene el envoltorio: `detail` con `loc`/`msg`/`type`, más `body` truncado a 500 caracteres |
| README en 7 idiomas | Solo inglés y español |
| Endpoint de gestión de cuentas en caliente | No existe en el original |

### 4.4 Dentro pero distinto

| Elemento | Cambio | Motivo |
|---|---|---|
| Imagen Docker | Imagen mínima con el binario estático en vez de `python:3.10-slim` | ~15 MB frente a ~150 MB, que es justo el objetivo del port |
| `GET /` y `GET /health` | JSON propio más rico: se mantienen los campos del original y se añaden cuenta activa, uptime y modo de operación | El usuario lo autorizó explícitamente; son healthchecks y nadie depende de su forma |
| Formato de logs de debug | Se replica el formato de texto, sin garantizar igualdad palabra por palabra de cada mensaje | El contenido de los logs no es contrato observable |
| Flag `--health` | Adición al CLI: hace un GET a `/health` y sale con código 0 o 1 | El healthcheck de Docker del original usa `httpx` desde dentro del contenedor, y en una imagen que solo contiene el binario no hay Python ni curl |

---

## 5. Arquitectura

### 5.1 Estructura del repositorio

```
kiro-gateway-go/
├── cmd/kiro-gateway/          # CLI, arranque, flag --health   (← main.py)
├── internal/                  # 30 paquetes espejo + 4 nuevos  (§5.2)
├── testdata/                  # corpus golden generado         (§8.1)
├── tools/corpus/              # plugin de pytest + script de generación
├── conformance/               # arnés de doble binario y Kiro falso
├── docs/
│   ├── MAPPING.md             # módulo Python → paquete Go, función a función
│   ├── DIFFERENCES.md         # diferencias conocidas con el original
│   ├── superpowers/specs/     # este documento
│   ├── superpowers/plans/     # los seis planes de implementación
│   └── es/README.md           # README en español
├── Taskfile.yml
├── Dockerfile
├── docker-compose.yml
├── .env.example
├── credentials.json.example
├── go.mod                     # module github.com/marr-cloud/kiro-gateway-go
├── LICENSE                    # AGPL-3.0
├── NOTICE                     # atribución al upstream
└── README.md
```

Todo el código vive bajo `internal/` para que no sea importable como librería: esto es un
binario, e `internal/` preserva la libertad de refactorizar sin romper a terceros.

### 5.2 Mapa de paquetes

Regla mecánica: nombre del módulo Python sin guiones bajos.

| Módulo Python | Paquete Go | Líneas Python |
|---|---|---|
| `main.py` | `cmd/kiro-gateway` + `internal/server` | 712 |
| `config.py` | `internal/config` | 581 |
| `utils.py` | `internal/utils` | 173 |
| `tokenizer.py` | `internal/tokenizer` | 327 |
| `parsers.py` | `internal/parsers` | 569 |
| `thinking_parser.py` | `internal/thinkingparser` | 385 |
| `truncation_state.py` | `internal/truncationstate` | 214 |
| `truncation_recovery.py` | `internal/truncationrecovery` | 112 |
| `payload_guards.py` | `internal/payloadguards` | 164 |
| `cache.py` | `internal/cache` | 182 |
| `model_resolver.py` | `internal/modelresolver` | 434 |
| `kiro_errors.py` | `internal/kiroerrors` | 141 |
| `network_errors.py` | `internal/networkerrors` | 436 |
| `account_errors.py` | `internal/accounterrors` | 134 |
| `exceptions.py` | `internal/validationerrors` *(renombrado)* | 106 |
| `models_openai.py` | `internal/modelsopenai` | 286 |
| `models_anthropic.py` | `internal/modelsanthropic` | 570 |
| `converters_core.py` | `internal/converterscore` | 1597 |
| `converters_openai.py` | `internal/convertersopenai` | 446 |
| `converters_anthropic.py` | `internal/convertersanthropic` | 488 |
| `streaming_core.py` | `internal/streamingcore` | 505 |
| `streaming_openai.py` | `internal/streamingopenai` | 688 |
| `streaming_anthropic.py` | `internal/streaminganthropic` | 949 |
| `routes_openai.py` | `internal/routesopenai` | 770 |
| `routes_anthropic.py` | `internal/routesanthropic` | 959 |
| `http_client.py` | `internal/httpclient` | 350 |
| `auth.py` | `internal/auth` | 977 |
| `account_manager.py` | `internal/accountmanager` | 897 |
| `mcp_tools.py` | `internal/mcptools` | 753 |
| `debug_logger.py` | `internal/debuglogger` | 403 |
| `debug_middleware.py` | `internal/debugmiddleware` | 116 |
| `__init__.py` | *(sin equivalente: solo reexporta)* | 137 |

Único renombrado: `exceptions.py` → `internal/validationerrors`, porque «exceptions» no
significa nada en Go.

Cuatro paquetes que no existen en el original:

| Paquete Go | Responsabilidad | Justificación |
|---|---|---|
| `internal/sse` | Formateo de eventos SSE (`event:`/`data:`/`\n\n`) | Rompe el ciclo `mcp_tools` ↔ `streaming_anthropic` y concentra el framing en un solo sitio |
| `internal/pyjson` | Emulación de `json.dumps` y `str()` de Python | Aísla las rarezas de Python en vez de dejarlas filtrarse (§6.2) |
| `internal/testutil` | Carga del corpus golden y generadores de chunks | Equivalente de los helpers `create_kiro_*_chunk` de `conftest.py` |
| `internal/server` | Router, middleware y ciclo de vida | Equivalente del `lifespan` de FastAPI, que en `main.py` está mezclado con el CLI |

### 5.3 Ciclos de importación y cómo se rompen

Python tolera dos ciclos que Go rechazaría al compilar:

**`utils` ↔ `auth`.** `auth.py:51` importa `get_machine_fingerprint` de `utils`, y `utils.py:35`
importa `KiroAuthManager` de `auth` bajo `TYPE_CHECKING`, o sea solo para anotar tipos.

*Solución:* `utils` declara la interfaz mínima que necesita y `auth.Manager` la satisface de
forma implícita. `utils` deja de depender de `auth`.

```go
// internal/utils
type TokenProvider interface {
    AccessToken(ctx context.Context) (string, error)
    ProfileARN() string
}
func GetKiroHeaders(ctx context.Context, tp TokenProvider) (http.Header, error)
```

**`mcp_tools` ↔ `streaming_anthropic`.** `mcp_tools.py:314` importa `format_sse_event` dentro de
una función, y `streaming_anthropic.py:356` importa `call_kiro_mcp_api` también dentro de una
función. Ambos son imports diferidos precisamente para sortear el ciclo.

*Solución:* extraer el formateo de SSE a `internal/sse`, que ambos importan. El ciclo desaparece
y el framing de SSE queda en un único lugar.

### 5.4 Flujo de una petición

Para `POST /v1/chat/completions` con `stream: true`:

```
net/http ServeMux
  → middleware: CORS → auth local (Bearer PROXY_API_KEY) → debug
    → routesopenai.ChatCompletions
      → modelresolver.Resolve            (alias → normalización → caché → oculto → passthrough)
      → accountmanager.Acquire           (sticky; salta cuentas con circuito abierto)
      → convertersopenai → converterscore.BuildKiroPayload
         │   cadena de normalización de roles, sanitizado de schemas de tools,
         │   inyección de etiquetas de thinking
         └── payloadguards.Check / Trim
      → httpclient.StreamWithRetry       ◄── FRONTERA 1: bytes idénticos a Python
        → bucle explícito, sin goroutines:
             resp.Body.Read(buf)
               → parsers.Feed(chunk) → []Event
                 → streamingcore aplica thinkingparser → []KiroEvent
                   → streamingopenai.Handle(ev, w)
                     → sse.FormatEvent → w.Write + Flusher.Flush
                                         ◄── FRONTERA 2: bytes idénticos a Python
```

Para `stream: false` el mismo bucle acumula en memoria y emite una única respuesta JSON, igual
que hace `collect_stream_response` en el original.

### 5.5 Ubicación de los timeouts

Tres timeouts en tres capas distintas, igual que en el original y por la misma razón.
**No existe un `http.Client.Timeout` global**: cortaría streams legítimos, y es el error clásico
al portar este tipo de proxy.

| Timeout | Valor | Dónde vive | Comportamiento al vencer |
|---|---|---|---|
| Conexión | 30 s | `http.Transport.DialContext` | Error de red, clasificado por `networkerrors` |
| Primer token | `FIRST_TOKEN_TIMEOUT` (15 s) | Deadline sobre la **primera** lectura del body, y solo sobre esa | Se aborta y se repite la petición completa hasta `FIRST_TOKEN_MAX_RETRIES` (3). Es seguro porque todavía no se escribió nada al cliente |
| Lectura entre chunks | `STREAMING_READ_TIMEOUT` (300 s) | Deadline renovado en cada lectura | Se cierra el stream y se propaga el error |

### 5.6 Modelo de concurrencia

Python se apoyaba en el GIL y en un único hilo de event loop. Go tiene paralelismo real, así que
cada estructura compartida necesita protección explícita.

| Estado compartido | Protección |
|---|---|
| Refresco de token | `golang.org/x/sync/singleflight`. Si N peticiones ven el token vencido a la vez, se hace **un** refresco y las N esperan el mismo resultado. Sin esto se dispararían N refrescos concurrentes contra AWS |
| Account manager | `sync.RWMutex` sobre el mapa de cuentas y el índice sticky |
| Caché de modelos | `sync.RWMutex`, TTL de `MODEL_CACHE_TTL` (3600 s) |
| Cachés de truncación | `sync.Mutex`, lectura destructiva |
| Guardado de `state.json` | Una goroutine con `context`, cada `STATE_SAVE_INTERVAL_SECONDS` (10 s), sustituyendo al `asyncio.create_task` del original |
| Parser del stream | Una instancia por petición: no se comparte |

---

## 6. Componentes

### 6.1 `internal/config`

Un struct poblado una vez al arranque. Precedencia: CLI sobre variable de entorno sobre valor por
defecto. Cuatro comportamientos del original que se replican:

1. **Lectura en crudo del `.env`.** `KIRO_CREDS_FILE` y `KIRO_CLI_DB_FILE` se leen del fichero
   `.env` sin procesar secuencias de escape, y solo si eso falla se recurre a la variable de
   entorno. Sin esto, una ruta de Windows como `C:\Users\x\creds.json` se corrompe.
2. **Degradación silenciosa.** Un valor no reconocido cae al valor por defecto sin error:
   `DEBUG_MODE=potato` resulta en `off`, y `FAKE_REASONING_HANDLING=xyz` en
   `as_reasoning_content`.
3. **Booleanos.** La regla general es `strings.ToLower(v)` contenido en `{"true","1","yes"}`.
4. **`FAKE_REASONING` va invertida.** Es la única: está activa salvo que el valor esté en
   `{"false","0","no","disabled","off"}`. Un valor vacío o ausente la **activa**.

### 6.2 `internal/pyjson`

Emula dos comportamientos de Python de los que depende el conteo de tokens, y por tanto el campo
`usage` que ven los clientes.

`json.dumps(x, ensure_ascii=False)` produce `{"a": 1, "b": 2}` con espacio tras los dos puntos y
tras la coma. `encoding/json` de Go produce `{"a":1,"b":2}`. Son cadenas distintas y por tanto
cuentan distinto número de tokens.

**Diseño:** `pyjson.Dumps` **reformatea los bytes JSON originales** en vez de serializar un mapa
de Go. Python hace `json.loads` y después `json.dumps`, y como el diccionario preserva el orden
del documento, el resultado es el JSON de entrada reformateado. Trabajando sobre los bytes
originales el orden de claves sale correcto por construcción, sin necesidad de mapas ordenados.
El único punto que requiere trabajo real son los números, que hay que reemitir con las reglas de
`repr` de Python (un float `1.0` se escribe `1.0`, no `1`).

```go
func Dumps(raw json.RawMessage) string  // emula json.dumps(x, ensure_ascii=False)
func Str(v any) string                  // emula str(x) de Python
```

`Str` es necesario porque el tokenizer hace `str(item.get("is_error"))`, y en Python `str(True)`
es `"True"` con mayúscula, no `"true"`. Cubre booleanos (`True`/`False`), `None` (`"None"`),
enteros, floats con reglas de `repr`, cadenas (idénticas) y contenedores con la sintaxis de
`repr` de Python.

Escapado: solo `"`, `\` y los caracteres de control por debajo de `0x20`. El resto se emite como
UTF-8 tal cual, que es lo que hace `ensure_ascii=False`.

### 6.3 `internal/tokenizer`

Vocabulario `cl100k_base` (~1,68 MB) embebido con `go:embed`, comprimido en el binario y
descomprimido en el primer uso con `sync.Once`. Es el único encoding que carga el original.

Las cuatro funciones de conteo (`CountTokens`, `CountMessageTokens`, `CountToolsTokens`,
`CountSystemTokens`) y su combinación `EstimateRequestTokens` se traducen literalmente,
incluyendo:

- Los tokens de servicio: 4 por mensaje, 4 por cada `tool_call`, y 3 al final del total.
- Coste fijo de 100 tokens por bloque de imagen.
- El factor `CLAUDE_CORRECTION_FACTOR = 1.15`, aplicado con truncamiento hacia cero
  (`int(total * 1.15)` en Python es `int(float64(total) * 1.15)` en Go: coinciden si el
  intermedio es `float64`).
- El fallback cuando el encoding no está disponible: `len(text)/4 + 1`. Ojo: es longitud en
  **caracteres Unicode**, o sea `len([]rune(s))` en Go, no `len(s)`.

El conteo no se usa para truncar nada: solo alimenta el `usage` de las respuestas, el
`input_tokens` de `/v1/messages/count_tokens` y los contadores del `web_search`.

### 6.4 `internal/parsers`

El componente de mayor riesgo del port.

```go
type Parser struct { /* buffer, lastContent, currentToolCall, toolCalls */ }
func (p *Parser) Feed(chunk []byte) []Event
```

Correspondencia directa con `feed(chunk: bytes) -> List[Dict]`, que ya es una máquina de estados
de tipo «entra chunk, salen eventos».

**Reconocimiento de eventos.** Se buscan siete prefijos literales en el buffer, y en cada
iteración se procesa el que aparezca en la posición más temprana:

```
{"content":   {"name":   {"input":   {"stop":
{"followupPrompt":   {"usage":   {"contextUsagePercentage":
```

Extraído el prefijo, un escáner de llaves con conciencia de cadenas y escapes
(`find_matching_brace`) delimita el objeto JSON completo. Si no cierra, se queda en el buffer
esperando más bytes.

**El detalle que decide si el port funciona.** El original hace:

```python
self.buffer += chunk.decode('utf-8', errors='ignore')
```

Cada chunk se decodifica **de forma independiente**. Si un carácter multibyte queda partido entre
dos chunks, los bytes parciales se descartan en ambos y el carácter se corrompe. Es un defecto
del original, y la paridad bug-for-bug exige replicarlo:

```go
p.buffer.Write(bytes.ToValidUTF8(chunk, nil))   // por chunk, sin reensamblar runas
```

Si se «arregla» reensamblando runas entre chunks, divergen los offsets del buffer y con ellos los
eventos emitidos.

**Verificación pendiente, no supuesta.** Creo que `bytes.ToValidUTF8(b, nil)` coincide con
`b.decode('utf-8', errors='ignore')` en todos los casos, pero no lo he comprobado. La fase 2
incluye un test diferencial que genera secuencias de bytes aleatorias, las pasa por Python y por
Go y compara la salida. Convierte una suposición en un hecho, y es barato.

También se portan: la deduplicación de contenido repetido, la agregación de `input` fragmentado
de tools, y el diagnóstico heurístico de truncación por llaves y comillas desbalanceadas que
alimenta a `truncationrecovery`.

### 6.5 `internal/thinkingparser`

Máquina de tres estados (`PreContent`, `InThinking`, `Streaming`) que detecta etiquetas de
razonamiento (`<thinking>`, `<think>`, `<reasoning>`, `<thought>`) **solo al principio** del
stream. Mantiene un buffer prudente de `FAKE_REASONING_INITIAL_BUFFER_SIZE` caracteres para no
partir una etiqueta de cierre entre dos chunks.

Cuatro modos según `FAKE_REASONING_HANDLING`: `as_reasoning_content` (por defecto), `remove`,
`pass` y `strip_tags`.

### 6.6 `internal/truncationstate` y `internal/truncationrecovery`

Dos cachés en memoria protegidas por `sync.Mutex`, indexadas por identificador de tool o por
SHA256 de los primeros 500 caracteres del contenido, con lectura destructiva: recuperar un
registro lo elimina. `truncationrecovery` solo genera los mensajes sintéticos de recuperación.

### 6.7 `internal/converterscore` y adaptadores

El bloque más grande (1.597 líneas en Python) y el que más casos golden va a tener.

`BuildKiroPayload` ensambla la estructura que espera Kiro:

```
conversationState
├── chatTriggerType
├── conversationId
├── currentMessage.userInputMessage
│   ├── content, modelId, origin: "AI_EDITOR", images
│   └── userInputMessageContext: { tools, toolResults }
└── history
```

**El orden de la cadena de normalización importa** y está documentado en el original como
resultado de dos incidencias concretas (#64 y #60):

```
ensureAssistantBeforeToolResults
  → mergeAdjacentMessages
    → ensureFirstMessageIsUser
      → normalizeMessageRoles
        → ensureAlternatingRoles
          → injectThinkingTags        (si FAKE_REASONING está activo)
```

Antes de la cadena: `processToolsWithLongDescriptions` mueve al system prompt toda descripción
que exceda `TOOL_DESCRIPTION_MAX_LENGTH` como `## Tool: {name}`, y `sanitizeJSONSchema` limpia
los esquemas de las tools. Después: las guardas de `payloadguards`.

Los dos adaptadores (`convertersopenai`, `convertersanthropic`) traducen su dialecto al tipo
unificado. La diferencia relevante entre ambos: en Anthropic el system prompt llega ya separado;
en OpenAI hay que extraerlo de la lista de mensajes.

### 6.8 `internal/streamingcore`, `internal/sse` y formatters

`streamingcore` transforma los eventos crudos del parser en `KiroEvent` unificados (tipos
`content`, `thinking`, `tool_use`, `usage`, `context_usage`, `error`) y aplica el parser de
thinking.

Los formatters implementan una interfaz común:

```go
type Formatter interface {
    Handle(ev KiroEvent, w io.Writer) error
    Finish(w io.Writer) error
}
```

Esta frontera es la que se graba para el corpus golden: `[]KiroEvent` de entrada, bytes de
salida. Ambos lados son serializables, así que es un caso de test puro.

**Dialecto OpenAI:** `data: {chunk}\n\n` con objetos `chat.completion.chunk`, cerrando con
`data: [DONE]\n\n`.

**Dialecto Anthropic:** `event: <tipo>\ndata: {...}\n\n` con la secuencia completa
`message_start` → `content_block_start`/`content_block_delta`/`content_block_stop` →
`message_delta` → `message_stop`, manteniendo índices de bloque coherentes entre bloques de
`thinking`, `text` y `tool_use`.

**Compromiso heredado que se replica:** Anthropic exige `input_tokens` en `message_start`, antes
de que Kiro haya emitido su `contextUsagePercentage`. El original recurre a
`estimate_request_tokens` en ese punto, y el port hace lo mismo.

El cálculo de tokens de prompt se deriva del porcentaje de uso de contexto que informa Kiro:
`prompt = total(context_usage × max_input) − completion(tiktoken)`.

### 6.9 `internal/httpclient`

Un `http.Transport` compartido:

| Ajuste | Valor | Motivo |
|---|---|---|
| `MaxIdleConns` | 100 | Equivale a `max_connections` de httpx |
| `MaxIdleConnsPerHost` | 20 | Equivale a `max_keepalive_connections` |
| `IdleConnTimeout` | 30 s | Equivale a `keepalive_expiry` |
| `ForceAttemptHTTP2` | `false` | `httpx` no activa HTTP/2; Go sí por defecto en HTTPS |
| `TLSNextProto` | mapa vacío | Necesario junto al anterior para desactivar HTTP/2 de verdad |

Cabecera `Connection: close` en las peticiones de streaming. Proxy con
`golang.org/x/net/proxy`, que cubre SOCKS5, respetando `HTTP_PROXY`, `HTTPS_PROXY`, `ALL_PROXY` y
`NO_PROXY`, y añadiendo `127.0.0.1,localhost` a `NO_PROXY` igual que el original.

Reintentos idénticos: un 403 fuerza el refresco del token y repite; 429 y 5xx esperan 1, 2 y 4
segundos (`BASE_RETRY_DELAY × 2^intento`, con `MAX_RETRIES = 3`).

Cabeceras salientes, byte a byte con el original:

```
Authorization: Bearer {token}
Content-Type: application/x-amz-json-1.0
x-amz-target: AmazonCodeWhispererStreamingService.GenerateAssistantResponse
User-Agent: aws-sdk-js/1.0.27 ua/2.1 os/win32#10.0.19044 lang/js md/nodejs#22.21.1
            api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.7.45-{fingerprint}
x-amz-user-agent: aws-sdk-js/1.0.27 KiroIDE-0.7.45-{fingerprint}
x-amzn-codewhisperer-optout: true
x-amzn-kiro-agent-mode: vibe
amz-sdk-invocation-id: {uuid}
amz-sdk-request: attempt=1; max=3
```

El `User-Agent` es una sola línea sin saltos: arriba aparece partido solo por ancho de página.
El fingerprint es `sha256("{hostname}-{username}-kiro-gateway")` en hexadecimal. Debe producir
exactamente el mismo valor que el original en la misma máquina.

### 6.10 `internal/auth`

Dos tipos de refresco:

| Tipo | Endpoint | Cuerpo |
|---|---|---|
| Kiro Desktop | `POST https://prod.{region}.auth.desktop.kiro.dev/refreshToken` | `{"refreshToken": "..."}` |
| AWS SSO OIDC | `POST https://oidc.{region}.amazonaws.com/token` | `{"grantType":"refresh_token","clientId":...,"clientSecret":...,"refreshToken":...}` en camelCase |

Tres fuentes de credenciales:

1. **JSON de Kiro Desktop.** Campos `refreshToken`, `accessToken`, `profileArn`, `region`,
   `expiresAt`. El token refrescado se persiste de vuelta al fichero.
2. **AWS SSO OIDC.** Se detecta por la presencia de `clientId` y `clientSecret`, ya sea
   directamente en el JSON o resueltos desde `~/.aws/sso/cache/{clientIdHash}.json` en el caso
   Enterprise.
3. **SQLite de kiro-cli.** Tabla `auth_kv`, claves por orden de prioridad:
   `kirocli:social:token`, luego `kirocli:odic:token`, luego `codewhisperer:odic:token`. El
   registro de dispositivo está en `kirocli:odic:device-registration` o
   `codewhisperer:odic:device-registration`. Campos en snake_case (`access_token`,
   `refresh_token`, `profile_arn`, `region`, `scopes`, `expires_at`). La región de la API se
   autodetecta del ARN en la tabla `state`, clave `api.codewhisperer.profile`.

**Escritura read-merge-write** en la SQLite: se lee el registro, se fusionan los campos
conocidos y se reescribe preservando los desconocidos. Respeta `SQLITE_READONLY`. Driver
`modernc.org/sqlite`, que es Go puro y es lo que permite `CGO_ENABLED=0`.

`expires_at` viene en RFC3339 con nanosegundos (9 dígitos) escritos por kiro-cli. Se parsea con
`time.RFC3339Nano` y con un fallback por expresión regular, igual que el original.

El refresco se dispara `TOKEN_REFRESH_THRESHOLD` (600 s) antes del vencimiento y va envuelto en
`singleflight`. Ante un 400 en OIDC se recarga la SQLite y se reintenta una vez. Si el refresco
falla pero el token todavía no ha vencido, se sigue usando: degradación grácil idéntica al
original.

Precedencia de la región de API: valor por cuenta en `credentials.json`, luego
`KIRO_API_REGION`, luego la detectada del ARN, luego `KIRO_REGION`.

### 6.11 `internal/accountmanager`

Estado por cuenta: contador de fallos consecutivos, instante del último fallo, modelos
disponibles con su TTL, y si está habilitada.

**Circuit breaker.** Tras un fallo la cuenta queda en cuarentena
`ACCOUNT_RECOVERY_TIMEOUT × 2^(fallos−1)` segundos, con el multiplicador capado a
`ACCOUNT_MAX_BACKOFF_MULTIPLIER` (1440, o sea 60 s × 1440 = un día). Una cuenta en cuarentena
puede reintentarse con probabilidad `ACCOUNT_PROBABILISTIC_RETRY_CHANCE` (0,1).

**Selección sticky.** Un índice global que solo avanza cuando la cuenta actual falla, de modo
que las peticiones consecutivas van a la misma cuenta.

**Clasificación de errores para failover**, literal del original, incluido lo contraintuitivo:

| Categoría | Códigos | Comportamiento |
|---|---|---|
| Recuperable | 402, 403, 429, y 400 con `INVALID_MODEL_ID` | Se pasa a la siguiente cuenta |
| Fatal | 400 con `CONTENT_LENGTH_EXCEEDS_THRESHOLD`, otros 400, 422, 5xx | Se devuelve al cliente tal cual |

Con una sola cuenta configurada se devuelve el error real. Con varias, tras completar el círculo
sin éxito, se devuelve 503 con el mensaje del último error.

**Descubrimiento de cuentas** desde `credentials.json`, que es un array de objetos con `type`
(`json`, `sqlite`, `refresh_token`), `enabled`, y opcionalmente `profile_arn`, `region` y
`api_region`. Si `path` apunta a un directorio, se escanea sin recursión. Un JSON es válido si
contiene `refreshToken` o `clientId`; una SQLite lo es si tiene la tabla `auth_kv`. El
identificador de cuenta es la ruta absoluta resuelta, o
`refresh_token_{sha256(token)[:16]}` para las de tipo `refresh_token`. El fichero de cuentas lo
nombra `ACCOUNTS_CONFIG_FILE` (por defecto `credentials.json`). **Nota de implementación:** el port
implementa SOLO este descubrimiento; no porta el modo de cuenta única del original
(`REFRESH_TOKEN`/`KIRO_CREDS_FILE`/`KIRO_CLI_DB_FILE` sueltos como fuente de cuenta) ni el gateo por
`ACCOUNT_SYSTEM` de la tabla §7.2, que queda inerte. Ver docs/DIFFERENCES.md §10.

**Persistencia de `state.json`.** Escritura a fichero temporal y `os.Rename`. Sobre Windows esto
**sí funciona**: `os.Rename` de Go usa `MoveFileEx` con reemplazo. Lo que puede fallar de forma
transitoria es el rename si otro proceso tiene el fichero abierto, típicamente un antivirus, así
que va con un reintento corto en vez de con una dependencia adicional.

### 6.12 `internal/modelresolver` y `internal/cache`

Resolución en cuatro capas: alias, normalización, caché dinámica, modelos ocultos, y si nada
encaja, passthrough del nombre a Kiro.

La normalización cubre: guiones a puntos, eliminación de fechas `YYYYMMDD`, eliminación del
sufijo `-latest`, formato invertido (`claude-4.5-opus-high` → `claude-opus-4.5`), sufijos de
ventana (`[1m]`, `[200k]`) y formato antiguo (`claude-3-7-sonnet` → `claude-3.7-sonnet`).

Catálogo estático (`FALLBACK_MODELS`), que en el original es una lista de objetos
`{"modelId": "..."}` y no de cadenas: `auto`, `claude-sonnet-4`, `claude-sonnet-4.5`,
`claude-sonnet-4.6`, `claude-haiku-4.5`, `claude-opus-4.5`, `claude-opus-4.6`,
`claude-opus-4.7`, `deepseek-3.2`, `glm-5`, `minimax-m2.1`, `minimax-m2.5`,
`qwen3-coder-next`. Alias: `auto-kiro` → `auto`. Oculto en el listado: `auto`.

Descubrimiento dinámico: contra el endpoint nuevo (`runtime.*.kiro.dev`) **no** se llama a
`ListAvailableModels` y se usa el catálogo estático. Contra el antiguo
(`q.*.amazonaws.com`) sí, con 3 reintentos y fallback al estático.

### 6.13 `internal/debuglogger` y `internal/debugmiddleware`

Tres modos: `off` no hace nada; `errors` acumula en memoria y solo escribe a disco si la petición
falla; `all` escribe siempre. Se vuelcan a `DEBUG_DIR` el cuerpo de la petición, el payload
enviado a Kiro, los chunks crudos del stream y los logs de la aplicación.

Los logs de aplicación por petición se capturan con un `slog.Handler` que escribe a un buffer
guardado en el `context`, replicando el formato del original:

```
{YYYY-MM-DD HH:mm:ss.SSS} | {LEVEL: <8} | {origen}:{función}:{línea} | {mensaje}
```

El middleware envuelve `/v1/chat/completions` y `/v1/messages`, y prepara el logger **antes** de
validar el cuerpo, para que un 422 también quede registrado.

### 6.14 `internal/mcptools`

`CallKiroMCPAPI` hace un POST a `{host}/mcp` con un cuerpo JSON-RPC 2.0
(`{"method":"tools/call","params":{"name":"web_search",...}}`) y timeout de 60 s. La respuesta
trae `result.content[0].text` como una cadena que a su vez contiene JSON, o sea que hay que
deserializar dos veces.

Dos caminos, ambos portados: Path A responde a tools nativas de servidor cuyo `type` empieza por
`web_search`; Path B inyecta la tool automáticamente si `WEB_SEARCH_ENABLED` está activo.

Los identificadores generados siguen patrones exactos que se replican: `srvtoolu_{32 hex}`,
`msg_{24 hex}` y `web_search_tooluse_{22}_{timestamp ms}_{8}`. El resumen de resultados se envía
troceado en fragmentos de 100 caracteres y envuelto en `<web_search>...</web_search>`.

### 6.15 Las cuatro taxonomías de error

Son cuatro paquetes independientes porque responden preguntas distintas, y mezclarlos es lo que
haría que un error de red se tratase como un error de cuenta.

**`internal/kiroerrors`** traduce lo que responde Kiro a un error del gateway. La entrada es el
código HTTP y el campo `reason` del cuerpo; la salida es un mensaje enriquecido. Cuatro casos con
texto literal que se conserva:

| Entrada | Mensaje |
|---|---|
| `CONTENT_LENGTH_EXCEEDS_THRESHOLD` | Informa de que se alcanzó el límite de contexto del modelo |
| `MONTHLY_REQUEST_COUNT` | Informa de que se superó la cuota mensual de peticiones |
| `INVALID_MODEL_ID` | Informa de ID de modelo inválido **o** nivel de suscripción insuficiente |
| `Improperly formed request.` con `reason` nula | Mensaje genérico con enlace al repositorio de incidencias |

**`internal/networkerrors`** clasifica fallos de transporte en categorías con mensaje legible:
resolución DNS, verificación de TLS, conexión rechazada, y timeout. Es donde el port cambia de
mecanismo: el original inspecciona tipos de excepción de `httpx`, y en Go se inspecciona con
`errors.As` sobre `*net.DNSError`, `*net.OpError`, `*tls.CertificateVerificationError` y
`os.ErrDeadlineExceeded`, más `errors.Is` para `context.DeadlineExceeded` y
`context.Canceled`. Las categorías y los mensajes son los mismos; los tipos que se examinan, no.

**`internal/accounterrors`** responde a una sola pregunta: ¿este error justifica cambiar de
cuenta? Devuelve `Fatal` o `Recoverable` según la tabla de §6.11.

**`internal/validationerrors`** produce la respuesta 422 cuando el cuerpo de la petición no
valida. Mantiene el envoltorio del original —`detail` con `loc`, `msg` y `type`, más `body`
truncado a 500 caracteres— sin perseguir la forma literal de Pydantic v2 (§4.3). Replica también
el saneado que convierte valores de tipo bytes a cadena, porque de lo contrario la respuesta no
es serializable a JSON.

---

## 7. Contratos observables

### 7.1 Endpoints

| Método | Ruta | Auth | Streaming | Respuesta |
|---|---|---|---|---|
| GET | `/` | pública | no | JSON de estado. Mantiene `status`, `message` y `version`; añade cuenta activa, uptime y modo |
| GET | `/health` | pública | no | JSON de salud. Mantiene `status`, `timestamp` y `version`; añade los mismos campos |
| GET | `/v1/models` | `Bearer` | no | `{"object":"list","data":[...]}` en formato OpenAI |
| POST | `/v1/chat/completions` | `Bearer` | sí | SSE con `chat.completion.chunk` y cierre `data: [DONE]`, o JSON `chat.completion` |
| POST | `/v1/messages` | `x-api-key` o `Bearer` | sí | SSE con eventos Anthropic, o JSON `message` |
| POST | `/v1/messages/count_tokens` | `x-api-key` o `Bearer` | no | `{"input_tokens": <int>}` |

CORS permisivo con `*` en orígenes, métodos y cabeceras, y credenciales permitidas, igual que el
original. `anthropic-version` se acepta y no se valida.

Formatos de error por dialecto:

```json
{"error": {"message": "...", "type": "kiro_api_error", "code": 429}}
{"type": "error", "error": {"type": "api_error", "message": "..."}}
```

Mensajes enriquecidos que se mantienen literales: `CONTENT_LENGTH_EXCEEDS_THRESHOLD` informa del
límite de contexto, `MONTHLY_REQUEST_COUNT` de la cuota mensual, e `INVALID_MODEL_ID` menciona el
nivel de suscripción.

### 7.2 Variables de entorno

Las 35, con nombre, tipo y valor por defecto exactos.

| Variable | Tipo | Default | Función |
|---|---|---|---|
| `PROXY_API_KEY` | str | `my-super-secret-password-123` | Clave que el gateway exige a sus clientes |
| `SERVER_HOST` | str | `0.0.0.0` | Interfaz de escucha |
| `SERVER_PORT` | int | `8000` | Puerto de escucha |
| `VPN_PROXY_URL` | str | `` | Proxy HTTP o SOCKS5 para alcanzar Kiro. Se añade `http://` si falta el esquema |
| `REFRESH_TOKEN` | str | `` | Refresh token de Kiro, alternativa a un fichero de credenciales |
| `PROFILE_ARN` | str | `` | ARN de CodeWhisperer, opcional |
| `KIRO_REGION` | str | `us-east-1` | Región de SSO y OIDC |
| `KIRO_API_REGION` | str | *(sin default)* | Fuerza la región de la API de Kiro |
| `KIRO_CREDS_FILE` | ruta | `` | JSON de credenciales. Se lee en crudo del `.env` |
| `KIRO_CLI_DB_FILE` | ruta | `` | SQLite de kiro-cli. Se lee en crudo del `.env` |
| `SQLITE_READONLY` | bool | `false` | Impide escribir tokens refrescados en la SQLite |
| `ACCOUNT_SYSTEM` | bool | `false` | Activa el sistema multi-cuenta |
| `ACCOUNTS_CONFIG_FILE` | ruta | `credentials.json` | Fichero de cuentas |
| `ACCOUNTS_STATE_FILE` | ruta | `state.json` | Estado persistido de las cuentas |
| `ACCOUNT_RECOVERY_TIMEOUT` | int (s) | `60` | Base del backoff del circuit breaker |
| `ACCOUNT_MAX_BACKOFF_MULTIPLIER` | float | `1440.0` | Tope del multiplicador de backoff |
| `ACCOUNT_PROBABILISTIC_RETRY_CHANCE` | float | `0.1` | Probabilidad de reintentar una cuenta en cuarentena |
| `ACCOUNT_CACHE_TTL` | int (s) | `43200` | TTL de la caché de modelos por cuenta |
| `STATE_SAVE_INTERVAL_SECONDS` | int | `10` | Periodo de guardado de `state.json` |
| `FIRST_TOKEN_TIMEOUT` | float (s) | `15` | Espera máxima del primer token |
| `FIRST_TOKEN_MAX_RETRIES` | int | `3` | Reintentos ante timeout de primer token |
| `STREAMING_READ_TIMEOUT` | float (s) | `300` | Espera máxima entre chunks |
| `FAKE_REASONING` | bool | `true` | Inyección de etiquetas de razonamiento. **Lógica invertida** |
| `FAKE_REASONING_MAX_TOKENS` | int | `4000` | Presupuesto de razonamiento por defecto |
| `FAKE_REASONING_BUDGET_CAP` | int | `10000` | Tope al presupuesto que pide el cliente. `0` desactiva el tope |
| `FAKE_REASONING_HANDLING` | enum | `as_reasoning_content` | Uno de `as_reasoning_content`, `remove`, `pass`, `strip_tags` |
| `FAKE_REASONING_INITIAL_BUFFER_SIZE` | int | `20` | Buffer inicial del detector de etiquetas |
| `WEB_SEARCH_ENABLED` | bool | `true` | Inyección automática de la tool `web_search` |
| `AUTO_TRIM_PAYLOAD` | bool | `false` | Recorta el historial si el payload excede el límite |
| `KIRO_MAX_PAYLOAD_BYTES` | int | `600000` | Tamaño máximo del payload hacia Kiro |
| `TOOL_DESCRIPTION_MAX_LENGTH` | int | `10000` | Umbral para mover descripciones al system prompt |
| `TRUNCATION_RECOVERY` | bool | `true` | Inyecta mensajes de recuperación ante truncados |
| `LOG_LEVEL` | str | `INFO` | Uno de TRACE, DEBUG, INFO, WARNING, ERROR, CRITICAL |
| `DEBUG_MODE` | enum | `off` | Uno de `off`, `errors`, `all`. Valor inválido cae a `off` |
| `DEBUG_DIR` | ruta | `debug_logs` | Directorio de volcados de debug |

Constantes no configurables que se mantienen: `TOKEN_REFRESH_THRESHOLD = 600`,
`MAX_RETRIES = 3`, `BASE_RETRY_DELAY = 1.0`, `MODEL_CACHE_TTL = 3600`,
`DEFAULT_MAX_INPUT_TOKENS = 200000`.

Plantillas de URL: `https://runtime.{region}.kiro.dev`,
`https://prod.{region}.auth.desktop.kiro.dev/refreshToken`,
`https://oidc.{region}.amazonaws.com/token`.

### 7.3 CLI

```
kiro-gateway [-H|--host HOST] [-p|--port PORT] [-v|--version] [-h|--help]
kiro-gateway --health          # adición: GET a /health, sale con 0 o 1
```

Precedencia: flag sobre variable de entorno sobre valor por defecto.

### 7.4 Versión reportada

`2.4.dev.13+go`. El prefijo indica con qué versión de upstream hay paridad; el sufijo identifica
la implementación, de modo que un informe de error sea inequívoco. No es semver válido, pero
tampoco lo es el `2.4.dev.13` del original, así que se conserva su esquema y se le añade el
sufijo. Ningún cliente parsea ese campo.

---

## 8. Estrategia de tests

### 8.1 Capa 1: corpus golden extraído del Python

Un plugin de pytest en `tools/corpus/` envuelve las funciones puras con un decorador que
serializa entrada y salida. Se ejecuta `pytest` una vez con el plugin activo, y cada llamada que
hacen los 1.662 tests existentes queda grabada.

```
testdata/<paquete>/<función>/<sha256 de la entrada>.json
```

**Deduplicación por SHA256 de la entrada, comparando la salida al colisionar.** Si dos llamadas
comparten digest de entrada, se comparan sus salidas (en las secuencias, los pasos): si coinciden
es un duplicado legítimo y se descarta; si no coinciden es un **conflicto**, que se registra en la
sección `conflicts` de `testdata/_report.json` y hace fallar `tools/corpus/validate.py` mientras no
esté declarado y justificado en `tools/corpus/floors.json`.

La regla original de esta sección era deduplicar solo por el hash de la entrada, y era
insuficiente: varias funciones del upstream no son puras, porque leen banderas de configuración a
nivel de módulo que los tests parchean, de modo que una misma entrada grabada podía tener dos
salidas y la segunda se descartaba en silencio contándose como duplicado. Sobre un corpus así el
criterio de terminado de §8.5 ("si todo caso del corpus pasa, hay paridad") es falso, porque el
corpus afirma una correspondencia entrada → salida que el propio original contradice. Con la regla
nueva esas banderas entran en la entrada grabada (`input.config`, declaradas por módulo en
`tools/corpus/targets.py`), y lo que no se puede completar así queda registrado como conflicto en
vez de desaparecer. El detalle operativo está en `docs/CORPUS.md`.

Si los argumentos de una llamada no son serializables, el caso se descarta sin romper la
grabación.

**Funciones instrumentadas:** el ensamblado del payload de Kiro y toda la cadena de normalización
de roles, los dos adaptadores de dialecto, el parser (`feed` y `find_matching_brace`), la máquina
de thinking, las cuatro funciones del tokenizer, la resolución de modelos, las guardas de
payload, los traductores de error, y los formatters SSE grabados en la frontera
`[]KiroEvent → bytes`.

**Reproducibilidad.** Python fijado a 3.10 con `uv` y dependencias instaladas desde
`tools/corpus/requirements.lock`, no resueltas de nuevo en cada máquina; tiempo y UUIDs
congelados en los mismos valores que ya usan los tests (`2024-01-01T12:00:00Z` y
`1704110400.0`); y la configuración del original fijada de forma explícita por la tarea
`corpus:record`, con la huella de los valores efectivos volcada en `_report.json`. Sin esto,
regenerar el corpus produce diferencias espurias y deja de servir como referencia.

**Presupuesto.** Tope determinista de 500 casos por función, eligiendo por hash ordenado, con un
total por debajo de 50 MB. El recorte es siempre el mismo.

**Lado Go.** `testutil.LoadCorpus(t, "converters_core/build_kiro_payload")` devuelve los casos, y el
test itera en formato table-driven con `t.Parallel()`. Un fallo imprime el diff contra el JSON de
Python e identifica el fichero exacto que lo reprodujo.

### 8.2 Capa 2: port a mano de los módulos con estado

`config`, `utils`, `httpclient`, `auth`, `accountmanager`, `cache`, `debuglogger`,
`debugmiddleware`, `server` y el CLI. La lista de casos se deriva de los nombres de los tests
Python, que están escritos de forma descriptiva.

Aislamiento de red total, igual que en el original: `httptest` y un Kiro falso. **Ninguna prueba
toca la red real.**

### 8.3 Capa 3: conformance de doble binario

Un servidor Kiro falso sirve streams de bytes grabados. El arnés lanza el gateway Python y el
binario Go contra él, envía las mismas peticiones a ambos y compara los bytes de respuesta.

Es el único mecanismo que detecta divergencias que ninguna de las dos capas anteriores anticipó,
y es la prueba de que la paridad de las dos fronteras de D1 se cumple de verdad.

### 8.4 Tests especiales

**Diferencial del decodificador UTF-8.** Genera secuencias de bytes aleatorias, incluidas
secuencias multibyte truncadas, las pasa por `bytes.decode('utf-8', errors='ignore')` de Python y
por `bytes.ToValidUTF8` de Go, y compara. Cierra la única suposición no verificada del diseño
(§6.4).

**Fingerprint.** Comprueba que el hash producido en la misma máquina coincide entre Python y Go.

### 8.5 Criterio de terminado

No es un porcentaje de cobertura. Son tres condiciones:

1. Todo caso del corpus golden pasa.
2. Cada módulo portado a mano tiene representados los casos de su equivalente Python.
3. El conformance de doble binario da cero diferencias de bytes.

---

## 9. Empaquetado y entrega

**Taskfile** con los objetivos `build`, `test`, `corpus`, `lint`, `conformance`, `release` y
`docker`. `corpus` crea el entorno con `uv`, ejecuta pytest con el plugin de grabación y
regenera `testdata/`.

**CI en GitHub Actions.** Tests en cada push. En cada tag, cross-compile de los cinco objetivos
con `CGO_ENABLED=0` y `-trimpath`, versión inyectada por `-ldflags`, y checksums publicados junto
a los binarios.

**Docker** multi-etapa con imagen final mínima y usuario no root. Mismas variables y mismo
puerto que el original. El healthcheck usa el flag `--health` del propio binario, porque en una
imagen que solo contiene el binario no hay Python ni curl.

**README** en inglés y español, escrito para alguien que llega sin contexto previo:

1. Qué es un gateway de este tipo y por qué querrías uno.
2. Cómo obtener credenciales de Kiro por cada una de las tres vías, con capturas de la
   estructura de ficheros esperada.
3. Arranque en tres comandos.
4. Tabla de las 35 variables, explicando qué hace cada una y en qué situación tocarla.
5. Guía de problemas frecuentes.
6. Aviso de licencia AGPL y atribución al upstream.

**`docs/DIFFERENCES.md`** con las diferencias conocidas: ausencia de `/docs`, `/redoc` y
`/openapi.json`; errores 422 sin paridad literal; `/` y `/health` con JSON más rico; flag
`--health`; e imagen Docker distinta.

---

## 10. Fases de implementación

Seis planes, en este orden. Cada uno produce algo verificable por sí mismo.

### Fase 1 — Andamiaje y corpus

Repositorio, `go.mod`, Taskfile, CI, `LICENSE`, `NOTICE`, `MAPPING.md`, el plugin de grabación y
la generación reproducible de `testdata/`.

*Entregable:* corpus generado con un solo comando, y CI en verde con un test trivial.

### Fase 2 — Cimientos puros

`config` (35 variables), `utils` (fingerprint y cabeceras), `pyjson`, `tokenizer` con el BPE
embebido, `parsers`, `thinkingparser`, `truncationstate`, `truncationrecovery`,
`payloadguards`, `modelresolver`, `cache`, y las cuatro taxonomías de error. Incluye el test
diferencial del decodificador UTF-8.

*Entregable:* todos estos paquetes verificados contra el corpus.

### Fase 3 — Modelos y converters

`modelsopenai`, `modelsanthropic` con el struct plano de D5, `converterscore` con la cadena de
normalización, y los dos adaptadores.

*Entregable:* el payload de Kiro generado en Go es byte a byte idéntico al de Python para todo el
corpus. Cierra la frontera 1 de D1.

### Fase 4 — Transporte

`httpclient` con reintentos y proxy, `auth` con las tres fuentes y el refresco en singleflight, y
`accountmanager` con el circuit breaker.

*Entregable:* tests a mano en verde contra un Kiro falso, incluidos los escenarios de failover.

### Fase 5 — Streaming y rutas

`sse`, `streamingcore`, los dos formatters, `routesopenai`, `routesanthropic` y `server` con su
ciclo de vida.

*Entregable:* **primer binario funcional end to end.** Los bytes SSE coinciden con los de Python
para todo el corpus. Cierra la frontera 2 de D1.

### Fase 6 — Extras, conformance y entrega

`mcptools`, `debuglogger`, `debugmiddleware`, la suite de conformance de doble binario, imagen
Docker, workflow de release para los cinco objetivos, README en dos idiomas y `DIFFERENCES.md`.

*Entregable:* release publicable con conformance en cero diferencias.

---

## 11. Riesgos y mitigaciones

| # | Riesgo | Impacto | Mitigación |
|---|---|---|---|
| 1 | El parser del stream. El atajo de descartar bytes inválidos y buscar prefijos JSON es el corazón del gateway y lo más frágil | Máximo: rompe todas las respuestas | Réplica literal, corpus golden de los 73 tests de `test_parsers.py`, y test diferencial del decodificador (§8.4) |
| 2 | Paridad de serialización JSON: separadores, escapado y orden de claves | Alto: altera el `usage` que ven los clientes | `pyjson` reformateando bytes originales (§6.2), acotado al tokenizer |
| 3 | SQLite de kiro-cli y escritura atómica del estado | Alto: corrupción persistente de credenciales | `modernc.org/sqlite`, read-merge-write, `SQLITE_READONLY`, rename con reintento |
| 4 | Cliente HTTP: HTTP/2, SOCKS5, timeouts | Alto: cuelgues y streams cortados | HTTP/2 desactivado explícitamente, sin timeout global, `x/net/proxy` |
| 5 | Concurrencia real frente al GIL | Medio: tormenta de refrescos de token | `singleflight` en el refresco, mutex en todo estado compartido |
| 6 | El corpus solo cubre lo instrumentado | Medio: falsa sensación de cobertura | Lista explícita de funciones instrumentadas, y el conformance como red de seguridad |
| 7 | Deriva de upstream durante el port | Bajo: el port apunta a un blanco móvil | Commit de origen fijado (`a5292ca`); los cambios posteriores se tratan como trabajo aparte |

**Lo que no se ha verificado y se asume:** el formato real de los bytes que devuelve Kiro no se
ha inspeccionado con un hexdump. El diseño no depende de saberlo, porque replica el parser
oportunista del original en vez de interpretar el framing, pero conviene tenerlo consciente.

---

## 12. Criterios de aceptación

1. Un único binario por plataforma, sin dependencias de runtime, construido con
   `CGO_ENABLED=0`, que arranca y sirve peticiones sin descargar nada.
2. Los 6 endpoints responden con la forma documentada en §7.1.
3. Las 35 variables de §7.2 se leen con los nombres y valores por defecto exactos.
4. Todo caso del corpus golden pasa.
5. El conformance de doble binario da cero diferencias de bytes en las dos fronteras de D1.
6. Los cinco objetivos de compilación producen binarios y checksums en el release.
7. `README.md` y `docs/es/README.md` permiten a alguien sin contexto previo poner el gateway en
   marcha, y explican cada una de las 35 variables.
8. `docs/MAPPING.md` cubre los 31 ficheros de `kiro/` más `main.py`, y `docs/DIFFERENCES.md` las
   diferencias de §4.3 y §4.4.
9. `LICENSE` es AGPL-3.0 y `NOTICE` atribuye el trabajo al upstream indicando el commit de
   origen.
