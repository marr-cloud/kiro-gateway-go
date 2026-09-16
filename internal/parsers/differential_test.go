// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package parsers

import (
	"bytes"
	"math/rand"
	"os/exec"
	"testing"
)

// TestUTF8IgnoreDifferential verifica que bytes.ToValidUTF8(seq, nil) se
// comporta igual que chunk.decode('utf-8', errors='ignore').encode('utf-8')
// de Python para secuencias de bytes arbitrarias.
//
// Este es el test diferencial que el spec §6.4 y §8.4 dejaban pendiente: "Creo
// que bytes.ToValidUTF8(b, nil) coincide con b.decode('utf-8', errors='ignore')
// en todos los casos, pero no lo he comprobado. La fase 2 incluye un test
// diferencial que genera secuencias de bytes aleatorias, las pasa por Python y
// por Go y compara la salida." La fase 2 nunca lo escribió; Feed (parser.go)
// depende exactamente de esta equivalencia para el manejo bug-a-bug de chunks
// UTF-8 partidos, así que se escribe aquí, en el primer consumidor real.
//
// Genera 1000 secuencias con semilla fija (rand.NewSource(42), determinista
// entre corridas y entre SO), de 1 a 64 bytes, sesgadas hacia el rango
// 0x80-0xFF donde ocurren los cortes de límite UTF-8 (continuaciones y bytes
// de inicio de secuencias multibyte). Para cada una calcula got con
// bytes.ToValidUTF8 y want ejecutando Python de verdad con el chunk por
// stdin, y compara byte a byte.
//
// Se salta limpiamente (t.Skip) si no hay python3 ni python en PATH, en vez
// de fallar: el resto del paquete no depende de que ESTE test se ejecute
// para ser correcto, depende del hecho matemático que este test solo
// confirma (ver el razonamiento ASCII-safe en el comentario de
// findMatchingBrace, brace_scanner.go).
func TestUTF8IgnoreDifferential(t *testing.T) {
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		pythonPath, err = exec.LookPath("python")
		if err != nil {
			t.Skip("python3/python no está en PATH: se salta el test diferencial")
		}
	}

	const n = 1000
	rng := rand.New(rand.NewSource(42))

	for i := 0; i < n; i++ {
		seq := randomByteSequence(rng)

		got := bytes.ToValidUTF8(seq, nil)
		want := decodeIgnoreViaPython(t, pythonPath, seq)

		if !bytes.Equal(got, want) {
			t.Fatalf("secuencia #%d (semilla 42) diverge:\n seq  = % x\n got  = % x\n want = % x", i, seq, got, want)
		}
	}
}

// randomByteSequence genera entre 1 y 64 bytes. El 70% de los bytes caen en
// 0x80-0xFF (continuaciones/cabeceras UTF-8 multibyte, donde 'errors=ignore'
// tiene que decidir qué descartar); el resto son ASCII imprimible, para que
// las secuencias mezclen texto válido con basura binaria, como un chunk real
// partido a mitad de un carácter.
func randomByteSequence(rng *rand.Rand) []byte {
	length := 1 + rng.Intn(64)
	seq := make([]byte, length)
	for i := range seq {
		if rng.Intn(100) < 70 {
			seq[i] = byte(0x80 + rng.Intn(0x80)) // 0x80..0xFF
		} else {
			seq[i] = byte(0x20 + rng.Intn(0x5f)) // 0x20..0x7e, ASCII imprimible
		}
	}
	return seq
}

// decodeIgnoreViaPython ejecuta
// chunk.decode('utf-8', errors='ignore').encode('utf-8') en un intérprete
// Python real, pasando seq por stdin para no depender de cómo el shell
// codifique argumentos de línea de comandos.
func decodeIgnoreViaPython(t *testing.T, pythonPath string, seq []byte) []byte {
	t.Helper()
	cmd := exec.Command(pythonPath, "-c",
		"import sys; sys.stdout.buffer.write(sys.stdin.buffer.read().decode('utf-8', errors='ignore').encode('utf-8'))")
	cmd.Stdin = bytes.NewReader(seq)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("ejecutando %s: %v", pythonPath, err)
	}
	return out.Bytes()
}
