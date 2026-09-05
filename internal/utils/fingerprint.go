// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package utils

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/user"
	"sync"
)

// La huella de máquina viaja en el User-Agent que el gateway manda a Kiro y
// se calcula una vez por proceso: no cambia durante la vida del binario y se
// consulta en cada petición.
//
// Fórmula (fija por paridad con .upstream/kiro/utils.py :: get_machine_fingerprint):
//
//	sha256_hex("{hostname}-{username}-kiro-gateway")
//
// Cualquier fallo obteniendo hostname o username usa la misma cadena de
// respaldo que el original, "default-kiro-gateway". No comparamos contra el
// corpus: fase 1 retiró ese objetivo a propósito porque el valor depende de
// la máquina que grabó (docs/CORPUS.md §4). La verificación de paridad la
// hace la fase 2a de forma manual, ejecutando Python y Go en la misma máquina
// (spec §8.4).
var (
	fingerprintOnce  sync.Once
	fingerprintValue string
)

// MachineFingerprint devuelve la huella descrita arriba, cacheada con
// sync.Once.
func MachineFingerprint() string {
	fingerprintOnce.Do(func() {
		fingerprintValue = computeFingerprint()
	})
	return fingerprintValue
}

func computeFingerprint() string {
	hostname, hostErr := os.Hostname()
	username, userErr := kiroUsername()
	var payload []byte
	if hostErr != nil || userErr != nil {
		payload = []byte("default-kiro-gateway")
	} else {
		payload = []byte(hostname + "-" + username + "-kiro-gateway")
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// kiroUsername replica getpass.getuser(): recorre LOGNAME, USER, LNAME,
// USERNAME y usa la primera no vacía. Esto es importante en Windows, donde
// os/user.Current() devuelve "DOMINIO\\usuario" mientras que getpass.getuser
// devuelve solo el valor de %USERNAME%. Sin esta réplica, Python y Go
// producirían huellas distintas en la misma máquina y romperían la paridad
// que verifica la Tarea 9 de la fase.
func kiroUsername() (string, error) {
	for _, name := range []string{"LOGNAME", "USER", "LNAME", "USERNAME"} {
		if v, ok := os.LookupEnv(name); ok && v != "" {
			return v, nil
		}
	}
	u, err := user.Current()
	if err != nil {
		return "", err
	}
	return u.Username, nil
}
