# Fase 1: Andamiaje y corpus — Plan de implementación

> **Para trabajadores agénticos:** SUB-SKILL OBLIGATORIA: usa
> `superpowers:subagent-driven-development` (recomendado) o `superpowers:executing-plans` para
> ejecutar este plan tarea por tarea. Los pasos usan sintaxis de casilla (`- [ ]`) para el
> seguimiento.

**Objetivo:** dejar el repositorio Go construible y con CI en verde, y generar con un solo comando
el corpus golden extraído de la suite de tests Python del upstream.

**Arquitectura:** el repositorio es un binario Go con todo el código bajo `internal/`. El corpus se
genera con un plugin de pytest que instrumenta las funciones del upstream Python, se ejecuta la
suite existente una vez, y cada llamada queda serializada como caso golden en `testdata/`. Los
tests Go de las fases siguientes consumen ese corpus mediante un cargador en `internal/testutil`.

**Stack:** Go 1.27 (`CGO_ENABLED=0`), Taskfile 3.x, GitHub Actions, y para la generación del corpus
Python 3.10 gestionado con `uv` más pytest.

**Spec:** `docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md`

## Restricciones globales

Aplican a todas las tareas de este plan y de los planes siguientes.

- Module path: `github.com/marr-cloud/kiro-gateway-go`
- Go 1.27, `CGO_ENABLED=0` en toda compilación. Ninguna dependencia que requiera cgo.
- Licencia AGPL-3.0. Todo fichero nuevo de código Go lleva la cabecera de licencia de dos líneas
  definida en la Tarea 3.
- Commit del upstream fijado: `a5292ca` (v2.4.dev.13) de `https://github.com/jwadow/kiro-gateway`.
- Versión que reporta el binario: `2.4.dev.13+go`
- Nombres de paquete Go = nombre del módulo Python sin guiones bajos. Único renombrado:
  `exceptions.py` → `internal/validationerrors`.
- Ningún test toca la red real. Nunca.
- Los ficheros generados en `testdata/` usan saltos de línea LF, forzados por `.gitattributes`.
- Se ejecuta `task lint` y `task test` en verde antes de cada commit.
- Mensajes de commit en inglés, con prefijo convencional (`feat:`, `test:`, `chore:`, `docs:`).

---

## Estructura de ficheros

Ficheros que crea esta fase, con su responsabilidad.

| Fichero | Responsabilidad |
|---|---|
| `go.mod` | Declara el module path y la versión de Go |
| `internal/version/version.go` | Única fuente de la cadena de versión, sobrescribible por `-ldflags` |
| `cmd/kiro-gateway/main.go` | Punto de entrada. En esta fase solo `-v` y `-h`; la fase 5 lo completa |
| `Taskfile.yml` | Automatización: `build`, `test`, `lint`, `corpus`, y sus dependencias |
| `LICENSE` | Texto literal de la AGPL-3.0 |
| `NOTICE` | Atribución al upstream y lista de cambios |
| `README.md` | Esqueleto mínimo. La fase 6 escribe el README completo |
| `docs/MAPPING.md` | Correspondencia módulo Python → paquete Go |
| `docs/DIFFERENCES.md` | Diferencias conocidas frente al original |
| `.github/workflows/ci.yml` | Vet, formato, tests y compilación de los cinco objetivos |
| `.gitattributes` | Normalización de saltos de línea del corpus |
| `internal/testutil/corpus.go` | Tipos del envoltorio del corpus y `LoadCorpus` |
| `internal/testutil/corpus_test.go` | Tests del cargador contra ficheros sintéticos |
| `internal/testutil/repo.go` | Localiza la raíz del repositorio subiendo hasta `go.mod` |
| `tools/corpus/pyproject.toml` | Dependencias Python pineadas para la generación |
| `tools/corpus/recorder.py` | Plugin de pytest que graba los casos |
| `tools/corpus/targets.py` | Tabla de funciones y clases a instrumentar |
| `tools/corpus/validate.py` | Aplica el tope de casos y emite el informe de tamaño |
| `tools/corpus/snapshot.py` | Copia y compara instantáneas del corpus sin depender de `cp` ni `diff` |
| `testdata/` | Corpus generado, versionado en el repositorio |

---

### Tarea 1: Módulo Go, paquete de versión y binario mínimo

**Ficheros:**
- Crear: `go.mod`
- Crear: `internal/version/version.go`
- Crear: `internal/version/version_test.go`
- Crear: `cmd/kiro-gateway/main.go`

**Interfaces:**
- Produce: `version.Version() string` y `version.Upstream` / `version.UpstreamCommit` como
  constantes. Todas las fases posteriores leen la versión de aquí y ningún otro sitio.

- [ ] **Paso 1: Inicializar el módulo**

```bash
cd C:/Users/maurr/kiro/kiro-gateway
go mod init github.com/marr-cloud/kiro-gateway-go
go mod edit -go=1.27
```

- [ ] **Paso 2: Escribir el test que falla**

Crear `internal/version/version_test.go`:

```go
package version

import "testing"

func TestVersionDefault(t *testing.T) {
	if got := Version(); got != "2.4.dev.13+go" {
		t.Errorf("Version() = %q, quiero %q", got, "2.4.dev.13+go")
	}
}

func TestUpstreamMetadata(t *testing.T) {
	if Upstream != "2.4.dev.13" {
		t.Errorf("Upstream = %q, quiero %q", Upstream, "2.4.dev.13")
	}
	if UpstreamCommit != "a5292ca" {
		t.Errorf("UpstreamCommit = %q, quiero %q", UpstreamCommit, "a5292ca")
	}
}
```

- [ ] **Paso 3: Ejecutar el test y comprobar que falla**

Ejecutar: `go test ./internal/version/`
Esperado: fallo de compilación, `undefined: Version`.

- [ ] **Paso 4: Implementar el paquete**

Crear `internal/version/version.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package version es la única fuente de la cadena de versión del binario.
package version

const (
	// Upstream es la versión de kiro-gateway con la que este port tiene paridad.
	Upstream = "2.4.dev.13"

	// UpstreamCommit es el commit exacto que se portó.
	UpstreamCommit = "a5292ca"
)

// build lo sobrescribe el enlazador con -ldflags "-X ...version.build=<valor>".
// Vacío significa compilación local de desarrollo.
var build string

// Version devuelve la versión que reporta el binario y los endpoints de salud.
func Version() string {
	if build != "" {
		return build
	}
	return Upstream + "+go"
}
```

- [ ] **Paso 5: Ejecutar el test y comprobar que pasa**

Ejecutar: `go test ./internal/version/ -v`
Esperado: `PASS`, dos tests.

- [ ] **Paso 6: Escribir el binario mínimo**

Crear `cmd/kiro-gateway/main.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Command kiro-gateway es un proxy local que traduce las APIs de OpenAI y
// Anthropic a la API de Kiro.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/marr-cloud/kiro-gateway-go/internal/version"
)

func main() {
	var (
		host        string
		port        int
		showVersion bool
	)

	fs := flag.NewFlagSet("kiro-gateway", flag.ExitOnError)
	fs.StringVar(&host, "host", "", "interfaz de escucha (por defecto SERVER_HOST o 0.0.0.0)")
	fs.StringVar(&host, "H", "", "abreviatura de --host")
	fs.IntVar(&port, "port", 0, "puerto de escucha (por defecto SERVER_PORT o 8000)")
	fs.IntVar(&port, "p", 0, "abreviatura de --port")
	fs.BoolVar(&showVersion, "version", false, "imprime la versión y sale")
	fs.BoolVar(&showVersion, "v", false, "abreviatura de --version")

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if showVersion {
		fmt.Printf("Kiro Gateway %s\n", version.Version())
		return
	}

	// La fase 5 sustituye esto por el arranque del servidor.
	fmt.Fprintln(os.Stderr, "el servidor todavía no está implementado: ver la fase 5 del port")
	os.Exit(1)
}
```

- [ ] **Paso 7: Comprobar que compila y que `-v` funciona**

Ejecutar:
```bash
go build -o kiro-gateway.exe ./cmd/kiro-gateway
./kiro-gateway.exe -v
```
Esperado: `Kiro Gateway 2.4.dev.13+go`

- [ ] **Paso 8: Commit**

```bash
git add go.mod internal/version cmd/kiro-gateway
git commit -m "feat: add Go module, version package and minimal entry point"
```

---

### Tarea 2: Taskfile

**Ficheros:**
- Crear: `Taskfile.yml`

**Interfaces:**
- Produce: los objetivos `build`, `test`, `lint`, `fmt` y `clean`. Los objetivos de corpus se
  añaden en la Tarea 7 y los de release y docker en la fase 6.

- [ ] **Paso 1: Escribir el Taskfile**

Crear `Taskfile.yml`:

```yaml
version: '3'

vars:
  BINARY: kiro-gateway
  MODULE: github.com/marr-cloud/kiro-gateway-go
  UPSTREAM_REPO: https://github.com/jwadow/kiro-gateway.git
  UPSTREAM_COMMIT: a5292ca
  # Intérprete del entorno virtual del upstream. Windows usa Scripts/, el resto bin/.
  PY: '{{if eq OS "windows"}}.venv/Scripts/python.exe{{else}}.venv/bin/python{{end}}'
  VERSION:
    sh: git describe --tags --always --dirty 2>/dev/null || echo dev

env:
  CGO_ENABLED: '0'

tasks:
  default:
    cmds: [task: test]

  build:
    desc: Compila el binario para la plataforma actual
    cmds:
      - go build -trimpath -ldflags "-s -w -X {{.MODULE}}/internal/version.build={{.VERSION}}" -o {{.BINARY}}{{exeExt}} ./cmd/kiro-gateway

  test:
    desc: Ejecuta todos los tests
    cmds:
      - go test ./...

  test:verbose:
    desc: Ejecuta todos los tests con salida detallada
    cmds:
      - go test ./... -v

  lint:
    desc: Comprueba formato y errores estáticos
    cmds:
      - go vet ./...
      - cmd: |
          out=$(gofmt -l .)
          if [ -n "$out" ]; then echo "ficheros sin formatear:"; echo "$out"; exit 1; fi

  fmt:
    desc: Formatea todo el código Go
    cmds:
      - gofmt -w .

  clean:
    desc: Borra artefactos de compilación
    cmds:
      - python -c "import pathlib; [p.unlink() for p in pathlib.Path('.').glob('kiro-gateway*') if p.is_file()]"
      - go clean -cache -testcache
```

**Por qué las operaciones de fichero van en Python.** Task interpreta los comandos con un shell
POSIX propio que funciona igual en Windows, pero los comandos externos como `rm`, `cp` y `diff` no
existen en Windows salvo que Git los haya puesto en el `PATH`. Python es dependencia obligatoria
del flujo de corpus, así que usarlo para estas operaciones elimina la dependencia frágil.

- [ ] **Paso 2: Verificar que los objetivos funcionan**

Ejecutar:
```bash
task build
task test
task lint
```
Esperado: los tres terminan con código 0. `task test` informa `ok` de `internal/version` y
`no test files` del resto.

- [ ] **Paso 3: Commit**

```bash
git add Taskfile.yml
git commit -m "chore: add Taskfile with build, test and lint targets"
```

---

### Tarea 3: Licencia, atribución y cabecera de ficheros

**Ficheros:**
- Crear: `LICENSE`
- Crear: `NOTICE`
- Crear: `README.md`

**Interfaces:**
- Produce: la cabecera de licencia de dos líneas que llevan todos los ficheros Go del proyecto:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.
```

- [ ] **Paso 1: Copiar el texto literal de la AGPL-3.0**

El upstream clonado ya lo contiene y es el texto canónico. Copiarlo sin modificar:

```bash
cp "$TEMP/kiro-gateway-upstream/LICENSE" LICENSE
```

Si el clon no está disponible, obtenerlo de `https://www.gnu.org/licenses/agpl-3.0.txt`.

- [ ] **Paso 2: Verificar que es el texto correcto**

Ejecutar: `head -2 LICENSE`
Esperado: `GNU AFFERO GENERAL PUBLIC LICENSE` y `Version 3, 19 November 2007`.

- [ ] **Paso 3: Escribir el NOTICE**

Crear `NOTICE`:

```text
kiro-gateway-go
Copyright (C) 2026 marr-cloud

Este programa es un port al lenguaje Go de kiro-gateway:

    https://github.com/jwadow/kiro-gateway
    Copyright (C) jwadow y colaboradores
    Versión de origen: 2.4.dev.13, commit a5292ca

kiro-gateway se distribuye bajo la GNU Affero General Public License v3.0.
Este port es obra derivada y se distribuye bajo la misma licencia.

Cambios respecto al original
----------------------------

- Reimplementación completa en Go como binario único sin dependencias de
  runtime. El original es Python con FastAPI y uvicorn.
- No se exponen los endpoints /docs, /redoc ni /openapi.json que FastAPI
  genera automáticamente.
- Los endpoints GET / y GET /health devuelven un JSON con campos adicionales:
  cuenta activa, tiempo en marcha y modo de operación.
- Las respuestas de error 422 conservan la estructura del original pero no la
  forma literal que produce Pydantic v2.
- Se añade el flag --health, que consulta /health y sale con código 0 o 1. El
  healthcheck de la imagen Docker lo usa, porque la imagen no contiene ni
  Python ni curl.
- La imagen Docker parte de una base mínima en lugar de python:3.10-slim.
- Documentación en inglés y español. El original la tiene en siete idiomas.

La lista completa y actualizada está en docs/DIFFERENCES.md.
```

- [ ] **Paso 4: Escribir el esqueleto del README**

Crear `README.md`:

```markdown
# kiro-gateway-go

Port a Go de [kiro-gateway](https://github.com/jwadow/kiro-gateway) como binario único.

Un proxy local que expone las APIs de OpenAI y Anthropic y traduce las peticiones a la API de
Kiro, de forma que cualquier cliente compatible con esas APIs pueda usar los modelos de Kiro.

> **Estado:** en construcción. El binario todavía no sirve peticiones. Ver
> `docs/superpowers/plans/` para el estado del port.

## Licencia

AGPL-3.0. Este proyecto es obra derivada de `jwadow/kiro-gateway`; ver [NOTICE](NOTICE) para la
atribución y la lista de cambios.

## Documentación

- [Diseño del port](docs/superpowers/specs/2026-09-04-kiro-gateway-go-port-design.md)
- [Correspondencia de módulos](docs/MAPPING.md)
- [Diferencias con el original](docs/DIFFERENCES.md)
```

- [ ] **Paso 5: Añadir la cabecera de licencia a los ficheros Go ya escritos**

Comprobar que `internal/version/version.go` y `cmd/kiro-gateway/main.go` la llevan (la Tarea 1 ya
la incluyó). Añadirla a `internal/version/version_test.go`, que no la tiene.

Ejecutar para verificar que no falta en ninguno:
```bash
for f in $(git ls-files '*.go'); do head -1 "$f" | grep -q 'SPDX-License-Identifier: AGPL-3.0-or-later' || echo "falta cabecera: $f"; done
```
Esperado: sin salida.

- [ ] **Paso 6: Commit**

```bash
git add LICENSE NOTICE README.md internal/version/version_test.go
git commit -m "docs: add AGPL-3.0 license, upstream attribution and README skeleton"
```

---

### Tarea 4: Documentos de correspondencia y de diferencias

**Ficheros:**
- Crear: `docs/MAPPING.md`
- Crear: `docs/DIFFERENCES.md`

**Interfaces:**
- Consume: la tabla de la sección §5.2 del spec.
- Produce: `docs/MAPPING.md`, que todas las fases posteriores actualizan añadiendo la columna de
  ficheros Go concretos a medida que se crean.

- [ ] **Paso 1: Escribir MAPPING.md**

Crear `docs/MAPPING.md` con la cabecera siguiente y la tabla completa de 32 filas copiada de la
sección §5.2 del spec, añadiendo una tercera columna llamada `Ficheros Go` que queda vacía y se
rellena en las fases siguientes:

```markdown
# Correspondencia entre el original Python y este port

Origen: `jwadow/kiro-gateway`, v2.4.dev.13, commit `a5292ca`.

Regla: el nombre del paquete Go es el del módulo Python sin guiones bajos. La columna
`Ficheros Go` se rellena a medida que cada fase implementa su paquete.

| Módulo Python | Paquete Go | Ficheros Go |
|---|---|---|
| `main.py` | `cmd/kiro-gateway` + `internal/server` | `cmd/kiro-gateway/main.go` |
| `config.py` | `internal/config` | |
```

Continuar con las 30 filas restantes de §5.2, en el mismo orden.

Después, dos secciones más:

```markdown
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
```

- [ ] **Paso 2: Escribir DIFFERENCES.md**

Crear `docs/DIFFERENCES.md` con las seis diferencias de las secciones §4.3 y §4.4 del spec, cada
una con qué cambia, por qué, y qué impacto tiene para quien venga del original. Las seis son:
ausencia de `/docs`, `/redoc` y `/openapi.json`; forma de los errores 422; JSON más rico en `/` y
`/health`; el flag `--health`; la imagen Docker mínima; y la documentación en dos idiomas en vez
de siete.

Añadir al final una sección titulada `Comportamientos del original que se replican a propósito`
con las cinco filas de la tabla §4.2 del spec, para que nadie los «arregle» por error.

- [ ] **Paso 3: Verificar la correspondencia**

Ejecutar para comprobar que MAPPING.md cubre los 32 ficheros Python:
```bash
grep -c '^| `[a-z_]*\.py` |' docs/MAPPING.md
```
Esperado: `32`.

- [ ] **Paso 4: Commit**

```bash
git add docs/MAPPING.md docs/DIFFERENCES.md
git commit -m "docs: add Python-to-Go module mapping and known differences"
```

---

### Tarea 5: Integración continua

**Ficheros:**
- Crear: `.github/workflows/ci.yml`

- [ ] **Paso 1: Escribir el workflow**

Crear `.github/workflows/ci.yml`:

```yaml
name: CI

on:
  push:
    branches: [main]
  pull_request:

permissions:
  contents: read

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - name: Comprobar formato
        run: |
          out=$(gofmt -l .)
          if [ -n "$out" ]; then echo "ficheros sin formatear:"; echo "$out"; exit 1; fi
      - name: Vet
        run: go vet ./...
      - name: Tests
        run: go test ./... -count=1
      - name: Cabecera de licencia en todos los ficheros Go
        run: |
          missing=0
          for f in $(git ls-files '*.go'); do
            head -1 "$f" | grep -q 'SPDX-License-Identifier: AGPL-3.0-or-later' || { echo "falta cabecera: $f"; missing=1; }
          done
          exit $missing

  build:
    runs-on: ubuntu-latest
    needs: test
    strategy:
      matrix:
        include:
          - { goos: windows, goarch: amd64 }
          - { goos: linux, goarch: amd64 }
          - { goos: linux, goarch: arm64 }
          - { goos: darwin, goarch: arm64 }
          - { goos: darwin, goarch: amd64 }
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - name: Compilar ${{ matrix.goos }}/${{ matrix.goarch }}
        env:
          CGO_ENABLED: '0'
          GOOS: ${{ matrix.goos }}
          GOARCH: ${{ matrix.goarch }}
        run: go build -trimpath -o /dev/null ./cmd/kiro-gateway
```

- [ ] **Paso 2: Verificar localmente lo que hará el CI**

Ejecutar:
```bash
task lint
task test
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /dev/null ./cmd/kiro-gateway
```
Esperado: los tres con código 0. El último confirma que el cross-compile sin cgo funciona.

- [ ] **Paso 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add test and five-target cross-compile workflow"
```

---

### Tarea 6: Formato del corpus y cargador en Go

**Ficheros:**
- Crear: `internal/testutil/corpus.go`
- Crear: `internal/testutil/repo.go`
- Crear: `internal/testutil/corpus_test.go`
- Crear: `internal/testutil/testdata/sample/function/aaaa000000000001.json`
- Crear: `internal/testutil/testdata/sample/sequence/bbbb000000000002.json`

**Interfaces:**
- Produce: `testutil.LoadCorpus(t *testing.T, target string) []Case` y los tipos `Case`, `Step` y
  `Kind`. **Todas las fases 2 a 5 consumen exactamente esta firma.**

El envoltorio JSON de cada caso tiene esta forma. Los tres valores posibles de `kind` determinan
qué campos están presentes:

```json
{
  "target": "kiro.payload_guards:check_payload_size",
  "kind": "function",
  "input": {"args": [...], "kwargs": {...}},
  "output": ...,
  "notes": {"unrecorded_args": {"client": "AsyncClient"}},
  "recorded_at_commit": "a5292ca"
}
```

```json
{
  "target": "kiro.parsers:AwsEventStreamParser",
  "kind": "sequence",
  "steps": [
    {"method": "feed", "input": {"args": [{"__bytes__": "eyJ..."}], "kwargs": {}}, "output": [...]}
  ],
  "recorded_at_commit": "a5292ca"
}
```

```json
{
  "target": "kiro.streaming_openai:stream_kiro_to_openai_internal",
  "kind": "generator",
  "input": {"kwargs": {"model": "claude-sonnet-4.5"}, "events": [...]},
  "output": ["data: {...}\n\n", "data: [DONE]\n\n"],
  "recorded_at_commit": "a5292ca"
}
```

Los valores de tipo `bytes` de Python se codifican como `{"__bytes__": "<base64 estándar>"}`.

- [ ] **Paso 1: Escribir los ficheros de ejemplo a mano**

Crear `internal/testutil/testdata/sample/function/aaaa000000000001.json`:

```json
{
  "target": "kiro.sample:add",
  "kind": "function",
  "input": {"args": [2, 3], "kwargs": {}},
  "output": 5,
  "recorded_at_commit": "a5292ca"
}
```

Crear `internal/testutil/testdata/sample/sequence/bbbb000000000002.json`:

```json
{
  "target": "kiro.sample:Accumulator",
  "kind": "sequence",
  "steps": [
    {"method": "feed", "input": {"args": [{"__bytes__": "aGk="}], "kwargs": {}}, "output": ["hi"]},
    {"method": "finalize", "input": {"args": [], "kwargs": {}}, "output": "hi"}
  ],
  "recorded_at_commit": "a5292ca"
}
```

- [ ] **Paso 2: Escribir los tests que fallan**

Crear `internal/testutil/corpus_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package testutil

import (
	"encoding/json"
	"testing"
)

// Los tests de Go se ejecutan en el directorio del paquete, así que el
// testdata de ejemplo de este paquete está en "testdata" relativo al CWD.

func TestLoadCorpusFunction(t *testing.T) {
	cases, err := readCases("testdata", "sample/function")
	if err != nil {
		t.Fatalf("readCases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("cargados %d casos, quiero 1", len(cases))
	}
	c := cases[0]
	if c.Kind != KindFunction {
		t.Errorf("Kind = %q, quiero %q", c.Kind, KindFunction)
	}
	if c.Target != "kiro.sample:add" {
		t.Errorf("Target = %q", c.Target)
	}
	var out int
	if err := json.Unmarshal(c.Output, &out); err != nil {
		t.Fatalf("Output no deserializa: %v", err)
	}
	if out != 5 {
		t.Errorf("Output = %d, quiero 5", out)
	}
	if c.Name != "aaaa000000000001" {
		t.Errorf("Name = %q, quiero el hash del fichero", c.Name)
	}
}

func TestLoadCorpusSequence(t *testing.T) {
	cases, err := readCases("testdata", "sample/sequence")
	if err != nil {
		t.Fatalf("readCases: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("cargados %d casos, quiero 1", len(cases))
	}
	steps := cases[0].Steps
	if len(steps) != 2 {
		t.Fatalf("%d pasos, quiero 2", len(steps))
	}
	if steps[0].Method != "feed" || steps[1].Method != "finalize" {
		t.Errorf("métodos = %q, %q", steps[0].Method, steps[1].Method)
	}
	chunk, err := DecodeBytes(mustField(t, steps[0].Input, "args", 0))
	if err != nil {
		t.Fatalf("DecodeBytes del primer argumento: %v", err)
	}
	if string(chunk) != "hi" {
		t.Errorf("primer chunk = %q, quiero %q", chunk, "hi")
	}
}

func TestDecodeBytes(t *testing.T) {
	got, err := DecodeBytes(json.RawMessage(`{"__bytes__":"aGk="}`))
	if err != nil {
		t.Fatalf("DecodeBytes: %v", err)
	}
	if string(got) != "hi" {
		t.Errorf("DecodeBytes = %q, quiero %q", got, "hi")
	}
}

func TestDecodeBytesRejectsPlainValue(t *testing.T) {
	if _, err := DecodeBytes(json.RawMessage(`"hola"`)); err == nil {
		t.Fatal("quiero error para un valor que no es bytes codificados, obtuve nil")
	}
}

func TestReadCasesMissingTargetFails(t *testing.T) {
	if _, err := readCases("testdata", "sample/noexiste"); err == nil {
		t.Fatal("quiero error para un objetivo inexistente, obtuve nil")
	}
}

func TestRepoRootFindsGoMod(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	if root == "" {
		t.Fatal("RepoRoot devolvió una cadena vacía")
	}
}

// mustField extrae input["args"][index] como JSON crudo.
func mustField(t *testing.T, raw json.RawMessage, key string, index int) json.RawMessage {
	t.Helper()
	var wrapper map[string][]json.RawMessage
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		t.Fatalf("deserializando %s: %v", raw, err)
	}
	items, ok := wrapper[key]
	if !ok || len(items) <= index {
		t.Fatalf("no existe %s[%d] en %s", key, index, raw)
	}
	return items[index]
}
```

- [ ] **Paso 3: Ejecutar y comprobar que falla**

Ejecutar: `go test ./internal/testutil/`
Esperado: fallo de compilación, `undefined: loadFrom`, `undefined: KindFunction`.

- [ ] **Paso 4: Implementar el localizador de la raíz del repositorio**

Crear `internal/testutil/repo.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package testutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// RepoRoot devuelve la raíz del repositorio subiendo desde el directorio de
// trabajo hasta encontrar go.mod. Los tests se ejecutan en el directorio de su
// paquete, así que el corpus, que vive en la raíz, no es accesible con una ruta
// relativa fija.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no se encontró go.mod subiendo desde el directorio de trabajo")
		}
		dir = parent
	}
}
```

- [ ] **Paso 5: Implementar el cargador**

Crear `internal/testutil/corpus.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package testutil carga el corpus golden grabado de la suite de tests del
// upstream Python y ofrece utilidades comunes a los tests del port.
package testutil

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Kind indica la forma del caso grabado.
type Kind string

const (
	// KindFunction es una llamada única a una función pura.
	KindFunction Kind = "function"
	// KindSequence es una secuencia de llamadas a métodos sobre un objeto con estado.
	KindSequence Kind = "sequence"
	// KindGenerator es una invocación de un generador, con los eventos que consumió
	// y los fragmentos que emitió.
	KindGenerator Kind = "generator"
)

// Case es un caso golden grabado del original Python.
type Case struct {
	Target string          `json:"target"`
	Kind   Kind            `json:"kind"`
	Input  json.RawMessage `json:"input,omitempty"`
	Output json.RawMessage `json:"output,omitempty"`
	Steps  []Step          `json:"steps,omitempty"`
	Notes  json.RawMessage `json:"notes,omitempty"`
	Commit string          `json:"recorded_at_commit"`

	// Name es el nombre del fichero sin extensión: el hash de la entrada.
	// Sirve para identificar el caso exacto que falla.
	Name string `json:"-"`
}

// Step es una llamada dentro de un caso de tipo KindSequence.
type Step struct {
	Method string          `json:"method"`
	Input  json.RawMessage `json:"input"`
	Output json.RawMessage `json:"output"`
}

// LoadCorpus devuelve todos los casos grabados de un objetivo, ordenados por
// nombre para que el recorrido sea determinista. target usa la forma
// "<modulo>/<funcion>", por ejemplo "converterscore/build_kiro_payload".
//
// Falla el test si el objetivo no existe: un corpus ausente es un error de
// configuración, no un caso sin casos.
func LoadCorpus(t *testing.T, target string) []Case {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("localizando la raíz del repositorio: %v", err)
	}
	cases, err := readCases(filepath.Join(root, "testdata"), target)
	if err != nil {
		t.Fatalf("cargando el corpus de %q: %v", target, err)
	}
	return cases
}

// loadFrom se eliminó a propósito: los tests de este paquete llaman a readCases
// directamente con una base relativa al directorio del paquete.

func readCases(base, target string) ([]Case, error) {
	dir := filepath.Join(base, filepath.FromSlash(target))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("leyendo %s: %w", dir, err)
	}

	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("sin ficheros .json en %s", dir)
	}
	sort.Strings(names)

	cases := make([]Case, 0, len(names))
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		var c Case
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		c.Name = strings.TrimSuffix(name, ".json")
		cases = append(cases, c)
	}
	return cases, nil
}

// DecodeBytes deserializa un valor que el grabador codificó como
// {"__bytes__": "<base64>"}, y devuelve los bytes originales.
func DecodeBytes(raw json.RawMessage) ([]byte, error) {
	var wrapper struct {
		B64 *string `json:"__bytes__"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, err
	}
	if wrapper.B64 == nil {
		return nil, fmt.Errorf("no es un valor de bytes codificado: %s", raw)
	}
	return base64.StdEncoding.DecodeString(*wrapper.B64)
}
```

- [ ] **Paso 6: Ejecutar y comprobar que pasa**

Ejecutar: `go test ./internal/testutil/ -v`
Esperado: `PASS`, cuatro tests.

- [ ] **Paso 7: Commit**

```bash
git add internal/testutil
git commit -m "feat(testutil): add golden corpus loader with function, sequence and generator cases"
```

---

### Tarea 7: Entorno Python fijado y checkout del upstream

**Ficheros:**
- Crear: `tools/corpus/pyproject.toml`
- Modificar: `Taskfile.yml` (añadir la sección de tareas de corpus)
- Modificar: `.gitignore` (añadir `.upstream/`)

**Interfaces:**
- Produce: la tarea `task corpus:setup`, que deja en `.upstream/` el repositorio del upstream en
  el commit `a5292ca` con un entorno virtual de Python 3.10 y las dependencias instaladas. Las
  tareas 8 a 11 asumen que esto ya se ejecutó.

- [ ] **Paso 1: Escribir las dependencias pineadas**

Crear `tools/corpus/pyproject.toml`. Las versiones se fijan a las que resuelva la primera
ejecución (paso 3), no antes: pinear a ciegas una versión que no existe rompe la reproducibilidad
que se busca.

```toml
[project]
name = "kiro-gateway-corpus"
version = "0"
requires-python = "==3.10.*"
dependencies = [
    "fastapi",
    "uvicorn[standard]",
    "httpx",
    "loguru",
    "python-dotenv",
    "tiktoken",
    "pytest",
    "pytest-asyncio",
]

[tool.uv]
package = false
```

- [ ] **Paso 2: Añadir las tareas de corpus al Taskfile**

Añadir a `Taskfile.yml`:

```yaml
  corpus:setup:
    desc: Clona el upstream en el commit fijado y prepara el entorno Python 3.10
    status:
      - test -d .upstream/.git
      - test -d .upstream/.venv
    cmds:
      - cmd: git clone {{.UPSTREAM_REPO}} .upstream
        ignore_error: true
      - git -C .upstream fetch --all --tags
      - git -C .upstream checkout --force {{.UPSTREAM_COMMIT}}
      - git -C .upstream clean -fd
      - uv venv --python 3.10 .upstream/.venv
      - uv pip install --python .upstream/.venv --project tools/corpus -r tools/corpus/pyproject.toml

  corpus:freeze:
    desc: Escribe el lockfile de las dependencias Python realmente instaladas
    deps: [corpus:setup]
    cmds:
      - uv pip freeze --python .upstream/.venv > tools/corpus/requirements.lock
```

- [ ] **Paso 3: Ejecutar el setup y comprobar que la suite del upstream pasa**

Ejecutar:
```bash
task corpus:setup
task corpus:freeze
cd .upstream && .venv/Scripts/python -m pytest -q 2>&1 | tail -20
```
Esperado: el clon queda en `a5292ca`, y pytest ejecuta la suite del upstream. **Anotar el número
de tests que pasan y fallan.** Si algún test falla en el upstream sin modificar, es un fallo
preexistente del original: documentarlo en el mensaje del commit y continuar. No hay que
arreglarlo.

En Linux y macOS la ruta del intérprete es `.venv/bin/python` en vez de `.venv/Scripts/python`.

- [ ] **Paso 4: Ignorar el clon del upstream**

Añadir a `.gitignore`:

```gitignore
# Clon del upstream para generar el corpus
/.upstream/
```

- [ ] **Paso 5: Commit**

```bash
git add tools/corpus/pyproject.toml tools/corpus/requirements.lock Taskfile.yml .gitignore
git commit -m "chore(corpus): pin upstream checkout and Python 3.10 environment"
```

---

### Tarea 8: Grabador, modo función

**Ficheros:**
- Crear: `tools/corpus/recorder.py`
- Crear: `tools/corpus/targets.py`
- Modificar: `Taskfile.yml` (añadir `corpus:record`)

**Interfaces:**
- Consume: el entorno que deja `task corpus:setup`.
- Produce: ficheros `testdata/<modulo>/<funcion>/<hash>.json` con `kind: "function"`, en el
  formato que define la Tarea 6, y un informe `testdata/_report.json`.

- [ ] **Paso 1: Escribir la tabla de objetivos**

Crear `tools/corpus/targets.py`. Los nombres están verificados contra el código del upstream en el
commit `a5292ca`:

```python
"""Funciones y clases del upstream que se graban como corpus golden.

FUNCTIONS: funciones puras. Se graba (args, kwargs) -> resultado.
SEQUENCES: clases con estado. Se graba la secuencia completa de llamadas por instancia.
GENERATORS: generadores asincronos de streaming. Se graba (eventos consumidos, args
            escalares) -> fragmentos emitidos.
"""

FUNCTIONS = [
    ("kiro.converters_core", "extract_text_content"),
    ("kiro.converters_core", "extract_images_from_content"),
    ("kiro.converters_core", "get_thinking_system_prompt_addition"),
    ("kiro.converters_core", "get_truncation_recovery_system_addition"),
    ("kiro.converters_core", "inject_thinking_tags"),
    ("kiro.converters_core", "sanitize_json_schema"),
    ("kiro.converters_core", "process_tools_with_long_descriptions"),
    ("kiro.converters_core", "validate_tool_names"),
    ("kiro.converters_core", "convert_tools_to_kiro_format"),
    ("kiro.converters_core", "convert_images_to_kiro_format"),
    ("kiro.converters_core", "convert_tool_results_to_kiro_format"),
    ("kiro.converters_core", "extract_tool_results_from_content"),
    ("kiro.converters_core", "extract_tool_uses_from_message"),
    ("kiro.converters_core", "tool_calls_to_text"),
    ("kiro.converters_core", "tool_results_to_text"),
    ("kiro.converters_core", "strip_all_tool_content"),
    ("kiro.converters_core", "ensure_assistant_before_tool_results"),
    ("kiro.converters_core", "merge_adjacent_messages"),
    ("kiro.converters_core", "ensure_first_message_is_user"),
    ("kiro.converters_core", "normalize_message_roles"),
    ("kiro.converters_core", "ensure_alternating_roles"),
    ("kiro.converters_core", "build_kiro_history"),
    ("kiro.converters_core", "build_kiro_payload"),
    ("kiro.converters_openai", "convert_openai_messages_to_unified"),
    ("kiro.converters_openai", "convert_openai_tools_to_unified"),
    ("kiro.converters_openai", "reasoning_effort_to_budget"),
    ("kiro.converters_openai", "extract_thinking_config_from_openai"),
    ("kiro.converters_openai", "build_kiro_payload"),
    ("kiro.converters_anthropic", "convert_anthropic_content_to_text"),
    ("kiro.converters_anthropic", "extract_system_prompt"),
    ("kiro.converters_anthropic", "extract_tool_results_from_anthropic_content"),
    ("kiro.converters_anthropic", "extract_images_from_tool_results"),
    ("kiro.converters_anthropic", "extract_tool_uses_from_anthropic_content"),
    ("kiro.converters_anthropic", "convert_anthropic_messages"),
    ("kiro.converters_anthropic", "convert_anthropic_tools"),
    ("kiro.converters_anthropic", "extract_thinking_config_from_anthropic"),
    ("kiro.converters_anthropic", "anthropic_to_kiro"),
    ("kiro.parsers", "find_matching_brace"),
    ("kiro.parsers", "parse_bracket_tool_calls"),
    ("kiro.parsers", "deduplicate_tool_calls"),
    ("kiro.tokenizer", "count_tokens"),
    ("kiro.tokenizer", "count_message_tokens"),
    ("kiro.tokenizer", "count_tools_tokens"),
    ("kiro.tokenizer", "count_system_tokens"),
    ("kiro.tokenizer", "estimate_request_tokens"),
    ("kiro.model_resolver", "to_runtime_model_id"),
    ("kiro.model_resolver", "normalize_model_name"),
    ("kiro.model_resolver", "extract_model_family"),
    ("kiro.payload_guards", "check_payload_size"),
    ("kiro.payload_guards", "trim_payload_to_limit"),
    ("kiro.kiro_errors", "enhance_kiro_error"),
    ("kiro.network_errors", "classify_network_error"),
    ("kiro.network_errors", "format_error_for_user"),
    ("kiro.network_errors", "get_short_error_message"),
    ("kiro.account_errors", "classify_error"),
    ("kiro.truncation_recovery", "should_inject_recovery"),
    ("kiro.truncation_recovery", "generate_truncation_tool_result"),
    ("kiro.truncation_recovery", "generate_truncation_user_message"),
    ("kiro.utils", "get_machine_fingerprint"),
    ("kiro.streaming_anthropic", "format_sse_event"),
]

SEQUENCES = [
    ("kiro.parsers", "AwsEventStreamParser", ["feed", "get_tool_calls", "reset"]),
    ("kiro.thinking_parser", "ThinkingParser", ["feed", "finalize", "reset", "process_for_output"]),
]

# Para cada generador: los nombres de los argumentos escalares que se graban. Los
# argumentos que son objetos (client, response, model_cache, auth_manager) no se
# graban; su presencia queda anotada en el campo notes del caso.
GENERATORS = [
    (
        "kiro.streaming_openai",
        "stream_kiro_to_openai_internal",
        ["model", "first_token_timeout", "request_messages", "request_tools", "conversation_id"],
    ),
    (
        "kiro.streaming_anthropic",
        "stream_kiro_to_anthropic",
        [
            "model",
            "first_token_timeout",
            "request_messages",
            "request_tools",
            "request_system",
            "conversation_id",
        ],
    ),
]

# Funciones que NO se graban, con el motivo. Sirve para que nadie las añada por error.
EXCLUDED = {
    "kiro.utils:generate_completion_id": "aleatorio, no reproducible",
    "kiro.utils:generate_conversation_id": "aleatorio, no reproducible",
    "kiro.utils:generate_tool_call_id": "aleatorio, no reproducible",
    "kiro.utils:get_kiro_headers": "depende del fingerprint de la máquina y de un uuid",
    "kiro.streaming_anthropic:generate_message_id": "aleatorio, no reproducible",
    "kiro.streaming_anthropic:generate_thinking_signature": "aleatorio, no reproducible",
    "kiro.model_resolver:get_model_id_for_kiro": "envoltorio de ModelResolver.resolve, que es asincrono y depende de red mockeada",
}
```

- [ ] **Paso 2: Escribir el grabador con soporte de modo función**

Crear `tools/corpus/recorder.py`:

```python
"""Plugin de pytest que graba casos golden de la suite del upstream.

Uso:
    CORPUS_OUT=/ruta/testdata pytest -p recorder

Se instala como plugin apuntando PYTHONPATH a este directorio.
"""

from __future__ import annotations

import base64
import dataclasses
import enum
import functools
import hashlib
import importlib
import json
import os
import sys
from collections import Counter
from pathlib import Path

import targets as targets_module

OUT = Path(os.environ.get("CORPUS_OUT", "testdata")).resolve()
COMMIT = os.environ.get("CORPUS_COMMIT", "unknown")
MAX_PER_TARGET = int(os.environ.get("CORPUS_MAX_PER_TARGET", "500"))

_stats: Counter[str] = Counter()
_skips: Counter[str] = Counter()
_written: dict[str, set[str]] = {}


# ----------------------------------------------------------------------------
# Serializacion
# ----------------------------------------------------------------------------

class Unserializable(Exception):
    """El valor no se puede representar en el corpus."""


def _default(obj):
    if isinstance(obj, bytes):
        return {"__bytes__": base64.b64encode(obj).decode("ascii")}
    if isinstance(obj, bytearray):
        return {"__bytes__": base64.b64encode(bytes(obj)).decode("ascii")}
    if dataclasses.is_dataclass(obj) and not isinstance(obj, type):
        return dataclasses.asdict(obj)
    if isinstance(obj, enum.Enum):
        return obj.value
    if isinstance(obj, (set, frozenset)):
        return {"__set__": sorted(obj, key=repr)}
    if isinstance(obj, tuple):
        return list(obj)
    raise Unserializable(type(obj).__name__)


def _dumps(obj) -> str:
    """Serializa preservando el orden de insercion. Lanza Unserializable si no puede."""
    try:
        return json.dumps(obj, ensure_ascii=False, default=_default)
    except Unserializable:
        raise
    except (TypeError, ValueError) as exc:
        raise Unserializable(str(exc)) from exc


def _hash(obj) -> str:
    canonical = json.dumps(
        obj, sort_keys=True, separators=(",", ":"), ensure_ascii=True, default=_default
    )
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()[:16]


def _slug(target: str) -> str:
    """kiro.converters_core:build_kiro_payload -> converters_core/build_kiro_payload"""
    module, _, name = target.partition(":")
    return f"{module.removeprefix('kiro.')}/{name}"


def _write(target: str, payload: dict) -> None:
    slug = _slug(target)
    seen = _written.setdefault(slug, set())
    if len(seen) >= MAX_PER_TARGET:
        _skips[f"{slug}: tope de {MAX_PER_TARGET} casos"] += 1
        return

    key_source = payload.get("input", payload.get("steps"))
    digest = _hash(key_source)
    if digest in seen:
        _stats[f"{slug}: duplicado"] += 1
        return

    payload["recorded_at_commit"] = COMMIT
    directory = OUT / slug
    directory.mkdir(parents=True, exist_ok=True)
    text = _dumps(payload)
    (directory / f"{digest}.json").write_text(text + "\n", encoding="utf-8", newline="\n")
    seen.add(digest)
    _stats[slug] += 1


# ----------------------------------------------------------------------------
# Modo funcion
# ----------------------------------------------------------------------------

def _record_function(target: str, args, kwargs, result) -> None:
    try:
        payload = {
            "target": target,
            "kind": "function",
            "input": json.loads(_dumps({"args": list(args), "kwargs": dict(kwargs)})),
            "output": json.loads(_dumps(result)),
        }
    except Unserializable as exc:
        _skips[f"{_slug(target)}: {exc}"] += 1
        return
    _write(target, payload)


def _wrap_function(module, name: str):
    original = getattr(module, name)
    if getattr(original, "_corpus_wrapped", False):
        return original
    target = f"{module.__name__}:{name}"

    @functools.wraps(original)
    def wrapper(*args, **kwargs):
        result = original(*args, **kwargs)
        _record_function(target, args, kwargs, result)
        return result

    wrapper._corpus_wrapped = True
    wrapper._corpus_original = original
    return wrapper


def _rebind_everywhere(original, replacement) -> None:
    """Sustituye original por replacement en TODOS los modulos que lo referencian.

    Necesario porque el upstream hace `from kiro.x import y`: parchear solo el
    modulo de origen no afecta a las referencias ya enlazadas en otros modulos.
    """
    for mod in list(sys.modules.values()):
        if mod is None or not hasattr(mod, "__dict__"):
            continue
        for attr, value in list(vars(mod).items()):
            if value is original:
                try:
                    setattr(mod, attr, replacement)
                except (AttributeError, TypeError):
                    pass


def _install_functions() -> None:
    for module_name, func_name in targets_module.FUNCTIONS:
        try:
            module = importlib.import_module(module_name)
        except ImportError as exc:
            _skips[f"{module_name}: no se pudo importar ({exc})"] += 1
            continue
        original = getattr(module, func_name, None)
        if original is None:
            _skips[f"{module_name}:{func_name}: no existe"] += 1
            continue
        wrapper = _wrap_function(module, func_name)
        _rebind_everywhere(original, wrapper)


# ----------------------------------------------------------------------------
# Hooks de pytest
# ----------------------------------------------------------------------------

def pytest_configure(config):
    OUT.mkdir(parents=True, exist_ok=True)
    _install_functions()


def pytest_sessionfinish(session, exitstatus):
    report = {
        "commit": COMMIT,
        "max_per_target": MAX_PER_TARGET,
        "recorded": dict(sorted(_stats.items())),
        "skipped": dict(sorted(_skips.items())),
        "total_cases": sum(v for k, v in _stats.items() if ":" not in k),
    }
    (OUT / "_report.json").write_text(
        json.dumps(report, indent=2, ensure_ascii=False) + "\n",
        encoding="utf-8",
        newline="\n",
    )
    print(f"\n[corpus] {report['total_cases']} casos escritos en {OUT}")
    print(f"[corpus] {len(_skips)} motivos distintos de descarte; ver _report.json")
```

- [ ] **Paso 3: Añadir la tarea de grabación al Taskfile**

Añadir a `Taskfile.yml`:

```yaml
  corpus:record:
    desc: Ejecuta la suite del upstream con el grabador y genera testdata/
    deps: [corpus:setup]
    cmds:
      - python -c "import shutil; shutil.rmtree('testdata', ignore_errors=True)"
      - cmd: '{{.PY}} -m pytest -q -p recorder'
        dir: .upstream
        ignore_error: true
        env:
          CORPUS_OUT: '{{.TASKFILE_DIR}}/testdata'
          CORPUS_COMMIT: '{{.UPSTREAM_COMMIT}}'
          PYTHONPATH: '{{.TASKFILE_DIR}}/tools/corpus'
          PYTHONHASHSEED: '0'
          TZ: UTC
```

`ignore_error: true` en pytest es deliberado: si algún test del upstream falla, el corpus de todo
lo demás sigue siendo válido y queremos generarlo. `PYTHONHASHSEED=0` y `TZ=UTC` eliminan dos
fuentes de no determinismo entre ejecuciones.

- [ ] **Paso 4: Grabar solo un módulo para validar el mecanismo**

Ejecutar:
```bash
cd .upstream
CORPUS_OUT=../testdata CORPUS_COMMIT=a5292ca PYTHONPATH=../tools/corpus \
  .venv/Scripts/python -m pytest -q -p recorder tests/unit/test_payload_guards.py
```
Esperado: aparecen ficheros en `testdata/payload_guards/check_payload_size/` y
`testdata/payload_guards/trim_payload_to_limit/`, y `testdata/_report.json` existe.

- [ ] **Paso 5: Inspeccionar un caso a mano**

Ejecutar: `cat testdata/payload_guards/check_payload_size/*.json | head -1`
Comprobar que el JSON tiene `target`, `kind: "function"`, `input` con `args` y `kwargs`, `output`,
y `recorded_at_commit: "a5292ca"`. **Si `input` está vacío o `output` es `null` en todos los
casos, el grabador está mal enganchado: no continuar hasta arreglarlo.**

- [ ] **Paso 6: Commit**

```bash
git add tools/corpus/recorder.py tools/corpus/targets.py Taskfile.yml
git commit -m "feat(corpus): add pytest recorder plugin with function mode"
```

---

### Tarea 9: Grabador, modo secuencia

**Ficheros:**
- Modificar: `tools/corpus/recorder.py`

**Interfaces:**
- Consume: `targets_module.SEQUENCES`.
- Produce: casos con `kind: "sequence"` y el array `steps` que define la Tarea 6.

El parser y el parser de thinking tienen estado: grabar una llamada aislada a `feed` no tiene
sentido, porque el resultado depende de todo lo que se alimentó antes. Se graba la secuencia
completa por instancia, y se vuelca al terminar cada test.

- [ ] **Paso 1: Añadir el modo secuencia al grabador**

Añadir a `tools/corpus/recorder.py`, antes de la sección de hooks:

```python
# ----------------------------------------------------------------------------
# Modo secuencia
# ----------------------------------------------------------------------------

_live_sequences: list = []

# Atributos que el grabador cuelga de cada instancia. Un solo subrayado a
# proposito: el doble subrayado activaria el mangling de nombres de Python y el
# atributo real pasaria a llamarse _Clase__corpus_steps, distinto en cada clase.
_STEPS_ATTR = "_corpus_steps"
_TARGET_ATTR = "_corpus_target"


def _install_sequences() -> None:
    for module_name, class_name, methods in targets_module.SEQUENCES:
        try:
            module = importlib.import_module(module_name)
        except ImportError as exc:
            _skips[f"{module_name}: no se pudo importar ({exc})"] += 1
            continue
        cls = getattr(module, class_name, None)
        if cls is None:
            _skips[f"{module_name}:{class_name}: no existe"] += 1
            continue

        target = f"{module_name}:{class_name}"
        original_init = cls.__init__

        @functools.wraps(original_init)
        def patched_init(self, *args, _orig=original_init, _target=target, **kwargs):
            _orig(self, *args, **kwargs)
            setattr(self, _STEPS_ATTR, [])
            setattr(self, _TARGET_ATTR, _target)
            _live_sequences.append(self)

        cls.__init__ = patched_init

        for method_name in methods:
            original_method = getattr(cls, method_name, None)
            if original_method is None:
                _skips[f"{target}.{method_name}: no existe"] += 1
                continue

            @functools.wraps(original_method)
            def patched(self, *args, _orig=original_method, _name=method_name, **kwargs):
                result = _orig(self, *args, **kwargs)
                steps = getattr(self, _STEPS_ATTR, None)
                if steps is not None:
                    try:
                        steps.append(
                            {
                                "method": _name,
                                "input": json.loads(
                                    _dumps({"args": list(args), "kwargs": dict(kwargs)})
                                ),
                                "output": json.loads(_dumps(result)),
                            }
                        )
                    except Unserializable as exc:
                        steps.append({"method": _name, "unserializable": str(exc)})
                return result

            setattr(cls, method_name, patched)


def _flush_sequences() -> None:
    for obj in _live_sequences:
        steps = getattr(obj, _STEPS_ATTR, None)
        target = getattr(obj, _TARGET_ATTR, None)
        if not steps or target is None:
            continue
        if any("unserializable" in step for step in steps):
            _skips[f"{_slug(target)}: paso no serializable"] += 1
            continue
        _write(target, {"target": target, "kind": "sequence", "steps": steps})
    _live_sequences.clear()
```

**Por qué `_live_sequences` se vacía en cada test.** Guardar referencias fuertes a cada instancia
creada durante toda la sesión haría crecer la memoria sin límite a lo largo de 1.662 tests. Vaciar
la lista en el teardown de cada test acota la memoria y, además, da la granularidad correcta: una
secuencia por instancia y por test.

- [ ] **Paso 2: Enganchar el flush al final de cada test**

Añadir a la sección de hooks de `tools/corpus/recorder.py`:

```python
def pytest_runtest_teardown(item, nextitem):
    _flush_sequences()
```

Y añadir la llamada a `_install_sequences()` dentro de `pytest_configure`, justo después de
`_install_functions()`.

- [ ] **Paso 3: Grabar el módulo de parsers y validar**

Ejecutar:
```bash
cd .upstream
CORPUS_OUT=../testdata CORPUS_COMMIT=a5292ca PYTHONPATH=../tools/corpus \
  .venv/Scripts/python -m pytest -q -p recorder tests/unit/test_parsers.py tests/unit/test_thinking_parser.py
```
Esperado: aparecen `testdata/parsers/AwsEventStreamParser/` y
`testdata/thinking_parser/ThinkingParser/` con casos que contienen `steps`.

- [ ] **Paso 4: Verificar que una secuencia tiene sentido**

Ejecutar:
```bash
python -c "import json,glob; d=json.load(open(glob.glob('testdata/parsers/AwsEventStreamParser/*.json')[0])); print(json.dumps(d, indent=2)[:1200])"
```
Comprobar que hay varios pasos `feed`, que las entradas son `{"__bytes__": ...}` y que las salidas
son listas de eventos. **Si solo hay un paso por caso y son todos idénticos, el flush está mal
enganchado.**

- [ ] **Paso 5: Commit**

```bash
git add tools/corpus/recorder.py
git commit -m "feat(corpus): record stateful parsers as full call sequences"
```

---

### Tarea 10: Grabador, modo generador

**Ficheros:**
- Modificar: `tools/corpus/recorder.py`

**Interfaces:**
- Consume: `targets_module.GENERATORS`.
- Produce: casos con `kind: "generator"`, cuyo `input` contiene `events` (los `KiroEvent` que
  consumió) y `kwargs` (los argumentos escalares), y cuyo `output` es la lista de fragmentos SSE
  emitidos.

Los formatters reciben un `httpx.Response`, no un generador de eventos: la frontera que interesa
grabar está dentro de la función, en la llamada a `parse_kiro_stream`. Se intercepta con un tee.
Ventaja: los muchos tests del upstream que ya sustituyen `parse_kiro_stream` por un mock quedan
capturados igual, porque el tee envuelve su mock.

- [ ] **Paso 1: Añadir el modo generador**

Añadir a `tools/corpus/recorder.py`, antes de la sección de hooks:

Añadir `inspect` al bloque de imports de la cabecera del fichero, junto a los que ya están, y
después añadir esta sección antes de la de hooks:

```python
# ----------------------------------------------------------------------------
# Modo generador
# ----------------------------------------------------------------------------

def _describe_unrecorded(bound, scalar_names) -> dict:
    """Anota el tipo de los argumentos que no se graban, como pista diagnostica."""
    notes = {}
    for name, value in bound.arguments.items():
        if name in scalar_names or name == "self":
            continue
        notes[name] = type(value).__name__
    return notes


def _install_generators() -> None:
    for module_name, func_name, scalar_names in targets_module.GENERATORS:
        try:
            module = importlib.import_module(module_name)
        except ImportError as exc:
            _skips[f"{module_name}: no se pudo importar ({exc})"] += 1
            continue
        original = getattr(module, func_name, None)
        if original is None:
            _skips[f"{module_name}:{func_name}: no existe"] += 1
            continue

        target = f"{module_name}:{func_name}"
        signature = inspect.signature(original)

        @functools.wraps(original)
        async def wrapper(
            *args,
            _orig=original,
            _target=target,
            _module=module,
            _sig=signature,
            _scalars=tuple(scalar_names),
            **kwargs,
        ):
            events: list = []
            upstream = getattr(_module, "parse_kiro_stream", None)

            if upstream is not None:
                async def tee(*a, **k):
                    async for event in upstream(*a, **k):
                        events.append(event)
                        yield event

                setattr(_module, "parse_kiro_stream", tee)

            chunks: list = []
            try:
                async for out in _orig(*args, **kwargs):
                    chunks.append(out)
                    yield out
            finally:
                if upstream is not None:
                    setattr(_module, "parse_kiro_stream", upstream)
                try:
                    bound = _sig.bind_partial(*args, **kwargs)
                    scalars = {
                        name: value
                        for name, value in bound.arguments.items()
                        if name in _scalars
                    }
                    payload = {
                        "target": _target,
                        "kind": "generator",
                        "input": json.loads(_dumps({"kwargs": scalars, "events": events})),
                        "output": json.loads(_dumps(chunks)),
                        "notes": {"unrecorded_args": _describe_unrecorded(bound, _scalars)},
                    }
                except Unserializable as exc:
                    _skips[f"{_slug(_target)}: {exc}"] += 1
                else:
                    _write(_target, payload)

        _rebind_everywhere(original, wrapper)
        setattr(module, func_name, wrapper)
```

- [ ] **Paso 2: Llamarlo desde pytest_configure**

Añadir `_install_generators()` en `pytest_configure`, después de `_install_sequences()`.

- [ ] **Paso 3: Grabar los módulos de streaming**

Ejecutar:
```bash
cd .upstream
CORPUS_OUT=../testdata CORPUS_COMMIT=a5292ca PYTHONPATH=../tools/corpus \
  .venv/Scripts/python -m pytest -q -p recorder tests/unit/test_streaming_openai.py tests/unit/test_streaming_anthropic.py
```

- [ ] **Paso 4: Medir la cobertura del modo generador**

Ejecutar:
```bash
python -c "import json; r=json.load(open('testdata/_report.json')); print(json.dumps({k:v for k,v in r['recorded'].items() if 'streaming' in k}, indent=2)); print('DESCARTES:'); print(json.dumps({k:v for k,v in r['skipped'].items() if 'streaming' in k}, indent=2))"
```

**Punto de decisión.** Anotar cuántos casos se grabaron y cuántos se descartaron por cada
formatter. Si se grabó menos del 30% de las invocaciones, el modo generador no sirve para
demostrar paridad de SSE y hay que decidir en la fase 5 entre portar esos tests a mano o construir
los casos desde un `httpx.Response` falso. **Registrar el número en el mensaje del commit**, para
que la fase 5 lo encuentre sin repetir la medición.

- [ ] **Paso 5: Commit**

Sustituir los dos números del cuerpo del mensaje por los que dio el paso 4. Este dato es la razón
de ser del commit: la fase 5 lo busca aquí.

```bash
git add tools/corpus/recorder.py
git commit -m "feat(corpus): record SSE formatters at the KiroEvent boundary

Intercepta parse_kiro_stream con un tee para capturar los eventos consumidos y
los fragmentos emitidos.

Cobertura medida en tests/unit/test_streaming_openai.py y
tests/unit/test_streaming_anthropic.py: N casos grabados, M invocaciones
descartadas."
```

---

### Tarea 11: Generación completa, tope de casos y prueba de determinismo

**Ficheros:**
- Crear: `tools/corpus/validate.py`
- Crear: `tools/corpus/snapshot.py`
- Modificar: `Taskfile.yml` (añadir `corpus`, `corpus:validate` y `corpus:verify-deterministic`)

**Interfaces:**
- Produce: la tarea `task corpus`, que es el único comando que hace falta para regenerar todo el
  corpus desde cero.

- [ ] **Paso 1: Escribir el validador**

Crear `tools/corpus/validate.py`:

```python
"""Valida el corpus generado y emite un informe de tamano.

Uso: python tools/corpus/validate.py testdata
"""

from __future__ import annotations

import json
import sys
from pathlib import Path

MAX_TOTAL_BYTES = 50 * 1024 * 1024
MAX_PER_TARGET = 500

REQUIRED_KEYS = {"target", "kind", "recorded_at_commit"}
VALID_KINDS = {"function", "sequence", "generator"}


def main(root: Path) -> int:
    if not root.is_dir():
        print(f"ERROR: {root} no existe")
        return 1

    problems: list[str] = []
    total_bytes = 0
    per_target: dict[str, int] = {}

    for path in sorted(root.rglob("*.json")):
        if path.name == "_report.json":
            continue
        total_bytes += path.stat().st_size
        target_dir = str(path.parent.relative_to(root)).replace("\\", "/")
        per_target[target_dir] = per_target.get(target_dir, 0) + 1

        try:
            data = json.loads(path.read_text(encoding="utf-8"))
        except json.JSONDecodeError as exc:
            problems.append(f"{path}: JSON invalido: {exc}")
            continue

        missing = REQUIRED_KEYS - data.keys()
        if missing:
            problems.append(f"{path}: faltan claves {sorted(missing)}")
        if data.get("kind") not in VALID_KINDS:
            problems.append(f"{path}: kind invalido {data.get('kind')!r}")
        if data.get("kind") == "sequence" and not data.get("steps"):
            problems.append(f"{path}: secuencia sin pasos")
        if data.get("kind") in {"function", "generator"} and "input" not in data:
            problems.append(f"{path}: falta input")

    for target, count in sorted(per_target.items()):
        if count > MAX_PER_TARGET:
            problems.append(f"{target}: {count} casos, tope {MAX_PER_TARGET}")

    print(f"objetivos: {len(per_target)}")
    print(f"casos:     {sum(per_target.values())}")
    print(f"tamano:    {total_bytes / 1024 / 1024:.2f} MB")
    print()
    for target, count in sorted(per_target.items(), key=lambda kv: -kv[1])[:15]:
        print(f"  {count:5d}  {target}")

    if total_bytes > MAX_TOTAL_BYTES:
        problems.append(
            f"tamano total {total_bytes / 1024 / 1024:.1f} MB supera el presupuesto de 50 MB"
        )

    if problems:
        print("\nPROBLEMAS:")
        for p in problems:
            print(f"  - {p}")
        return 1

    print("\ncorpus valido")
    return 0


if __name__ == "__main__":
    sys.exit(main(Path(sys.argv[1] if len(sys.argv) > 1 else "testdata")))
```

- [ ] **Paso 2: Escribir el comparador de instantáneas**

`cp -r` y `diff -r` no existen en Windows, así que la comprobación de determinismo va en Python.

Crear `tools/corpus/snapshot.py`:

```python
"""Copia y compara instantaneas del corpus, sin depender de cp ni diff.

Uso:
    python tools/corpus/snapshot.py save testdata .corpus-check
    python tools/corpus/snapshot.py compare .corpus-check testdata
    python tools/corpus/snapshot.py drop .corpus-check
"""

from __future__ import annotations

import shutil
import sys
from pathlib import Path

IGNORED = {"_report.json"}


def _files(root: Path) -> dict[str, bytes]:
    result = {}
    for path in sorted(root.rglob("*.json")):
        if path.name in IGNORED:
            continue
        key = str(path.relative_to(root)).replace("\\", "/")
        result[key] = path.read_bytes()
    return result


def compare(left: Path, right: Path) -> int:
    a, b = _files(left), _files(right)

    only_left = sorted(a.keys() - b.keys())
    only_right = sorted(b.keys() - a.keys())
    differing = sorted(k for k in a.keys() & b.keys() if a[k] != b[k])

    for key in only_left[:20]:
        print(f"solo en {left}: {key}")
    for key in only_right[:20]:
        print(f"solo en {right}: {key}")
    for key in differing[:20]:
        print(f"contenido distinto: {key}")

    total = len(only_left) + len(only_right) + len(differing)
    if total:
        print(f"\n{total} diferencias: el corpus NO es determinista")
        return 1
    print(f"corpus determinista: {len(a)} ficheros idénticos")
    return 0


def main(argv: list[str]) -> int:
    if len(argv) < 3:
        print(__doc__)
        return 2
    command = argv[0]
    if command == "save":
        src, dst = Path(argv[1]), Path(argv[2])
        shutil.rmtree(dst, ignore_errors=True)
        shutil.copytree(src, dst)
        print(f"instantánea guardada en {dst}")
        return 0
    if command == "compare":
        return compare(Path(argv[1]), Path(argv[2]))
    print(f"comando desconocido: {command}")
    return 2


if __name__ == "__main__":
    args = sys.argv[1:]
    if args and args[0] == "drop":
        shutil.rmtree(Path(args[1]), ignore_errors=True)
        sys.exit(0)
    sys.exit(main(args))
```

- [ ] **Paso 3: Añadir las tareas de orquestación**

Añadir a `Taskfile.yml`:

```yaml
  corpus:
    desc: Regenera el corpus golden completo desde el upstream fijado
    cmds:
      - task: corpus:record
      - task: corpus:validate

  corpus:validate:
    desc: Valida la estructura y el tamano del corpus
    cmds:
      - python tools/corpus/validate.py testdata

  corpus:verify-deterministic:
    desc: Genera el corpus dos veces y comprueba que sale idéntico
    cmds:
      - task: corpus:record
      - python tools/corpus/snapshot.py save testdata .corpus-check
      - task: corpus:record
      - python tools/corpus/snapshot.py compare .corpus-check testdata
      - python tools/corpus/snapshot.py drop .corpus-check
```

- [ ] **Paso 4: Generar el corpus completo**

Ejecutar: `task corpus`

Esperado: la suite completa del upstream se ejecuta con el grabador, y `validate.py` informa del
número de objetivos, de casos y del tamaño total, terminando con `corpus valido`. Esto tarda
varios minutos.

Si `validate.py` falla por tamaño, bajar `CORPUS_MAX_PER_TARGET` y regenerar. Si falla por
estructura, el fallo está en el grabador: arreglarlo antes de continuar.

- [ ] **Paso 5: Comprobar el determinismo**

Ejecutar: `task corpus:verify-deterministic`
Esperado: `corpus determinista: N ficheros idénticos`, sin diferencias.

Si aparecen diferencias, la causa habitual es un valor dependiente del tiempo, del azar o del
orden de iteración de un conjunto que se colara en algún caso. Identificar el objetivo por la
salida del comparador y añadirlo a `EXCLUDED` en `targets.py` con su motivo, o corregir la
serialización.

- [ ] **Paso 6: Añadir la exclusión del directorio temporal**

Añadir a `.gitignore`:

```gitignore
# Copia temporal de la comprobacion de determinismo
/.corpus-check/
```

- [ ] **Paso 7: Commit**

```bash
git add tools/corpus/validate.py tools/corpus/snapshot.py Taskfile.yml .gitignore
git commit -m "feat(corpus): add validator, snapshot comparator and case budget"
```

---

### Tarea 12: Versionar el corpus

**Ficheros:**
- Crear: `.gitattributes`
- Crear: `testdata/` (contenido generado)

- [ ] **Paso 1: Escribir .gitattributes**

Crear `.gitattributes`:

```gitattributes
# El corpus se compara byte a byte: los saltos de linea nunca se convierten
testdata/** text eol=lf
*.json text eol=lf
*.go text eol=lf
*.py text eol=lf
*.yml text eol=lf
Taskfile.yml text eol=lf

# El corpus es generado: no ensucia los diffs de las revisiones
testdata/** linguist-generated=true
```

- [ ] **Paso 2: Comprobar que no hay CRLF en el corpus**

Ejecutar:
```bash
python -c "
import glob,sys
bad=[p for p in glob.glob('testdata/**/*.json',recursive=True) if b'\r\n' in open(p,'rb').read()]
print(f'ficheros con CRLF: {len(bad)}')
sys.exit(1 if bad else 0)"
```
Esperado: `ficheros con CRLF: 0` y código de salida 0.

- [ ] **Paso 3: Commitear el corpus**

```bash
git add .gitattributes testdata
git commit -m "test(corpus): add golden corpus recorded from upstream a5292ca

Generado con 'task corpus'. Objetivos, casos y tamano segun testdata/_report.json."
```

- [ ] **Paso 4: Verificar que el repositorio sigue limpio**

Ejecutar: `git status --short`
Esperado: sin salida.

---

### Tarea 13: Validación estructural del corpus real desde Go

**Ficheros:**
- Crear: `internal/testutil/corpus_integrity_test.go`

**Interfaces:**
- Consume: `LoadCorpus`, `DecodeBytes` y el corpus de `testdata/`.
- Produce: la garantía de que el corpus generado es legible desde Go. Es la prueba de que las
  fases 2 a 5 pueden empezar.

- [ ] **Paso 1: Escribir el test**

Crear `internal/testutil/corpus_integrity_test.go`:

```go
// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package testutil

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCorpusIsReadable recorre todo el corpus generado y comprueba que cada
// caso deserializa a la estructura Case y respeta las invariantes de su kind.
// Si este test falla, ninguna fase posterior puede confiar en el corpus.
func TestCorpusIsReadable(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	base := filepath.Join(root, "testdata")

	var targets []string
	err = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(d.Name(), ".json") || d.Name() == "_report.json" {
			return nil
		}
		rel, err := filepath.Rel(base, filepath.Dir(path))
		if err != nil {
			return err
		}
		slug := filepath.ToSlash(rel)
		if len(targets) == 0 || targets[len(targets)-1] != slug {
			targets = append(targets, slug)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("recorriendo el corpus: %v", err)
	}
	if len(targets) == 0 {
		t.Fatal("corpus vacío: ejecuta 'task corpus'")
	}
	t.Logf("%d objetivos en el corpus", len(targets))

	total := 0
	for _, target := range targets {
		cases := LoadCorpus(t, target)
		total += len(cases)
		for _, c := range cases {
			if c.Target == "" {
				t.Errorf("%s/%s: target vacío", target, c.Name)
			}
			if c.Commit == "" {
				t.Errorf("%s/%s: recorded_at_commit vacío", target, c.Name)
			}
			switch c.Kind {
			case KindFunction, KindGenerator:
				if len(c.Input) == 0 {
					t.Errorf("%s/%s: kind %q sin input", target, c.Name, c.Kind)
				}
			case KindSequence:
				if len(c.Steps) == 0 {
					t.Errorf("%s/%s: kind sequence sin pasos", target, c.Name)
				}
				for i, s := range c.Steps {
					if s.Method == "" {
						t.Errorf("%s/%s: paso %d sin método", target, c.Name, i)
					}
				}
			default:
				t.Errorf("%s/%s: kind desconocido %q", target, c.Name, c.Kind)
			}
		}
	}
	t.Logf("%d casos válidos en total", total)
}

// TestCorpusReportMatchesFiles comprueba que el informe del grabador cuadra con
// los ficheros realmente presentes, para detectar un corpus commiteado a medias.
func TestCorpusReportMatchesFiles(t *testing.T) {
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("RepoRoot: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "testdata", "_report.json"))
	if err != nil {
		t.Fatalf("leyendo _report.json: %v", err)
	}
	var report struct {
		Commit     string         `json:"commit"`
		Recorded   map[string]int `json:"recorded"`
		TotalCases int            `json:"total_cases"`
	}
	if err := json.Unmarshal(raw, &report); err != nil {
		t.Fatalf("deserializando _report.json: %v", err)
	}
	if report.Commit != "a5292ca" {
		t.Errorf("el corpus se grabó del commit %q, quiero a5292ca", report.Commit)
	}
	if report.TotalCases == 0 {
		t.Error("el informe dice 0 casos")
	}
}
```

- [ ] **Paso 2: Ejecutar el test**

Ejecutar: `go test ./internal/testutil/ -v -run TestCorpus`
Esperado: `PASS`, con los mensajes de log indicando el número de objetivos y de casos.

- [ ] **Paso 3: Ejecutar toda la verificación**

Ejecutar:
```bash
task lint
task test
```
Esperado: ambos en verde.

- [ ] **Paso 4: Commit**

```bash
git add internal/testutil/corpus_integrity_test.go
git commit -m "test(testutil): verify the generated corpus is readable from Go"
```

---

## Criterios de aceptación de la fase 1

1. `task build` produce el binario, y `./kiro-gateway -v` imprime `Kiro Gateway 2.4.dev.13+go`.
2. `task test` y `task lint` terminan en verde.
3. `task corpus` regenera todo el corpus desde cero con un solo comando.
4. `task corpus:verify-deterministic` no encuentra diferencias entre dos generaciones.
5. `testdata/` está versionado, con LF, y `testdata/_report.json` registra el commit `a5292ca`.
6. `go test ./internal/testutil/` valida todos los casos del corpus.
7. `LICENSE` es la AGPL-3.0 literal y `NOTICE` atribuye el trabajo al upstream con su commit.
8. `docs/MAPPING.md` contiene las 32 filas de módulos Python.
9. Todo fichero `.go` lleva la cabecera SPDX, comprobado por el CI.
10. Queda registrado en el historial de commits el porcentaje de invocaciones de formatters SSE
    que el modo generador consiguió grabar, que es el dato que la fase 5 necesita.
