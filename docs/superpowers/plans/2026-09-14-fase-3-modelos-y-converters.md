# Fase 3: Modelos y converters — Plan de implementación

> **Para trabajadores agénticos:** SUB-SKILL OBLIGATORIA: usa
> `superpowers:subagent-driven-development` (recomendado) o `superpowers:executing-plans` para
> ejecutar este plan tarea por tarea. Los pasos usan sintaxis de casilla (`- [ ]`).

**Objetivo:** portar los tres paquetes que construyen el payload que se manda a Kiro:
`modelsopenai`, `modelsanthropic` y las tres piezas de conversión (`converterscore`,
`convertersopenai`, `convertersanthropic`). Al cerrar, el payload generado en Go es **byte a byte
idéntico** al de Python para los **1 356 casos golden** de los tres targets (§8.5 y frontera 1 de
D1).

**Arquitectura:** cinco paquetes, tres capas. Los dos de modelos son tipos con `UnmarshalJSON`,
sin lógica. `converterscore` es el motor (23 targets del corpus, 964 casos): expone tipos
unificados (`UnifiedMessage`, `UnifiedTool`, `ThinkingConfig`, `KiroPayloadResult`), la cadena de
normalización de roles y el ensamblado final del payload. Los dos adaptadores (OpenAI y Anthropic)
sólo traducen su dialecto al tipo unificado y llaman a `converterscore.BuildKiroPayload`.

**Decisión de arquitectura confirmada por el spec (D5):** los bloques de contenido polimórficos
son un único `ContentBlock` con los campos de todas las variantes opcionales, un `UnmarshalJSON`
que discrimina por `type`, y un campo `Raw json.RawMessage` que conserva los bytes originales.
Motivo: reemitir campos no modelados sin perderlos y controlar el orden de reserialización.

**Stack:** Go 1.27, `CGO_ENABLED=0`, **sin dependencias externas nuevas**. Consume `internal/pyjson`
(fase 2a) para reemitir números y `internal/tokenizer` (fase 2b) para los conteos que aparecen en
`build_kiro_payload`.

**Spec:** `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md` (§5.2 mapa de
paquetes, §6.7 converterscore y adaptadores, D5 tipos polimórficos, §10 Fase 3).
**Contrato del corpus:** `docs/CORPUS.md`.
**Fuentes a portar:**
- `.upstream/kiro/models_openai.py` (285 líneas)
- `.upstream/kiro/models_anthropic.py` (570 líneas)
- `.upstream/kiro/converters_core.py` (1 596 líneas)
- `.upstream/kiro/converters_openai.py` (445 líneas)
- `.upstream/kiro/converters_anthropic.py` (488 líneas)

**Fase anterior:** `docs/superpowers/plans/2026-09-05-fase-2b-tokenizer.md`.

## Restricciones globales

- Module path `github.com/marr-cloud/kiro-gateway-go`, Go 1.27, `CGO_ENABLED=0`.
- Todo fichero `.go`, incluidos los `_test.go`, lleva como dos primeras líneas:
  ```go
  // SPDX-License-Identifier: AGPL-3.0-or-later
  // Port a Go de jwadow/kiro-gateway. Ver NOTICE.
  ```
- **Ninguna dependencia externa nueva.** Sólo `pyjson` y `tokenizer` del proyecto, y la biblioteca
  estándar. Si crees que hace falta una dependencia, párate y repórtalo.
- Ningún test toca la red real.
- Nombres de paquete: `modelsopenai`, `modelsanthropic`, `converterscore`, `convertersopenai`,
  `convertersanthropic`. Al terminar cada tarea se rellena la fila correspondiente de
  `docs/MAPPING.md`.
- Al cerrar la fase, `task lint`, `task test` y `task corpus:validate` en verde.
- Mensajes de commit en inglés, prefijo convencional.
- **El fallo que da un test contra el corpus tiene que incluir el `Case.Name`** (hash de la
  entrada), o los diagnósticos son inservibles.

## Estructura de ficheros

Ficheros que crea esta fase, con su responsabilidad.

| Fichero | Responsabilidad |
|---|---|
| `internal/modelsopenai/models.go` | Tipos OpenAI: `ChatMessage`, `Tool`, `ChatCompletionRequest`, respuestas y chunks |
| `internal/modelsopenai/models_test.go` | Round-trip de `UnmarshalJSON` sobre entradas reales del corpus |
| `internal/modelsanthropic/blocks.go` | `ContentBlock` polimórfico con `Raw` y `UnmarshalJSON` (D5) |
| `internal/modelsanthropic/models.go` | Resto de tipos: `AnthropicMessage`, `AnthropicTool`, request, eventos |
| `internal/modelsanthropic/models_test.go` | Round-trip y discriminación por `type` |
| `internal/converterscore/types.go` | `ThinkingConfig`, `UnifiedMessage`, `UnifiedTool`, `KiroPayloadResult` |
| `internal/converterscore/extract.go` | Extractores puros: texto, imágenes, tool_results, tool_uses |
| `internal/converterscore/extract_test.go` | Tests contra 6 targets del corpus |
| `internal/converterscore/text.go` | `ToolCallsToText`, `ToolResultsToText` |
| `internal/converterscore/text_test.go` | Tests contra 2 targets del corpus |
| `internal/converterscore/tools.go` | `SanitizeJSONSchema`, `ProcessToolsWithLongDescriptions`, `ValidateToolNames`, `ConvertToolsToKiroFormat` |
| `internal/converterscore/tools_test.go` | Tests contra 4 targets del corpus |
| `internal/converterscore/images.go` | `ConvertImagesToKiroFormat`, `ConvertToolResultsToKiroFormat` |
| `internal/converterscore/images_test.go` | Tests contra 2 targets del corpus |
| `internal/converterscore/normalize.go` | La cadena de 6 funciones de normalización de mensajes |
| `internal/converterscore/normalize_test.go` | Tests contra 6 targets del corpus |
| `internal/converterscore/thinking.go` | `GetThinkingSystemPromptAddition`, `GetTruncationRecoverySystemAddition`, `InjectThinkingTags`, `StripAllToolContent` |
| `internal/converterscore/thinking_test.go` | Tests contra 4 targets del corpus |
| `internal/converterscore/payload.go` | `BuildKiroHistory`, `BuildKiroPayload` |
| `internal/converterscore/payload_test.go` | Test contra los 2 targets integradores (120 casos) |
| `internal/convertersopenai/converters.go` | Adaptador OpenAI → unificado → `BuildKiroPayload` |
| `internal/convertersopenai/converters_test.go` | Tests contra los 5 targets del corpus |
| `internal/convertersanthropic/converters.go` | Adaptador Anthropic → unificado → `BuildKiroPayload` |
| `internal/convertersanthropic/converters_test.go` | Tests contra los 9 targets del corpus |

**Presupuesto de casos golden a verificar en esta fase:**

- `converters_core/*` — 23 targets, 964 casos.
- `converters_openai/*` — 5 targets, 141 casos.
- `converters_anthropic/*` — 9 targets, 251 casos.
- **Total: 37 targets, 1 356 casos.**

---

## Cómo se consume el corpus

Recordatorio del cargador congelado en fase 1 (más detalle en `docs/CORPUS.md` y en el plan de
fase 2a):

```go
func LoadCorpus(tb testing.TB, target string) []Case
func Args(tb testing.TB, input json.RawMessage) []json.RawMessage
func Arg(tb testing.TB, input json.RawMessage, index int) json.RawMessage
func Kwarg(tb testing.TB, input json.RawMessage, name string) json.RawMessage
```

Cada `Case` tiene `Target`, `Kind`, `Input`, `Output`, `Steps`, `Notes` y `Name`. `Name` es el hash
del caso y **debe salir en cualquier mensaje de error**. Convención en esta fase:

```go
for _, c := range testutil.LoadCorpus(t, "converters_core/extract_text_content") {
    t.Run(c.Name, func(t *testing.T) {
        // ...
    })
}
```

**Comparación de salida.** Cuando `Output` es un JSON estructurado (dict o list), se comparan las
representaciones canónicas: se serializa la salida Go con `json.Marshal` y se compara con
`c.Output` **por igualdad de `json.RawMessage` normalizado**, no por igualdad de bytes crudos, para
evitar falsos negativos por diferencias de espaciado. La regla queda encapsulada en un helper
`testutil.AssertJSONEqual(t, got, want, caseName)` que **añades tú en la primera tarea que lo
necesite** (Tarea 2) y reutilizan las siguientes. Cuando la salida es una cadena, `==` directo.

---

## Tareas

### Task 1: Tipos de modelos (`modelsopenai` y `modelsanthropic`)

Traducir los dos módulos de Pydantic a structs Go. Sin lógica, sólo tipos que se serializan y
deserializan. La única complejidad real es el `ContentBlock` polimórfico de Anthropic (D5).

**Files:**
- Create: `internal/modelsopenai/models.go`
- Create: `internal/modelsopenai/models_test.go`
- Create: `internal/modelsanthropic/blocks.go`
- Create: `internal/modelsanthropic/models.go`
- Create: `internal/modelsanthropic/models_test.go`
- Modify: `docs/MAPPING.md` (filas `models_openai.py` y `models_anthropic.py`)

**Interfaces:**
- Produces:
  - `modelsopenai.ChatMessage`, `Tool`, `ToolFunction`, `ChatCompletionRequest`, `ChatCompletionResponse`, `ChatCompletionChunk`, `OpenAIModel`, `ModelList`.
  - `modelsanthropic.ContentBlock` con campos `Type string`, `Text *string`, `Thinking *string`, `Signature *string`, `Name *string`, `Input json.RawMessage`, `ID *string`, `ToolUseID *string`, `Content json.RawMessage`, `IsError *bool`, `Source *ImageSource`, `Raw json.RawMessage`.
  - `modelsanthropic.AnthropicMessage{Role string; Content []ContentBlock}` con `UnmarshalJSON` que acepta también una cadena y la convierte en un único bloque de tipo `text`.
  - `modelsanthropic.AnthropicTool`, `AnthropicMessagesRequest`, y los tipos de eventos de streaming.

- [ ] **Paso 1: Escribir los tests que fallan.**

  En `internal/modelsopenai/models_test.go`, un test que carga casos del corpus
  `converters_openai/convert_openai_messages_to_unified` y comprueba que **`json.Unmarshal` sobre
  el arg 0 no falla y round-trippea a JSON equivalente**:

  ```go
  func TestChatMessageRoundTrip(t *testing.T) {
      for _, c := range testutil.LoadCorpus(t, "converters_openai/convert_openai_messages_to_unified") {
          t.Run(c.Name, func(t *testing.T) {
              raw := testutil.Arg(t, c.Input, 0) // []ChatMessage serializado
              var msgs []modelsopenai.ChatMessage
              if err := json.Unmarshal(raw, &msgs); err != nil {
                  t.Fatalf("case %s: unmarshal: %v", c.Name, err)
              }
              // No re-marshal check aquí, sólo que decodifica.
          })
      }
  }
  ```

  En `internal/modelsanthropic/models_test.go`, dos tests: (a) el mismo round-trip sobre
  `converters_anthropic/convert_anthropic_messages` y (b) la discriminación explícita, con casos a
  mano para cada valor de `type` (`text`, `thinking`, `tool_use`, `tool_result`, `image`,
  `tool_reference`) verificando que el `ContentBlock` lo captura y que `Raw` preserva los bytes
  originales.

- [ ] **Paso 2: Correr los tests para verificar que fallan.**

  ```
  go test ./internal/modelsopenai ./internal/modelsanthropic
  ```
  Expected: FAIL con paquete no encontrado.

- [ ] **Paso 3: Implementar los tipos.**

  - `modelsopenai/models.go`: mapa 1:1 con `models_openai.py`. Todos los campos opcionales de
    Pydantic van como punteros o slices `nil-able`. El campo `content` de `ChatMessage` es
    `json.RawMessage` (puede ser string o lista), lo desempaqueta el converter, no el struct.
  - `modelsanthropic/blocks.go`: `ContentBlock` con `UnmarshalJSON(data []byte) error` que:
    1. Guarda `data` en `Raw`.
    2. Decodifica en una struct auxiliar para leer `type` y todos los campos opcionales.
    3. No falla si aparecen campos desconocidos: los deja en `Raw` para la reemisión.
  - `modelsanthropic/models.go`: resto. `AnthropicMessage.UnmarshalJSON` acepta también una cadena
    y la convierte en `[]ContentBlock{{Type: "text", Text: ptr(s)}}`, replicando el validador
    `field_validator` del original.

- [ ] **Paso 4: Correr los tests y ver que pasan.**

  ```
  go test ./internal/modelsopenai ./internal/modelsanthropic -v
  ```
  Expected: PASS.

- [ ] **Paso 5: Actualizar `docs/MAPPING.md`** con los nombres de fichero.

- [ ] **Paso 6: Commit.**

  ```
  git add internal/modelsopenai internal/modelsanthropic docs/MAPPING.md
  git commit -m "feat(models): port OpenAI and Anthropic type models"
  ```

---

### Task 2: `converterscore/extract.go` — extractores puros

Cuatro funciones que sacan piezas de una entrada polimórfica. Son las funciones más "llamadas" del
corpus (339 casos en total).

**Files:**
- Create: `internal/converterscore/types.go`
- Create: `internal/converterscore/extract.go`
- Create: `internal/converterscore/extract_test.go`
- Modify: `internal/testutil/corpus.go` (añadir `AssertJSONEqual`)
- Modify: `internal/testutil/corpus_test.go` (test del helper)

**Interfaces:**
- Consumes: `internal/testutil`, `encoding/json`.
- Produces:
  ```go
  type ThinkingConfig struct { Enabled bool; BudgetTokens *int }
  type UnifiedMessage struct { Role string; Content any; ToolCalls []map[string]any; ToolResults []map[string]any; Images []map[string]any }
  type UnifiedTool struct { Name string; Description string; InputSchema map[string]any }
  type KiroPayloadResult struct { Payload map[string]any; ToolDocumentation string }

  func ExtractTextContent(content any) string
  func ExtractImagesFromContent(content any) []map[string]any
  func ExtractToolResultsFromContent(content any) []map[string]any
  func ExtractToolUsesFromMessage(msg UnifiedMessage) []map[string]any
  ```

- [ ] **Paso 1: Añadir `AssertJSONEqual` a `internal/testutil`.**

  ```go
  // AssertJSONEqual compares got and want as canonicalised JSON. Fails the test citing caseName.
  func AssertJSONEqual(tb testing.TB, got any, want json.RawMessage, caseName string) {
      tb.Helper()
      gotBytes, err := json.Marshal(got)
      if err != nil { tb.Fatalf("case %s: marshal got: %v", caseName, err); return }
      var gotN, wantN any
      _ = json.Unmarshal(gotBytes, &gotN)
      _ = json.Unmarshal(want,     &wantN)
      if !reflect.DeepEqual(gotN, wantN) {
          tb.Fatalf("case %s:\n got: %s\nwant: %s", caseName, string(gotBytes), string(want))
      }
  }
  ```

  Y un test contra un par de fixtures sintéticas.

- [ ] **Paso 2: Escribir los tests que fallan** en `extract_test.go`.

  Una tabla con los cinco targets:
  ```go
  targets := []string{
      "converters_core/extract_text_content",
      "converters_core/extract_images_from_content",
      "converters_core/extract_tool_results_from_content",
      "converters_core/extract_tool_uses_from_message",
  }
  ```
  Cada uno itera los casos, decodifica `Arg(0)`, llama a la función, y usa `AssertJSONEqual`
  contra `c.Output`.

- [ ] **Paso 3: Correr los tests, ver que fallan.**

  ```
  go test ./internal/converterscore
  ```
  Expected: FAIL con `undefined: ExtractTextContent`.

- [ ] **Paso 4: Implementar `types.go` y `extract.go`.**

  Traducción literal de las funciones `extract_text_content`, `extract_images_from_content`,
  `extract_tool_results_from_content` y `extract_tool_uses_from_message` de `converters_core.py`.
  Detalles a preservar: el orden de comprobación de `type`, el fallback a `str()` (`pyjson.Str`)
  cuando el valor no es ni string ni list ni dict, y el paso silencioso de `image` y
  `tool_reference` en `extract_text_content` (línea 170 del original).

- [ ] **Paso 5: Correr los tests, ver que pasan (339 casos).**

  ```
  go test ./internal/converterscore -run TestExtract -v
  ```
  Expected: PASS.

- [ ] **Paso 6: Commit.**

  ```
  git add internal/converterscore internal/testutil
  git commit -m "feat(converterscore): extractors and unified types"
  ```

---

### Task 3: `converterscore/text.go` — formateo de tool_calls y tool_results

Dos funciones que traducen listas de tool_calls y tool_results a la representación textual
`<tool_use>...</tool_use>` que Kiro entiende. **Aquí es donde `pyjson.Dumps` empieza a ganarse su
existencia**: los argumentos serializados deben salir con los separadores de Python (`", "` y
`": "`) porque el conteo de tokens depende de ello.

**Files:**
- Create: `internal/converterscore/text.go`
- Create: `internal/converterscore/text_test.go`

**Interfaces:**
- Consumes: `internal/pyjson.Dumps`, `internal/pyjson.Str`.
- Produces:
  ```go
  func ToolCallsToText(toolCalls []map[string]any) string
  func ToolResultsToText(toolResults []map[string]any) string
  ```

- [ ] **Paso 1: Tests que fallan** con dos targets del corpus:
  `converters_core/tool_calls_to_text` (22 casos) y `converters_core/tool_results_to_text`
  (29 casos). Comparar con `==` porque la salida es una cadena.

- [ ] **Paso 2: Correr, ver fallar.**

- [ ] **Paso 3: Implementar.** Portar literalmente de `converters_core.py:826-908`. La
  serialización de argumentos usa `pyjson.Dumps` sobre el `input`; los `content` de tool_results
  pasan por `ExtractTextContent`; el manejador de `is_error` usa `pyjson.Str` sobre `True`/`False`.

- [ ] **Paso 4: Correr, ver pasar (51 casos).**

- [ ] **Paso 5: Commit.**

  ```
  git commit -m "feat(converterscore): text formatters for tool calls and results"
  ```

---

### Task 4: `converterscore/tools.go` — schemas y descripciones de tools

Cuatro funciones que preparan las tools antes de mandárselas a Kiro. La secuencia importa: primero
validar los nombres (§4 del original), luego mover descripciones largas al system prompt, luego
sanitizar los schemas.

**Files:**
- Create: `internal/converterscore/tools.go`
- Create: `internal/converterscore/tools_test.go`

**Interfaces:**
- Consumes: `internal/config` (para leer `TOOL_DESCRIPTION_MAX_LENGTH` en el paso previo — pero
  aquí se pasa como argumento, la función no lee config global).
- Produces:
  ```go
  func SanitizeJSONSchema(schema map[string]any) map[string]any
  func ProcessToolsWithLongDescriptions(tools []UnifiedTool, maxLen int) (processed []UnifiedTool, systemPromptAddition string)
  func ValidateToolNames(tools []UnifiedTool) error  // devuelve *validationerrors.Error
  func ConvertToolsToKiroFormat(tools []UnifiedTool) []map[string]any
  ```

- [ ] **Paso 1: Tests que fallan** con cuatro targets: `sanitize_json_schema` (25 casos),
  `process_tools_with_long_descriptions` (30), `validate_tool_names` (25),
  `convert_tools_to_kiro_format` (27).

  `validate_tool_names` es especial: si el corpus da `Kind: "raise"` o similar, el test verifica
  que la función devuelve un error, y que su mensaje coincide con `c.Output.error_message`. Ver
  `testutil.IsException` y `testutil.DecodeException` en la fase 2a.

- [ ] **Paso 2-4: Fallar, implementar, pasar** contra los 107 casos.

  Detalles del original que hay que preservar:
  - `SanitizeJSONSchema` recorre recursivamente y **elimina** las claves `additionalProperties`,
    `$schema`, `definitions`, `$defs`, `$ref` y `default: null`. Es una lista corta, exacta.
  - `ProcessToolsWithLongDescriptions` compara `len(description)` en **caracteres Unicode** (no
    bytes), o sea `len([]rune(desc))`. Si supera `maxLen`, mueve la descripción al system prompt
    con el formato `## Tool: {name}\n\n{description}\n\n` y deja la descripción del tool en
    `Description of this tool has been moved to the system prompt.`. Ojo: el orden en el
    system prompt es el orden de aparición.

- [ ] **Paso 5: Commit.**

  ```
  git commit -m "feat(converterscore): tool schema sanitization and long-description handling"
  ```

---

### Task 5: `converterscore/images.go` — imágenes y tool_results a formato Kiro

Dos funciones que convierten al formato binario que quiere Kiro. `ConvertImagesToKiroFormat` es
donde se decodifica el base64 y se reemite; `ConvertToolResultsToKiroFormat` extrae las imágenes
embebidas en tool_results y las envía por el mismo canal.

**Files:**
- Create: `internal/converterscore/images.go`
- Create: `internal/converterscore/images_test.go`

**Interfaces:**
- Produces:
  ```go
  func ConvertImagesToKiroFormat(images []map[string]any) []map[string]any
  func ConvertToolResultsToKiroFormat(toolResults []map[string]any) []map[string]any
  ```

- [ ] **Paso 1: Tests que fallan** contra `converters_core/convert_images_to_kiro_format`
  (25 casos) y `converters_core/convert_tool_results_to_kiro_format` (17 casos).

- [ ] **Paso 2-4: Fallar, implementar, pasar** (42 casos).

  El formato Kiro es `{"format": "jpeg", "source": {"bytes": "<base64>"}}` con `format` inferido
  del `media_type`. Si el `media_type` no está en el mapa `{image/jpeg, image/png, image/gif,
  image/webp}` se descarta la imagen (no error). Todo esto está en `converters_core.py:641-744`.

- [ ] **Paso 5: Commit.**

  ```
  git commit -m "feat(converterscore): image and tool-result Kiro-format conversion"
  ```

---

### Task 6: `converterscore/normalize.go` — la cadena de seis funciones

El bloque más delicado. Seis funciones que normalizan la lista de `UnifiedMessage` antes de
mandarla a Kiro, **en el orden documentado en el spec §6.7** y en `converters_core.py:1071-1318`.
Orden documentado como resultado de las incidencias #64 y #60 del upstream:

```
ensure_assistant_before_tool_results
  → merge_adjacent_messages
    → ensure_first_message_is_user
      → normalize_message_roles
        → ensure_alternating_roles
```

`strip_all_tool_content` no forma parte de la cadena principal (se llama sólo cuando se aborta la
inyección de tools) y va en la tarea siguiente.

**Files:**
- Create: `internal/converterscore/normalize.go`
- Create: `internal/converterscore/normalize_test.go`

**Interfaces:**
- Produces:
  ```go
  func EnsureAssistantBeforeToolResults(msgs []UnifiedMessage) (out []UnifiedMessage, modified bool)
  func MergeAdjacentMessages(msgs []UnifiedMessage) []UnifiedMessage
  func EnsureFirstMessageIsUser(msgs []UnifiedMessage) []UnifiedMessage
  func NormalizeMessageRoles(msgs []UnifiedMessage) []UnifiedMessage
  func EnsureAlternatingRoles(msgs []UnifiedMessage) []UnifiedMessage
  ```

- [ ] **Paso 1: Tests que fallan** con los cinco targets del corpus:
  - `ensure_assistant_before_tool_results` (33 casos, output es tupla `[messages, modified]` —
    hay que decodificarla como `[2]json.RawMessage`).
  - `merge_adjacent_messages` (41 casos).
  - `ensure_first_message_is_user` (40 casos).
  - `normalize_message_roles` (42 casos).
  - `ensure_alternating_roles` (43 casos).

  **Cada función se prueba de forma independiente** (no encadenada) — los casos golden ya cubren
  cada punto de la cadena por separado. La comprobación integrada la hace la Tarea 8.

- [ ] **Paso 2-4: Fallar, implementar, pasar** (199 casos).

  Puntos de detalle no negociables:
  - `MergeAdjacentMessages` **preserva** los tool_calls, tool_results e images del primer mensaje
    fusionado y descarta los del segundo si no hay conflicto; ver casos 1094-1108 del original.
    Los conflictos se resuelven concatenando.
  - `NormalizeMessageRoles` mapea cualquier rol que no sea `user`/`assistant`/`system` a `user`
    (línea 1223). El `system` se conserva porque `build_kiro_payload` lo saca aparte antes.
  - `EnsureAlternatingRoles` inserta un mensaje `assistant` con contenido `"Continue."` cuando
    ve dos `user` consecutivos, y un mensaje `user` con contenido `"Continue."` cuando ve dos
    `assistant`. Constante literal.

- [ ] **Paso 5: Commit.**

  ```
  git commit -m "feat(converterscore): message role normalization chain"
  ```

---

### Task 7: `converterscore/thinking.go` — additions al system prompt y `InjectThinkingTags`

Cuatro funciones auxiliares que giran alrededor del system prompt y del modo `FAKE_REASONING`:

**Files:**
- Create: `internal/converterscore/thinking.go`
- Create: `internal/converterscore/thinking_test.go`

**Interfaces:**
- Produces:
  ```go
  func GetThinkingSystemPromptAddition() string
  func GetTruncationRecoverySystemAddition() string
  func InjectThinkingTags(content string, cfg ThinkingConfig) string
  func StripAllToolContent(msgs []UnifiedMessage) (out []UnifiedMessage, modified bool)
  ```

- [ ] **Paso 1: Tests que fallan** contra cuatro targets:
  `get_thinking_system_prompt_addition` (3 casos), `get_truncation_recovery_system_addition`
  (4 casos), `inject_thinking_tags` (55 casos), `strip_all_tool_content` (44 casos, output es
  tupla).

- [ ] **Paso 2-4: Fallar, implementar, pasar** (106 casos).

  Los dos `Get...` devuelven constantes literales de texto — cópialas verbatim del original
  (líneas 304-359). `InjectThinkingTags` es un template con `budget_tokens` interpolado; el
  original hace `int(budget)` con truncamiento, que en Go es `strconv.Itoa` sobre el int ya
  convertido. `StripAllToolContent` recorre los mensajes y elimina `tool_calls`, `tool_results`,
  y las llamadas `<tool_use>` dentro del texto; ver 911-993.

- [ ] **Paso 5: Commit.**

  ```
  git commit -m "feat(converterscore): thinking-tag injection and system prompt additions"
  ```

---

### Task 8: `converterscore/payload.go` — `BuildKiroHistory` y `BuildKiroPayload`

La función terminal: integra todo lo anterior y produce el `map[string]any` con la estructura
`conversationState` que Kiro consume. Es el target que cierra la **frontera 1 de D1**.

**Files:**
- Create: `internal/converterscore/payload.go`
- Create: `internal/converterscore/payload_test.go`

**Interfaces:**
- Consumes: todo lo anterior de `converterscore`.
- Produces:
  ```go
  func BuildKiroHistory(msgs []UnifiedMessage, modelID string) []map[string]any

  func BuildKiroPayload(
      systemPrompt string,
      messages []UnifiedMessage,
      tools []UnifiedTool,
      modelID string,
      thinkingCfg ThinkingConfig,
      truncationRecovery bool,
      toolDescriptionMaxLength int,
  ) KiroPayloadResult
  ```

  La firma del original tiene esos siete argumentos con esos nombres. Se mantienen.

- [ ] **Paso 1: Tests que fallan** contra los dos targets integradores:
  - `converters_core/build_kiro_history` (37 casos).
  - `converters_core/build_kiro_payload` (83 casos).

  La comparación es `AssertJSONEqual` sobre `map[string]any` — el orden de claves da igual.

- [ ] **Paso 2-4: Fallar, implementar, pasar** (120 casos).

  El orden de la cadena de normalización es el del spec §6.7 y **NO** se altera. La `struct` de
  respuesta encaja con `converters_core.py:1405-1595`. Detalles:
  - Antes de la cadena: `ProcessToolsWithLongDescriptions` y `SanitizeJSONSchema` sobre cada tool.
  - La `tool_documentation` que devuelve se **prepone** al system prompt.
  - Si `truncationRecovery` está activo, se añade `GetTruncationRecoverySystemAddition()` al
    system.
  - Si `thinkingCfg.Enabled`, el last message del rol `user` recibe `InjectThinkingTags` sobre su
    contenido textual, y al system se le añade `GetThinkingSystemPromptAddition()`.
  - El `conversationId` es un UUID v4 que en el original se pasa como argumento indirecto vía
    `uuid.uuid4()`. Para el test golden, el corpus fija el UUID (ver `docs/CORPUS.md` §3, "UUIDs
    congelados"), y en Go debemos aceptar `conversationID` inyectado como argumento — **añádelo
    a la firma** como último parámetro `conversationID string`. Si el llamador pasa `""`, se
    genera con `crypto/rand`.

  > **Nota de firma:** la firma final queda con **ocho** argumentos, no siete. Actualiza la
  > interfaz declarada arriba y asegúrate de que los tests inyectan el `conversationID` que
  > figura en la salida esperada, decodificándolo del propio `c.Output.conversationId`.

- [ ] **Paso 5: Ejecutar la cadena entera y ver los 120 casos en verde.**

  ```
  go test ./internal/converterscore -run TestBuildKiroPayload -v
  go test ./internal/converterscore -run TestBuildKiroHistory -v
  ```

- [ ] **Paso 6: Commit.**

  ```
  git commit -m "feat(converterscore): Kiro payload and history assembly"
  ```

---

### Task 9: Adaptador Anthropic (`convertersanthropic`)

Ocho funciones que traducen del dialecto Anthropic al tipo unificado, más el `AnthropicToKiro`
integrador.

**Files:**
- Create: `internal/convertersanthropic/converters.go`
- Create: `internal/convertersanthropic/converters_test.go`

**Interfaces:**
- Consumes: `modelsanthropic`, `converterscore`, `config`.
- Produces:
  ```go
  func ConvertAnthropicContentToText(content any) string
  func ExtractSystemPrompt(system any) string
  func ExtractToolResultsFromAnthropicContent(content any) []map[string]any
  func ExtractImagesFromToolResults(content any) []map[string]any
  func ExtractToolUsesFromAnthropicContent(content any) []map[string]any
  func ConvertAnthropicMessages(msgs []modelsanthropic.AnthropicMessage) (systemPrompt string, unified []converterscore.UnifiedMessage)
  func ConvertAnthropicTools(tools []modelsanthropic.AnthropicTool) []converterscore.UnifiedTool
  func ExtractThinkingConfigFromAnthropic(req *modelsanthropic.AnthropicMessagesRequest) converterscore.ThinkingConfig
  func AnthropicToKiro(req *modelsanthropic.AnthropicMessagesRequest, cfg *config.Config) converterscore.KiroPayloadResult
  ```

- [ ] **Paso 1: Tests que fallan** contra los nueve targets del corpus (251 casos).

- [ ] **Paso 2-4: Fallar, implementar, pasar.**

  `AnthropicToKiro` es donde se compone todo: llama a las tres `Extract*` para armar el input,
  llama a `ConvertAnthropicMessages` y `ConvertAnthropicTools`, resuelve `ThinkingConfig` con
  `ExtractThinkingConfigFromAnthropic`, y termina con `converterscore.BuildKiroPayload`.

- [ ] **Paso 5: Commit.**

  ```
  git commit -m "feat(convertersanthropic): Anthropic dialect adapter"
  ```

---

### Task 10: Adaptador OpenAI (`convertersopenai`)

Cinco funciones análogas más el `BuildKiroPayload` integrador (que en el adaptador se llama
`BuildKiroPayload` también, con un parámetro `*modelsopenai.ChatCompletionRequest`).

**Files:**
- Create: `internal/convertersopenai/converters.go`
- Create: `internal/convertersopenai/converters_test.go`

**Interfaces:**
- Consumes: `modelsopenai`, `converterscore`, `config`.
- Produces:
  ```go
  func ConvertOpenAIMessagesToUnified(msgs []modelsopenai.ChatMessage) (systemPrompt string, unified []converterscore.UnifiedMessage)
  func ConvertOpenAIToolsToUnified(tools []modelsopenai.Tool) []converterscore.UnifiedTool
  func ReasoningEffortToBudget(maxTokens int, effort string) int
  func ExtractThinkingConfigFromOpenAI(req *modelsopenai.ChatCompletionRequest) converterscore.ThinkingConfig
  func BuildKiroPayload(req *modelsopenai.ChatCompletionRequest, cfg *config.Config) converterscore.KiroPayloadResult
  ```

- [ ] **Paso 1: Tests que fallan** contra los cinco targets del corpus (141 casos).

- [ ] **Paso 2-4: Fallar, implementar, pasar.**

  Detalle no obvio: `ConvertOpenAIMessagesToUnified` extrae el system prompt de la lista (el
  primer mensaje con rol `system`, concatenando si hay varios). Los mensajes `tool` (rol) se
  convierten en `user` con `tool_results`. Ver `converters_openai.py:141-251`.

  `ReasoningEffortToBudget` es una tabla explícita:
  ```
  low    → max_tokens * 0.2
  medium → max_tokens * 0.5
  high   → max_tokens * 0.8
  none   → 0
  otro   → max_tokens * 0.5 (default = medium)
  ```

- [ ] **Paso 5: Commit.**

  ```
  git commit -m "feat(convertersopenai): OpenAI dialect adapter"
  ```

---

### Task 11: Cierre de la fase

- [ ] **Paso 1:** Correr toda la suite completa.

  ```
  task lint
  task test
  task corpus:validate
  ```

  Los tres en verde. Si falla algún caso del corpus, revisar el `Case.Name` que imprime el test y
  volver a la tarea que corresponda.

- [ ] **Paso 2:** Rellenar en `docs/MAPPING.md` las cinco filas de los paquetes portados con las
  rutas de ficheros Go definitivas.

- [ ] **Paso 3:** Actualizar `README.md` — la sección "Estado" — indicando que la fase 3 está
  cerrada y que el payload es byte a byte compatible con el original para los 1 412 casos golden.

- [ ] **Paso 4:** Commit final.

  ```
  git add docs/MAPPING.md README.md
  git commit -m "docs: close phase 3 — models and converters"
  ```

---

## Criterio de terminado

1. Todos los tests de fase 3 en verde.
2. `task corpus:validate` no reporta targets sin cobertura para los 37 de la fase.
3. `docs/MAPPING.md` refleja los cinco paquetes nuevos.
4. **Frontera 1 de D1 cerrada:** el payload generado en Go coincide byte a byte con el del
   original para los 121 casos de `build_kiro_payload` (83 en `converters_core` + 38 en
   `converters_openai`) y para los 32 casos integradores de Anthropic (`anthropic_to_kiro`).
