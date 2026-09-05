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
// "<modulo>/<funcion>", por ejemplo "converters_core/build_kiro_payload".
//
// Acepta testing.TB, así que sirve igual en un test, en un benchmark o en un fuzz.
//
// Falla el test si el objetivo no existe: un corpus ausente es un error de
// configuración, no un caso sin casos.
func LoadCorpus(tb testing.TB, target string) []Case {
	tb.Helper()
	root, err := RepoRoot()
	if err != nil {
		tb.Fatalf("localizando la raíz del repositorio: %v", err)
	}
	cases, err := readCases(filepath.Join(root, "testdata"), target)
	if err != nil {
		tb.Fatalf("cargando el corpus de %q: %v", target, err)
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

// CODIFICACIONES CON MARCADOR QUE EL CORPUS PUEDE CONTENER
//
// El grabador (tools/corpus/recorder.py, función _default) representa los valores
// que no son JSON nativo con un objeto de una sola clave. Son tres, y este paquete
// sabe decodificar las tres:
//
//	{"__bytes__":     "<base64>"}                             -> DecodeBytes
//	{"__set__":       [<elementos ordenados por repr>]}        -> DecodeSet
//	{"__exception__": {"type","module","args","str","cause"?}} -> DecodeException
//
// Los modelos pydantic NO llevan marcador a propósito: se graban como el objeto
// JSON plano que el port recibe por la red, así que se deserializan sin más.
//
// La lista completa y actualizada vive en docs/CORPUS.md; si aquí aparece una
// cuarta codificación, ese documento es el que hay que mirar primero.

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

// DecodeSet deserializa un valor que el grabador codificó como
// {"__set__": [...]}, y devuelve sus elementos. El grabador los ordena por repr()
// de Python, así que el orden es estable entre grabaciones pero NO es el orden
// natural del tipo en Go: compara como conjunto, no como lista.
func DecodeSet(raw json.RawMessage) ([]json.RawMessage, error) {
	var wrapper struct {
		Items *[]json.RawMessage `json:"__set__"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, err
	}
	if wrapper.Items == nil {
		return nil, fmt.Errorf("no es un conjunto codificado: %s", raw)
	}
	return *wrapper.Items, nil
}

// Exception es una excepción de Python grabada con el marcador __exception__.
//
// Type y Module son la parte que importa para la paridad: la correspondencia que
// se comprueba es tipo de excepción -> resultado. Str y Args están porque hay
// clasificadores que miran el mensaje, y Cause porque hay ramas que deciden por la
// excepción encadenada (kiro.network_errors.classify_network_error mira
// isinstance(err.__cause__, socket.gaierror) y saca el errno de sus args).
//
// El decodificador acepta las dos formas en que un valor Exception aparece en
// el corpus: la envuelta {"__exception__": {...}} (la que graba el recorder,
// tanto en el nivel raíz como en cada `cause` encadenada) y la plana con los
// campos directos. Sin esto la cadena de causas se perdía: json.Unmarshal
// dejaba Type/Module vacíos en la excepción encadenada porque los buscaba en
// el nivel del wrapper, no dentro del marcador.
type Exception struct {
	Type   string            `json:"type"`
	Module string            `json:"module"`
	Args   []json.RawMessage `json:"args"`
	Str    string            `json:"str"`
	Cause  *Exception        `json:"cause,omitempty"`
}

// UnmarshalJSON deserializa una Exception aceptando ambas formas del corpus.
// Se implementa aquí en vez de exponer un helper aparte para que la
// desenvoltura funcione también en los campos anidados (Cause), donde
// json.Unmarshal llama recursivamente al UnmarshalJSON del tipo.
func (e *Exception) UnmarshalJSON(data []byte) error {
	// Forma envuelta: {"__exception__": {campos}}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err == nil {
		if inner, ok := probe["__exception__"]; ok {
			return e.decodeFields(inner)
		}
	}
	return e.decodeFields(data)
}

// decodeFields deserializa los campos planos de una Exception, evitando la
// recursión infinita en UnmarshalJSON con el truco del type alias.
func (e *Exception) decodeFields(data []byte) error {
	type alias Exception
	var raw alias
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	*e = Exception(raw)
	return nil
}

// IsException dice si un valor crudo es una excepción codificada, sin fallar si no
// lo es. Sirve para los objetivos cuya salida es un valor normal o una excepción
// según la entrada.
func IsException(raw json.RawMessage) bool {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(raw, &probe); err != nil {
		return false
	}
	_, ok := probe["__exception__"]
	return ok
}

// DecodeException deserializa un valor que el grabador codificó como
// {"__exception__": {...}}, con su cadena de causas.
func DecodeException(raw json.RawMessage) (*Exception, error) {
	var wrapper struct {
		Exc *Exception `json:"__exception__"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, err
	}
	if wrapper.Exc == nil {
		return nil, fmt.Errorf("no es una excepción codificada: %s", raw)
	}
	return wrapper.Exc, nil
}

// ACCESO A LA ENTRADA DE UN CASO
//
// El campo Input de un caso es un objeto cuya forma depende del kind:
//
//	function  {"args": [...], "kwargs": {...}, "config"?: {...}}
//	generator {"kwargs": {...}, "events": [...], "config"?: {...}}
//	sequence  no tiene Input; cada paso de Steps tiene el suyo, con la misma forma
//	          que el de function. El paso 0 es siempre el constructor, con
//	          Method == "__init__".
//
// "config" solo aparece en los objetivos cuyo módulo lee banderas de kiro.config
// (tools/corpus/targets.py, CONFIG_INPUTS): esas banderas son entrada aunque no
// lleguen por argumento, y el port tiene que aplicarlas para reproducir la salida.

// Field devuelve input[key] como JSON crudo, y falla el test si no existe.
func Field(tb testing.TB, input json.RawMessage, key string) json.RawMessage {
	tb.Helper()
	raw, ok := OptionalField(input, key)
	if !ok {
		tb.Fatalf("no existe el campo %q en %s", key, input)
	}
	return raw
}

// OptionalField devuelve input[key] y si estaba presente, sin fallar el test.
func OptionalField(input json.RawMessage, key string) (json.RawMessage, bool) {
	var wrapper map[string]json.RawMessage
	if err := json.Unmarshal(input, &wrapper); err != nil {
		return nil, false
	}
	raw, ok := wrapper[key]
	return raw, ok
}

// Args devuelve los argumentos posicionales de la entrada de un caso o de un paso.
func Args(tb testing.TB, input json.RawMessage) []json.RawMessage {
	tb.Helper()
	var items []json.RawMessage
	if err := json.Unmarshal(Field(tb, input, "args"), &items); err != nil {
		tb.Fatalf("deserializando args de %s: %v", input, err)
	}
	return items
}

// Arg devuelve el argumento posicional en la posición index.
func Arg(tb testing.TB, input json.RawMessage, index int) json.RawMessage {
	tb.Helper()
	items := Args(tb, input)
	if index < 0 || index >= len(items) {
		tb.Fatalf("no existe args[%d]: hay %d argumentos en %s", index, len(items), input)
	}
	return items[index]
}

// Kwargs devuelve los argumentos con nombre de la entrada de un caso o de un paso.
func Kwargs(tb testing.TB, input json.RawMessage) map[string]json.RawMessage {
	tb.Helper()
	var named map[string]json.RawMessage
	if err := json.Unmarshal(Field(tb, input, "kwargs"), &named); err != nil {
		tb.Fatalf("deserializando kwargs de %s: %v", input, err)
	}
	return named
}

// Kwarg devuelve el argumento con nombre name.
func Kwarg(tb testing.TB, input json.RawMessage, name string) json.RawMessage {
	tb.Helper()
	raw, ok := Kwargs(tb, input)[name]
	if !ok {
		tb.Fatalf("no existe el argumento con nombre %q en %s", name, input)
	}
	return raw
}

// Config devuelve las banderas de configuración que el grabador guardó como parte
// de la entrada, o nil si el objetivo no lee ninguna. No falla cuando faltan: la
// mayoría de los objetivos no tienen configuración que aplicar.
func Config(input json.RawMessage) map[string]json.RawMessage {
	raw, ok := OptionalField(input, "config")
	if !ok {
		return nil
	}
	var flags map[string]json.RawMessage
	if err := json.Unmarshal(raw, &flags); err != nil {
		return nil
	}
	return flags
}

// Events devuelve los eventos que consumió un caso de kind generator.
func Events(tb testing.TB, input json.RawMessage) []json.RawMessage {
	tb.Helper()
	var items []json.RawMessage
	if err := json.Unmarshal(Field(tb, input, "events"), &items); err != nil {
		tb.Fatalf("deserializando events de %s: %v", input, err)
	}
	return items
}
