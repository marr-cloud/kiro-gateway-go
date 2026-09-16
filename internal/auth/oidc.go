// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

// Package auth (este fichero): port de
// .upstream/kiro/auth.py:743-869 (_refresh_token_aws_sso_oidc +
// _do_aws_sso_oidc_refresh) y :458-487 (_load_enterprise_device_registration).
//
// # Por qué NO se usa httpclient.Client.RequestWithRetry aquí
//
// El brief de esta Task pedía usar httpclient.Client.RequestWithRetry para
// el POST del refresco OIDC. Verificado contra el original y descartado por
// tres razones independientes, cada una suficiente por sí sola:
//
//  1. Deadlock real. RequestWithRetry construye las cabeceras en CADA
//     intento con utils.GetKiroHeaders(ctx, tp) (client.go:337-338), que
//     llama incondicionalmente a tp.AccessToken(ctx). Si tp fuera el propio
//     Manager, AccessToken() intenta tomar m.mu (manager.go:335) -- pero
//     refreshAWSSSO corre DENTRO de refreshLocked, que ya tiene m.mu tomado
//     por AccessToken/ForceRefresh. Es un mutex no reentrante: deadlock
//     garantizado, no hipotético.
//  2. Cabeceras equivocadas. GetKiroHeaders fija
//     Content-Type: application/x-amz-json-1.0 y un juego de cabeceras
//     específicas de la API de Kiro (x-amz-target, User-Agent con
//     fingerprint, etc. -- headers.go:46-56). El endpoint OIDC de AWS SSO
//     exige Content-Type: application/json y NINGUNA otra cabecera
//     (auth.py:817-819, confirmado leyendo el payload real que construye
//     _do_aws_sso_oidc_refresh). No hay forma de pedirle a GetKiroHeaders
//     ese Content-Type distinto: es una función completamente fija.
//  3. El propio original NO envuelve esta llamada en su capa de reintentos
//     compartida. _do_aws_sso_oidc_refresh abre un httpx.AsyncClient(
//     timeout=30) ad-hoc (auth.py:825) y hace una ÚNICA petición sin
//     reintentos de 403/429/5xx -- el único reintento que existe es el
//     recarga-SQLite-y-reintenta-una-vez ante 400, a nivel de aplicación,
//     que SÍ se replica aquí (ver refreshAWSSSO).
//
// En su lugar, este fichero usa un *http.Client de stdlib con Timeout: 30s
// (mismo valor que httpx.AsyncClient(timeout=30)), construido en cada
// intento -- igual que el original construye un AsyncClient nuevo con
// `async with` en cada llamada a _do_aws_sso_oidc_refresh. Ver el ruling
// correspondiente en el informe de la Task 3.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// oidcClientTimeout replica timeout=30 de httpx.AsyncClient (auth.py:825):
// no es STREAMING_READ_TIMEOUT ni ningún otro valor de *config.Config -- el
// original lo escribe como literal en esta llamada concreta, no como
// constante de módulo, así que aquí también es un literal local.
const oidcClientTimeout = 30 * time.Second

// oidcRefreshRequest es el cuerpo JSON camelCase que exige la AWS SSO OIDC
// CreateToken API (auth.py:810-815): grantType, no grant_type.
type oidcRefreshRequest struct {
	GrantType    string `json:"grantType"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken"`
}

// oidcRefreshResponse son los campos camelCase que devuelve la API
// (auth.py:846-849). ExpiresIn es un puntero a propósito: el original usa
// result.get("expiresIn", 3600), que solo aplica el default cuando la CLAVE
// está ausente, no cuando vale 0 explícitamente -- un *int deja distinguir
// "ausente" (nil) de "presente y cero" igual que dict.get lo distingue.
type oidcRefreshResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ExpiresIn    *int   `json:"expiresIn"`
}

// refreshAWSSSO es el port de _refresh_token_aws_sso_oidc (auth.py:743-775).
// Asume que el caller (refreshLocked) ya tiene m.mu tomado -- no toma el
// lock aquí.
//
// Estrategia: un intento con las credenciales en memoria; si falla con
// HTTP 400 Y m.authType == AuthTypeKiroCLI (el equivalente Go de
// self._sqlite_db siendo truthy, ver el ruling de la Task 2 sobre este
// AuthType), recarga desde SQLite (loadFromSQLite, stub hasta la Task 4) y
// reintenta UNA vez sin protección adicional -- si ese segundo intento
// también falla, el error se propaga tal cual, igual que el original deja
// que la segunda llamada a _do_aws_sso_oidc_refresh (sin try/except propio)
// se propague sin capturar (auth.py:773).
//
// Deviación deliberada frente al brief: el brief pedía condicionar el
// reintento a que el cuerpo del 400 sea {"error":"invalid_client"}. El
// original NO inspecciona el cuerpo del error para esta decisión -- solo
// mira e.response.status_code == 400 (auth.py:770); el cuerpo se parsea
// aparte solo para LOGGING (auth.py:833-839), nunca para la condición del
// if. Upstream gana: aquí se reintenta ante cualquier 400, sin mirar el
// cuerpo.
//
// Task 5 (graceful degradation): wraps errors in OIDCError to preserve the
// HTTP status code, so refresh.go's graceful-degradation logic can distinguish
// 400 errors from others. This is necessary for the SQLite+400 graceful-
// degradation path (auth.py:906-919) — if the error loses its status code,
// the caller can't know whether to apply degradation.
func (m *Manager) refreshAWSSSO(ctx context.Context) error {
	statusCode, err := m.doAWSSSORefreshAttempt(ctx)
	if err == nil {
		return nil
	}
	if statusCode == http.StatusBadRequest && m.authType == AuthTypeKiroCLI {
		_ = m.loadFromSQLite(m.sqliteDBPath)
		statusCode, err = m.doAWSSSORefreshAttempt(ctx)
		if err != nil && statusCode > 0 {
			return &OIDCError{StatusCode: statusCode, Err: err}
		}
		return err
	}
	// Wrap error with status code if we have one
	if statusCode > 0 {
		return &OIDCError{StatusCode: statusCode, Err: err}
	}
	return err
}

// doAWSSSORefreshAttempt es el port de _do_aws_sso_oidc_refresh
// (auth.py:777-869): UN intento completo (validación, resolución Enterprise,
// petición, parseo, mutación de estado, persistencia). Devuelve el status
// code HTTP recibido (0 si la petición ni siquiera obtuvo respuesta, p.ej.
// error de red) para que refreshAWSSSO decida si toca reintentar.
func (m *Manager) doAWSSSORefreshAttempt(ctx context.Context) (int, error) {
	// Enterprise Kiro IDE: si solo tenemos el hash (auth.py:430-433 lo
	// resuelve al cargar el fichero; aquí, dado que json_source.go todavía
	// no rellena ClientIDHash -- ver el ruling de la Task 3 -- se resuelve
	// de forma perezosa aquí, antes de construir el cuerpo, tal como pide
	// el brief de esta Task).
	if m.creds.ClientID == "" && m.creds.ClientSecret == "" && m.creds.ClientIDHash != "" {
		_ = m.loadEnterpriseDeviceRegistration(m.creds.ClientIDHash)
	}

	// Validaciones, mismo orden y mismas condiciones que auth.py:793-798.
	if m.refreshToken == "" {
		return 0, errors.New("auth: refresh token is not set")
	}
	if m.creds.ClientID == "" {
		return 0, errors.New("auth: client id is not set (required for AWS SSO OIDC)")
	}
	if m.creds.ClientSecret == "" {
		return 0, errors.New("auth: client secret is not set (required for AWS SSO OIDC)")
	}

	// SSO region: self._sso_region or self._region (auth.py:804).
	ssoRegion := m.creds.SSORegion
	if ssoRegion == "" {
		ssoRegion = m.region
	}
	url := m.oidcURL(ssoRegion)

	reqBody := oidcRefreshRequest{
		GrantType:    "refresh_token",
		ClientID:     m.creds.ClientID,
		ClientSecret: m.creds.ClientSecret,
		RefreshToken: m.refreshToken,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return 0, fmt.Errorf("auth: encoding aws sso oidc refresh request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return 0, fmt.Errorf("auth: building aws sso oidc refresh request: %w", err)
	}
	// Content-Type: application/json -- NO application/x-amz-json-1.1 (el
	// brief lo sugería como posibilidad a verificar). auth.py:817-819 fija
	// literalmente headers = {"Content-Type": "application/json"} y ninguna
	// otra cabecera (ni Authorization, ni User-Agent propio: httpx manda el
	// suyo por defecto, igual que net/http mandará el suyo aquí al no
	// fijarlo explícitamente).
	httpReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: oidcClientTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("auth: aws sso oidc refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, fmt.Errorf("auth: reading aws sso oidc response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		// TODO(security): redact secrets in response body before logging (matches upstream's equally-loose behavior).
		return resp.StatusCode, fmt.Errorf("auth: aws sso oidc refresh failed: status %d: %s", resp.StatusCode, string(respBody))
	}

	var parsed oidcRefreshResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return resp.StatusCode, fmt.Errorf("auth: parsing aws sso oidc response: %w", err)
	}
	if parsed.AccessToken == "" {
		return resp.StatusCode, fmt.Errorf("auth: aws sso oidc response does not contain accessToken: %s", string(respBody))
	}

	// expires_in = result.get("expiresIn", 3600) (auth.py:849): el default
	// solo aplica si la clave está ausente, no si vale 0 -- de ahí el
	// puntero en oidcRefreshResponse.
	expiresIn := 3600
	if parsed.ExpiresIn != nil {
		expiresIn = *parsed.ExpiresIn
	}
	// self._expires_at = now + timedelta(seconds=expires_in - 60)
	// (auth.py:860): colchón de 60s restado del expiresIn declarado.
	expiresAt := time.Now().UTC().Add(time.Duration(expiresIn-60) * time.Second)

	m.tokens.AccessToken = parsed.AccessToken
	m.tokens.ExpiresAt = expiresAt
	m.creds.AccessToken = parsed.AccessToken
	m.creds.ExpiresAt = expiresAt
	// if new_refresh_token: self._refresh_token = new_refresh_token
	// (auth.py:856-857) -- solo se pisa si la respuesta trae uno.
	if parsed.RefreshToken != "" {
		m.refreshToken = parsed.RefreshToken
		m.creds.RefreshToken = parsed.RefreshToken
	}

	// Write-through: auth.py:864-868 guarda siempre tras un refresco
	// exitoso (a fichero o SQLite según _sqlite_db). _save_credentials_to_file
	// (auth.py:489-522) y _save_credentials_to_sqlite envuelven TODO su
	// cuerpo en un try/except que solo loguea -- un fallo al persistir
	// NUNCA hace fallar el refresco ya conseguido. Se replica ignorando el
	// error de Save aquí (este paquete no tiene todavía una dependencia de
	// logging establecida -- ver el ruling de la Task 3).
	// TODO(logging): log this error once observability lands (matches upstream's try/except).
	if m.source != nil {
		_ = m.source.Save(m.creds)
	}

	return resp.StatusCode, nil
}

// loadEnterpriseDeviceRegistration es el port de
// _load_enterprise_device_registration (auth.py:458-487): resuelve
// clientId/clientSecret desde ~/.aws/sso/cache/{clientIDHash}.json cuando
// las credenciales de Enterprise Kiro IDE solo traen el hash.
//
// El original envuelve TODO el cuerpo de esta función en un único
// try/except Exception que solo loguea (auth.py:468-487) -- ni un fichero
// ausente, ni un JSON corrupto, ni cualquier otro fallo de E/S detienen la
// carga de credenciales ni el refresco: como mucho, clientId/clientSecret
// se quedan vacíos y la validación de doAWSSSORefreshAttempt (más arriba)
// es quien finalmente reporta el problema ("client id is not set"). Se
// replica devolviendo siempre nil -- la firma conserva `error` porque así
// la pide el brief de esta Task, pero hoy nunca es no-nil, a propósito.
func (m *Manager) loadEnterpriseDeviceRegistration(clientIDHash string) error {
	home, err := m.homeDir()
	if err != nil {
		return nil
	}

	path := filepath.Join(home, ".aws", "sso", "cache", clientIDHash+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		// Fichero ausente (o cualquier otro error de lectura): no-fatal,
		// igual que el warning-y-sigue del original (auth.py:471-473).
		return nil
	}

	var reg struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		// JSON corrupto: también no-fatal (auth.py:486-487).
		return nil
	}

	if reg.ClientID != "" {
		m.creds.ClientID = reg.ClientID
	}
	if reg.ClientSecret != "" {
		m.creds.ClientSecret = reg.ClientSecret
	}
	return nil
}
