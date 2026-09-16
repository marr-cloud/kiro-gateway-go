// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	_ "modernc.org/sqlite"
)

// SQLite token keys (searched in priority order, matching auth.py:54-59)
var sqliteTokenKeys = []string{
	"kirocli:social:token",     // Social login (Google, GitHub, Microsoft, etc.)
	"kirocli:odic:token",       // AWS SSO OIDC (kiro-cli corporate)
	"codewhisperer:odic:token", // Legacy AWS SSO OIDC
}

// SQLite device registration keys (matching auth.py:62-65)
var sqliteRegistrationKeys = []string{
	"kirocli:odic:device-registration",
	"codewhisperer:odic:device-registration",
}

// loadFromSQLite reads credentials from an on-disk SQLite kiro-cli DB.
// Sets m.creds fields and m.sqliteKeyRead to the key it read from.
// Called by NewManagerForAccount for AuthTypeKiroCLI creds, and by
// refreshAWSSSO's 400-invalid_client retry path.
func (m *Manager) loadFromSQLite(dbPath string) error {
	if dbPath == "" {
		return nil
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		// Database open error: non-fatal, just return nil like upstream
		return nil
	}
	defer db.Close()

	// Try all possible token keys in priority order
	var tokenRow *string
	for _, key := range sqliteTokenKeys {
		var value string
		err := db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", key).Scan(&value)
		if err == nil {
			m.sqliteKeyRead = key
			tokenRow = &value
			break
		} else if !errors.Is(err, sql.ErrNoRows) {
			// Query error other than "no rows" - still non-fatal, continue
			continue
		}
	}

	if tokenRow != nil {
		var tokenData map[string]any
		if err := json.Unmarshal([]byte(*tokenRow), &tokenData); err == nil && tokenData != nil {
			// Load token fields (using snake_case as in the Rust struct)
			if v, ok := tokenData["access_token"].(string); ok && v != "" {
				m.creds.AccessToken = v
			}
			if v, ok := tokenData["refresh_token"].(string); ok && v != "" {
				m.creds.RefreshToken = v
			}
			if v, ok := tokenData["profile_arn"].(string); ok && v != "" {
				m.creds.ProfileARN = v
			}
			if v, ok := tokenData["region"].(string); ok && v != "" {
				// Store SSO region for OIDC token refresh
				// Note: API region is determined separately (see resolveAPIRegion for priority logic)
				m.creds.SSORegion = v
			}

			// Load scopes if available
			if v, ok := tokenData["scopes"].([]any); ok {
				// Convert []any to keep as-is for later serialization
				m.creds.Scopes = v
			}

			// Parse expires_at (RFC3339Nano format)
			if v, ok := tokenData["expires_at"].(string); ok && v != "" {
				if t, perr := parseExpiresAt(v); perr == nil {
					m.creds.ExpiresAt = t
				}
				// Malformed expires_at is non-fatal, matching upstream (auth.py:320-321)
			}
		}
	}

	// Load device registration (client_id, client_secret) - try all possible keys
	for _, key := range sqliteRegistrationKeys {
		var value string
		err := db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", key).Scan(&value)
		if err == nil {
			var regData map[string]any
			if err := json.Unmarshal([]byte(value), &regData); err == nil && regData != nil {
				if v, ok := regData["client_id"].(string); ok && v != "" {
					m.creds.ClientID = v
				}
				if v, ok := regData["client_secret"].(string); ok && v != "" {
					m.creds.ClientSecret = v
				}
				// SSO region from registration (fallback if not in token data)
				if v, ok := regData["region"].(string); ok && v != "" && m.creds.SSORegion == "" {
					m.creds.SSORegion = v
				}
			}
			break
		}
	}

	// Try to auto-detect API region from profile ARN in state table
	// This is separate from SSO region because q.amazonaws.com endpoints
	// only exist in specific regions (Issue #132, #133)
	var stateValue string
	err = db.QueryRow("SELECT value FROM state WHERE key = 'api.codewhisperer.profile'").Scan(&stateValue)
	if err == nil {
		var profileData map[string]any
		if err := json.Unmarshal([]byte(stateValue), &profileData); err == nil {
			if arn, ok := profileData["arn"].(string); ok && arn != "" {
				if m.creds.ProfileARN == "" {
					m.creds.ProfileARN = arn
				}
				// ARN format: arn:aws:codewhisperer:REGION:account:profile/id
				// Extract region from 4th component (index 3)
				if r, ok := regionFromARN(arn); ok {
					m.creds.Region = r
				}
			}
		}
	}

	return nil
}

// saveToSQLite writes m.creds back to the same key it was read from.
// No-op if cfg.SQLiteReadonly. Read-merge-write: preserves unknown fields.
func (m *Manager) saveToSQLite() error {
	if m.sqliteDBPath == "" {
		return nil
	}

	// Check read-only mode
	if m.cfg.SQLiteReadOnly {
		return nil
	}

	db, err := sql.Open("sqlite", m.sqliteDBPath)
	if err != nil {
		// Database open error: non-fatal
		return nil
	}
	defer db.Close()

	// Try to save to the known key first (if we have it)
	if m.sqliteKeyRead != "" {
		if ok, err := m.trySaveToKey(db, m.sqliteKeyRead); ok {
			return nil
		} else if err != nil {
			// Log but don't fail on this key, try fallback
		}
	}

	// Fallback: try all keys (for edge cases where source key is unknown or deleted)
	for _, key := range sqliteTokenKeys {
		if ok, _ := m.trySaveToKey(db, key); ok {
			return nil
		}
	}

	// If we get here, no keys were updated - non-fatal
	return nil
}

// trySaveToKey attempts to save credentials to a specific SQLite key using read-merge-write.
// Returns (true, nil) if successful, (false, error) otherwise.
func (m *Manager) trySaveToKey(db *sql.DB, key string) (bool, error) {
	// Read existing data
	var existingValue string
	err := db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", key).Scan(&existingValue)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}

	// Parse existing JSON
	var existingData map[string]any
	if err := json.Unmarshal([]byte(existingValue), &existingData); err != nil {
		// Failed to parse existing JSON - non-fatal, skip this key
		return false, err
	}

	// Merge: update ONLY our fields, preserve EVERYTHING else
	existingData["access_token"] = m.creds.AccessToken
	existingData["refresh_token"] = m.creds.RefreshToken
	if !m.creds.ExpiresAt.IsZero() {
		existingData["expires_at"] = m.creds.ExpiresAt.Format(time.RFC3339Nano)
	} else {
		existingData["expires_at"] = nil
	}
	if m.creds.SSORegion != "" {
		existingData["region"] = m.creds.SSORegion
	} else if m.region != "" {
		existingData["region"] = m.region
	}

	// Update scopes if we have them
	if m.creds.Scopes != nil {
		existingData["scopes"] = m.creds.Scopes
	}

	// Marshal back to JSON
	newValue, err := json.Marshal(existingData)
	if err != nil {
		return false, err
	}

	// Write back merged data
	result, err := db.Exec(
		"UPDATE auth_kv SET value = ? WHERE key = ?",
		string(newValue), key,
	)
	if err != nil {
		return false, err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}

	return rows > 0, nil
}
