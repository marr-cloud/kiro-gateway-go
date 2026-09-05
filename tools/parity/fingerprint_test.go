// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

//go:build parity

package parity

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// TestFingerprintParity ejecuta el get_machine_fingerprint del upstream Python y
// utils.MachineFingerprint() del port en la MISMA máquina y comprueba que
// coinciden. Es la comprobación que pide el spec §8.4 y que el corpus NO puede
// hacer: el valor depende de la máquina que lo calcula, así que la fase 1 retiró
// ese objetivo del corpus a propósito (docs/CORPUS.md §4). El User-Agent que se
// envía a Kiro incrusta esta huella, así que una divergencia rompería la paridad
// de las cabeceras salientes.
//
// Va detrás del build tag `parity` para que el CI no la ejecute: necesita el
// clon del upstream en .upstream/ y su entorno virtual .upstream/.venv. Se lanza
// a mano con:
//
//	go test -tags parity ./tools/parity/
//
// Si no están el clon ni el venv, el test se SALTA (no falla): la ausencia del
// upstream no es un fallo de paridad. Prepáralos con `task corpus:setup`.
func TestFingerprintParity(t *testing.T) {
	root := repoRoot(t)

	python := filepath.Join(root, ".upstream", ".venv", "Scripts", "python.exe")
	if runtime.GOOS != "windows" {
		python = filepath.Join(root, ".upstream", ".venv", "bin", "python")
	}
	if _, err := os.Stat(python); err != nil {
		t.Skipf("no hay intérprete del venv en %s (ejecuta `task corpus:setup`): %v", python, err)
	}

	// Ejecuta el fingerprint del upstream con .upstream/ inyectado en sys.path.
	// La ruta va como argumento (sys.argv[1]) para no interpolarla en la fuente.
	const script = "import sys; sys.path.insert(0, sys.argv[1]); " +
		"from kiro.utils import get_machine_fingerprint; print(get_machine_fingerprint())"
	out, err := exec.Command(python, "-c", script, filepath.Join(root, ".upstream")).Output()
	if err != nil {
		t.Fatalf("ejecutando get_machine_fingerprint del upstream: %v", err)
	}
	pythonFP := strings.TrimSpace(string(out))

	goFP := utils.MachineFingerprint()

	if pythonFP == "" {
		t.Fatal("el upstream devolvió una huella vacía")
	}
	if pythonFP != goFP {
		t.Fatalf("las huellas NO coinciden en esta máquina:\n  python: %q\n  go:     %q\n"+
			"El User-Agent que se envía a Kiro depende de este valor (spec §8.4); hay que arreglarlo.",
			pythonFP, goFP)
	}
	t.Logf("huella coincide en esta máquina: %s", goFP)
}

// repoRoot sube desde el directorio de trabajo del test (el del paquete cuando
// se ejecuta `go test ./tools/parity/`) hasta encontrar go.mod.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no se encontró go.mod subiendo desde el directorio de trabajo")
		}
		dir = parent
	}
}
