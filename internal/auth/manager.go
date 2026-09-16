// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
)

const (
	// tokenRefreshThreshold replica TOKEN_REFRESH_THRESHOLD
	// (.upstream/kiro/config.py:193; spec línea 805): un token se
	// considera "por expirar" 600 segundos antes de su vencimiento real.
	tokenRefreshThreshold = 600 * time.Second

	// defaultRegion es el último nivel de la precedencia de región: el
	// valor por defecto tanto del parámetro `region` del constructor
	// original (.upstream/kiro/auth.py:123) como de la propia
	// KIRO_REGION (.upstream/kiro/config.py:147). En la práctica
	// config.Load ya aplica este mismo default a Config.KiroRegion, así
	// que esta constante solo actúa quando alguien construye un
	// *config.Config a mano con KiroRegion == "".
	defaultRegion = "us-east-1"

	// kiroAPIHostTemplate y kiroQHostTemplate: en el original ambos hosts
	// comparten literalmente la misma plantilla desde el issue #58
	// (.upstream/kiro/config.py:178-185 — "codewhisperer.{region}
	// .amazonaws.com doesn't exist for non-us-east-1 regions"). El brief
	// de esta Task sugería "q.{region}.amazonaws.com" para QHost, pero
	// eso es una versión vieja/incorrecta: el upstream real que tenemos
	// en .upstream/kiro/config.py usa el mismo host runtime.{region}
	// .kiro.dev para las dos cosas. Upstream gana — ver el ruling en el
	// informe de la Task 2.
	kiroAPIHostTemplate = "https://runtime.%s.kiro.dev"
	kiroQHostTemplate   = "https://runtime.%s.kiro.dev"
)

// ErrRefreshNotImplemented es el error estable que devuelve el stub de
// refresco de esta Task. Task 5 sustituye el cuerpo de refreshLocked por el
// refresco real (Kiro Desktop u OIDC, según Type()) envuelto en
// singleflight; hasta entonces, tanto AccessToken (near-expiry) como
// ForceRefresh (camino de 403 de httpclient, ver
// internal/httpclient/client.go:145-160) devuelven este error de forma
// predecible.
var ErrRefreshNotImplemented = errors.New("refresh not implemented until Task 5")

// Manager es el port de KiroAuthManager (.upstream/kiro/auth.py:85-977).
// Implementa utils.TokenProvider (AccessToken + ProfileARN) y, además, la
// interfaz opcional forceRefresher que internal/httpclient busca por type
// assertion para el camino de refresco forzado en 403.
type Manager struct {
	mu  sync.Mutex
	cfg *config.Config

	// source es nil cuando no hay ni fichero JSON (KiroCredsFile) ni,
	// Task 4, SQLite configurados: el Manager corre en modo "solo env"
	// con RefreshToken/ProfileARN venidos directamente de *config.Config.
	source Source

	creds Credentials
	// refreshToken es una copia de creds.RefreshToken. Se mantiene como
	// campo propio (igual que el brief describe el struct: "creds,
	// refresh_token, tokens, cfg, mu") porque, tras un refresco real
	// (Task 5), el refresh token puede rotar de forma independiente del
	// resto de creds — exactamente como el original guarda
	// self._refresh_token aparte de cualquier otro estado.
	refreshToken string
	tokens       Tokens

	authType AuthType

	// region es la región "base"/SSO — self._region en el original
	// (auth.py:955-957): NO cambia con la precedencia de API-region, es
	// literalmente el valor pasado al constructor (aquí, cfg.KiroRegion,
	// con fallback a defaultRegion). Region() devuelve este campo tal
	// cual.
	region string
	// apiRegion es el resultado de la precedencia completa (ver
	// resolveAPIRegion) y solo se usa para construir APIHost()/QHost():
	// el original tampoco expone la región de API resuelta como
	// propiedad propia, solo los hosts ya construidos con ella
	// (auth.py:959-967).
	apiRegion string

	// homeDir resuelve el directorio home del usuario, para ~/.aws/sso/cache/...
	// Por defecto os.UserHomeDir; inyectable en tests sin modificar vars de paquete.
	// Fix round 1 (Critical 1): trasladado desde el var de paquete homeDirFunc de oidc.go.
	homeDir func() (string, error)

	// oidcURL construye la URL del endpoint OIDC a partir de la sso region.
	// Por defecto la plantilla real; inyectable en tests sin modificar vars de paquete.
	// Fix round 1 (Critical 1): trasladado desde el var de paquete oidcTokenURL de oidc.go.
	oidcURL func(ssoRegion string) string
}

// Aserciones en tiempo de compilación (fix round 1, Minor #2):
//   - *Manager satisface utils.TokenProvider (AccessToken + ProfileARN),
//     la interfaz que internal/utils.GetKiroHeaders exige.
//   - *Manager también satisface la FORMA de la interfaz opcional
//     forceRefresher que internal/httpclient busca por type assertion en
//     el camino de 403 (internal/httpclient/client.go:145-160). Esa
//     interfaz es no exportada allí — no se puede importar — así que aquí
//     se restata su forma exacta (mismo nombre y firma de método) solo
//     para fijar la aserción; si algún día diverge de httpclient, este
//     compilador NO lo detectaría (son tipos estructuralmente iguales, no
//     el mismo tipo), pero al menos deja constancia explícita del
//     contrato en vez de depender solo de los tests de httpclient.
var (
	_ utils.TokenProvider = (*Manager)(nil)
	_ interface {
		ForceRefresh(context.Context) (string, error)
	} = (*Manager)(nil)
)

// NewManager construye un Manager en modo "cuenta única": la variante de
// un solo argumento que exige el contrato de la Task. Es azúcar sobre
// NewManagerForAccount con el override de región por cuenta vacío.
func NewManager(cfg *config.Config) (*Manager, error) {
	return NewManagerForAccount(cfg, "")
}

// NewManagerForAccount es como NewManager pero acepta el equivalente Go del
// parámetro `api_region` del constructor original (auth.py:128), que
// account_manager.py:473-492 pasa por cuenta desde credentials.json ("valor
// por cuenta" — el nivel más alto de la precedencia de región, spec §6.10).
// La Task 6 (internal/accountmanager) llamará a esta variante directamente
// por cada entrada de credentials.json; NewManager cubre el caso de una
// sola cuenta configurada solo por entorno.
func NewManagerForAccount(cfg *config.Config, apiRegionOverride string) (*Manager, error) {
	if cfg == nil {
		return nil, errors.New("auth: nil config")
	}

	// Arranca desde los valores "de entorno" (equivalentes a los
	// parámetros refresh_token/profile_arn del constructor original,
	// auth.py:121-122) y los deja que un Source los sobrescriba solo si
	// realmente trae ese campo poblado — igual que
	// _load_credentials_from_file solo pisa self._refresh_token si
	// 'refreshToken' in data (auth.py:417-422).
	creds := Credentials{
		RefreshToken: cfg.RefreshToken,
		ProfileARN:   cfg.ProfileARN,
	}

	var source Source
	if cfg.KiroCredsFile != "" {
		source = newJSONFileSource(cfg.KiroCredsFile)
		loaded, err := source.Load()
		if err != nil {
			return nil, fmt.Errorf("auth: loading Kiro Desktop credentials from %s: %w", cfg.KiroCredsFile, err)
		}
		mergeCredentials(&creds, loaded)
	}

	baseRegion := cfg.KiroRegion
	if baseRegion == "" {
		baseRegion = defaultRegion
	}

	apiRegion := resolveAPIRegion(apiRegionOverride, cfg, creds, baseRegion)

	m := &Manager{
		cfg:          cfg,
		source:       source,
		creds:        creds,
		refreshToken: creds.RefreshToken,
		tokens: Tokens{
			AccessToken: creds.AccessToken,
			ExpiresAt:   creds.ExpiresAt,
		},
		authType:  detectAuthType(creds, false),
		region:    baseRegion,
		apiRegion: apiRegion,
	}

	// Fix round 1 (Critical 1): initializar los campos inyectables con sus defaults
	// para que no sean nil. En tests, los campos pueden ser reasignados sobre la
	// instancia de Manager construida directamente, sin necesidad de vars de paquete.
	if m.homeDir == nil {
		m.homeDir = os.UserHomeDir
	}
	if m.oidcURL == nil {
		m.oidcURL = func(ssoRegion string) string {
			return "https://oidc." + ssoRegion + ".amazonaws.com/token"
		}
	}

	// Fix round 1 (Important 2): resolver Enterprise clientIdHash ANTES de
	// detectAuthType, igual que el original (auth.py:430-433). Si la credencial
	// trae SOLO clientIdHash (sin clientId/clientSecret directo), resolver desde
	// ~/.aws/sso/cache/{clientIdHash}.json aquí, y entonces detectAuthType verá
	// los clientId/clientSecret resueltos para decidir que es AuthTypeAWSSSO.
	if m.creds.ClientID == "" && m.creds.ClientSecret == "" && m.creds.ClientIDHash != "" {
		// Necesitamos un Manager "temporal" para poder llamar a
		// loadEnterpriseDeviceRegistration, que usa m.homeDir. Pero como m ya
		// existe, simplemente llámalo con el hash, y él actualizará m.creds.
		_ = m.loadEnterpriseDeviceRegistration(m.creds.ClientIDHash)
		// Redetectar el tipo de auth ahora que tenemos clientId/clientSecret resueltos.
		m.authType = detectAuthType(m.creds, false)
	}

	return m, nil
}

// mergeCredentials copia a dst solo los campos que loaded trae poblados,
// dejando intactos los que ya tenía dst — el equivalente Go de los `if
// 'campo' in data:` del original (auth.py:417-439).
//
// Deviación deliberada (ruling de la Task 2, fix round 1, Important #2):
// el original pisa el campo de todas formas cuando la clave está presente
// en el JSON, incluso si su valor es la cadena vacía (`if 'refreshToken' in
// data: self._refresh_token = data['refreshToken']` — la comprobación es
// sobre la CLAVE, no sobre el valor). Aquí solo se pisa cuando el valor
// cargado es no vacío: un `"refreshToken": ""` explícito en el JSON NO
// borra un valor previo (p.ej. el de *config.Config). No es un patrón que
// el original documente como funcionalidad («borrar poniendo cadena
// vacía»), y el idiom de Go para "ausente" es el cero del tipo, así que se
// mantiene el comportamiento Go-idiomático en vez de replicar la
// comprobación de presencia de clave — ver
// TestMergeCredentials_EmptyStringDoesNotOverwrite, que fija este
// comportamiento como contrato explícito para que no derive sin querer.
func mergeCredentials(dst *Credentials, loaded Credentials) {
	if loaded.RefreshToken != "" {
		dst.RefreshToken = loaded.RefreshToken
	}
	if loaded.AccessToken != "" {
		dst.AccessToken = loaded.AccessToken
	}
	if loaded.ProfileARN != "" {
		dst.ProfileARN = loaded.ProfileARN
	}
	if loaded.Region != "" {
		dst.Region = loaded.Region
	}
	if loaded.SSORegion != "" {
		dst.SSORegion = loaded.SSORegion
	}
	if !loaded.ExpiresAt.IsZero() {
		dst.ExpiresAt = loaded.ExpiresAt
	}
	if loaded.ClientID != "" {
		dst.ClientID = loaded.ClientID
	}
	if loaded.ClientSecret != "" {
		dst.ClientSecret = loaded.ClientSecret
	}
	// Fix round 1 (Important 2): copiar ClientIDHash junto con ClientID/ClientSecret.
	if loaded.ClientIDHash != "" {
		dst.ClientIDHash = loaded.ClientIDHash
	}
}

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
// no está por expirar; si lo está, intenta refrescar y, si el refresco
// falla, PROPAGA el error — no hay degradación grácil aquí. Ver el ruling
// de la Task 2: la degradación grácil del original (auth.py:906-919) es
// estrictamente para el modo SQLite con un 400 de OIDC; en modo JSON/Kiro
// Desktop (auth.py:925-926, "Non-SQLite mode or non-400 error - propagate
// the exception") el fallo de refresco siempre se propaga, igual que aquí.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now().UTC()
	if m.tokens.AccessToken != "" && !m.tokens.IsExpiringSoon(now, tokenRefreshThreshold) {
		return m.tokens.AccessToken, nil
	}

	if _, err := m.refreshLocked(ctx); err != nil {
		return "", err
	}
	if m.tokens.AccessToken == "" {
		return "", errors.New("auth: failed to obtain access token")
	}
	return m.tokens.AccessToken, nil
}

// ForceRefresh es la interfaz opcional forceRefresher que
// internal/httpclient busca por type assertion en el camino de 403
// (internal/httpclient/client.go:145-160). Hoy es un stub — Task 5 le pone
// el cuerpo real (singleflight + refresco OIDC/Kiro Desktop).
func (m *Manager) ForceRefresh(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.refreshLocked(ctx)
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

// loadFromSQLite es el seam que la Task 4 reemplaza con el cuerpo real (la
// fuente SQLite de kiro-cli, auth.py:294-366). refreshAWSSSO (oidc.go) lo
// llama tras un 400 cuando m.authType == AuthTypeKiroCLI, replicando
// _load_credentials_from_sqlite + retry único (auth.py:770-773). Por ahora
// es un no-op que siempre devuelve nil — preflight ruling documentado en
// progress.md: "Task 3 depende de loadFromSQLite de la Task 4; stub no-op
// hasta que aterrice".
// dbPath is deliberately unused pending Task 4's SQLite source.
// Task 4 will add a field to Manager holding the real path and update the
// call site in oidc.go to pass it.
func (m *Manager) loadFromSQLite(dbPath string) error {
	return nil
}

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
