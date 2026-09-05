// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package parity contiene comprobaciones cruzadas que comparan este port con el
// upstream Python en la MISMA máquina. Van detrás del build tag `parity` para
// que el CI —que no tiene el clon del upstream ni su entorno virtual— no las
// ejecute ni las necesite. Se lanzan a mano:
//
//	go test -tags parity ./tools/parity/
//
// Este fichero no lleva build tag a propósito: sin él el directorio no tendría
// ningún fichero Go compilable con el tag desactivado, y `go vet ./...` fallaría
// con "build constraints exclude all Go files".
package parity
