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
