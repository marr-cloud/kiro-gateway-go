// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/utils"
	"golang.org/x/sync/singleflight"
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

	// sqliteDBPath holds the path to the SQLite database for kiro-cli credentials
	// (Task 4). Set by NewManagerForAccount when loading from SQLite.
	sqliteDBPath string

	// sqliteKeyRead holds the exact key that was read from SQLite, used to
	// write back to the same location (Task 4, read-merge-write pattern).
	sqliteKeyRead string

	// sfGroup coalesces concurrent Refresh() calls into one via singleflight.
	// Multiple goroutines calling Refresh() concurrently will all receive the
	// same result from a single OIDC refresh request (Task 5).
	sfGroup singleflight.Group
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
	var sqliteKeyRead string

	// Task 4: Detect SQLite source (via KiroCLIDBFile extension or env var)
	sqliteDBPath := cfg.KiroCLIDBFile
	if sqliteDBPath != "" {
		// Load from SQLite for kiro-cli credentials
		tempM := &Manager{
			cfg:          cfg,
			creds:        creds,
			sqliteDBPath: sqliteDBPath,
		}
		if err := tempM.loadFromSQLite(sqliteDBPath); err != nil {
			return nil, fmt.Errorf("auth: loading SQLite credentials from %s: %w", sqliteDBPath, err)
		}
		creds = tempM.creds
		sqliteKeyRead = tempM.sqliteKeyRead
		source = nil // SQLite source is not wrapped in a Source interface in Task 4
	} else if cfg.KiroCredsFile != "" {
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
		authType:      detectAuthType(creds, sqliteDBPath != ""),
		region:        baseRegion,
		apiRegion:     apiRegion,
		sqliteDBPath:  sqliteDBPath,
		sqliteKeyRead: sqliteKeyRead,
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
