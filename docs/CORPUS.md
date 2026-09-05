# El corpus golden

`testdata/` es la capa 1 de la estrategia de tests (§8.1 del diseño): 2104 casos grabados de las
1691 pruebas del original Python `jwadow/kiro-gateway` fijado en el commit **a5292ca**, repartidos
en 64 objetivos. Es la referencia contra la que las fases 2 a 5 comprueban que el port en Go se
comporta igual.

Este documento es el sitio donde vive lo que hay que saber para usarlo y para regenerarlo. Todo lo
que está aquí estaba antes solo en comentarios de código, en un fichero que git ignora o en un
mensaje de commit, es decir, a un `squash` de desaparecer.

---

## 1. Cómo se genera y se regenera

```
task corpus            # graba y valida
task corpus:record     # solo graba (borra testdata/ y lo rehace)
task corpus:validate   # solo valida lo que haya en testdata/
```

`task corpus:setup` prepara el entorno la primera vez: clona el original en `.upstream/`, hace
checkout del commit fijado y crea `.upstream/.venv` con Python 3.10.

Las dependencias se instalan **desde `tools/corpus/requirements.lock`**, no resolviendo
`pyproject.toml`. No es un detalle de estilo: `pydantic` decide la forma de las entradas grabadas
(todo lo que pasa por `model_dump`) y `tiktoken` decide la salida de los cinco objetivos del
tokenizer, más de 250 casos. Resolviendo en cada máquina, el corpus dejaba de ser reproducible.
Para cambiar dependencias a propósito:

```
task corpus:relock     # reresuelve desde pyproject.toml y reescribe el lockfile
```

La configuración del original también está fijada. `kiro/config.py` llama a `load_dotenv()` al
importarse y deriva unos 34 valores de variables de entorno; ningún `.env` está versionado, así que
sin fijarla el corpus dependía del entorno de quien lo generase. La tarea `corpus:record` define
todas esas variables con los valores por defecto del original, el grabador **aborta si encuentra un
`.env`** en el camino de búsqueda (`CORPUS_ALLOW_DOTENV=1` lo permite, a sabiendas), y la huella de
los valores efectivos se vuelca en la sección `config` de `testdata/_report.json`. Los nombres que
parecen credenciales o rutas locales salen como `<puesto>` / `<vacio>`, para que la huella se pueda
comparar entre máquinas sin publicar secretos.

### El corpus solo es reproducible con la suite completa y en el orden canónico

Los identificadores deterministas salen de **contadores globales de sesión** que no se reinician
entre tests, a propósito: reiniciarlos por test hace colisionar casos distintos y se pierden casos
por deduplicación. Consecuencia directa:

- Grabar un subconjunto (`pytest tests/unit/test_x.py`, `-k`, `--lf`) produce identificadores
  distintos y un corpus que **no** se puede comparar con el commiteado. Regenera siempre con
  `task corpus`.
- Cualquier cosa que altere el orden de ejecución (un plugin que aleatoriza o paraleliza, otra
  versión de pytest que colecte en otro orden) desplaza todos los identificadores aunque el código
  del original no haya cambiado.

Contra lo primero no hay defensa técnica, solo este aviso. Contra lo segundo hay dos:
`recorder.py` impone su propio orden canónico con `pytest_collection_modifyitems(trylast=True)`, que
gana al de cualquier plugin, y `corpus:record` bloquea por línea de comandos los plugins de
aleatorización y paralelismo. La huella del orden queda en `_report.json`
(`test_order.sha256_16`); si cambia entre dos grabaciones, los identificadores se desplazan.

### Comprobar el determinismo

```
task corpus:verify-deterministic     # graba dos veces y compara byte a byte
task corpus:verify-deterministic-3   # graba tres veces y desglosa la varianza por objetivo
```

---

## 2. El formato del envoltorio

Un caso es un fichero `testdata/<módulo>/<función>/<sha256_16 de la entrada>.json`:

```json
{
  "target": "kiro.converters_core:build_kiro_payload",
  "kind": "function",
  "input":  { "args": [], "kwargs": {}, "config": {} },
  "output": {},
  "steps":  [],
  "notes":  {},
  "recorded_at_commit": "a5292ca"
}
```

**Las siete claves de nivel superior son un contrato**: el cargador en Go
(`internal/testutil/corpus.go`) depende de ellas. No se cambian sin decirlo.

| Clave | Presente en | Qué es |
|---|---|---|
| `target` | siempre | `<módulo Python>:<nombre>`, tal cual |
| `kind` | siempre | `function`, `sequence` o `generator` |
| `input` | `function`, `generator` | La entrada completa. Ver abajo |
| `output` | `function`, `generator` | El valor devuelto, o la lista de fragmentos emitidos |
| `steps` | `sequence` | La secuencia de llamadas de una instancia |
| `notes` | `generator` | Diagnóstico: tipos de los argumentos no grabados y desenlace |
| `recorded_at_commit` | siempre | Commit del original del que se grabó |

La forma de `input` depende del `kind`:

- `function`: `{"args": [...], "kwargs": {...}, "config"?: {...}}`
- `generator`: `{"kwargs": {...}, "events": [...], "config"?: {...}}` — `events` son los eventos
  que el generador consumió del stream de Kiro, y su último elemento puede ser una excepción
  (marcador `__exception__`) cuando el stream terminó reventando.
- `sequence`: no tiene `input`; cada paso de `steps` tiene el suyo, con la misma forma que el de
  `function`. **El paso 0 es siempre el constructor**, con `"method": "__init__"` y `"output": null`.
  Está ahí porque es entrada: dos instancias construidas de forma distinta responden distinto a las
  mismas llamadas, y sin grabarlo sus casos eran indistinguibles.

### `input.config`: la configuración es entrada

Varias funciones del original **no son puras**: leen banderas de `kiro.config` que no llegan por
argumento, y los tests las parchean. `get_thinking_system_prompt_addition` devuelve el texto o
cadena vacía según `FAKE_REASONING_ENABLED`; `should_inject_recovery` devuelve `True` o `False`
según `TRUNCATION_RECOVERY`. Grabando solo `(args, kwargs)` la entrada quedaba **incompleta**.

`tools/corpus/targets.py` declara, por módulo, las banderas que ese módulo puede leer, y el
grabador las mete en `input.config` (en las secuencias, en el paso `__init__`). El port **tiene que
aplicarlas** para reproducir la salida: un caso de `inject_thinking_tags` con
`FAKE_REASONING_ENABLED: false` devuelve el contenido sin tocar, y compararlo contra un port que
tiene la bandera a `true` falla por la configuración, no por el port.

La declaración es por módulo, no por función: la lista se obtiene de lo que el módulo importa de
`kiro.config`, así que es completa por construcción, más las banderas de los módulos a los que
delega (`converters_openai` y `converters_anthropic` cargan con las de `converters_core`; los dos de
streaming, con las de `thinking_parser`). Una función que no lee ninguna carga con las de su módulo:
eso puede duplicar algún caso, pero no puede ocultar una contradicción.

---

## 3. Codificaciones con marcador: la lista completa

Los valores que no son JSON nativo se representan con un objeto de una sola clave. **Son estas
tres, y las tres tienen decodificador en `internal/testutil/corpus.go`:**

| Marcador | Origen en Python | Decodificador en Go |
|---|---|---|
| `{"__bytes__": "<base64>"}` | `bytes`, `bytearray` | `testutil.DecodeBytes` |
| `{"__set__": [...]}` | `set`, `frozenset`, ordenados por `repr()` | `testutil.DecodeSet` |
| `{"__exception__": {"type","module","args","str","cause"?}}` | cualquier `BaseException` | `testutil.DecodeException`, `testutil.IsException` |

Detalles que importan:

- `__set__` viene ordenado por `repr()` de Python, no por el orden natural del tipo en Go: compara
  como conjunto, no como lista.
- `__exception__` lleva `cause` (de `__cause__`) y es imprescindible, no adorno: la rama de DNS de
  `kiro.network_errors.classify_network_error` decide con
  `isinstance(error.__cause__, socket.gaierror)` y saca el `errno` de sus `args`. Sin la causa,
  `httpx.ConnectError("Connection failed")` con y sin `gaierror` encadenada tendrían la misma
  entrada grabada.
- **Los modelos pydantic NO llevan marcador**, a propósito: un modelo pydantic *es* un objeto JSON,
  y su forma natural en el corpus es el objeto plano, que es justo lo que el port recibe por la red.
  Se vuelcan con `model_dump()` en modo python, sin `exclude_none` ni `exclude_unset`, calcando lo
  que hace el original (todas sus llamadas son `msg.model_dump()` a secas), así que los campos con
  valor nulo salen en el volcado.

Si algún día aparece un cuarto marcador, se añade a esta tabla y a `corpus.go` en el mismo commit.

---

## 4. Congelación de identificadores y de tiempo: los formatos exactos

El grabador sustituye los generadores de identificadores por versiones deterministas y congela
`time.time()`. Estos son los valores exactos que aparecen en el corpus.

### Tiempo

`FROZEN_TIME = 1704110400.0`, es decir `2024-01-01T12:00:00Z`, el mismo valor que ya usan los tests
del original. Solo se congela `time.time()`, y solo en los módulos que lo usan para sellos de
tiempo en respuestas: `kiro.streaming_openai` (campo `created` de los chunks), `kiro.models_openai`
(`created` de las respuestas), `kiro.mcp_tools` y `kiro.truncation_state`.

`kiro.cache` y `kiro.account_manager` quedan fuera **a propósito**: usan `time.time()` para TTL y
congelarlos rompe tests del original.

### Identificadores

`n` es un contador global de sesión, uno por familia, que empieza en 1.

| Generador del original | Formato determinista | Ejemplo |
|---|---|---|
| `kiro.utils.generate_completion_id` | `chatcmpl-{n:032x}` | `chatcmpl-00000000000000000000000000000029` |
| `kiro.utils.generate_tool_call_id` | `call_{n:08x}` | `call_0000001a` |
| `kiro.streaming_anthropic.generate_message_id` | `msg_{n:024x}` | `msg_00000000000000000000007e` |
| `kiro.streaming_anthropic.generate_thinking_signature` | `sig_{n:032x}` | `sig_00000000000000000000000000000004` |
| `uuid.uuid4()` | `UUID(int=(n << 96) \| n, version=4)` | `00000001-0000-4000-8000-000000000001` |

El contador de `uuid4` va en los 32 bits altos para que los recortes que hace el original
(`hex[:8]`, `hex[:24]`, `hex[:32]`) sigan siendo distintos entre llamadas. `uuid.uuid4` se parchea
en los módulos que construyen identificadores en línea sin pasar por los generadores:
`kiro.mcp_tools` (`msg_{uuid4().hex[:24]}`, `srvtoolu_{uuid4().hex[:32]}`),
`kiro.streaming_anthropic` (`toolu_{uuid4().hex[:24]}`) y `kiro.utils`.

`kiro.utils.generate_conversation_id` **no** se sustituye: no es aleatoria, es un sha256 estable del
historial de mensajes, y solo cae a `uuid4()` cuando la llaman sin mensajes. Se graba como objetivo y
lo único que se hace determinista es esa rama. Ver la nota sobre ella en el apartado 5.

### Lo que no se graba, y por qué

`tools/corpus/targets.py` lleva el diccionario `EXCLUDED` con el motivo de cada exclusión. Dos
merecen mención aquí porque el motivo es el mismo y no es obvio:

- `kiro.utils:get_machine_fingerprint` y `kiro.utils:get_kiro_headers` dependen del `sha256` del
  hostname y del usuario. Un golden así **solo vale en la máquina que lo grabó**: pondría el CI en
  rojo aunque el port fuese correcto, y además publicaría un identificador pseudónimo estable del
  desarrollador en un fichero versionado. La prueba correcta la define §8.4 del diseño: comparar
  Python y Go **en la misma máquina**, sin pasar por el corpus.

---

## 5. Conflictos de salida

Un **conflicto** es una entrada a la que el original contestó con dos salidas distintas.

La regla original del diseño era deduplicar por el hash de la entrada, sin más. Con esa regla, la
segunda salida se descartaba en silencio y se contaba como duplicado: el corpus tenía 3221 descartes
así y nadie sabía cuántos eran contradictorios. Sobre un corpus así, el criterio de terminado
(§8.5: «si todo caso del corpus pasa, hay paridad») es **falso**.

Regla vigente, en `recorder.py`:

1. Al colisionar el digest de la entrada se **compara la salida** (en las secuencias, las salidas de
   los pasos; el digest de una secuencia se calcula solo sobre la parte de entrada de cada paso).
2. Salidas iguales → duplicado legítimo, se descarta y se cuenta en `duplicates`.
3. Salidas distintas → **conflicto**, registrado en la sección `conflicts` de `_report.json` con el
   objetivo, el digest de la entrada, la entrada, todas las variantes de salida observadas, cuántas
   llamadas produjo cada una y **el test que produjo cada variante**.

Cada conflicto se clasifica comparando las salidas otra vez con los identificadores generados
enmascarados:

- **`identifier_only`**: las salidas coinciden al normalizar los identificadores. No es una
  contradicción de conducta, pero sí rompe la comparación byte a byte, así que el objetivo tiene que
  estar declarado en `known_conflicting_targets` de `tools/corpus/floors.json`, con su causa y con
  **qué debe hacer la fase que lo consuma**. Sin declarar, `validate.py` falla.
- **`behavioural`**: las salidas no se reconcilian. El corpus afirmaría dos respuestas para la misma
  pregunta y ninguna fase posterior puede cumplir las dos. **Falla siempre y no se puede declarar
  como conocido**: la salida es completar la entrada grabada (`CONFIG_INPUTS`, desenlace del stream)
  o retirar el objetivo. Si se pudiera silenciar con una nota, la nota se escribiría una vez y nadie
  la volvería a leer, que es exactamente como se llegó a tener 3221 descartes ciegos.

Falla, no avisa: un aviso no protege la implicación de §8.5.

### Estado actual: 13 conflictos, los 13 `identifier_only`

| Objetivo | Entradas | Llamadas descartadas | Qué tiene que hacer la fase que lo use |
|---|---|---|---|
| `streaming_openai/stream_kiro_to_openai_internal` | 9 | 19 | Inyectar el generador de ids en el port y comparar contra el id del caso, o normalizar `chatcmpl-<hex>` en los dos lados |
| `streaming_anthropic/stream_kiro_to_anthropic` | 3 | 4 | Igual, con `msg_<hex>`, `sig_<hex>` y `toolu_<hex>` |
| `utils/generate_conversation_id` | 1 | 44 | Comprobar la **forma** (que sea un uuid4 válido), no el valor |

Los tres son la misma causa: el identificador sale de un contador global de sesión, así que dos
llamadas con la misma entrada emiten la misma salida con distinto id.

El de `generate_conversation_id` merece una advertencia aparte. Los cuatro sitios del original que
la llaman (`routes_openai:323,571` y `routes_anthropic:376,684`) la llaman **sin mensajes**, así que
la única rama que la suite ejecuta es la de respaldo, la del `uuid4`. **La rama del hash estable del
historial no tiene ninguna cobertura golden**, por mucho que el objetivo aparezca en el corpus con un
caso: hay que portarla con tests escritos a mano a partir del algoritmo (`kiro/utils.py:102`).

---

## 6. Cobertura de los formatters SSE

Medición sobre el corpus actual, que es la que vale. Una «invocación» es una llamada real durante la
suite; un «caso» es una entrada distinta que quedó en `testdata/`.

| Objetivo | Invocaciones | Capturadas | Casos | Casos con eventos |
|---|---|---|---|---|
| `streaming_openai/stream_kiro_to_openai_internal` | 63 | 63 (100 %) | 40 | 40 |
| `streaming_anthropic/stream_kiro_to_anthropic` | 25 | 24 (96 %) | 20 | 19 |
| **Total de los dos generadores** | **88** | **87 (98,9 %)** | **60** | **59** |
| `streaming_anthropic/format_sse_event` | 159 | 159 (100 %) | 83 | — |

La única invocación no capturada es una en la que el test consumidor cortó el stream a mitad: sus
fragmentos son un prefijo del stream y no valen como salida golden, así que el grabador la descarta y
lo dice en `skipped`.

**Sobre las cifras de commits anteriores.** El commit `a3b802c` publicó 68 invocaciones y 38 casos
distintos. Esa medición se hizo con una ejecución parcial y con un grabador anterior, y las dos
cifras han cambiado por motivos distintos:

- Las **invocaciones** reales siempre fueron 88, no 68: la medición parcial no las contó todas.
- Los **casos** pasaron de 38 a 51 cuando el grabador aprendió a serializar modelos pydantic
  (commit `9ee6e5a`), porque hasta entonces las invocaciones con `request_messages` o `request_tools`
  como modelos pydantic se descartaban enteras. Y de 51 a **60** en esta ronda, al meter en la
  entrada grabada las banderas de configuración del módulo de streaming y el desenlace del stream:
  entradas que antes colapsaban en un caso ahora se distinguen.

En los tres momentos el corpus supera con holgura el umbral del 30 % del criterio de aceptación 10, y
sirve para demostrar paridad de SSE en la fase 5.

---

## 7. `_report.json`

No es un caso, es el informe de la grabación. `snapshot.py` lo ignora al comparar instantáneas
(cambia entre generaciones aunque el corpus sea idéntico), pero `validate.py` y
`TestCorpusReportMatchesFiles` lo leen y lo contrastan con los ficheros.

| Sección | Qué es |
|---|---|
| `commit` | Commit del original |
| `max_per_target` | Tope de casos por objetivo (500). El recorte elige por **hash ordenado**, así que bajar el tope recorta siempre los mismos casos |
| `upstream_exit_status` | Código de salida de la suite. `corpus:record` ignora el fallo de pytest a propósito, para que un test roto no tire la grabación; esto es lo que hace que no pase desapercibido |
| `test_order` | Número de tests y huella del orden canónico |
| `config` | Huella y volcado de la configuración efectiva, más los `.env` encontrados |
| `recorded` | Casos escritos por objetivo. Tiene que cuadrar exactamente con los ficheros |
| `duplicates` | Llamadas con la misma entrada **y** la misma salida |
| `conflicts` | Ver el apartado 5 |
| `skipped` | Llamadas no grabadas, con el motivo |
| `total_cases` | Suma de `recorded` |

---

## 8. Qué protege qué

| Comprobación | Dónde | Qué detecta |
|---|---|---|
| `tools/corpus/validate.py` | `task corpus:validate` y **CI** | Estructura, commit por caso, informe frente a ficheros, política de conflictos, mínimos de `floors.json`, correspondencia con `targets.py` |
| `TestCorpusIsReadable` | `go test ./...` | Que cada caso deserializa y respeta las invariantes de su `kind` |
| `TestCorpusReportMatchesFiles` | `go test ./...` | Recuento exacto por objetivo, `exitstatus` de la suite y ausencia de conflictos de conducta |
| `snapshot.py compare` / `variance` | `task corpus:verify-deterministic[-3]` | Que N generaciones seguidas salen idénticas, con desglose por objetivo |
| `floors.json` | `validate.py` | Que la validación no pueda pasar en vacío ni tras perder casos u objetivos |

`validate.py` solo necesita la biblioteca estándar y `tools/corpus/targets.py`: no hace falta el
clon del original ni su venv, y por eso puede correr en CI con un simple paso de Python.
