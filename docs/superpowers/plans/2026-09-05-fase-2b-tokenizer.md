# Fase 2b: El tokenizer — Plan de implementación

> **Para trabajadores agénticos:** SUB-SKILL OBLIGATORIA: usa
> `superpowers:subagent-driven-development` (recomendado) o `superpowers:executing-plans` para
> ejecutar este plan tarea por tarea. Los pasos usan sintaxis de casilla (`- [ ]`).

**Objetivo:** portar `internal/tokenizer`, el único paquete de la fase 2 que no cabía en los
cimientos sin dependencias. Reproduce el conteo de tokens de `tiktoken` con la codificación
`cl100k_base` **byte a byte** y le aplica el factor de corrección de Claude, verificado contra los
**255 casos golden** de los cinco objetivos del tokenizer.

**Decisión de arquitectura (D de sesión, validada con el usuario): Opción A — stdlib puro.** El
vocabulario BPE se **embebe con `go:embed`** (spec D6) y el codificador se implementa a mano: no se
añade ninguna dependencia externa de Go. La razón: el binario queda autocontenido, el proyecto
mantiene su postura stdlib-only, y los 255 casos del corpus verifican la exactitud byte a byte, así
que un error aflora de inmediato. El obstáculo real que esto sortea: el patrón de pre-tokenización
de `cl100k_base` usa una anticipación negativa `(?!\S)` y cuantificadores posesivos que el motor
`regexp` (RE2) de Go **no soporta**; por eso el pre-tokenizador se escribe como un escáner a mano,
no como una regexp.

**Stack:** Go 1.27, `CGO_ENABLED=0`, **sin dependencias externas nuevas**. Todo con la biblioteca
estándar. El paquete consume `internal/pyjson` (fase 2a).

**Spec:** `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md` (§10 Fase 2, D6, §4.2
factor 1.15).
**Contrato del corpus:** `docs/CORPUS.md`.
**Fuente a portar:** `.upstream/kiro/tokenizer.py`.
**Fase anterior:** `docs/superpowers/plans/2026-09-05-fase-2a-cimientos.md` (aporta `pyjson`).

## Restricciones globales

- Module path `github.com/marr-cloud/kiro-gateway-go`, Go 1.27, `CGO_ENABLED=0`.
- Todo fichero `.go`, incluidos los `_test.go`, lleva como dos primeras líneas:
  ```go
  // SPDX-License-Identifier: AGPL-3.0-or-later
  // Port a Go de jwadow/kiro-gateway. Ver NOTICE.
  ```
- **Ninguna dependencia externa de Go nueva.** El único activo de terceros que entra es el fichero
  de datos del vocabulario `cl100k_base`, embebido; su procedencia y licencia se documentan en
  `NOTICE` (Tarea 1). Si crees que hace falta una dependencia de código, párate y repórtalo.
- Ningún test toca la red real.
- El vocabulario embebido se genera **desde el `tiktoken` fijado en `.upstream/.venv`**, no se
  descarga: así el vocabulario coincide exactamente con el que grabó el corpus.
- Nombre de paquete: `tokenizer`. Al terminar la fase se rellena la fila `tokenizer.py` de
  `docs/MAPPING.md`.
- `task lint` y `task test` en verde antes de cada commit. `task corpus:validate` en verde al cerrar.
- Mensajes de commit con asunto en inglés y prefijo convencional.

## Ground truth de `cl100k_base` (extraído del `tiktoken` fijado)

Esto es lo que el port debe reproducir. Confírmalo tú mismo si dudas, pero está verificado:

- **Patrón de pre-tokenización** (posesivo, con anticipación; hay que emularlo a mano):
  ```
  '(?i:[sdmt]|ll|ve|re)|[^\r\n\p{L}\p{N}]?+\p{L}++|\p{N}{1,3}+| ?[^\s\p{L}\p{N}]++[\r\n]*+|\s++$|\s*[\r\n]|\s+(?!\S)|\s
  ```
  Alternativas, en orden (la primera que casa gana; los cuantificadores son posesivos = greedy sin
  retroceso):
  1. `'(?i:[sdmt]|ll|ve|re)` — contracción: `'` + uno de `s d m t ll ve re`, **case-insensitive**
     (`'S`, `'LL`, ... también).
  2. `[^\r\n\p{L}\p{N}]?+\p{L}++` — opcional un carácter que no sea CR/LF/letra/número, seguido de
     una o más letras.
  3. `\p{N}{1,3}+` — de 1 a 3 dígitos.
  4. ` ?[^\s\p{L}\p{N}]++[\r\n]*+` — espacio opcional, uno o más caracteres que no sean
     espacio/letra/número, y luego cero o más CR/LF.
  5. `\s++$` — uno o más espacios al final de la cadena.
  6. `\s*[\r\n]` — cero o más espacios y un único CR o LF.
  7. `\s+(?!\S)` — uno o más espacios **no seguidos** de un no-espacio (la anticipación que RE2 no
     tiene: en el escáner, un tramo de espacios del que se reserva el último si le sigue un
     no-espacio).
  8. `\s` — un espacio suelto (fallback).
  `\p{L}`, `\p{N}`, `\s` son las clases Unicode de Go (`unicode.IsLetter`, `unicode.IsNumber`, y el
  `\s` de Perl: `[\t\n\f\r ]` más los espacios Unicode que usa el `\s` de la regex — usa
  `unicode.IsSpace`, y **documenta y testea** cualquier borde donde difiera de la `\s` del patrón).
- **BPE:** las `mergeable_ranks` mapean secuencias de **bytes crudos** → rango. Son **100256**
  entradas (rangos 0..100255). El token más largo es de **128 bytes**. El algoritmo es el
  `byte_pair_encode` de tiktoken: sobre los bytes de cada pieza, fusiona repetidamente el par
  adyacente de menor rango hasta que no queden fusiones, y emite los rangos resultantes. No hay
  mapa byte→unicode estilo GPT-2: se trabaja sobre `[]byte`.
- **Tokens especiales** (rangos altos, **no** son mergeable ranks):
  `<|endoftext|>`=100257, `<|fim_prefix|>`=100258, `<|fim_middle|>`=100259, `<|fim_suffix|>`=100260,
  `<|endofprompt|>`=100276. `n_vocab`=100277.
- **`.encode()` del original** usa `disallowed_special="all"`: si el texto contiene una de esas
  cinco cadenas literales, **lanza**, y `count_tokens` lo captura y cae al estimador
  `len(text)//4 + 1`. El camino normal (sin tokens especiales) es BPE ordinario. Ver Tarea 3.
- **Codificaciones de referencia** para tests a mano: `encode("hello world")` = `[15339, 1917]`;
  `encode("")` = `[]`; `encode('{"a": 1, "b": [1, 2]}')` =
  `[5018, 64, 794, 220, 16, 11, 330, 65, 794, 510, 16, 11, 220, 17, 14316]`.

## Cómo se consume el corpus

API congelada en `internal/testutil` (fase 1). Los cinco objetivos y sus recuentos:

| Objetivo | Casos |
|---|---|
| `tokenizer/count_tokens` | 136 |
| `tokenizer/count_message_tokens` | 43 |
| `tokenizer/count_tools_tokens` | 28 |
| `tokenizer/count_system_tokens` | 19 |
| `tokenizer/estimate_request_tokens` | 29 |

Todos son `kind: function` con `input.args`/`input.kwargs`. El tokenizer **no lee banderas de
`kiro.config`**, así que no hay `input.config`. El kwarg `apply_claude_correction` (por defecto
`True`) aparece en algunos casos: **léelo de args o kwargs** como en `accounterrors` de la fase 2a.
Incluye SIEMPRE `c.Name` en los mensajes de error. `count_tokens` y `count_message_tokens` tienen
un caso de ~25 KB: el conteo de textos grandes es parte del contrato.

## El factor de corrección

`CLAUDE_CORRECTION_FACTOR = 1.15`. Cuando se aplica: `int(base_tokens * 1.15)`. En Python `int()`
trunca hacia cero; en Go `int(float64(base) * 1.15)` hace lo mismo para valores no negativos. El
recuento base nunca es negativo. Reproduce el truncamiento exacto: primero el `float64`, luego el
`int()`.

## Estructura de ficheros

| Fichero | Responsabilidad |
|---|---|
| `internal/tokenizer/cl100k_base.tiktoken` | Datos del vocabulario embebidos (generados del venv) |
| `internal/tokenizer/vocab.go` | `go:embed` del vocabulario y su parseo a un mapa de rangos |
| `internal/tokenizer/vocab_test.go` | El vocabulario carga, tiene 100256 entradas, spot-checks |
| `internal/tokenizer/pretoken.go` | El escáner que emula el patrón de pre-tokenización |
| `internal/tokenizer/bpe.go` | `byte_pair_encode` y `Encode`/`EncodeOrdinary` |
| `internal/tokenizer/encode_test.go` | Tests a mano del escáner y de `Encode` (ids de referencia) |
| `internal/tokenizer/count.go` | `CountTokens` y el factor 1.15 |
| `internal/tokenizer/messages.go` | `CountMessageTokens` |
| `internal/tokenizer/tools_system.go` | `CountToolsTokens` y `CountSystemTokens` |
| `internal/tokenizer/estimate.go` | `EstimateRequestTokens` |
| `internal/tokenizer/*_test.go` | Un test golden por objetivo del corpus |
| `tools/parity/tokenizer_test.go` | (opcional) parity build-tagged contra el venv |

Los nombres exactos de las funciones exportadas los decide el implementador de forma idiomática
(`CountTokens`, `CountMessageTokens`, `CountSystemTokens`, `CountToolsTokens`,
`EstimateRequestTokens`), documentando la correspondencia con `tokenizer.py`.

---

### Task 1 — Vocabulario embebido

**Ficheros:** crear `internal/tokenizer/cl100k_base.tiktoken`, `internal/tokenizer/vocab.go`,
`internal/tokenizer/vocab_test.go`; modificar `NOTICE` y `Taskfile.yml`.

**Interfaces:** produce el mapa de rangos cargado (`map[string]int` con clave = bytes del token como
`string`, o la estructura que elija el implementador) y su índice inverso si hace falta. Lo consume
la Tarea 2.

- [ ] **Paso 1: Generar el fichero de vocabulario desde el venv fijado.** Añade a `Taskfile.yml` un
  objetivo `tokenizer:vocab` que ejecute `{{.PY}}` para volcar `e._mergeable_ranks` de
  `tiktoken.get_encoding("cl100k_base")` al formato canónico `.tiktoken` (líneas
  `base64(token_bytes) <espacio> rank`, ordenadas por rango) en
  `internal/tokenizer/cl100k_base.tiktoken`. Ejecútalo. El fichero (~1.7 MB, 100256 líneas) se
  **commitea** (es el dato que `go:embed` necesita). Genera desde el venv, nunca descargando.
- [ ] **Paso 2: Documentar procedencia y licencia en `NOTICE`.** Añade una entrada: el vocabulario
  `cl100k_base` procede de `tiktoken` de OpenAI (licencia MIT); se embebe como dato funcional para
  reproducir el conteo del original. Cítalo con la versión de `tiktoken` del `requirements.lock`.
- [ ] **Paso 3: Escribir los tests que fallan.** `vocab_test.go`: el vocabulario carga sin error,
  tiene exactamente 100256 entradas, y algunos spot-checks de bytes→rango tomados del venv (p.ej.
  el token `b" world"` tiene su rango conocido; deriva 2-3 del propio venv y fíjalos).
- [ ] **Paso 4: Implementar el cargador.** `vocab.go`: `//go:embed cl100k_base.tiktoken`, parsea una
  vez (`sync.Once` o variable de paquete inicializada) a la estructura de rangos. Base64-decodifica
  cada línea. Falla ruidosamente si el recuento no es 100256 (un vocabulario truncado es un bug).
- [ ] **Paso 5: Verde.** `go test ./internal/tokenizer/` (solo los de vocab de momento).
- [ ] **Paso 6: Commit.**
  ```bash
  git add internal/tokenizer/cl100k_base.tiktoken internal/tokenizer/vocab.go internal/tokenizer/vocab_test.go NOTICE Taskfile.yml
  git commit -m "feat(tokenizer): embed the cl100k_base BPE vocabulary"
  ```

---

### Task 2 — El codificador: pre-tokenizador + BPE + Encode

**Ficheros:** crear `internal/tokenizer/pretoken.go`, `internal/tokenizer/bpe.go`,
`internal/tokenizer/encode_test.go`.

**Esta es la tarea con el riesgo de exactitud de la fase. Léela entera antes de escribir código.**

- [ ] **Paso 1: Tests a mano del pre-tokenizador.** En `encode_test.go`, table-driven: para cadenas
  de entrada, la lista exacta de piezas que produce el patrón. Cubre cada alternativa: contracciones
  (`don't`, `I'LL`), letras con prefijo (` hello`, `"hola`), dígitos en grupos de 3 (`12345` →
  `123`,`45`), puntuación con CR/LF, espacios finales (`\s++$`), saltos de línea, y espacios
  interiores vs finales (la regla de `(?!\S)`). Deriva los splits esperados razonando el patrón; si
  dudas de un caso, compáralo con el venv (`e._pat_str` + `regex`), pero los valores fijados van a
  mano.
- [ ] **Paso 2: Tests a mano de `Encode`.** Fija los ids de referencia del ground truth:
  `Encode("hello world")==[]int{15339,1917}`, `Encode("")==nil/[]`,
  `Encode('{"a": 1, "b": [1, 2]}')==[]int{5018,64,794,220,16,11,330,65,794,510,16,11,220,17,14316}`,
  y 2-3 más que derives del venv (incluye uno con no-ASCII/emoji para ejercitar bytes multibyte).
- [ ] **Paso 3: Rojo.** `go test ./internal/tokenizer/` — fallan por símbolos indefinidos.
- [ ] **Paso 4: Implementar el pre-tokenizador.** `pretoken.go`: un escáner que recorre el texto por
  runas y emite piezas según las ocho alternativas, en orden, sin retroceso (posesivo). La
  alternativa 7 (`\s+(?!\S)`) se implementa reservando el último espacio de un tramo cuando le sigue
  un no-espacio. Trabaja en índices de bytes pero decide por runa. Documenta la correspondencia
  alternativa-por-alternativa con el patrón.
- [ ] **Paso 5: Implementar BPE.** `bpe.go`: `bytePairEncode(piece []byte) []int` con el algoritmo
  de tiktoken (fusión del par de menor rango hasta agotar). Si la pieza entera está en los rangos,
  emite ese rango. `EncodeOrdinary(text string) []int` = pretokenizar + BPE de cada pieza,
  concatenando. `Encode` = `EncodeOrdinary` salvo la semántica de tokens especiales de la Tarea 3.
- [ ] **Paso 6: Verde.** `go test ./internal/tokenizer/ -v` — pre-tokenizador y `Encode` en verde.
- [ ] **Paso 7: (recomendado) Parity build-tagged.** Añade `tools/parity/tokenizer_test.go` (build
  tag `parity`) que compare `Encode` del port contra `e.encode_ordinary` del venv sobre una batería
  amplia de cadenas (ASCII, no-ASCII, emoji, código, JSON, espacios raros). Da confianza más allá de
  los 255 casos. Documenta cómo lanzarlo. No corre en CI.
- [ ] **Paso 8: Commit.**
  ```bash
  git add internal/tokenizer/pretoken.go internal/tokenizer/bpe.go internal/tokenizer/encode_test.go tools/parity
  git commit -m "feat(tokenizer): hand-rolled cl100k_base pre-tokenizer and BPE encoder"
  ```

---

### Task 3 — `count_tokens` y el factor 1.15

**Ficheros:** crear `internal/tokenizer/count.go`, `internal/tokenizer/count_test.go`.

**Corpus:** `tokenizer/count_tokens`, 136 casos.

- [ ] **Paso 1: Inspeccionar el corpus.** Abre varios casos, incluido el de ~25 KB y los pequeños
  (`5c9293aeb92cbbb7`, `f2b7f84703ca143c`). Confirma la forma de `args`/`kwargs` y si aparece el
  kwarg `apply_claude_correction`. **Busca si algún caso ejercita el estimador de reserva** (texto
  con un token especial → el original lanza y cae a `len(text)//4+1`) o si todos son BPE puro.
  Anótalo en el informe.
- [ ] **Paso 2: Test golden que falla.** `count_test.go`: recorre `count_tokens`, extrae `text` y
  `apply_claude_correction` (de args o kwargs; por defecto `True`), compara `CountTokens` con la
  salida. `c.Name` en cada fallo.
- [ ] **Paso 3: Implementar.** `count.go`: `CountTokens(text string, applyCorrection bool) int`.
  Texto vacío → 0. Base = `len(EncodeOrdinary(text))`. Si `applyCorrection`,
  `int(float64(base)*1.15)`. **Semántica de tokens especiales:** si el texto contiene una de las
  cinco cadenas especiales, replica lo que hace el original (lanza→estimador de reserva); si el
  corpus no cubre ese caso, documenta que queda sin cobertura y decide según la fuente. El estimador
  de reserva `len(text)//4+1` cuenta **code points** (`utf8.RuneCountInString`), no bytes.
- [ ] **Paso 4: Verde.** `go test ./internal/tokenizer/ -run CountTokens -v` — 136 subtests.
  **Si algún caso falla, el corpus manda:** el fallo casi siempre estará en la Tarea 2 (encoder);
  lee el caso, reproduce el texto contra el venv, y corrige el encoder.
- [ ] **Paso 5: Commit.**
  ```bash
  git add internal/tokenizer/count.go internal/tokenizer/count_test.go
  git commit -m "feat(tokenizer): count_tokens with the Claude 1.15 correction, 136 golden cases"
  ```

---

### Task 4 — `count_message_tokens`

**Ficheros:** crear `internal/tokenizer/messages.go`, `internal/tokenizer/messages_test.go`.

**Corpus:** `tokenizer/count_message_tokens`, 43 casos. **Consume `pyjson`.**

Porta la contabilidad EXACTA de `tokenizer.py::count_message_tokens` (léela línea a línea):
`+4` por mensaje, rol (sin corrección), contenido (str o lista de bloques), `tool_calls` (`+4` cada
uno + name + arguments), `tool_call_id`, y `+3` final; corrección `*1.15` al total. Detalles que
importan:

- El contenido en bloques: `text`, `image_url`/`image` (coste fijo `100`), `tool_use` (id + name +
  `json.dumps(input, ensure_ascii=False)` → **`pyjson.Dumps`**; `input` ausente = `{}`), y
  `tool_result` (tool_use_id, `str(is_error)` si no es None → **`pyjson.Str`** sobre el bool =
  `"True"`/`"False"`, y su `content` que puede ser str, lista de bloques, o un valor suelto
  `str(...)`).
- Bloque desconocido → `json.dumps(item, ensure_ascii=False)` → **`pyjson.Dumps`**.
- Ítem no-dict → `str(item)` → **`pyjson.Str`**.
- Todos los `count_tokens` internos van con `apply_claude_correction=False`; la corrección se aplica
  una sola vez al total.

- [ ] **Paso 1:** inspecciona 3-4 casos (incluido `67742e24e6c5a0ac` y el de ~25 KB) para ver las
  formas de bloque presentes.
- [ ] **Paso 2:** test golden que falla, recorriendo el objetivo.
- [ ] **Paso 3:** implementar, deserializando los mensajes a una forma que preserve los bytes JSON de
  `input`/bloques desconocidos para pasarlos a `pyjson.Dumps` sin reserializar con separadores de
  Go.
- [ ] **Paso 4:** verde, 43 subtests.
- [ ] **Paso 5: Commit.**
  ```bash
  git add internal/tokenizer/messages.go internal/tokenizer/messages_test.go
  git commit -m "feat(tokenizer): count_message_tokens, 43 golden cases"
  ```

---

### Task 5 — `count_tools_tokens` y `count_system_tokens`

**Ficheros:** crear `internal/tokenizer/tools_system.go`,
`internal/tokenizer/tools_system_test.go`.

**Corpus:** `tokenizer/count_tools_tokens` (28) y `tokenizer/count_system_tokens` (19). **Consumen
`pyjson`.** Se hacen juntas por ser de la misma forma.

- `count_tools_tokens`: `+4` por tool; soporta tool OpenAI (`type=="function"`, campo `function`) y
  plano (Anthropic); name + description + `json.dumps(input_schema || parameters, ensure_ascii=False)`
  vía **`pyjson.Dumps`**; corrección al total.
- `count_system_tokens`: str, o lista de bloques (`text` + `cache_control` vía `json.dumps` si no es
  None), o `str(...)` de un valor suelto; corrección al total.

- [ ] **Paso 1:** inspecciona un par de casos de cada objetivo (incluido uno con `input_schema` y
  uno con `parameters`, y un system en lista de bloques con `cache_control`).
- [ ] **Paso 2:** dos tests golden (uno por objetivo) que fallan.
- [ ] **Paso 3:** implementar ambos.
- [ ] **Paso 4:** verde, 28 + 19 subtests.
- [ ] **Paso 5: Commit.**
  ```bash
  git add internal/tokenizer/tools_system.go internal/tokenizer/tools_system_test.go
  git commit -m "feat(tokenizer): count_tools_tokens and count_system_tokens, 47 golden cases"
  ```

---

### Task 6 — `estimate_request_tokens` y cierre de fase

**Ficheros:** crear `internal/tokenizer/estimate.go`, `internal/tokenizer/estimate_test.go`;
modificar `docs/MAPPING.md`.

**Corpus:** `tokenizer/estimate_request_tokens`, 29 casos. Combina los tres conteos (cada uno con su
corrección) y devuelve el objeto `{messages_tokens, tools_tokens, system_tokens, total_tokens}`. Los
nombres de campo JSON son los que grabó el corpus; compara la salida serializada o con
`reflect.DeepEqual` sobre la estructura.

- [ ] **Paso 1:** test golden que falla (lee messages/tools/system de args/kwargs; ojo con los
  opcionales ausentes).
- [ ] **Paso 2:** implementar.
- [ ] **Paso 3:** verde, 29 subtests.
- [ ] **Paso 4: Rellenar `docs/MAPPING.md`:** fila `tokenizer.py` → `internal/tokenizer` con los
  ficheros Go.
- [ ] **Paso 5: Verificación completa.** `task lint`, `task test`, `task corpus:validate`, los tres
  en código 0. Pega en el informe el recuento de subtests por objetivo (136/43/28/19/29 = 255).
- [ ] **Paso 6: Commit.**
  ```bash
  git add internal/tokenizer/estimate.go internal/tokenizer/estimate_test.go docs/MAPPING.md
  git commit -m "feat(tokenizer): estimate_request_tokens and map the package, 29 golden cases"
  ```

---

## Criterios de aceptación de la fase 2b

1. `internal/tokenizer` implementado en stdlib puro, sin dependencias externas de Go nuevas.
2. `task lint`, `task test` y `task corpus:validate` en código 0.
3. Los 255 casos golden de los cinco objetivos pasan: 136 + 43 + 28 + 19 + 29.
4. El vocabulario `cl100k_base` está embebido con `go:embed`, generado desde el `tiktoken` fijado,
   con 100256 entradas, y su procedencia/licencia documentada en `NOTICE`.
5. El pre-tokenizador reproduce el patrón `cl100k_base` (incluida la regla `(?!\S)`) con tests a
   mano por alternativa, y `Encode` coincide con los ids de referencia del ground truth.
6. `count_tokens` aplica el factor 1.15 con el truncamiento `int()` exacto, y trata los tokens
   especiales y el estimador de reserva según la fuente (o documenta que el corpus no los cubre).
7. Las cuatro funciones de conteo estructurado consumen `pyjson` para los `json.dumps`/`str` del
   original, no `encoding/json` con separadores de Go.
8. `docs/MAPPING.md` tiene la fila `tokenizer.py` rellena.

## Lo que esta fase deja preparado y lo que no

**Preparado:** el tokenizer que las rutas (fase 5) usan para el campo `usage`, y la primera prueba
de que `pyjson` de la fase 2a hace su trabajo (aquí se ejercita de verdad).

**No preparado:** `parsers`, `thinkingparser`, `truncationstate`, `truncationrecovery`,
`payloadguards`, `modelresolver`, `cache` y el test diferencial de UTF-8 quedan para las fases 2c/2d
según el orden final del grafo de dependencias.
