// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

//go:build parity

package parity

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/tokenizer"
)

// TestTokenizerEncodeOrdinaryParity compara `tokenizer.EncodeOrdinary` del
// port con `tiktoken.get_encoding("cl100k_base").encode_ordinary(...)` del
// upstream (venv fijado en tools/corpus/requirements.lock) sobre una batería
// amplia de entradas: prosa ASCII, código, JSON, non-ASCII, emojis,
// whitespace exótico y prosa larga. Los ids se generan en el venv y se
// comparan enteros; una divergencia en cualquier caso invalida el port.
//
// Los cinco contadores de fase 2b (count_tokens, count_message_tokens,
// count_tools_tokens, count_system_tokens, estimate_request_tokens) apilan
// sobre esta función, así que este test es el complemento de banda ancha
// del test de referencia de encode_test.go. Va detrás del build tag
// `parity` — igual que fingerprint_test.go — para que el CI no lo ejecute
// (no dispone del clon del upstream ni de su venv) y se lanza a mano con
//
//	go test -tags parity ./tools/parity/
//
// Si el intérprete del venv o el clon del upstream no están, el test se
// SALTA: la ausencia del entorno de paridad no es un fallo del port.
// Prepararlo se hace con `task corpus:setup`.
func TestTokenizerEncodeOrdinaryParity(t *testing.T) {
	root := repoRoot(t)
	python := filepath.Join(root, ".upstream", ".venv", "Scripts", "python.exe")
	if runtime.GOOS != "windows" {
		python = filepath.Join(root, ".upstream", ".venv", "bin", "python")
	}
	if _, err := os.Stat(python); err != nil {
		t.Skipf("no hay intérprete del venv en %s (ejecuta `task corpus:setup`): %v", python, err)
	}

	// Cada caso lleva su propio nombre; el input va POR STDIN como JSON a un
	// script que devuelve los ids en JSON, para no interpolar el texto en la
	// línea de comandos (incompatible con CR/LF, comillas anidadas, etc.).
	cases := []struct {
		name string
		in   string
	}{
		{"vacío", ""},
		{"ASCII básico", "hello world"},
		{"contracciones", "don't  I'LL  they've it's she'd"},
		{"JSON pequeño", `{"a": 1, "b": [1, 2]}`},
		{"JSON anidado", `{"users":[{"id":1,"name":"Ana"},{"id":2,"name":"Bob"}],"count":2}`},
		{"non-ASCII + emoji", "Moscú café 😀"},
		{"emojis variados", "🦊🐶🌍🚀 fin"},
		{"código Go pequeño",
			"package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"hola\")\n}\n"},
		{"código Python pequeño",
			"def add(a, b):\n    return a + b\n\nprint(add(1, 2))\n"},
		{"whitespace exótico", " \t\v \n \r\n  \u00a0\u2028\u3000abc"},
		{"whitespace boundaries", "a  b\ta \n b\r\nc  \n  d"},
		{"puntuación densa", "!!!,,,...???---'''\"\"\"(())[[]]{{}}"},
		{"dígitos y decimales",
			"1234567890 3.14159 -42.5e-3 0x1F 0b1010 100_000"},
		{"URLs y paths",
			"See https://example.com/path?q=1&b=2 or /var/log/kiro/foo.txt or C:\\Users\\a b\\c.txt"},
		{"Unicode mezclado",
			"日本語のテキスト、中文文本、Русский текст, ελληνικά, العربية, हिन्दी."},
		{"markdown",
			"# Título\n\n- Uno\n- Dos\n\n```go\nfunc x() {}\n```\n"},
		{"prosa larga en inglés",
			strings.Repeat("The quick brown fox jumps over the lazy dog. ", 20)},
		{"CRLF pesado",
			"linea 1\r\nlinea 2\r\n\r\nlinea 4\r\r\n\r\n"},
		{"trailing whitespace variado", "hola   \t \n\n\n"},
		{"apóstrofes exóticos", "'ll 've 're 't 's 'D 'M 'ss 'x '' '"},
		{"long-s Unicode CI", "'\u017fabc otro texto"},
	}

	// Un único proceso Python que lee líneas (JSON-encoded strings) y devuelve
	// una línea (JSON de ids) por cada entrada; evita el coste de arrancar el
	// intérprete N veces. En Windows el intérprete abre sys.stdin/stdout con
	// la codepage de la consola (cp1252 por defecto), lo que mutila los bytes
	// UTF-8 que Go escribe por json.Marshal; reconfiguramos ambos flujos a
	// UTF-8 para que las secuencias no-ASCII (`ú`, `😀`, `日本語`, `\u017f`…)
	// crucen el pipe sin corrupción.
	const script = `import sys, json, tiktoken
sys.stdin.reconfigure(encoding="utf-8")
sys.stdout.reconfigure(encoding="utf-8")
enc = tiktoken.get_encoding("cl100k_base")
for line in sys.stdin:
    text = json.loads(line)
    ids = enc.encode_ordinary(text)
    sys.stdout.write(json.dumps(ids) + "\n")
    sys.stdout.flush()
`
	cmd := exec.Command(python, "-c", script)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin del venv: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout del venv: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("arrancando venv: %v", err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	}()

	reader := bufio.NewReader(stdout)

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			payload, err := json.Marshal(c.in)
			if err != nil {
				t.Fatalf("marshal input: %v", err)
			}
			payload = append(payload, '\n')
			if _, err := stdin.Write(payload); err != nil {
				t.Fatalf("escribiendo al venv: %v (stderr=%q)", err, stderr.String())
			}
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatalf("leyendo del venv: %v (stderr=%q)", err, stderr.String())
			}
			var want []int
			if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &want); err != nil {
				t.Fatalf("decode ids del venv: %v (linea=%q)", err, line)
			}
			got := tokenizer.EncodeOrdinary(c.in)
			if !intSliceEqual(got, want) {
				t.Errorf("EncodeOrdinary(%q):\n  got:  %v\n  want: %v\n  %s", c.in, got, want, describeDiff(got, want))
			}
		})
	}
}

func intSliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// describeDiff resume la primera divergencia entre dos secuencias de ids para
// que el mensaje de fallo indique exactamente dónde se descuadra el port.
func describeDiff(got, want []int) string {
	n := len(got)
	if len(want) < n {
		n = len(want)
	}
	for i := 0; i < n; i++ {
		if got[i] != want[i] {
			return fmt.Sprintf("primer desajuste en idx=%d: got=%d, want=%d", i, got[i], want[i])
		}
	}
	if len(got) != len(want) {
		return fmt.Sprintf("longitudes distintas: len(got)=%d len(want)=%d", len(got), len(want))
	}
	return ""
}
