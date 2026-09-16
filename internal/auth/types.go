// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package auth es el port de .upstream/kiro/auth.py: gestiona el ciclo de
// vida del access token de Kiro (carga de credenciales, detección del tipo
// de autenticación, refresco y persistencia write-through). Manager
// implementa utils.TokenProvider sin importar internal/utils (ver el
// comentario de ese paquete y spec §5.3 sobre el ciclo utils↔auth).
package auth

import "time"

// AuthType identifica el mecanismo de autenticación detectado a partir de
// las credenciales cargadas. El original (.upstream/kiro/auth.py:68-83) solo
// define dos miembros de verdad (KIRO_DESKTOP y AWS_SSO_OIDC); KioCLI y
// RefreshOnly son una extensión deliberada del port, ver el ruling
// correspondiente en el informe de la Task 2: reflejan los tres `type` que
// ya existen en credentials.json ("json", "sqlite", "refresh_token", spec
// §6.11) y le dan a cada uno un valor propio en vez de que kiro-cli y
// refresh-token-only colapsen silenciosamente en KIRO_DESKTOP como hace hoy
// el Python.
type AuthType int

const (
	// AuthTypeUnknown es el valor cero: no se cargó ninguna credencial
	// utilizable (ni refreshToken, ni accessToken+profileArn, ni
	// clientId/clientSecret). No existe en el original — allí el default
	// es KIRO_DESKTOP incluso sin credenciales (auth.py:173) — pero un
	// modo "unknown" explícito es más seguro que fingir que un Manager
	// vacío es Kiro Desktop.
	AuthTypeUnknown AuthType = iota
	// AuthTypeKiroDesktop: JSON completo (accessToken + refreshToken +
	// profileArn), o credenciales por variables de entorno equivalentes.
	AuthTypeKiroDesktop
	// AuthTypeAWSSSO: clientId + clientSecret presentes (auth.py:241-243).
	AuthTypeAWSSSO
	// AuthTypeKiroCLI: credenciales cargadas desde la SQLite de kiro-cli
	// (Task 4). El JSON source de esta Task nunca produce este valor.
	AuthTypeKiroCLI
	// AuthTypeRefreshOnly: solo hay refreshToken (sin accessToken previo
	// verificado), típico de una entrada credentials.json de tipo
	// "refresh_token" (spec §6.11, account_manager.py:486-492).
	AuthTypeRefreshOnly
)

// String facilita logs/debug; no hay equivalente directo en el original
// porque allí AuthType es un Enum de Python con .value en snake_case, que
// replicamos aquí.
func (t AuthType) String() string {
	switch t {
	case AuthTypeKiroDesktop:
		return "kiro_desktop"
	case AuthTypeAWSSSO:
		return "aws_sso_oidc"
	case AuthTypeKiroCLI:
		return "kiro_cli"
	case AuthTypeRefreshOnly:
		return "refresh_only"
	default:
		return "unknown"
	}
}

// Credentials es el conjunto normalizado de campos que puede producir
// cualquier Source: el JSON de Kiro Desktop hoy (json_source.go), y la
// SQLite de kiro-cli en la Task 4 (sqlite_source.go). No todos los campos
// los rellena toda fuente — Manager decide el AuthType a partir de qué
// combinación de campos llegó poblada, igual que
// .upstream/kiro/auth.py:234-246 y :416-439.
type Credentials struct {
	AccessToken  string
	RefreshToken string
	ProfileARN   string
	// Region es la región "detectada" de la credencial — el equivalente de
	// self._detected_api_region en el original: para el JSON de Kiro
	// Desktop, el campo `region` tal cual (auth.py:426-428); para la
	// SQLite de kiro-cli (Task 4), la región extraída del ARN en la tabla
	// `state` (auth.py:357-364). Es el nivel 3 de resolveAPIRegion, NO
	// necesariamente la región de API final.
	Region string
	// SSORegion es la región "sso" de la credencial — el equivalente de
	// self._sso_region en el original: para el JSON de hoy se rellena con
	// el mismo campo `region` que Region (auth.py:424-425 pone las dos
	// asignaciones seguidas, mismo valor). Para la SQLite (Task 4) puede
	// venir de un campo distinto (`region` del token o del
	// device-registration, auth.py:300-303, :339-342) y por tanto diferir
	// de Region — de ahí que sea un campo propio en vez de reutilizar
	// Region: es el nivel 4 de resolveAPIRegion, el fallback que solo
	// importa cuando el nivel 3 no resolvió nada.
	SSORegion    string
	ExpiresAt    time.Time
	ClientID     string
	ClientSecret string
	// ClientIDHash es el equivalente de self._client_id_hash (auth.py:158):
	// presente solo en credenciales de Enterprise Kiro IDE, cuando el JSON
	// trae `clientIdHash` en vez de `clientId`/`clientSecret` directos
	// (auth.py:431-433). Task 3 (internal/auth/oidc.go) lo usa para
	// resolver clientId/clientSecret desde
	// ~/.aws/sso/cache/{clientIdHash}.json en el momento del refresco.
	//
	// Nota de alcance (Task 3): esta Task solo añade el campo — Save() ya
	// lo preserva sin cambios porque jsonFileSource.Save hace
	// read-merge-write sobre un map[string]any y nunca borra claves
	// desconocidas (ver TestJSONFileSource_SavePreservesUnknownFields).
	// jsonFileSource.Load() NO llena todavía este campo (ni mergeCredentials
	// lo copia, ni detectAuthType lo consulta) — Task 2 nunca lo implementó
	// pese a que el brief de esta Task 3 asumía que sí. Ver el ruling
	// correspondiente en el informe de la Task 3: el camino Enterprise vía
	// fichero credentials.json real queda pendiente de una ronda de fix (o
	// de la Task 4), y por ahora solo es alcanzable construyendo
	// Credentials{ClientIDHash: ...} directamente (como hacen los tests de
	// esta Task, igual que hará la fuente SQLite de la Task 4).
	ClientIDHash string
}

// Source carga y persiste Credentials contra un almacén concreto. Hoy solo
// existe jsonFileSource (Kiro Desktop); sqliteSource llega en la Task 4.
type Source interface {
	// Load lee las credenciales actuales del almacén. Un almacén ausente
	// (fichero que no existe todavía) no es un error — devuelve
	// Credentials{} y nil, igual que el original registra un warning y
	// sigue (auth.py:409-411) en vez de abortar la construcción del
	// Manager.
	Load() (Credentials, error)
	// Save persiste unas Credentials refrescadas de vuelta al almacén,
	// preservando los campos desconocidos que ya hubiera (write-through
	// read-merge-write, auth.py:489-522).
	Save(Credentials) error
}

// Tokens es el par access token / expiración vigente en memoria. Separado
// de Credentials porque, tras un refresco, solo cambian estos dos campos
// (más, a veces, el refresh token — ver Manager.refreshToken) y no el resto
// de metadatos de la credencial (profileArn, region, etc. normalmente se
// mantienen).
type Tokens struct {
	AccessToken string
	ExpiresAt   time.Time
}

// IsExpiringSoon replica is_token_expiring_soon (auth.py:634-648): sin
// ExpiresAt se asume que hay que refrescar; si no, expira "pronto" cuando
// ExpiresAt cae dentro de threshold desde now.
func (t Tokens) IsExpiringSoon(now time.Time, threshold time.Duration) bool {
	if t.ExpiresAt.IsZero() {
		return true
	}
	return !t.ExpiresAt.After(now.Add(threshold))
}

// IsExpired replica is_token_expired (auth.py:650-665): sin ExpiresAt se
// asume expirado; si no, lo está cuando now ya alcanzó o pasó ExpiresAt.
func (t Tokens) IsExpired(now time.Time) bool {
	if t.ExpiresAt.IsZero() {
		return true
	}
	return !now.Before(t.ExpiresAt)
}
