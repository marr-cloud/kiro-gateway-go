# Fase 5: Streaming y rutas — Plan de implementación

> **Para trabajadores agénticos:** SUB-SKILL OBLIGATORIA: usa
> `superpowers:subagent-driven-development` (recomendado) o `superpowers:executing-plans` para
> ejecutar este plan tarea por tarea. Los pasos usan sintaxis de casilla (`- [ ]`).

**Objetivo:** portar los ocho paquetes que llevan el chunk bruto de Kiro hasta bytes SSE en el
cliente, más el servidor HTTP con su ciclo de vida. Al cerrar la fase, `kiro-gateway` es un
binario funcional end-to-end: los seis endpoints de §7.1 responden, los bytes SSE coinciden con
Python para todo el corpus, y la frontera 2 de D1 queda cerrada.

**Arquitectura:** ocho paquetes bajo `internal/` (parsers, thinkingparser, truncationstate,
truncationrecovery, sse, streamingcore, streamingopenai, streaminganthropic) + dos paquetes de
rutas (routesopenai, routesanthropic) + un paquete de servidor (`internal/server`) + wire en
`cmd/kiro-gateway/main.go`. La cadena de una petición streaming ejecuta un bucle explícito sin
goroutines (D4), preservando la paridad byte-a-byte del framing SSE (frontera 2 de D1).

**Decisiones del spec confirmadas:**
- **D4 (pipeline):** bucle explícito `resp.Body.Read → parsers.Feed → streamingcore →
  formatter.Handle`. Sin goroutines por chunk, ni canales, ni pipelines concurrentes.
- **§6.4 defecto replicado:** `p.buffer.Write(bytes.ToValidUTF8(chunk, nil))` por chunk, sin
  reensamblar runas entre chunks. Bug-for-bug con `chunk.decode('utf-8', errors='ignore')` de
  Python. Si «se arregla», divergen offsets y eventos.
- **§6.8 formatters:** interfaz común `Handle(ev KiroEvent, w io.Writer) error` +
  `Finish(w io.Writer) error`. Frontera de tests: `[]KiroEvent` in, bytes out.
- **§7.1 endpoints:** los 6 exactos, con auth por dialecto (Bearer vs x-api-key/Bearer),
  CORS `*`, y formatos de error por dialecto (OpenAI `{error:{message,type,code}}`, Anthropic
  `{type:error,error:{type,message}}`).
- **§7.2 env vars:** 35 nombres exactos, con valores por defecto exactos. Ya cargadas por
  `internal/config` de la fase 2; esta fase las **consume** vía `*config.Config`.
- **§5.4 flujo:** `payloadguards.Check / Trim` (ya en `converterscore/payload.go` desde fase 3)
  se invoca antes de `httpclient`; converterscore levanta panic en 3 sitios (§10, deuda técnica
  fase 3) — esta fase añade el middleware `recover()` en el servidor.
- **§7.4 versión:** `2.4.dev.13+go`. Ya en `internal/version` de la fase 1.
- **Deuda técnica de fase 4 que esta fase resuelve:**
  1. Wire de las 7 package vars de `converterscore` desde `*config.Config` al arranque
     (`main.go`). Se toca aquí, en Task 10.
  2. Panic-recovery middleware HTTP (converterscore panica en 3 sitios). Task 10.
  3. Aplicación de `HIDDEN_MODELS` — deferred a `modelresolver` de fase 6; en fase 5 los
     endpoints `/v1/models` devuelven el catálogo de `accountmanager.GetAllAvailableModels()`
     sin post-procesar.
  4. Logging framework — sigue como TODO(logging) donde ya estaba; no se introduce ahora
     (esta fase deja placeholders `slog.Default()` a bajo volumen, con logger real en fase 6
     junto al `debuglogger`/`debugmiddleware`).

**Stack:** Go 1.27, `CGO_ENABLED=0`. Sin nuevas dependencias externas. Solo `net/http`,
`log/slog`, `context`, `os/signal`, `flag` y las 4 deps ya autorizadas (`golang.org/x/net`,
`golang.org/x/sync`, `modernc.org/sqlite`, indirectas).

**Spec:** `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md` — §5.4 flujo,
§6.4-6.8 componentes, §7.1-7.4 contratos, §8.2 tests capa 2, §10 fase 5, §11 riesgo #1 parser.

**Fase anterior:** `docs/superpowers/plans/2026-09-15-fase-4-transporte.md` (mergeada a `main`
como `683adf3`).

**Fuentes a portar:**

| Fichero upstream | Líneas | Paquete Go |
|---|---:|---|
| `parsers.py` | 568 | `internal/parsers` |
| `thinking_parser.py` | 384 | `internal/thinkingparser` |
| `truncation_state.py` | 214 | `internal/truncationstate` |
| `truncation_recovery.py` | 112 | `internal/truncationrecovery` |
| `streaming_core.py` | 504 | `internal/streamingcore` |
| `streaming_openai.py` | 687 | `internal/streamingopenai` |
| `streaming_anthropic.py` | 949 | `internal/streaminganthropic` |
| `routes_openai.py` | 769 | `internal/routesopenai` |
| `routes_anthropic.py` | 959 | `internal/routesanthropic` |
| (no upstream — servidor) | — | `internal/server` + `cmd/kiro-gateway/main.go` |

## Restricciones globales

- Module path `github.com/marr-cloud/kiro-gateway-go`, Go 1.27, `CGO_ENABLED=0`.
- Todo fichero `.go` (incluidos `_test.go`) lleva como dos primeras líneas:
  ```go
  // SPDX-License-Identifier: AGPL-3.0-or-later
  // Port a Go de jwadow/kiro-gateway. Ver NOTICE.
  ```
- **Sin nuevas dependencias externas.** Si algo parece pedir una, párate y reporta.
- Ningún test toca la red real ni la SQLite del usuario. Todo con `httptest.Server` y
  `t.TempDir()`.
- Nombres de paquete: `parsers`, `thinkingparser`, `truncationstate`, `truncationrecovery`,
  `sse`, `streamingcore`, `streamingopenai`, `streaminganthropic`, `routesopenai`,
  `routesanthropic`, `server`. Al terminar cada tarea, rellena la fila correspondiente en
  `docs/MAPPING.md`.
- Al cerrar la fase: `task lint` y `task test` en verde.
- Mensajes de commit en inglés, prefijos convencionales.
- **Nada de globals mutables nuevos.** Estado en structs; tests inyectan el struct. La única
  excepción autorizada son las 7 package vars de `converterscore` que ya existen desde fase 3;
  la Task 10 las asigna desde `*config.Config` al arranque **una vez**.
- **BINDING CONTRACT: los detalles vienen del upstream, no del brief.** Fases 2-4 encontraron
  desviaciones brief-vs-upstream en TODAS las tareas. Verifica cada función contra
  `.upstream/kiro/*.py` antes de escribir tests. Si el brief y el upstream discrepan, upstream
  gana. Ledger el ruling.

## Estructura de ficheros

| Fichero | Responsabilidad |
|---|---|
| `internal/parsers/parser.go` | `Parser.Feed(chunk) []Event`; máquina de estados con reconocimiento de 7 prefijos |
| `internal/parsers/brace_scanner.go` | `findMatchingBrace` con conciencia de cadenas y escapes |
| `internal/parsers/dedup.go` | Deduplicación de contenido repetido; agregación de `input` fragmentado |
| `internal/parsers/truncation.go` | Heurística de truncación por llaves/comillas desbalanceadas |
| `internal/parsers/parser_test.go` | Tests contra corpus golden de `testdata/parsers/` (73 casos) |
| `internal/parsers/differential_test.go` | Test diferencial vs Python: bytes aleatorios in, mismo output |
| `internal/thinkingparser/state_machine.go` | 3 estados: `PreContent`, `InThinking`, `Streaming` |
| `internal/thinkingparser/handling.go` | 4 modos: `as_reasoning_content`, `remove`, `pass`, `strip_tags` |
| `internal/thinkingparser/parser_test.go` | Corpus de `testdata/thinking_parser/` + tests hand-written |
| `internal/truncationstate/cache.go` | Dos cachés en memoria con `sync.Mutex`, lectura destructiva |
| `internal/truncationstate/cache_test.go` | Índices por tool ID y por SHA256(content[:500]) |
| `internal/truncationrecovery/recovery.go` | Genera mensajes sintéticos de recuperación |
| `internal/truncationrecovery/recovery_test.go` | Corpus de `testdata/truncation_recovery/` |
| `internal/sse/framing.go` | `FormatEvent(name string, data []byte) []byte` — un solo lugar para el framing SSE |
| `internal/sse/framing_test.go` | Hand-written; tests directos de framing |
| `internal/streamingcore/events.go` | `type KiroEvent` (content, thinking, tool_use, usage, context_usage, error) |
| `internal/streamingcore/pipeline.go` | Orquesta `parsers.Feed → thinkingparser → []KiroEvent` |
| `internal/streamingcore/pipeline_test.go` | Tests hand-written; sin corpus |
| `internal/streamingopenai/formatter.go` | `Formatter.Handle/Finish` → `data: {chunk}\n\n` + `[DONE]` |
| `internal/streamingopenai/formatter_test.go` | Corpus de `testdata/streaming_openai/` |
| `internal/streaminganthropic/formatter.go` | Secuencia completa `message_start` → `content_block_*` → `message_delta/stop` |
| `internal/streaminganthropic/block_index.go` | Índices coherentes entre thinking/text/tool_use |
| `internal/streaminganthropic/formatter_test.go` | Corpus de `testdata/streaming_anthropic/` |
| `internal/routesopenai/handler.go` | Handlers de `/v1/models` y `/v1/chat/completions` |
| `internal/routesopenai/failover.go` | Bucle de failover: GetNextAccount + Report + `ExhaustedAccountsError` |
| `internal/routesopenai/handler_test.go` | Tests con `httptest.Server` de Kiro falso |
| `internal/routesanthropic/handler.go` | Handlers de `/v1/messages` y `/v1/messages/count_tokens` |
| `internal/routesanthropic/failover.go` | Igual que en OpenAI pero con auth dual (x-api-key + Bearer) |
| `internal/routesanthropic/handler_test.go` | Tests con `httptest.Server` |
| `internal/server/middleware.go` | CORS + auth (Bearer/x-api-key) + panic-recovery |
| `internal/server/server.go` | HTTP server, `/health`, `/`, ciclo de vida, graceful shutdown |
| `internal/server/server_test.go` | Tests hand-written de middleware + rutas de estado |
| `cmd/kiro-gateway/main.go` | CLI (`-H/-p/-v/-h/--health`), wire config → server, wire de package vars de `converterscore` |
| `cmd/kiro-gateway/main_test.go` | Tests de precedencia flag > env > default (opcional) |

---

## Preflight — interfaces entre tareas

| Producer | Consumer | Surface | Finding |
|---|---|---|---|
| Task 1 (`parsers.Parser`, `Event`) | Tasks 5, 8, 9 | Chunk-in / events-out | Clean |
| Task 2 (`thinkingparser`) | Task 5 | Thinking extraction | Clean |
| Task 3 (`truncationstate`) | Task 4 | State cache | Clean |
| Task 4 (`truncationrecovery`) | Tasks 8, 9 | Synthetic messages on truncation | Clean |
| Task 5 (`sse.FormatEvent`) | Tasks 6, 7 (formatters) + fase 6 mcptools | SSE framing | Clean — resuelve ciclo `mcp_tools ↔ streaming_anthropic` de §5.3 |
| Task 6 (`streamingcore.KiroEvent`, `Pipeline`) | Tasks 7, 8, 9 | Pipeline | Clean |
| Task 7 (`streamingopenai.Formatter`) | Task 8 | OpenAI SSE | Clean |
| Task 8 (`streaminganthropic.Formatter`) | Task 9 | Anthropic SSE (con block indexing) | Clean |
| Task 9 (`routesopenai`) | Task 11 | OpenAI endpoints | Clean |
| Task 10 (`routesanthropic`) | Task 11 | Anthropic endpoints | Clean |
| Task 11 (`server`, `main`) | fase 6 conformance | End-to-end binary | Clean |

Sin ciclos.

---

## Task 1: `parsers` — SSE parser (RIESGO #1 del spec)

**Files:**
- Create: `internal/parsers/parser.go`
- Create: `internal/parsers/brace_scanner.go`
- Create: `internal/parsers/dedup.go`
- Create: `internal/parsers/truncation.go`
- Create: `internal/parsers/parser_test.go`
- Create: `internal/parsers/differential_test.go`
- Modify: `docs/MAPPING.md` (fila `parsers.py`)

**Interfaces:**
- Produces:
  ```go
  type Event struct {
      Kind  string            // "content" | "name" | "input" | "stop" | "followupPrompt" | "usage" | "contextUsagePercentage"
      Raw   []byte            // el JSON crudo del evento, para D5 (bytes originales)
      Value map[string]any    // parsed
  }
  type Parser struct { /* buffer, lastContent, currentToolCall, toolCalls, truncationHeuristic */ }
  func NewParser() *Parser
  func (p *Parser) Feed(chunk []byte) []Event
  func (p *Parser) Finish() []Event    // vacía buffer al final del stream
  func (p *Parser) TruncationDiagnosis() (kind string, ok bool)
  ```

**Detalles del original (`.upstream/kiro/parsers.py:1-568`):**
- **Los 7 prefijos exactos:** `{"content":`, `{"name":`, `{"input":`, `{"stop":`,
  `{"followupPrompt":`, `{"usage":`, `{"contextUsagePercentage":`. En cada iteración se procesa
  el que aparece en la posición más temprana del buffer.
- **`find_matching_brace`:** escáner que respeta cadenas dobles y escapes (`\"`, `\\`).
- **`self.buffer += chunk.decode('utf-8', errors='ignore')`:** cada chunk decodifica
  independiente. **NO se reensamblan runas entre chunks.** Go: `p.buffer.Write(bytes.ToValidUTF8(chunk, nil))`.
- **Deduplicación de contenido:** si el nuevo `content` es igual al anterior, se descarta.
- **Agregación de `input`:** un tool_call puede llegar en varios eventos `input`; se acumulan
  por `toolUseId`.
- **Heurística de truncación:** llaves y comillas desbalanceadas en el buffer residual al final
  → alimenta `truncationrecovery`.

**Corpus disponible:** `testdata/parsers/` con 4 subdirectorios (`AwsEventStreamParser`,
`deduplicate_tool_calls`, `find_matching_brace`, `parse_bracket_tool_calls`).

**Test diferencial (§8.4):** genera N bytes aleatorios (semilla fija), pasa por
`bytes.ToValidUTF8` y compara con la salida de `python -c "print(sys.stdin.buffer.read().decode('utf-8', errors='ignore'))"` bajo `.upstream/kiro/parsers.py`. Convierte la suposición del spec §6.4 en un hecho.

- [ ] **Paso 1:** Corpus loader hand-written en `parser_test.go` para los 4 subdirectorios de `testdata/parsers/`.
- [ ] **Paso 2:** Correr, fallar (paquete vacío).
- [ ] **Paso 3:** Implementar `brace_scanner.go` primero (más simple, aislado). Verificar con `find_matching_brace` corpus.
- [ ] **Paso 4:** Implementar `parser.go` (state machine + 7 prefijos), `dedup.go`, `truncation.go`.
- [ ] **Paso 5:** Correr corpus, pasar. Si algún caso falla, verificar upstream directamente y ledger el ruling.
- [ ] **Paso 6:** Escribir `differential_test.go` (fixture pequeña; genera 1000 secuencias con `rand.NewSource(42)`, guarda las esperadas como golden si Python no está disponible en CI).
- [ ] **Paso 7:** Commit `feat(parsers): SSE parser with dedup and truncation heuristic`.

**Constraints específicos:**
- El buffer debe ser `bytes.Buffer` o `strings.Builder` interno; no compartir slices con el llamador (D5 exige preservar bytes originales).
- `Event.Raw` es una copia del rango del buffer que contiene el JSON, NO un slice reutilizable.

---

## Task 2: `thinkingparser` — máquina de tres estados

**Files:**
- Create: `internal/thinkingparser/state_machine.go`
- Create: `internal/thinkingparser/handling.go`
- Create: `internal/thinkingparser/parser_test.go`
- Modify: `docs/MAPPING.md` (fila `thinking_parser.py`)

**Interfaces:**
- Consumes: `converterscore` package vars (`FakeReasoningEnabled`, `FakeReasoningHandling`, `FakeReasoningInitialBufferSize`).
- Produces:
  ```go
  type State int
  const (
      StatePreContent State = iota
      StateInThinking
      StateStreaming
  )
  type Parser struct { /* state, buffer, handling */ }
  func NewParser(handling string, initialBufferSize int) *Parser
  func (p *Parser) Feed(text string) (thinking string, content string)
  func (p *Parser) Finish() (thinking string, content string)
  ```

**Detalles del original (`.upstream/kiro/thinking_parser.py:1-384`):**
- Detecta 4 etiquetas al PRINCIPIO del stream: `<thinking>`, `<think>`, `<reasoning>`, `<thought>`.
- Buffer prudente de `FAKE_REASONING_INITIAL_BUFFER_SIZE` (default 20) chars para no partir cierre entre chunks.
- 4 modos:
  - `as_reasoning_content` (default): extrae thinking a `reasoning_content`, resto es contenido.
  - `remove`: descarta la parte de thinking.
  - `pass`: no filtra nada (todo va como contenido).
  - `strip_tags`: quita las etiquetas pero mantiene el texto.

**Corpus:** `testdata/thinking_parser/` — verificar subdirectorios.

- [ ] **Paso 1:** Tests hand-written cubriendo los 4 modos + los 4 nombres de etiqueta + el caso de etiqueta partida entre chunks.
- [ ] **Paso 2:** Corpus loader para `testdata/thinking_parser/`.
- [ ] **Paso 3:** Correr, fallar.
- [ ] **Paso 4:** Implementar `state_machine.go` + `handling.go`.
- [ ] **Paso 5:** Correr, pasar.
- [ ] **Paso 6:** Commit `feat(thinkingparser): three-state machine for reasoning tags`.

---

## Task 3: `truncationstate` — dos cachés en memoria

**Files:**
- Create: `internal/truncationstate/cache.go`
- Create: `internal/truncationstate/cache_test.go`
- Modify: `docs/MAPPING.md` (fila `truncation_state.py`)

**Interfaces:**
- Produces:
  ```go
  type State struct { /* toolCache, contentCache, mu sync.Mutex */ }
  func New() *State
  // Set y Get son destructivos en Get: recuperar un registro lo elimina.
  func (s *State) SetTool(toolID string, record any)
  func (s *State) GetTool(toolID string) (record any, ok bool)
  func (s *State) SetContent(contentFirst500 string, record any)   // internamente hashea con SHA256
  func (s *State) GetContent(contentFirst500 string) (record any, ok bool)
  ```

**Detalles del original (`.upstream/kiro/truncation_state.py:1-214`):**
- Dos `dict` protegidos con `threading.Lock`.
- Índice de contenido: `hashlib.sha256(content[:500]).hexdigest()`.
- Get destructivo: `dict.pop(key)`, retorna `None` si no existe.

- [ ] **Paso 1:** Tests hand-written: SetTool + GetTool retorna+elimina; SetContent + GetContent con SHA256; concurrencia (10 goroutines Set/Get).
- [ ] **Paso 2:** Correr, fallar.
- [ ] **Paso 3:** Implementar `cache.go`.
- [ ] **Paso 4:** Correr, pasar.
- [ ] **Paso 5:** Commit `feat(truncationstate): destructive-read caches by tool ID and content SHA`.

---

## Task 4: `truncationrecovery` — mensajes sintéticos

**Files:**
- Create: `internal/truncationrecovery/recovery.go`
- Create: `internal/truncationrecovery/recovery_test.go`
- Modify: `docs/MAPPING.md` (fila `truncation_recovery.py`)

**Interfaces:**
- Consumes: `internal/truncationstate.State`, `converterscore.TruncationRecoveryEnabled`.
- Produces:
  ```go
  // BuildToolResult devuelve el tool_result sintético si hay un registro previo
  // truncado en state.GetTool(toolID); si no, devuelve nil.
  func BuildToolResult(state *truncationstate.State, toolID string) (map[string]any, bool)

  // BuildUserMessage devuelve el mensaje user sintético si hay contenido truncado
  // previo en state.GetContent(contentFirst500).
  func BuildUserMessage(state *truncationstate.State, contentFirst500 string) (map[string]any, bool)
  ```

**Detalles del original (`.upstream/kiro/truncation_recovery.py:1-112`):**
- Solo si `TRUNCATION_RECOVERY=true`.
- El tool_result sintético usa un mensaje de error literal que se copia del upstream.
- El mensaje user sintético notifica al modelo del truncado.

**Corpus:** `testdata/truncation_recovery/`.

- [ ] **Paso 1:** Corpus loader.
- [ ] **Paso 2:** Tests hand-written para el caso de flag deshabilitada.
- [ ] **Paso 3:** Correr, fallar.
- [ ] **Paso 4:** Implementar.
- [ ] **Paso 5:** Correr, pasar.
- [ ] **Paso 6:** Commit `feat(truncationrecovery): synthetic recovery messages`.

---

## Task 5: `sse` — event framing helper

**Files:**
- Create: `internal/sse/framing.go`
- Create: `internal/sse/framing_test.go`
- Modify: `docs/MAPPING.md` (nueva fila — resuelve ciclo `mcp_tools ↔ streaming_anthropic`)

**Interfaces:**
- Produces:
  ```go
  // FormatEvent devuelve los bytes de un evento SSE. Si name == "" (dialecto OpenAI),
  // omite la línea "event:" y usa solo "data: {payload}\n\n". Si name != "" (dialecto
  // Anthropic), emite "event: {name}\ndata: {payload}\n\n".
  func FormatEvent(name string, data []byte) []byte

  // FormatDone devuelve "data: [DONE]\n\n" (solo OpenAI).
  func FormatDone() []byte
  ```

**Detalles del original:** `mcp_tools.py:314` importaba `format_sse_event` dentro de una función
para sortear el ciclo con `streaming_anthropic.py:356`. Al extraerlo a `internal/sse` el ciclo
desaparece.

- [ ] **Paso 1:** Tests hand-written (7-10 casos): OpenAI framing, Anthropic framing, payload con newlines, payload con `\r\n`, `[DONE]`.
- [ ] **Paso 2:** Correr, fallar.
- [ ] **Paso 3:** Implementar.
- [ ] **Paso 4:** Correr, pasar.
- [ ] **Paso 5:** Commit `feat(sse): SSE event framing helper`.

---

## Task 6: `streamingcore` — pipeline de eventos

**Files:**
- Create: `internal/streamingcore/events.go`
- Create: `internal/streamingcore/pipeline.go`
- Create: `internal/streamingcore/pipeline_test.go`
- Modify: `docs/MAPPING.md` (fila `streaming_core.py`)

**Interfaces:**
- Consumes: `internal/parsers`, `internal/thinkingparser`, `internal/converterscore` (para
  package vars).
- Produces:
  ```go
  type KiroEvent struct {
      Kind         string  // "content" | "thinking" | "tool_use" | "usage" | "context_usage" | "error"
      Content      string
      Thinking     string
      ToolUse      *ToolUseData     // si Kind == "tool_use"
      Usage        *UsageData       // si Kind == "usage"
      ContextUsage float64          // si Kind == "context_usage"
      Error        string           // si Kind == "error"
  }
  type ToolUseData struct {
      ID    string
      Name  string
      Input map[string]any
  }
  type UsageData struct {
      Input, Output, CacheRead, CacheCreation int
  }
  type Pipeline struct {
      parser   *parsers.Parser
      thinking *thinkingparser.Parser
  }
  func NewPipeline(handling string, initialBufferSize int) *Pipeline
  func (p *Pipeline) Feed(chunk []byte) []KiroEvent
  func (p *Pipeline) Finish() []KiroEvent
  ```

**Detalles del original (`.upstream/kiro/streaming_core.py:1-504`):**
- Aplica `thinkingparser` al contenido de eventos `content`; los eventos de tools y usage pasan
  intactos.
- Cuando `parser.Feed` devuelve una tanda de events, se traduce cada uno al `KiroEvent`
  correspondiente.
- El evento `contextUsagePercentage` se mapea a `Kind == "context_usage"` con el float directo.
- Si el buffer tiene truncación diagnosticada al final, emite un `KiroEvent{Kind: "error"}`
  con el diagnóstico (alimentado por `truncationrecovery` en la ruta que lo usa).

- [ ] **Paso 1:** Tests hand-written: chunk con content → KiroEvent content pasado por thinkingparser; chunk con tool_use → agregación correcta; chunk con usage → mapeo correcto; chunk con contextUsagePercentage → float.
- [ ] **Paso 2:** Correr, fallar.
- [ ] **Paso 3:** Implementar.
- [ ] **Paso 4:** Correr, pasar.
- [ ] **Paso 5:** Commit `feat(streamingcore): parsers → KiroEvent pipeline`.

---

## Task 7: `streamingopenai` — formatter OpenAI SSE

**Files:**
- Create: `internal/streamingopenai/formatter.go`
- Create: `internal/streamingopenai/formatter_test.go`
- Modify: `docs/MAPPING.md` (fila `streaming_openai.py`)

**Interfaces:**
- Consumes: `internal/streamingcore.KiroEvent`, `internal/sse.FormatEvent`, `internal/pyjson`,
  `internal/tokenizer` (para prompt-token estimation en el usage final).
- Produces:
  ```go
  type Formatter struct { /* completionID, model, created, aggregatedUsage */ }
  func New(model string) *Formatter
  func (f *Formatter) Handle(ev streamingcore.KiroEvent, w io.Writer) error
  func (f *Formatter) Finish(w io.Writer) error
  ```

**Detalles del original (`.upstream/kiro/streaming_openai.py:1-687`):**
- Cada evento emite un `chat.completion.chunk` con la forma OpenAI: `{id, object, created,
  model, choices:[{index, delta:{...}, finish_reason:null|"stop"|"tool_calls"}]}`.
- Tool calls: `delta.tool_calls[]` con `id`, `type: "function"`, `function.name`,
  `function.arguments` (streamed).
- Al final, `Finish` emite el `finish_reason: "stop"` con `usage` completo y luego
  `data: [DONE]\n\n`.
- Cálculo de prompt tokens: `prompt = total(context_usage × max_input) − completion(tiktoken)`
  usando `internal/tokenizer.EncodeChatCompletionText`.
- `id` = `chatcmpl-{uuid7}[:29]` (verificar formato exacto contra upstream).

**Corpus:** `testdata/streaming_openai/`.

- [ ] **Paso 1:** Corpus loader.
- [ ] **Paso 2:** Tests hand-written: content chunk, tool_call chunks, usage final, `[DONE]`.
- [ ] **Paso 3:** Correr, fallar.
- [ ] **Paso 4:** Implementar.
- [ ] **Paso 5:** Correr, pasar.
- [ ] **Paso 6:** Commit `feat(streamingopenai): OpenAI SSE formatter`.

---

## Task 8: `streaminganthropic` — formatter Anthropic SSE (el más pesado)

**Files:**
- Create: `internal/streaminganthropic/formatter.go`
- Create: `internal/streaminganthropic/block_index.go`
- Create: `internal/streaminganthropic/formatter_test.go`
- Modify: `docs/MAPPING.md` (fila `streaming_anthropic.py`)

**Interfaces:**
- Consumes: `internal/streamingcore.KiroEvent`, `internal/sse.FormatEvent`, `internal/pyjson`,
  `internal/tokenizer` (para prompt-token estimation), `internal/converterscore` (para
  `estimate_request_tokens` — el compromiso heredado).
- Produces:
  ```go
  type Formatter struct { /* messageID, model, blocks, currentBlockIdx, aggregatedUsage */ }
  func New(model string, requestForTokenEstimate map[string]any) *Formatter
  func (f *Formatter) Handle(ev streamingcore.KiroEvent, w io.Writer) error
  func (f *Formatter) Finish(w io.Writer) error
  ```

**Detalles del original (`.upstream/kiro/streaming_anthropic.py:1-949`):**
- Secuencia obligatoria: `message_start` → múltiples ciclos de
  `content_block_start` + `content_block_delta`* + `content_block_stop` → `message_delta`
  → `message_stop`.
- **`message_start` requiere `input_tokens` ANTES de que Kiro haya emitido `contextUsagePercentage`.**
  Solución del original: `estimate_request_tokens` en ese punto. Portada como
  `converterscore.EstimateRequestTokens(request)` — verificar disponibilidad; si no existe,
  ledger.
- Índices de bloque coherentes: thinking → índice 0; text → índice 1; tool_use → índices ≥2,
  uno por tool. `block_index.go` mantiene la coherencia.
- Cada bloque tiene 3 eventos: `content_block_start` (metadata), `content_block_delta`
  (parciales), `content_block_stop`.
- `message_delta` con `stop_reason` (`end_turn`, `tool_use`, `max_tokens`), y `usage` con
  output tokens.
- `message_stop` es el evento final.

**Corpus:** `testdata/streaming_anthropic/`.

- [ ] **Paso 1:** Corpus loader.
- [ ] **Paso 2:** Tests hand-written: thinking-then-text, thinking-then-tool_use, text-then-tool_use, error mid-stream.
- [ ] **Paso 3:** Correr, fallar.
- [ ] **Paso 4:** Implementar `block_index.go` primero, luego `formatter.go`.
- [ ] **Paso 5:** Correr, pasar.
- [ ] **Paso 6:** Commit `feat(streaminganthropic): Anthropic SSE formatter with block indexing`.

---

## Task 9: `routesopenai` — endpoints OpenAI-compatibles

**Files:**
- Create: `internal/routesopenai/handler.go`
- Create: `internal/routesopenai/failover.go`
- Create: `internal/routesopenai/handler_test.go`
- Modify: `docs/MAPPING.md` (fila `routes_openai.py`)

**Interfaces:**
- Consumes: `internal/accountmanager`, `internal/httpclient`, `internal/convertersopenai`,
  `internal/converterscore`, `internal/streamingcore`, `internal/streamingopenai`,
  `internal/accounterrors`.
- Produces:
  ```go
  type Handler struct { /* accounts, http, cfg */ }
  func New(accounts *accountmanager.Manager, client *httpclient.Client, cfg *config.Config) *Handler
  func (h *Handler) Models(w http.ResponseWriter, r *http.Request)              // GET /v1/models
  func (h *Handler) ChatCompletions(w http.ResponseWriter, r *http.Request)     // POST /v1/chat/completions
  ```

**Detalles del original (`.upstream/kiro/routes_openai.py:1-769`):**
- `/v1/models`: retorna `{"object":"list","data":[{"id":..., "object":"model", "created":..., "owned_by":"kiro"}...]}` con `accounts.GetAllAvailableModels()`.
- `/v1/chat/completions`:
  - Parsea request JSON, extrae `stream`.
  - Extrae system prompt de la lista de mensajes (diferencia con Anthropic).
  - Bucle de failover:
    ```go
    exclude := map[string]struct{}{}
    for {
        acc, err := h.accounts.GetNextAccount(model, exclude)
        if err != nil { /* &ExhaustedAccountsError → 503, else 500 */ }
        // convertersopenai → converterscore.BuildKiroPayload → payload guards
        // POST via httpclient.RequestWithRetry
        // si !2xx: h.accounts.ReportFailure(acc.ID, model, statusCode, reason, msg)
        //   si Fatal → propaga error real de Kiro al cliente (dialecto OpenAI)
        //   si Recoverable → exclude[acc.ID] = struct{}{}; continue
        // si 2xx: h.accounts.ReportSuccess(acc.ID, model)
        //   si stream: streamingcore.Pipeline + streamingopenai.Formatter loop
        //   si !stream: collectStreamResponse acumula y devuelve una sola respuesta
        //   return
    }
    ```

**Test scenarios:**
- [ ] `/v1/models` con 2 cuentas mockeadas → union sorted.
- [ ] `/v1/chat/completions` stream: mock Kiro devuelve corpus fixture; verificar bytes SSE finales.
- [ ] `/v1/chat/completions` no-stream: mock Kiro devuelve stream; verificar respuesta JSON única.
- [ ] Failover: cuenta 0 devuelve 500, cuenta 1 devuelve 200 → cliente recibe respuesta de cuenta 1.
- [ ] Multi-account exhausted → 503 con formato OpenAI error.
- [ ] Single-account Fatal (400+CONTENT_LENGTH_EXCEEDS_THRESHOLD) → error real de Kiro al cliente, no 503.

- [ ] **Paso 1:** Tests hand-written con `httptest.NewServer` como Kiro falso.
- [ ] **Paso 2:** Correr, fallar.
- [ ] **Paso 3:** Implementar `handler.go` + `failover.go`.
- [ ] **Paso 4:** Correr, pasar.
- [ ] **Paso 5:** Commit `feat(routesopenai): /v1/models and /v1/chat/completions`.

---

## Task 10: `routesanthropic` — endpoints Anthropic-compatibles

**Files:**
- Create: `internal/routesanthropic/handler.go`
- Create: `internal/routesanthropic/failover.go`
- Create: `internal/routesanthropic/handler_test.go`
- Modify: `docs/MAPPING.md` (fila `routes_anthropic.py`)

**Interfaces:**
- Consumes: como Task 9 + `internal/convertersanthropic`, `internal/streaminganthropic`.
- Produces:
  ```go
  type Handler struct { /* accounts, http, cfg */ }
  func New(accounts *accountmanager.Manager, client *httpclient.Client, cfg *config.Config) *Handler
  func (h *Handler) Messages(w http.ResponseWriter, r *http.Request)              // POST /v1/messages
  func (h *Handler) CountTokens(w http.ResponseWriter, r *http.Request)           // POST /v1/messages/count_tokens
  ```

**Detalles del original (`.upstream/kiro/routes_anthropic.py:1-959`):**
- `/v1/messages`: como OpenAI pero:
  - System prompt ya llega separado (diferencia clave con OpenAI).
  - Formatter Anthropic con block indexing.
  - Formato de error: `{"type":"error","error":{"type":"api_error","message":"..."}}`.
- `/v1/messages/count_tokens`: no toca Kiro; usa `internal/tokenizer` directamente y devuelve
  `{"input_tokens": <int>}`. NO requiere `GetNextAccount` (es puramente local).

**Test scenarios:** similares a Task 9 + count_tokens con hand-written cases.

- [ ] **Paso 1:** Tests hand-written.
- [ ] **Paso 2:** Correr, fallar.
- [ ] **Paso 3:** Implementar.
- [ ] **Paso 4:** Correr, pasar.
- [ ] **Paso 5:** Commit `feat(routesanthropic): /v1/messages and /v1/messages/count_tokens`.

---

## Task 11: `server` + `cmd/kiro-gateway/main.go` — ciclo de vida completo

**Files:**
- Create: `internal/server/middleware.go`
- Create: `internal/server/server.go`
- Create: `internal/server/server_test.go`
- Create: `cmd/kiro-gateway/main.go`
- Modify: `docs/MAPPING.md` (fila server/main)
- Modify: `README.md` — sección "Estado" al terminar (fase 5 completada).

**Interfaces:**
- Consumes: **todos** los paquetes anteriores de esta fase + `internal/config` +
  `internal/converterscore` (para el wire de package vars).
- Produces:
  ```go
  type Server struct { /* http.Server, accounts, cfg, logger */ }
  func New(cfg *config.Config, accounts *accountmanager.Manager, client *httpclient.Client) *Server
  func (s *Server) Start(ctx context.Context) error
  func (s *Server) Shutdown(ctx context.Context) error
  ```

**Middleware (§5.4):**
1. CORS permisivo (`*` orígenes, métodos, cabeceras; `credentials: true`).
2. Auth por endpoint:
   - `/` y `/health`: sin auth.
   - `/v1/models`, `/v1/chat/completions`: `Authorization: Bearer <PROXY_API_KEY>`.
   - `/v1/messages`, `/v1/messages/count_tokens`: `x-api-key: <PROXY_API_KEY>` OR `Authorization: Bearer <PROXY_API_KEY>`.
3. Panic recovery: si el handler panica (converterscore tiene 3 `panic` de fase 3),
   el middleware captura y devuelve 500 con formato de error según el path (OpenAI vs Anthropic).
4. `anthropic-version` header: se acepta, no se valida (spec §7.1).

**Endpoints de estado (§7.1):**
- `GET /`: JSON con `status`, `message`, `version`, cuenta activa (via `accounts.GetFirstAccount`), uptime (desde `Server.Start`), modo (`ACCOUNT_SYSTEM`).
- `GET /health`: JSON con `status`, `timestamp`, `version` y los mismos campos.

**Wire en `main.go`:**
- CLI flags: `-H/--host`, `-p/--port`, `-v/--version`, `-h/--help`, `--health`.
- Precedencia flag > env > default.
- `--version` → imprime `internal/version.String()` y sale con 0.
- `--health` → GET a `http://{host}:{port}/health`, imprime y sale con 0 (si OK) o 1 (si no).
- Config: `config.Load()` de fase 2.
- **Wire de las 7 package vars de converterscore** desde `cfg`:
  ```go
  converterscore.FakeReasoningEnabled = cfg.FakeReasoning
  converterscore.FakeReasoningMaxTokens = cfg.FakeReasoningMaxTokens
  converterscore.FakeReasoningBudgetCap = cfg.FakeReasoningBudgetCap
  converterscore.TruncationRecoveryEnabled = cfg.TruncationRecovery
  converterscore.ToolDescriptionMaxLength = cfg.ToolDescriptionMaxLength
  converterscore.AutoTrimPayload = cfg.AutoTrimPayload
  converterscore.KiroMaxPayloadBytes = cfg.KiroMaxPayloadBytes
  ```
- `httpclient.New(cfg)` → `accountmanager.NewManager(cfg)` → `LoadCredentials(ctx)` →
  `Initialize(ctx)` → `SaveStatePeriodically(ctx)` como goroutine.
- `server.New(cfg, accounts, http)` → `Start(ctx)`.
- Graceful shutdown: `signal.Notify(ctx.Done(), SIGINT, SIGTERM)` → `Shutdown(ctx)` con timeout
  30s.

**Test scenarios:**
- [ ] `GET /` no auth → 200 con JSON esperado.
- [ ] `GET /health` no auth → 200 con JSON esperado.
- [ ] `GET /v1/models` sin auth → 401.
- [ ] `GET /v1/models` con Bearer PROXY_API_KEY → 200.
- [ ] `POST /v1/messages` con x-api-key → 200. Con Bearer → 200. Sin ninguna → 401.
- [ ] Panic en handler → 500 con formato OpenAI o Anthropic según path.
- [ ] CORS preflight `OPTIONS /v1/chat/completions` → 200 con headers `*`.

- [ ] **Paso 1:** Tests hand-written de middleware y endpoints de estado.
- [ ] **Paso 2:** Correr, fallar.
- [ ] **Paso 3:** Implementar `middleware.go`, `server.go`.
- [ ] **Paso 4:** Correr, pasar.
- [ ] **Paso 5:** Implementar `cmd/kiro-gateway/main.go`.
- [ ] **Paso 6:** Verificar `go build -o kiro-gateway ./cmd/kiro-gateway/` produce un binario.
- [ ] **Paso 7:** Actualizar `README.md` "Estado": fase 5 completa, primer binario funcional, frontera 2 de D1 cerrada.
- [ ] **Paso 8:** Correr `go test ./...` full-repo verde.
- [ ] **Paso 9:** Commit `feat(server,cmd): HTTP server with middleware and CLI entrypoint`.
- [ ] **Paso 10:** Commit `docs: close phase 5 — streaming and routes`.

---

## Criterio de terminado

1. Los tres paquetes de streaming (`parsers`, `thinkingparser`, `truncationstate`,
   `truncationrecovery`, `sse`, `streamingcore`, los dos formatters) compilan y sus tests pasan.
2. Los dos paquetes de rutas (`routesopenai`, `routesanthropic`) tienen tests de failover
   verdes.
3. El servidor arranca, sirve `/`, `/health`, y los 4 endpoints de la API.
4. `go build -o kiro-gateway ./cmd/kiro-gateway/` produce un binario que:
   - `./kiro-gateway --version` imprime `2.4.dev.13+go`.
   - `./kiro-gateway --help` imprime la ayuda de las flags.
   - `./kiro-gateway -H 127.0.0.1 -p 8000` levanta el servidor (con credenciales configuradas).
5. `task lint`, `task test` verdes en todo el repo.
6. `docs/MAPPING.md` refleja los 8 paquetes nuevos + `server` + `cmd`.
7. Ninguna nueva dependencia externa en `go.mod`.
8. Ningún test toca red real (todo con `httptest.Server`).
9. La frontera 2 de D1 queda formalmente cerrada: los bytes SSE producidos por los formatters
   coinciden con los goldens del corpus para todo caso.

---

## Auto-review (crítica del propio plan)

**Cobertura de spec:**
- §6.4 parsers → Task 1 ✓
- §6.5 thinkingparser → Task 2 ✓
- §6.6 truncation → Tasks 3 + 4 ✓
- §6.8 streamingcore + sse + formatters → Tasks 5 + 6 + 7 + 8 ✓
- §7.1 endpoints → Tasks 9 + 10 + 11 ✓
- §7.2 env vars → consumidas vía `*config.Config` en cada task ✓
- §7.3 CLI → Task 11 ✓
- §7.4 versión → ya en `internal/version` de fase 1; consumida en Task 11 ✓
- §8.2 tests capa 2 → cada task tiene sus tests hand-written + corpus donde disponible ✓
- §11 riesgo #1 parser → Task 1 con corpus + differential test ✓

**Deuda técnica de fase 4 direccionada aquí:**
- Wire de 7 package vars converterscore → Task 11 ✓
- Panic recovery middleware → Task 11 ✓
- HIDDEN_MODELS → deferred a fase 6 (documentado en el header)
- Logging framework → deferred a fase 6 (documentado)

**Riesgos residuales:**
- Task 8 (streaminganthropic) es la más pesada (949 líneas upstream + block indexing +
  estimate_request_tokens hack). Si excede el presupuesto, romper en 8a (formatter base) + 8b
  (block indexing + token estimation).
- Task 9 y 10 (rutas) son grandes (~770 y ~960 líneas upstream). Si excede, romper cada una en
  2 sub-tasks (endpoint principal + endpoint secundario/failover).
- La detección de qué formatter usar (OpenAI vs Anthropic) vive en el dispatcher HTTP; no hay
  un handler unificado, cada ruta instancia su propio formatter (Task 9 y 10).

**Escenarios NO cubiertos aquí (fase 6):**
- MCP tools endpoint (`internal/mcptools`).
- Debug logger + debug middleware.
- Suite de conformance de doble binario.
- Docker image + workflow de release.
- README ES/EN completos + DIFFERENCES.md exhaustivo.
- `internal/modelresolver` + limpieza del subset duplicado en convertersanthropic/convertersopenai.
- Corpus regeneration (arreglar `tools/corpus/recorder.py` + regen).
