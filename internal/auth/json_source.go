// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// jsonFileSource es la fuente de credenciales de Kiro Desktop: un fichero
// JSON con refreshToken/accessToken/profileArn/region/expiresAt (y,
// opcionalmente, clientId/clientSecret para AWS SSO OIDC). Port de
// _load_credentials_from_file / _save_credentials_to_file
// (.upstream/kiro/auth.py:384-457, :489-522).
type jsonFileSource struct {
	path string
}

func newJSONFileSource(path string) *jsonFileSource {
	return &jsonFileSource{path: path}
}

// Load lee el fichero y devuelve las Credentials que contiene. Un fichero
// ausente no es un error: el original registra un warning y sigue
// (auth.py:409-411) en vez de abortar, así que aquí se devuelve
// Credentials{} y nil. Un JSON corrupto sí se propaga como error — a
// diferencia del original, que lo traga con un `except Exception` genérico
// (auth.py:455-456); ver el ruling correspondiente en el informe de la
// Task 2: es una desviación deliberada, justificada por que
// `NewManager`/`NewManagerForAccount` ya declaran que pueden fallar
// (firma `(*Manager, error)`), así que un fichero de credenciales
// verdaderamente corrupto (no simplemente ausente) es mejor que aborte la
// construcción con un error claro en vez de arrancar en silencio con un
// Manager sin credenciales utilizables.
func (s *jsonFileSource) Load() (Credentials, error) {
	var creds Credentials

	data, err := os.ReadFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return creds, nil
		}
		return creds, fmt.Errorf("auth: reading %s: %w", s.path, err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return creds, fmt.Errorf("auth: parsing %s: %w", s.path, err)
	}

	if v, ok := raw["refreshToken"].(string); ok {
		creds.RefreshToken = v
	}
	if v, ok := raw["accessToken"].(string); ok {
		creds.AccessToken = v
	}
	if v, ok := raw["profileArn"].(string); ok {
		creds.ProfileARN = v
	}
	if v, ok := raw["region"].(string); ok {
		creds.Region = v
	}
	if v, ok := raw["clientId"].(string); ok {
		creds.ClientID = v
	}
	if v, ok := raw["clientSecret"].(string); ok {
		creds.ClientSecret = v
	}
	if v, ok := raw["expiresAt"].(string); ok && v != "" {
		if t, perr := parseExpiresAt(v); perr == nil {
			creds.ExpiresAt = t
		}
		// Un expiresAt malformado se traga igual que el warning del
		// original (auth.py:450-451): ExpiresAt se queda en su cero,
		// que Tokens.IsExpiringSoon/IsExpired ya interpretan como "hay
		// que refrescar".
	}

	return creds, nil
}

// Save persiste las Credentials refrescadas de vuelta al fichero,
// preservando cualquier campo desconocido que ya hubiera —
// read-merge-write, igual que _save_credentials_to_file
// (auth.py:489-522). Solo se escriben accessToken/refreshToken siempre
// (igual que el original, que los asigna sin condición) y
// expiresAt/profileArn solo si vienen poblados (auth.py:510-513).
func (s *jsonFileSource) Save(c Credentials) error {
	existing := map[string]any{}

	data, err := os.ReadFile(s.path)
	switch {
	case err == nil:
		if len(data) > 0 {
			if uerr := json.Unmarshal(data, &existing); uerr != nil {
				return fmt.Errorf("auth: existing credentials file %s is not valid JSON, aborting write-through: %w", s.path, uerr)
			}
		}
	case errors.Is(err, os.ErrNotExist):
		// Sin fichero previo: existing_data = {} tal cual el original
		// (auth.py:502-503).
	default:
		return fmt.Errorf("auth: reading %s for write-through: %w", s.path, err)
	}

	existing["accessToken"] = c.AccessToken
	existing["refreshToken"] = c.RefreshToken
	if !c.ExpiresAt.IsZero() {
		existing["expiresAt"] = c.ExpiresAt.Format(time.RFC3339Nano)
	}
	if c.ProfileARN != "" {
		existing["profileArn"] = c.ProfileARN
	}

	out, merr := json.MarshalIndent(existing, "", "  ")
	if merr != nil {
		return fmt.Errorf("auth: encoding credentials for %s: %w", s.path, merr)
	}
	if werr := os.WriteFile(s.path, out, 0o600); werr != nil {
		return fmt.Errorf("auth: writing %s: %w", s.path, werr)
	}
	return nil
}

// parseExpiresAt parsea expiresAt en RFC3339, con o sin fracción de
// segundos, terminado en 'Z' o en un offset numérico — el equivalente de
// datetime.fromisoformat(expires_str.replace('Z', '+00:00')) del original
// (auth.py:443-449). A diferencia de Python 3.10 (limitado a 6 dígitos de
// fracción, ver el comentario de auth.py:316-318 sobre la SQLite), el
// layout time.RFC3339Nano de Go acepta nativamente 'Z' y entre 0 y 9
// dígitos de fracción sin necesitar el reemplazo manual ni el truncado por
// regex que hace el original: no hace falta replicar esa limitación aquí.
func parseExpiresAt(s string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, s)
}
