// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

// arnRegionPattern valida el componente de región de un ARN, igual que
// auth.py:362 (`re.match(r'^[a-z]+-[a-z]+-\d+$', parts[3])`).
var arnRegionPattern = regexp.MustCompile(`^[a-z]+-[a-z]+-\d+$`)

// regionFromARN extrae y valida el componente de región (índice 3) de un
// ARN como "arn:aws:codewhisperer:us-east-1:123456789012:profile/xyz",
// replicando la lógica que el original solo aplica en el loader de SQLite
// (auth.py:357-366). El JSON source de esta Task la reutiliza como
// fallback cuando el JSON no trae 'region' — ver el ruling sobre el nivel
// "detectado del ARN" de la precedencia en el informe de la Task 2.
func regionFromARN(arn string) (string, bool) {
	parts := strings.Split(arn, ":")
	if len(parts) < 4 || parts[3] == "" {
		return "", false
	}
	if !arnRegionPattern.MatchString(parts[3]) {
		return "", false
	}
	return parts[3], true
}

// detectedRegion es el equivalente de self._detected_api_region: prefiere
// el campo Region tal cual llegó de la credencial (el 'region' del JSON,
// auth.py:426-428) y solo cae al ARN si ese campo vino vacío.
func detectedRegion(c Credentials) string {
	if c.Region != "" {
		return c.Region
	}
	if c.ProfileARN != "" {
		if r, ok := regionFromARN(c.ProfileARN); ok {
			return r
		}
	}
	return ""
}

// resolveAPIRegion replica la cadena de precedencia de auth.py:188-215:
//  1. override explícito por cuenta (api_region del constructor).
//  2. KIRO_API_REGION, ya resuelto en cfg.KiroAPIRegion por internal/config
//     — este paquete no llama a os.Getenv por su cuenta, ver el comentario
//     de package config sobre ser "la ÚNICA fuente de configuración
//     derivada del entorno".
//  3. región "detectada" de la credencial: Credentials.Region (campo
//     'region' del JSON, o el ARN si falta — ver detectedRegion).
//  4. región "sso" de la credencial: Credentials.SSORegion. Para el JSON
//     source de hoy vale siempre lo mismo que el nivel 3 (auth.py:424-425
//     asigna el mismo data['region'] a los dos atributos), así que este
//     nivel nunca cambia el resultado hoy — pero es un campo
//     independiente, no una repetición de la comprobación del nivel 3
//     (fix round 1, Important #1: la versión anterior repetía `c.Region`,
//     que detectedRegion ya había consultado, y por tanto era código
//     muerto de verdad). La Task 4 (SQLite) puede poblar Region (desde el
//     ARN de la tabla `state`) y SSORegion (desde el `region` del token o
//     del device-registration) con valores distintos — ahí este nivel sí
//     puede ganar.
//
// baseRegion (KIRO_REGION, con su propio default ya aplicado por el único
// caller, NewManagerForAccount) es el último recurso y SIEMPRE llega no
// vacío: no hay un nivel 5 adicional que caiga a defaultRegion aquí — eso
// sería, de nuevo, código muerto dado el precondition del caller (fix round
// 1, Important #1).
func resolveAPIRegion(explicit string, cfg *config.Config, c Credentials, baseRegion string) string {
	if explicit != "" {
		return explicit
	}
	if cfg.KiroAPIRegion != "" {
		return cfg.KiroAPIRegion
	}
	if r := detectedRegion(c); r != "" {
		return r
	}
	if c.SSORegion != "" {
		return c.SSORegion
	}
	return baseRegion
}

// detectAuthType decide el AuthType a partir de qué campos trae c, más la
// señal fromSQLite (Task 4: si las credenciales vinieron de la SQLite de
// kiro-cli, el tipo es KIRO_CLI sin mirar nada más — auth.py no lo dice
// explícitamente porque allí no existe ese AuthType, pero es la lectura
// natural de "Loaded from SQLite → KIRO_CLI" del brief de esta Task).
//
// Para JSON (fromSQLite == false, el único caso que esta Task ejercita):
//  1. clientId + clientSecret → AWS_SSO_OIDC (auth.py:241, requiere AMBOS,
//     no solo clientId — el brief simplifica a "clientId presente").
//  2. accessToken + refreshToken + profileArn → KIRO_DESKTOP ("JSON
//     completo" del brief).
//  3. solo refreshToken (sin accessToken verificado) → REFRESH_ONLY.
//  4. nada de lo anterior → UNKNOWN.
func detectAuthType(c Credentials, fromSQLite bool) AuthType {
	if fromSQLite {
		return AuthTypeKiroCLI
	}
	if c.ClientID != "" && c.ClientSecret != "" {
		return AuthTypeAWSSSO
	}
	if c.AccessToken != "" && c.RefreshToken != "" && c.ProfileARN != "" {
		return AuthTypeKiroDesktop
	}
	if c.RefreshToken != "" {
		return AuthTypeRefreshOnly
	}
	return AuthTypeUnknown
}

// AccessToken implementa utils.TokenProvider. Devuelve el token actual si
// no está por expirar; si lo está, llama a Refresh() (que usa singleflight
// para coalescer llamadas concurrentes). La degradación grácil (Task 5,
// auth.py:906-919) solo aplica a SQLite+400 y es manejada internamente por
// Refresh().
//
// Restructuración para Task 5: AccessToken ahora lee el token bajo lock,
// suelta el lock, y LUEGO llama a Refresh() si es necesario. Esto evita un
// deadlock: Refresh() usa singleflight internally que puede tomar m.mu, y
// un mutex sync.Mutex no es reentrante.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	now := time.Now().UTC()
	token := m.tokens.AccessToken
	isExpiringSoon := m.tokens.IsExpiringSoon(now, tokenRefreshThreshold)
	m.mu.Unlock()

	// If token is fresh, return it immediately without refresh
	if token != "" && !isExpiringSoon {
		return token, nil
	}

	// Token is expiring soon (or empty); call Refresh to refresh it
	// Refresh uses singleflight to coalesce concurrent calls
	return m.Refresh(ctx)
}

// ForceRefresh es la interfaz opcional forceRefresher que
// internal/httpclient busca por type assertion en el camino de 403
// (internal/httpclient/client.go:145-160). Task 5 implementa el cuerpo real:
// llama a Refresh(ctx) con una flag interna que bypassa el gate de
// isExpiringSoon, garantizando un refresco incondicional. Usa singleflight
// para coalescing, y NO aplica graceful degradation (un 403 requiere un
// token realmente nuevo; devolver el antiguo no ayudaría).
func (m *Manager) ForceRefresh(ctx context.Context) (string, error) {
	return m.refreshCoalesced(ctx, true)
}

// refreshLocked asume que el caller ya tiene m.mu. Task 3 le añade la rama
// AWS SSO OIDC (refreshAWSSSO, oidc.go); Task 5 sustituye el resto (Kiro
// Desktop, RefreshOnly) por el refresco real envuelto en singleflight,
// dejando la firma intacta para que AccessToken y ForceRefresh no cambien.
//
// Fix round 1 (Important 3): ampliar para también rutear AuthTypeKiroCLI a
// refreshAWSSSO. La diferencia entre AuthTypeAWSSSO y AuthTypeKiroCLI es la
// FUENTE (JSON vs SQLite), no el método de refresco. Ambos pueden tener
// credenciales OIDC (clientId/clientSecret) y usar el mismo refreshAWSSSO.
// La única diferencia observable es el comportamiento ante un 400: el
// 400-retry-with-SQLite-reload es controlado por refreshAWSSSO mismo
// revisando si m.authType == AuthTypeKiroCLI.
func (m *Manager) refreshLocked(ctx context.Context) (string, error) {
	switch m.authType {
	case AuthTypeAWSSSO, AuthTypeKiroCLI:
		if err := m.refreshAWSSSO(ctx); err != nil {
			return "", err
		}
		return m.tokens.AccessToken, nil
	default:
		return "", ErrRefreshNotImplemented
	}
}

// loadFromSQLite is implemented in sqlite.go (Task 4). It is called by
// refreshAWSSSO when handling a 400 error for AuthTypeKiroCLI credentials,
// replicating _load_credentials_from_sqlite + retry behavior (auth.py:770-773).

// ProfileARN devuelve el ARN de perfil de CodeWhisperer actual. Con mutex
// porque, a partir de la Task 5, un refresco puede actualizarlo
// (auth.py:725-726: `if new_profile_arn: self._profile_arn =
// new_profile_arn`).
func (m *Manager) ProfileARN() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.creds.ProfileARN
}

// Region, APIHost, QHost y Type leen sin m.mu porque region/apiRegion/
// authType los fija NewManagerForAccount una sola vez y nada más los muta
// después — a diferencia de creds.ProfileARN, que ProfileARN() sí protege
// porque Task 5 lo actualiza en un refresco real. Si una tarea futura
// vuelve mutable alguno de estos tres (p.ej. authType cambiando ante un
// fallo de refresco), hay que pasar su accessor a tomar m.mu también (fix
// round 1, Minor #3).

// Region devuelve la región base/SSO — self._region del original
// (auth.py:955-957). No es la región de API resuelta; ver el comentario del
// campo region y de resolveAPIRegion.
func (m *Manager) Region() string {
	return m.region
}

// APIHost devuelve la URL completa del host de la API principal
// (generateAssistantResponse), con esquema incluido — igual que
// auth_manager.api_host en el original (auth.py:959-962,
// config.py:573-576), que routes_anthropic.py:410 usa directamente como
// `f"{auth_manager.api_host}/generateAssistantResponse"`.
func (m *Manager) APIHost() string {
	return fmt.Sprintf(kiroAPIHostTemplate, m.apiRegion)
}

// QHost devuelve la URL completa del host de la Q API (ListAvailableModels,
// MCP). Comparte plantilla con APIHost desde el issue #58 — ver la
// constante kiroQHostTemplate.
func (m *Manager) QHost() string {
	return fmt.Sprintf(kiroQHostTemplate, m.apiRegion)
}

// Fingerprint devuelve la huella de máquina que viaja en el User-Agent.
// Delega en utils.MachineFingerprint, que ya cachea el valor con
// sync.Once — dos llamadas devuelven siempre la misma cadena.
func (m *Manager) Fingerprint() string {
	return utils.MachineFingerprint()
}

// Type devuelve el AuthType detectado en la construcción.
func (m *Manager) Type() AuthType {
	return m.authType
}
