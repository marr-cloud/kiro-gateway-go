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
