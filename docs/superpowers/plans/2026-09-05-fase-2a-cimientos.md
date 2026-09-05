# Fase 2a: Cimientos sin dependencias — Plan de implementación

> **Para trabajadores agénticos:** SUB-SKILL OBLIGATORIA: usa
> `superpowers:subagent-driven-development` (recomendado) o `superpowers:executing-plans` para
> ejecutar este plan tarea por tarea. Los pasos usan sintaxis de casilla (`- [ ]`).

**Objetivo:** portar los paquetes Go que no dependen de ningún otro paquete del proyecto: la
configuración, las utilidades de identidad, el marshaller compatible con Python, y las cuatro
taxonomías de error.

**Arquitectura:** siete paquetes bajo `internal/`, todos hoja en el grafo de dependencias. Tres se
verifican con el corpus golden de la fase 1 (`accounterrors`, `kiroerrors`, `networkerrors`, 88
casos entre los tres) y cuatro con tests escritos a mano, porque su lógica no es una función pura
instrumentable: leer variables de entorno, calcular un hash de la máquina, emular `json.dumps`, y
dar forma a un error de validación.

**Stack:** Go 1.27, `CGO_ENABLED=0`, sin dependencias externas nuevas. Todo con la biblioteca
estándar.

**Spec:** `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md`
**Contrato del corpus:** `docs/CORPUS.md`
**Fase anterior:** `docs/superpowers/plans/2026-09-04-fase-1-andamiaje-y-corpus.md`

## Restricciones globales

- Module path `github.com/marr-cloud/kiro-gateway-go`, Go 1.27, `CGO_ENABLED=0`.
- Todo fichero `.go`, incluidos los `_test.go`, lleva como dos primeras líneas:
  ```go
  // SPDX-License-Identifier: AGPL-3.0-or-later
  // Port a Go de jwadow/kiro-gateway. Ver NOTICE.
  ```
- Ninguna dependencia externa nueva en esta fase. Si crees que hace falta una, párate y repórtalo.
- Ningún test toca la red real.
- Origen a portar: `.upstream/kiro/`, fijado en el commit `a5292ca`. Está en el árbol de trabajo,
  ignorado por git. Si no está, se recrea con `task corpus:setup`.
- Los nombres de paquete Go son el nombre del módulo Python sin guiones bajos. Al terminar cada
  tarea se rellena la columna `Ficheros Go` de `docs/MAPPING.md`.
- `task lint` y `task test` en verde antes de cada commit.
- Mensajes de commit con asunto en inglés y prefijo convencional.

## Cómo se consume el corpus

La API está congelada en `internal/testutil` desde la fase 1. Lo que usarás:

```go
func LoadCorpus(tb testing.TB, target string) []Case   // target: "kiro_errors/enhance_kiro_error"
func Args(tb testing.TB, input json.RawMessage) []json.RawMessage
func Arg(tb testing.TB, input json.RawMessage, index int) json.RawMessage
func Kwargs(tb testing.TB, input json.RawMessage) map[string]json.RawMessage
func Kwarg(tb testing.TB, input json.RawMessage, name string) json.RawMessage
func Config(input json.RawMessage) map[string]json.RawMessage
func IsException(raw json.RawMessage) bool
func DecodeException(raw json.RawMessage) (*Exception, error)
func DecodeBytes(raw json.RawMessage) ([]byte, error)
```

Cada `Case` tiene `Target`, `Kind`, `Input`, `Output`, `Steps`, `Notes`, `Commit` y `Name`. `Name`
es el hash de la entrada y es lo que identifica el caso que falla: **inclúyelo siempre en el
mensaje de error**, o un fallo entre cientos de casos es indiagnosticable.

Lee `docs/CORPUS.md` antes de empezar. En particular su sección 3, con las codificaciones con
marcador, y su sección 5, con los conflictos declarados.

---

## Estructura de ficheros

| Fichero | Responsabilidad |
|---|---|
| `internal/pyjson/dumps.go` | Reformatea JSON con los separadores de Python |
| `internal/pyjson/str.go` | Emula `str()` de Python para los tipos que aparecen |
| `internal/pyjson/pyjson_test.go` | Tests de ambos, escritos a mano |
| `internal/config/config.go` | El struct `Config` y `Load` |
| `internal/config/parse.go` | Conversión y validación de cada tipo de valor |
| `internal/config/dotenv.go` | Lectura del `.env`, incluida la lectura en crudo |
| `internal/config/config_test.go` | Tests de las 35 variables y sus rarezas |
| `internal/utils/fingerprint.go` | Huella de la máquina |
| `internal/utils/ids.go` | Generadores de identificadores |
| `internal/utils/headers.go` | Cabeceras salientes hacia Kiro y la interfaz `TokenProvider` |
| `internal/utils/utils_test.go` | Tests a mano |
| `internal/accounterrors/classify.go` | `Fatal` o `Recoverable` |
| `internal/accounterrors/classify_test.go` | Golden, 27 casos |
| `internal/kiroerrors/enhance.go` | Enriquecimiento de errores de Kiro |
| `internal/kiroerrors/enhance_test.go` | Golden, 21 casos |
| `internal/networkerrors/types.go` | `Category`, `Info` y sus valores |
| `internal/networkerrors/classify.go` | Clasificación de errores de transporte |
| `internal/networkerrors/messages.go` | Formateo para el usuario |
| `internal/networkerrors/networkerrors_test.go` | Golden, 40 casos entre tres objetivos |
| `internal/validationerrors/validation.go` | Forma de la respuesta 422 |
| `internal/validationerrors/validation_test.go` | Tests a mano |

---

### Task 1 — internal/pyjson

**Ficheros:**
- Crear: `internal/pyjson/dumps.go`, `internal/pyjson/str.go`, `internal/pyjson/pyjson_test.go`

**Interfaces:**
- Produce: `pyjson.Dumps(raw json.RawMessage) (string, error)` y `pyjson.Str(raw json.RawMessage) string`.
  **La fase 2b los consume** desde el tokenizer y son la razón de que este paquete exista.

**Por qué existe este paquete.** El tokenizer del original cuenta los tokens de una cadena que
produce `json.dumps(x, ensure_ascii=False)`, y ese conteo aparece en el campo `usage` que ven los
clientes. Python escribe `{"a": 1, "b": 2}`, con espacio tras los dos puntos y tras la coma;
`encoding/json` de Go escribe `{"a":1,"b":2}`. Son cadenas distintas y cuentan distinto número de
tokens. Además el tokenizer llama a `str()` sobre valores booleanos, y en Python `str(True)` es
`"True"` con mayúscula.

**El diseño, y es lo que hace este paquete tratable.** `Dumps` **no serializa un mapa de Go**:
reformatea los bytes JSON originales. Python hace `json.loads` y después `json.dumps`, y como el
diccionario preserva el orden del documento, el resultado es el JSON de entrada reformateado.
Trabajando sobre los bytes originales el orden de claves sale correcto por construcción, sin
necesidad de mapas ordenados. Usa `json.Decoder` con `UseNumber()` y recorre los tokens.

**Las reglas exactas que debes reproducir:**

| Aspecto | Python | Go por defecto | Lo que debes hacer |
|---|---|---|---|
| Separador de elementos | `, ` | `,` | Emitir `, ` |
| Separador clave-valor | `: ` | `:` | Emitir `: ` |
| No ASCII | se emite tal cual con `ensure_ascii=False` | se emite tal cual | Nada |
| `<`, `>`, `&` | no se escapan | se escapan a `\u003c` etc. | `SetEscapeHTML(false)` o escapar a mano |
| Escapes obligatorios | `"`, `\`, y control `< 0x20` | igual | Nada |
| Enteros | tal cual | tal cual | Nada |
| Floats | reglas de `repr`: `1.0` se escribe `1.0` | `1` | Emitir con `.0` cuando el float es entero |
| `true`/`false`/`null` | igual | igual | Nada |

- [ ] **Paso 1: Escribir los tests que fallan**

Crear `internal/pyjson/pyjson_test.go` con la cabecera de licencia y estos casos, en formato
table-driven. Son los que distinguen el comportamiento de Python del de Go:

```go
func TestDumps(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"objeto simple", `{"a":1,"b":2}`, `{"a": 1, "b": 2}`},
		{"objeto anidado", `{"a":{"b":[1,2]}}`, `{"a": {"b": [1, 2]}}`},
		{"objeto vacio", `{}`, `{}`},
		{"array vacio", `[]`, `[]`},
		{"orden preservado", `{"z":1,"a":2}`, `{"z": 1, "a": 2}`},
		{"float entero lleva .0", `{"x":1.0}`, `{"x": 1.0}`},
		{"float no entero", `{"x":1.5}`, `{"x": 1.5}`},
		{"entero no lleva .0", `{"x":1}`, `{"x": 1}`},
		{"no ascii sin escapar", `{"k":"Moscú"}`, `{"k": "Moscú"}`},
		{"html sin escapar", `{"k":"a<b>c&d"}`, `{"k": "a<b>c&d"}`},
		{"comilla escapada", `{"k":"a\"b"}`, `{"k": "a\"b"}`},
		{"barra invertida", `{"k":"a\\b"}`, `{"k": "a\\b"}`},
		{"salto de linea", `{"k":"a\nb"}`, `{"k": "a\nb"}`},
		{"null", `{"k":null}`, `{"k": null}`},
		{"booleanos", `{"t":true,"f":false}`, `{"t": true, "f": false}`},
		{"cadena suelta", `"hola"`, `"hola"`},
		{"numero suelto", `42`, `42`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Dumps(json.RawMessage(c.in))
			if err != nil {
				t.Fatalf("Dumps(%s): %v", c.in, err)
			}
			if got != c.want {
				t.Errorf("Dumps(%s) = %s, quiero %s", c.in, got, c.want)
			}
		})
	}
}

func TestStr(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"true en mayuscula", `true`, "True"},
		{"false en mayuscula", `false`, "False"},
		{"null es None", `null`, "None"},
		{"entero", `42`, "42"},
		{"float entero", `1.0`, "1.0"},
		{"float", `1.5`, "1.5"},
		{"cadena sin comillas", `"hola"`, "hola"},
		{"cadena vacia", `""`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Str(json.RawMessage(c.in)); got != c.want {
				t.Errorf("Str(%s) = %q, quiero %q", c.in, got, c.want)
			}
		})
	}
}

func TestDumpsRejectsInvalidJSON(t *testing.T) {
	if _, err := Dumps(json.RawMessage(`{"a":`)); err == nil {
		t.Fatal("quiero error con JSON invalido, obtuve nil")
	}
}
```

- [ ] **Paso 2: Ejecutar y comprobar que falla**

Ejecutar: `go test ./internal/pyjson/`
Esperado: fallo de compilación, `undefined: Dumps`.

- [ ] **Paso 3: Implementar Dumps**

Crear `internal/pyjson/dumps.go`. El paquete lleva un comentario de paquete que explique por qué
existe, con el ejemplo del espacio en los separadores: quien lo lea dentro de un año tiene que
entender por qué no se usa `encoding/json` directamente.

Implementación: `json.Decoder` sobre los bytes con `UseNumber()`, recorriendo tokens y escribiendo
a un `strings.Builder`. Para las cadenas, escribe tú el escapado en vez de delegar en
`json.Marshal`, porque `Marshal` escapa `<`, `>` y `&`. Para los números, `json.Number`: si
`strings.ContainsAny(s, ".eE")` es un float y hay que asegurar que lleve parte decimal; si no, va
tal cual.

- [ ] **Paso 4: Implementar Str**

Crear `internal/pyjson/str.go`. Discrimina por el primer byte no blanco del JSON: `t` es `True`,
`f` es `False`, `n` es `None`, `"` es la cadena sin comillas y con los escapes resueltos, y el
resto es el número con las reglas de `repr`. Los contenedores no aparecen en el uso real del
tokenizer; si te llega uno, devuelve la forma de `repr` de Python con comillas simples y anota en
el informe que apareció, porque significa que el uso es más amplio de lo previsto.

- [ ] **Paso 5: Ejecutar y comprobar que pasa**

Ejecutar: `go test ./internal/pyjson/ -v`
Esperado: `PASS` en los 26 subtests.

- [ ] **Paso 6: Commit**

```bash
git add internal/pyjson
git commit -m "feat(pyjson): emulate Python json.dumps separators and str()"
```

---

### Task 2 — internal/config

**Ficheros:**
- Crear: `internal/config/config.go`, `internal/config/parse.go`, `internal/config/dotenv.go`,
  `internal/config/config_test.go`

**Interfaces:**
- Consume: nada.
- Produce: el struct `Config` con un campo por variable, y `config.Load() (*Config, error)`.
  **Todas las fases posteriores leen la configuración de aquí y de ningún otro sitio.** Ningún
  paquete llama a `os.Getenv` por su cuenta.

**Fuente a portar:** `.upstream/kiro/config.py`. Las 35 variables con su nombre, tipo y valor por
defecto exactos están en la sección §7.2 del spec, que es la referencia normativa para esta tarea:
tenla abierta y ve una por una.

**Las cuatro rarezas que se replican a propósito.** Están así en el original y cambiarlas rompe
compatibilidad:

1. **Lectura en crudo del `.env`.** `KIRO_CREDS_FILE` y `KIRO_CLI_DB_FILE` se leen del fichero
   `.env` **sin procesar secuencias de escape**, y solo si eso falla se recurre a la variable de
   entorno. Sin esto, una ruta de Windows como `C:\Users\x\creds.json` se corrompe porque `\U`
   se interpreta como escape. Ver `config.py` y su función de lectura cruda.
2. **Degradación silenciosa.** Un valor no reconocido cae al valor por defecto sin error ni aviso:
   `DEBUG_MODE=potato` resulta en `off`, y `FAKE_REASONING_HANDLING=xyz` en `as_reasoning_content`.
3. **Booleanos.** La regla general es `strings.ToLower(v)` contenido en `{"true","1","yes"}`.
4. **`FAKE_REASONING` va invertida, y es la única.** Está activa **salvo** que el valor esté en
   `{"false","0","no","disabled","off"}`. Un valor vacío o ausente la **activa**.

**No hay corpus para este paquete**: leer variables de entorno no es una función pura y no se
instrumentó. Los tests van a mano.

- [ ] **Paso 1: Escribir los tests que fallan**

Crear `internal/config/config_test.go`. Usa `t.Setenv`, que restaura el valor al terminar el test.
Cubre como mínimo:

```go
func TestLoadDefaults(t *testing.T) {
	// Ninguna variable puesta: comprueba los valores por defecto de las 35.
	// Como mínimo estas, que son las que más se consultan:
	//   PROXY_API_KEY   = "my-super-secret-password-123"
	//   SERVER_HOST     = "0.0.0.0"
	//   SERVER_PORT     = 8000
	//   KIRO_REGION     = "us-east-1"
	//   FIRST_TOKEN_TIMEOUT = 15
	//   STREAMING_READ_TIMEOUT = 300
	//   FIRST_TOKEN_MAX_RETRIES = 3
	//   KIRO_MAX_PAYLOAD_BYTES = 600000
	//   TOOL_DESCRIPTION_MAX_LENGTH = 10000
	//   ACCOUNT_RECOVERY_TIMEOUT = 60
	//   ACCOUNT_MAX_BACKOFF_MULTIPLIER = 1440.0
	//   ACCOUNT_PROBABILISTIC_RETRY_CHANCE = 0.1
	//   ACCOUNT_CACHE_TTL = 43200
	//   STATE_SAVE_INTERVAL_SECONDS = 10
	//   DEBUG_MODE = "off", DEBUG_DIR = "debug_logs", LOG_LEVEL = "INFO"
	//   ACCOUNTS_CONFIG_FILE = "credentials.json", ACCOUNTS_STATE_FILE = "state.json"
	// Y los booleanos: SQLITE_READONLY=false, ACCOUNT_SYSTEM=false,
	//   AUTO_TRIM_PAYLOAD=false, TRUNCATION_RECOVERY=true, WEB_SEARCH_ENABLED=true.
}

func TestFakeReasoningIsInverted(t *testing.T) {
	// Vacío o ausente: activa. "false", "0", "no", "disabled", "off": desactivada.
	// Cualquier otra cosa, incluido "potato": ACTIVA. Es lo que hace el original.
}

func TestBooleanParsing(t *testing.T) {
	// Para una variable booleana normal como SQLITE_READONLY:
	// "true", "1", "yes" y sus variantes en mayúsculas activan; el resto no.
	// En particular "TRUE" activa y "potato" no.
}

func TestEnumsFallBackSilently(t *testing.T) {
	// DEBUG_MODE: "off", "errors", "all" se aceptan; "potato" y "" caen a "off".
	// FAKE_REASONING_HANDLING: los cuatro válidos se aceptan;
	//   cualquier otro cae a "as_reasoning_content".
}

func TestCredsPathFromDotenvIsNotUnescaped(t *testing.T) {
	// Escribe un .env temporal con KIRO_CREDS_FILE=C:\Users\x\creds.json
	// y comprueba que el valor cargado conserva las barras invertidas literales.
	// Este es el test que protege a los usuarios de Windows.
}

func TestCLIOverridesEnvironment(t *testing.T) {
	// El host y el puerto que vienen por flag ganan a la variable de entorno,
	// que a su vez gana al valor por defecto.
}
```

- [ ] **Paso 2: Ejecutar y comprobar que falla**

Ejecutar: `go test ./internal/config/`
Esperado: fallo de compilación.

- [ ] **Paso 3: Implementar el parseo de valores**

Crear `internal/config/parse.go` con los ayudantes: cadena con defecto, entero con defecto, float
con defecto, booleano con la regla general, booleano invertido para `FAKE_REASONING`, y enumerado
con lista blanca y caída silenciosa. Cada uno de una sola responsabilidad y testeable por separado.

**Un valor inválido en un entero o un float cae al valor por defecto, no da error**, igual que el
original: `int(os.getenv(...))` en Python lanzaría, pero el original nunca pone valores inválidos
en producción y el comportamiento observable que importa es no reventar al arrancar. Si al portar
ves que el original **sí** revienta con un entero inválido, replica eso en su lugar y dilo en el
informe: la fuente manda sobre esta nota.

- [ ] **Paso 4: Implementar la lectura del `.env`**

Crear `internal/config/dotenv.go`. Dos funciones: la carga normal, que pone en el entorno del
proceso los pares del fichero sin sobrescribir las variables ya presentes, y la lectura en crudo
de una clave concreta, que devuelve el valor textual tal cual aparece en el fichero sin procesar
escapes. Compara con `config.py` para respetar el orden de precedencia exacto.

- [ ] **Paso 5: Implementar Config y Load**

Crear `internal/config/config.go`. Un campo por variable, con el nombre Go idiomático y un
comentario que indique el nombre de la variable de entorno. `Load` acepta los valores que vengan
del CLI para que la precedencia se resuelva en un solo sitio.

- [ ] **Paso 6: Ejecutar y comprobar que pasa**

Ejecutar: `go test ./internal/config/ -v`

- [ ] **Paso 7: Verificar que están las 35**

Comprueba a mano contra la tabla §7.2 del spec que no falta ninguna variable y que ningún valor
por defecto difiere. Pega en el informe la lista de las 35 con el valor que devuelve tu `Load` sin
nada puesto en el entorno, para que el revisor pueda compararla con la tabla sin recalcularla.

- [ ] **Paso 8: Commit**

```bash
git add internal/config
git commit -m "feat(config): port the 35 environment variables with exact defaults"
```

---

### Task 3 — internal/utils: huella e identificadores

**Ficheros:**
- Crear: `internal/utils/fingerprint.go`, `internal/utils/ids.go`, `internal/utils/utils_test.go`

**Interfaces:**
- Produce: `utils.MachineFingerprint() string`, y los generadores de identificadores con sus
  formatos exactos.

**Fuente a portar:** `.upstream/kiro/utils.py`.

**La huella.** Es `sha256("{hostname}-{username}-kiro-gateway")` en hexadecimal minúscula. Va
dentro del `User-Agent` que se envía a Kiro, así que **tiene que dar exactamente el mismo valor
que el original en la misma máquina**.

**No la compares contra el corpus.** La fase 1 retiró ese objetivo a propósito: su valor depende
de la máquina que grabó, así que un caso golden pondría el CI en rojo con una implementación
correcta. La comprobación correcta, que es la que pide la sección §8.4 del spec, es ejecutar el
Python y el Go en la misma máquina y comparar. Eso es la tarea 6 de este plan.

**Los identificadores.** Cuatro generadores con formatos que los clientes reconocen. Sácalos de
`utils.py` y de `streaming_anthropic.py`, y respeta el prefijo y la longitud de cada uno. Ojo con
uno: `generate_conversation_id` **no es aleatorio**, es un hash estable de los mensajes, y solo
cae a un identificador aleatorio si no hay mensajes. Pórtalo con esa estructura.

**Sobre el único caso de corpus de `utils/generate_conversation_id`:** su salida es
`00000001-0000-4000-8000-000000000001`, que es el identificador determinista que la fase 1 inyectó
durante la grabación, y corresponde a la rama aleatoria. **Ese caso no sirve para verificar nada**
y no debes escribir un test contra él: comparar contra un valor inyectado solo demuestra que
copiaste el valor. Escribe en su lugar un test a mano de la rama del hash, derivado del algoritmo
del original, y deja un comentario explicando por qué se ignora el caso golden.

- [ ] **Paso 1: Escribir los tests que fallan**

Crear `internal/utils/utils_test.go`:

```go
func TestMachineFingerprintIsStable(t *testing.T) {
	// Dos llamadas dan el mismo valor.
	// Longitud 64 y solo dígitos hexadecimales minúsculos.
}

func TestMachineFingerprintMatchesTheFormula(t *testing.T) {
	// Calcula sha256("hostname-username-kiro-gateway") con los valores que
	// devuelve el sistema y compara con MachineFingerprint().
	// Esto verifica la fórmula, no el valor, que depende de la máquina.
}

func TestConversationIDFromMessagesIsStable(t *testing.T) {
	// Con los mismos mensajes, dos llamadas dan el mismo identificador.
	// Con mensajes distintos, identificadores distintos.
	// Sin mensajes, dos llamadas dan identificadores DISTINTOS: es la rama aleatoria.
}

func TestIDFormats(t *testing.T) {
	// Para cada generador: comprueba el prefijo y la longitud que espera el cliente.
	// Los formatos exactos salen de utils.py y streaming_anthropic.py.
}
```

- [ ] **Paso 2: Ejecutar y comprobar que falla**

Ejecutar: `go test ./internal/utils/`

- [ ] **Paso 3: Implementar la huella**

Crear `internal/utils/fingerprint.go`. `os.Hostname()` y `user.Current()`. Decide qué hacer si
alguno falla y **compáralo con lo que hace el original**: si el original tiene un valor de reserva,
usa el mismo; si no, replica su comportamiento. Cachea el resultado con `sync.Once`: no cambia
durante la vida del proceso y se consulta en cada petición.

- [ ] **Paso 4: Implementar los identificadores**

Crear `internal/utils/ids.go`. Usa `crypto/rand` para las partes aleatorias. La generación tiene
que ser **inyectable para los tests**, porque las fases 4 y 5 comparan contra casos golden grabados
con identificadores congelados: expón una variable de paquete o un campo que los tests puedan
sustituir, y documenta el contrato de congelación que describe la sección 4 de `docs/CORPUS.md`.

- [ ] **Paso 5: Ejecutar y comprobar que pasa**

Ejecutar: `go test ./internal/utils/ -v`

- [ ] **Paso 6: Commit**

```bash
git add internal/utils
git commit -m "feat(utils): port the machine fingerprint and identifier generators"
```

---

### Task 4 — internal/utils: cabeceras salientes

**Ficheros:**
- Crear: `internal/utils/headers.go`
- Modificar: `internal/utils/utils_test.go`

**Interfaces:**
- Produce:
  ```go
  type TokenProvider interface {
      AccessToken(ctx context.Context) (string, error)
      ProfileARN() string
  }
  func GetKiroHeaders(ctx context.Context, tp TokenProvider) (http.Header, error)
  ```
- **La interfaz se declara aquí, en el consumidor, y no en `auth`.** Es lo que rompe el ciclo
  `utils` ↔ `auth` que existe en el original, donde `auth` importa `utils` para la huella y `utils`
  importa `auth` para el tipo. `auth.Manager` satisfará esta interfaz sin saberlo, en la fase 2d o
  la 3 según el orden final.

**Estas cabeceras son una frontera de paridad byte a byte.** El backend de Kiro puede validarlas y
el `User-Agent` mimetiza un cliente concreto. Los valores exactos, verificados contra
`.upstream/kiro/utils.py`:

```
Authorization: Bearer {token}
Content-Type: application/x-amz-json-1.0
x-amz-target: AmazonCodeWhispererStreamingService.GenerateAssistantResponse
User-Agent: aws-sdk-js/1.0.27 ua/2.1 os/win32#10.0.19044 lang/js md/nodejs#22.21.1 api/codewhispererstreaming#1.0.27 m/E KiroIDE-0.7.45-{fingerprint}
x-amz-user-agent: aws-sdk-js/1.0.27 KiroIDE-0.7.45-{fingerprint}
x-amzn-codewhisperer-optout: true
x-amzn-kiro-agent-mode: vibe
amz-sdk-invocation-id: {uuid}
amz-sdk-request: attempt=1; max=3
```

El `User-Agent` es **una sola línea sin saltos**: arriba aparece partido solo por ancho de página.

**No hay corpus para esto**: la fase 1 excluyó `get_kiro_headers` porque depende de la huella de la
máquina y de un uuid nuevo en cada llamada.

- [ ] **Paso 1: Escribir el test que falla**

Añadir a `internal/utils/utils_test.go` un test que use un `TokenProvider` de mentira y compruebe,
cabecera por cabecera, el valor exacto. Para las dos que contienen la huella, constrúyelas en el
test a partir de `MachineFingerprint()`. Para `amz-sdk-invocation-id`, comprueba que es un UUID
válido y que **dos llamadas dan valores distintos**.

Comprueba también que no sobra ninguna cabecera: cuenta las claves y compara con 9.

- [ ] **Paso 2: Ejecutar y comprobar que falla**

Ejecutar: `go test ./internal/utils/ -run TestKiroHeaders`

- [ ] **Paso 3: Implementar**

Crear `internal/utils/headers.go`. **Cuidado con la capitalización:** `http.Header.Set` canonicaliza
las claves a `X-Amz-Target`, y el original envía `x-amz-target` en minúsculas. Sobre HTTP/1.1 los
nombres de cabecera son insensibles a mayúsculas y Go las envía canonicalizadas, así que en la
práctica no importa; pero **compruébalo y dilo en el informe**, porque si el backend fuese
sensible habría que escribir el mapa directamente en vez de usar `Set`. Es exactamente el tipo de
detalle que solo se ve al portar.

- [ ] **Paso 4: Ejecutar y comprobar que pasa**

Ejecutar: `go test ./internal/utils/ -v`

- [ ] **Paso 5: Commit**

```bash
git add internal/utils
git commit -m "feat(utils): port the outbound Kiro headers with a consumer-side TokenProvider"
```

---

### Task 5 — internal/accounterrors

**Ficheros:**
- Crear: `internal/accounterrors/classify.go`, `internal/accounterrors/classify_test.go`

**Interfaces:**
- Produce: `accounterrors.Type` con los valores `Fatal` y `Recoverable`, y
  `accounterrors.Classify(statusCode int, reason string) Type`.
- **La fase 2d o la 3 lo consume** desde el gestor de cuentas para decidir si cambia de cuenta.

**Corpus:** `accounterrors` no existe como directorio; el objetivo se llama
`account_errors/classify_error` y tiene **27 casos**. Salidas observadas en el corpus: `"fatal"` y
`"recoverable"`, y nada más. Entradas: casos con 2 argumentos y casos con 0 argumentos, que
ejercitan los valores por defecto de los parámetros.

**Fuente a portar:** `.upstream/kiro/account_errors.py`.

La tabla que decide, del spec §6.11, y que el corpus verifica:

| Categoría | Códigos |
|---|---|
| Recuperable, se pasa a la siguiente cuenta | 402, 403, 429, y 400 con `INVALID_MODEL_ID` |
| Fatal, se devuelve al cliente tal cual | 400 con `CONTENT_LENGTH_EXCEEDS_THRESHOLD`, otros 400, 422, 5xx |

Lo contraintuitivo es deliberado: un contexto demasiado grande no se arregla cambiando de cuenta.

- [ ] **Paso 1: Escribir el test golden que falla**

Crear `internal/accounterrors/classify_test.go`:

```go
func TestClassifyAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "account_errors/classify_error") {
		t.Run(c.Name, func(t *testing.T) {
			args := testutil.Args(t, c.Input)

			// El corpus tiene casos con 0 y con 2 argumentos: los de 0 ejercitan
			// los valores por defecto de los parámetros del original.
			status, reason := defaultStatus, defaultReason
			if len(args) >= 1 {
				if err := json.Unmarshal(args[0], &status); err != nil {
					t.Fatalf("argumento 0: %v", err)
				}
			}
			if len(args) >= 2 {
				if err := json.Unmarshal(args[1], &reason); err != nil {
					t.Fatalf("argumento 1: %v", err)
				}
			}

			var want string
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("salida esperada: %v", err)
			}

			if got := string(Classify(status, reason)); got != want {
				t.Errorf("Classify(%d, %q) = %q, quiero %q", status, reason, got, want)
			}
		})
	}
}
```

`defaultStatus` y `defaultReason` los saca de la firma del original: mira los valores por defecto
de los parámetros en `account_errors.py` y usa esos.

- [ ] **Paso 2: Ejecutar y comprobar que falla**

Ejecutar: `go test ./internal/accounterrors/`
Esperado: fallo de compilación, `undefined: Classify`.

- [ ] **Paso 3: Implementar**

Crear `internal/accounterrors/classify.go`. `Type` es un `string` con constantes `Fatal = "fatal"` y
`Recoverable = "recoverable"`, para que el valor coincida con lo que el corpus grabó del enumerado
de Python. Los identificadores de razón como `INVALID_MODEL_ID` van como constantes con nombre, no
como literales sueltos por el código.

- [ ] **Paso 4: Ejecutar y comprobar que pasa**

Ejecutar: `go test ./internal/accounterrors/ -v`
Esperado: `PASS` con 27 subtests, uno por caso del corpus.

**Si algún caso falla, no ajustes el test: el corpus es la referencia.** Lee el fichero que el
nombre del subtest identifica, mira su entrada y su salida, y corrige la implementación.

- [ ] **Paso 5: Commit**

```bash
git add internal/accounterrors
git commit -m "feat(accounterrors): classify failover errors, verified against 27 golden cases"
```

---

### Task 6 — internal/kiroerrors

**Ficheros:**
- Crear: `internal/kiroerrors/enhance.go`, `internal/kiroerrors/enhance_test.go`

**Interfaces:**
- Produce:
  ```go
  type Info struct {
      Reason          string `json:"reason"`
      UserMessage     string `json:"user_message"`
      OriginalMessage string `json:"original_message"`
  }
  func Enhance(message, reason string) Info
  ```
  Los nombres de los campos JSON son los que el corpus grabó y **no se pueden cambiar**: el test
  compara contra ellos.

**Corpus:** `kiro_errors/enhance_kiro_error`, **21 casos**. La entrada es un objeto con `message` y
`reason`; la salida es un objeto con `reason`, `user_message` y `original_message`. Razones
presentes en el corpus, incluida la cadena vacía:

```
(vacía)  CONTENT_LENGTH_EXCEEDS_THRESHOLD  INVALID_MODEL_ID  MONTHLY_REQUEST_COUNT
RATE_LIMIT_EXCEEDED  SERVICE_UNAVAILABLE  SOME_ERROR  UNKNOWN  UNKNOWN_ERROR
UNKNOWN_FUTURE_ERROR  VALIDATION_ERROR
```

Que estén `SOME_ERROR`, `UNKNOWN` y `UNKNOWN_FUTURE_ERROR` te dice que hay un camino genérico para
razones que el original no conoce, y el corpus fija su forma: `"{mensaje} (reason: {razón})"`.

**Fuente a portar:** `.upstream/kiro/kiro_errors.py`. Los cuatro mensajes enriquecidos del spec
§6.15 son texto literal que el usuario ve: cópialos exactamente, sin reescribirlos.

- [ ] **Paso 1: Escribir el test golden que falla**

Crear `internal/kiroerrors/enhance_test.go`. La entrada es **un solo argumento que es un objeto**,
no dos argumentos, así que deserialízalo a un struct auxiliar. Compara el resultado completo:

```go
func TestEnhanceAgainstCorpus(t *testing.T) {
	for _, c := range testutil.LoadCorpus(t, "kiro_errors/enhance_kiro_error") {
		t.Run(c.Name, func(t *testing.T) {
			var in struct {
				Message string `json:"message"`
				Reason  string `json:"reason"`
			}
			if err := json.Unmarshal(testutil.Arg(t, c.Input, 0), &in); err != nil {
				t.Fatalf("entrada: %v", err)
			}
			var want Info
			if err := json.Unmarshal(c.Output, &want); err != nil {
				t.Fatalf("salida esperada: %v", err)
			}
			if got := Enhance(in.Message, in.Reason); got != want {
				t.Errorf("Enhance(%q, %q):\n  obtuve %+v\n  quiero %+v", in.Message, in.Reason, got, want)
			}
		})
	}
}
```

**Antes de escribir el test, abre uno de los 21 ficheros y confirma la forma de la entrada.** Puede
que además de `message` y `reason` haya otras claves; si las hay, inclúyelas en el struct auxiliar y
dilo en el informe.

- [ ] **Paso 2: Ejecutar y comprobar que falla**

Ejecutar: `go test ./internal/kiroerrors/`

- [ ] **Paso 3: Implementar**

Crear `internal/kiroerrors/enhance.go`. Los mensajes literales van como constantes con nombre. El
camino genérico para razones desconocidas tiene que producir exactamente la forma que el corpus
fija.

- [ ] **Paso 4: Ejecutar y comprobar que pasa**

Ejecutar: `go test ./internal/kiroerrors/ -v`
Esperado: `PASS` con 21 subtests.

- [ ] **Paso 5: Commit**

```bash
git add internal/kiroerrors
git commit -m "feat(kiroerrors): enrich upstream error messages, verified against 21 golden cases"
```

---

### Task 7 — internal/networkerrors

**Ficheros:**
- Crear: `internal/networkerrors/types.go`, `internal/networkerrors/classify.go`,
  `internal/networkerrors/messages.go`, `internal/networkerrors/networkerrors_test.go`

**Interfaces:**
- Produce:
  ```go
  type Category string
  type Info struct {
      Category            Category `json:"category"`
      UserMessage         string   `json:"user_message"`
      TroubleshootingSteps []string `json:"troubleshooting_steps"`
      TechnicalDetails    string   `json:"technical_details"`
      IsRetryable         bool     `json:"is_retryable"`
      SuggestedHTTPCode   int      `json:"suggested_http_code"`
  }
  func Classify(err error) Info
  func FormatForUser(info Info) string
  func ShortMessage(info Info) string
  ```
  Los nombres JSON son los que grabó el corpus y no se pueden cambiar.

**Un detalle que cuesta tiempo descubrir:** `Info` contiene un slice, así que **no es comparable
con `==`**. El compilador te lo dirá, pero por si acaso: usa `reflect.DeepEqual`, o mejor compara
las dos formas serializadas a JSON, que además da un mensaje de error legible cuando falla. En
`kiroerrors` sí se puede usar `==` porque todos sus campos son cadenas.

**Corpus:** tres objetivos, 40 casos en total.
- `network_errors/classify_network_error`: 27 casos
- `network_errors/format_error_for_user`: 4 casos
- `network_errors/get_short_error_message`: 9 casos

Las diez categorías que el corpus contiene, y no hay más:

```
connection_refused  connection_reset  dns_resolution  network_unreachable  proxy_error
ssl_error  timeout_connect  timeout_read  too_many_redirects  unknown
```

Códigos HTTP sugeridos observados: `500`, `502`, `504`.

**Fuente a portar:** `.upstream/kiro/network_errors.py`.

**Esta es la tarea con el problema de diseño interesante de la fase, y merece que lo leas entero
antes de escribir código.**

Los 27 casos de `classify_network_error` tienen como entrada una **excepción de Python
serializada**, con esta forma:

```json
{"__exception__": {"type": "ConnectError", "module": "httpx",
                   "args": ["Connection failed"], "str": "Connection failed",
                   "cause": {"__exception__": {"type": "gaierror", "module": "socket",
                                               "args": [11001, "getaddrinfo failed"],
                                               "str": "[Errno 11001] getaddrinfo failed"}}}}
```

Los tipos de excepción que aparecen, con su módulo:

```
httpx.ConnectError            httpx.ConnectError con causa socket.gaierror
httpx.ConnectTimeout          httpx.ProxyError
httpx.ReadTimeout             httpx.RequestError
httpx.TimeoutException        httpx.TooManyRedirects
builtins.Exception
```

En Go no existe ninguno de esos tipos. `Classify` recibirá errores de `net/http`, `net` y
`crypto/tls`. Así que el corpus **no se puede comparar pasándole una excepción**: hay que decidir
cómo se cruza esa frontera, y es una decisión de diseño, no un detalle de test.

Lo que propongo, y si ves algo mejor dilo antes de implementarlo:

`Classify(err error) Info` es la función que usa el resto del programa. Por debajo, la
clasificación real la hace una función interna que trabaja sobre una descripción del error, no
sobre el error: algo como `classify(kind errKind, message string, causeKind errKind) Info`, donde
`errKind` es un enumerado de las clases de fallo de transporte que importan. `Classify` traduce un
error de Go a esa descripción con `errors.As` y `errors.Is` sobre `*net.DNSError`, `*net.OpError`,
`*tls.CertificateVerificationError`, `os.ErrDeadlineExceeded`, `context.DeadlineExceeded` y
`context.Canceled`. El test golden traduce los nombres de excepción de Python a la misma
descripción con una tabla explícita.

Por qué así: la lógica de decisión queda verificada por los 27 casos del corpus, y la traducción
de errores de Go, que es lo único que el corpus no puede cubrir, queda como una tabla pequeña,
legible y testeable a mano. La alternativa de fabricar errores de Go que imiten a los de httpx
mezcla las dos cosas y no verifica ninguna bien.

**La tabla de traducción del test es contenido del port, no un apaño.** Documéntala: para cada
tipo de excepción de Python, qué clase de fallo representa y qué error de Go le corresponde en
producción. Un revisor tiene que poder juzgar si esa correspondencia es correcta, porque de ella
depende que la clasificación real haga lo mismo que el original.

Los otros dos objetivos son más simples: reciben un `Info` ya construido y devuelven una cadena,
así que se deserializa la entrada al struct y se compara la salida.

- [ ] **Paso 1: Inspeccionar el corpus antes de diseñar**

Abre al menos cinco de los 27 casos de `classify_network_error`, incluyendo el que tiene causa
`gaierror`, y uno de cada uno de los otros dos objetivos. Escribe en el informe qué forma tienen y
si la propuesta de arriba encaja. **Si no encaja, dilo antes de implementar.**

- [ ] **Paso 2: Escribir los tests que fallan**

Crear `internal/networkerrors/networkerrors_test.go` con tres funciones de test, una por objetivo
del corpus, más un test a mano de la traducción de errores de Go a clases de fallo. El de
clasificación usa `testutil.IsException` y `testutil.DecodeException` para leer la entrada.

Incluye en cada mensaje de error el nombre del caso, y para la clasificación imprime también el
tipo de excepción de origen: con 27 casos y diez categorías, un fallo sin esa información cuesta
mucho de localizar.

- [ ] **Paso 3: Ejecutar y comprobar que falla**

Ejecutar: `go test ./internal/networkerrors/`

- [ ] **Paso 4: Implementar los tipos**

Crear `internal/networkerrors/types.go` con `Category`, sus diez constantes, `Info`, y el
enumerado de clases de fallo.

- [ ] **Paso 5: Implementar la clasificación**

Crear `internal/networkerrors/classify.go` con la función interna que decide sobre la descripción,
y `Classify` que traduce un error de Go a esa descripción. Los mensajes al usuario y los pasos de
resolución son texto literal que el usuario ve: cópialos del original sin reescribirlos.

- [ ] **Paso 6: Implementar el formateo**

Crear `internal/networkerrors/messages.go` con `FormatForUser` y `ShortMessage`.

- [ ] **Paso 7: Ejecutar y comprobar que pasa**

Ejecutar: `go test ./internal/networkerrors/ -v`
Esperado: `PASS` con 40 subtests del corpus más los de la tabla de traducción.

- [ ] **Paso 8: Commit**

```bash
git add internal/networkerrors
git commit -m "feat(networkerrors): classify transport failures, verified against 40 golden cases"
```

---

### Task 8 — internal/validationerrors

**Ficheros:**
- Crear: `internal/validationerrors/validation.go`, `internal/validationerrors/validation_test.go`

**Interfaces:**
- Produce: el tipo de la respuesta 422 y la función que la construye a partir de una lista de
  fallos de validación.
- **La fase 3 la consume** desde los modelos de petición de los dos dialectos.

**Fuente a portar:** `.upstream/kiro/exceptions.py`.

**Sin corpus**: la forma del 422 depende de Pydantic y no se instrumentó.

**El alcance está deliberadamente recortado, y está en el spec §4.3.** No se persigue la forma
literal de Pydantic v2, con sus campos `input` y `url`. Se mantiene el envoltorio: `detail` con
una lista de fallos que llevan `loc`, `msg` y `type`, más `body` con el cuerpo de la petición
truncado a 500 caracteres. Eso está documentado como diferencia conocida en `docs/DIFFERENCES.md`.

Replica también el saneado que convierte valores de tipo bytes a cadena: sin él la respuesta no
es serializable a JSON, y es el motivo por el que la función existe en el original.

- [ ] **Paso 1: Escribir los tests que fallan**

Crear `internal/validationerrors/validation_test.go` cubriendo: un fallo simple, varios fallos, un
`loc` anidado, el truncado del cuerpo a 500 caracteres exactos, un cuerpo más corto que no se
trunca, y un valor de tipo bytes que se convierte a cadena.

Comprueba el resultado **serializado a JSON**, no el struct: lo que importa es la forma que ve el
cliente. Y comprueba que 500 significa 500: escribe un cuerpo de 501 caracteres y cuenta.

- [ ] **Paso 2: Ejecutar y comprobar que falla**

Ejecutar: `go test ./internal/validationerrors/`

- [ ] **Paso 3: Implementar**

Crear `internal/validationerrors/validation.go`.

- [ ] **Paso 4: Ejecutar y comprobar que pasa**

Ejecutar: `go test ./internal/validationerrors/ -v`

- [ ] **Paso 5: Commit**

```bash
git add internal/validationerrors
git commit -m "feat(validationerrors): build the 422 response envelope"
```

---

### Task 9 — Cierre de la fase: correspondencia y comprobación cruzada de la huella

**Ficheros:**
- Modificar: `docs/MAPPING.md`
- Crear: `tools/parity/fingerprint_test.go` o el equivalente que decidas

**Interfaces:**
- Consume: todo lo anterior.

Esta tarea cierra dos cosas que no pertenecen a ningún paquete concreto.

- [ ] **Paso 1: Rellenar la columna de ficheros Go en MAPPING.md**

Para los siete módulos que esta fase porta —`config.py`, `utils.py`, `kiro_errors.py`,
`network_errors.py`, `account_errors.py`, `exceptions.py`— rellena la columna `Ficheros Go` con las
rutas reales. Y añade `internal/pyjson` a la tabla de paquetes que no existen en el original, si
no está ya.

- [ ] **Paso 2: La comprobación cruzada de la huella**

Es lo que pide la sección §8.4 del spec y no se puede hacer con el corpus. Escribe una prueba que
ejecute el Python del upstream y el Go en la misma máquina y compare la huella:

```bash
.upstream/.venv/Scripts/python.exe -c "import sys; sys.path.insert(0,'.upstream'); from kiro.utils import get_machine_fingerprint; print(get_machine_fingerprint())"
```

Compara esa salida con la de `utils.MachineFingerprint()`. Ponla detrás de un build tag para que
el CI no la ejecute, porque necesita el clon del upstream y su entorno virtual, y documenta cómo
se lanza a mano. En Linux y macOS el intérprete está en `.upstream/.venv/bin/python`.

**Si las dos huellas no coinciden, eso es un hallazgo importante y hay que arreglarlo antes de
cerrar la fase**: el `User-Agent` que se envía a Kiro depende de ese valor.

- [ ] **Paso 3: Verificación completa**

```bash
task lint
task test
task corpus:validate
```

Los tres en código 0. Pega la salida de `go test ./... -v` filtrada a los paquetes nuevos, con el
recuento de subtests de cada objetivo del corpus, para que el revisor pueda comparar con los
recuentos que este plan declara: 27 de `account_errors/classify_error`, 21 de
`kiro_errors/enhance_kiro_error`, y 27 más 4 más 9 de los tres de `network_errors`.

- [ ] **Paso 4: Commit**

```bash
git add docs/MAPPING.md tools/parity
git commit -m "docs: map phase 2a packages and add the fingerprint parity check"
```

---

## Criterios de aceptación de la fase 2a

1. Siete paquetes nuevos bajo `internal/`, sin dependencias externas y sin depender entre sí más
   allá de lo que declara este plan.
2. `task lint`, `task test` y `task corpus:validate` en código 0.
3. Los 88 casos golden de los tres objetivos de error pasan: 27, 21 y 40.
4. Las 35 variables de configuración se leen con los nombres y valores por defecto exactos de la
   sección §7.2 del spec, y la lista completa con sus valores queda pegada en el informe.
5. Las cuatro rarezas de la configuración están replicadas y cada una tiene un test que la cubre,
   en especial la lógica invertida de `FAKE_REASONING` y la lectura en crudo de las rutas.
6. Las nueve cabeceras salientes coinciden byte a byte con las del original, y la capitalización
   quedó comprobada y documentada.
7. La huella de la máquina coincide entre el Python y el Go en la misma máquina, comprobado con la
   prueba cruzada de la tarea 9.
8. La interfaz `TokenProvider` está declarada en `internal/utils`, y no en `auth`, de modo que el
   ciclo del original no se reproduce.
9. `pyjson` reproduce los separadores de `json.dumps`, el `ensure_ascii=False`, el orden de claves
   y la forma de los floats, con un test por cada regla de la tabla de la tarea 1.
10. `docs/MAPPING.md` tiene la columna de ficheros Go rellena para los siete módulos de esta fase.

## Lo que esta fase deja preparado y lo que no

**Preparado:** la configuración que todo lo demás lee, la identidad que necesita el cliente HTTP,
el marshaller que necesita el tokenizer de la fase 2b, y las tres taxonomías de error que
necesitan el cliente HTTP y el gestor de cuentas.

**No preparado, y es a propósito:** `internal/validationerrors` construye la respuesta pero nadie
la usa hasta que existan los modelos de petición en la fase 3. `TokenProvider` no tiene
implementación hasta que exista `auth`. Y `pyjson` no tiene consumidor hasta el tokenizer.

Los tres son paquetes hoja terminados y verificados: que no tengan consumidor todavía es la
consecuencia de construir de abajo hacia arriba, no una tarea a medias.
