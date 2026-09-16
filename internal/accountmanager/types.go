// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package accountmanager es el port de .upstream/kiro/account_manager.py:127-431.
// Gestiona múltiples cuentas de Kiro con descubrimiento, persistencia de estado
// y preparación para failover/circuit breaker en tareas posteriores.
package accountmanager

import (
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/auth"
)

// Account representa una cuenta Kiro con su configuración, estado de autenticación
// y estadísticas de uso.
type Account struct {
	// ID es el identificador único de la cuenta: para json/sqlite, la ruta
	// absoluta resuelta; para refresh_token, "refresh_token_{sha256(token)[:16]}".
	ID string

	// Type es el tipo de credencial: "json", "sqlite", o "refresh_token".
	Type string

	// Path es la ruta del fichero o directorio de credenciales. Vacío para
	// refresh_token.
	Path string

	// Enabled indica si la cuenta está activa en credentials.json.
	Enabled bool

	// ProfileARN es el ARN del perfil (opcional).
	ProfileARN string

	// Region es la región base/SSO (opcional).
	Region string

	// APIRegion es la región de API (opcional), que fuerza runtime.{region}.
	APIRegion string

	// Auth es el *auth.Manager completamente inicializado para esta cuenta.
	Auth *auth.Manager

	// Stats contiene estadísticas de fallos y recuperación para circuit breaker
	// (Task 7) y failover.
	Stats AccountStats

	// Models contiene la lista de modelos disponibles (poblada por Task 8).
	Models ModelAccountList
}

// AccountStats rastrea los fallos y su recuperación.
type AccountStats struct {
	// ConsecutiveFailures es el contador de fallos consecutivos.
	ConsecutiveFailures int

	// LastFailure es la marca de tiempo del último fallo.
	LastFailure time.Time

	// LastFailureMsg es el mensaje de error del último fallo.
	LastFailureMsg string
}

// ModelAccountList contiene la lista de modelos disponibles en una cuenta,
// con cacheo y TTL.
type ModelAccountList struct {
	// Models es la lista de IDs de modelos disponibles.
	Models []string

	// LoadedAt es la marca de tiempo del último carguío.
	LoadedAt time.Time

	// TTL es el tiempo de vida del caché (desde AccountCacheTTL del config).
	TTL time.Duration
}
