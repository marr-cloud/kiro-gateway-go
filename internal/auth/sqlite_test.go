// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// createTestDB creates a temporary SQLite database with auth_kv and state tables.
func createTestDB(t *testing.T) (*sql.DB, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("failed to open SQLite: %v", err)
	}

	// Create auth_kv table
	if _, err := db.Exec(`CREATE TABLE auth_kv (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("failed to create auth_kv table: %v", err)
	}

	// Create state table
	if _, err := db.Exec(`CREATE TABLE state (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
		t.Fatalf("failed to create state table: %v", err)
	}

	return db, dbPath
}

// insertTokenRow inserts a token key-value pair into the database.
func insertTokenRow(t *testing.T, db *sql.DB, key string, tokenData map[string]any) {
	t.Helper()
	data, err := json.Marshal(tokenData)
	if err != nil {
		t.Fatalf("failed to marshal token data: %v", err)
	}
	_, err = db.Exec("INSERT INTO auth_kv (key, value) VALUES (?, ?)", key, string(data))
	if err != nil {
		t.Fatalf("failed to insert token row: %v", err)
	}
}

// insertDeviceRegistration inserts a device registration key-value pair.
func insertDeviceRegistration(t *testing.T, db *sql.DB, key string, regData map[string]any) {
	t.Helper()
	data, err := json.Marshal(regData)
	if err != nil {
		t.Fatalf("failed to marshal registration data: %v", err)
	}
	_, err = db.Exec("INSERT INTO auth_kv (key, value) VALUES (?, ?)", key, string(data))
	if err != nil {
		t.Fatalf("failed to insert registration row: %v", err)
	}
}

// insertStateRow inserts a state table row.
func insertStateRow(t *testing.T, db *sql.DB, key string, value map[string]any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("failed to marshal state data: %v", err)
	}
	_, err = db.Exec("INSERT INTO state (key, value) VALUES (?, ?)", key, string(data))
	if err != nil {
		t.Fatalf("failed to insert state row: %v", err)
	}
}

// TestLoadFromSQLite_KiroSocialToken loads from kirocli:social:token
func TestLoadFromSQLite_KiroSocialToken(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	insertTokenRow(t, db, "kirocli:social:token", map[string]any{
		"access_token":  "social-access",
		"refresh_token": "social-refresh",
		"profile_arn":   "arn:aws:codewhisperer:us-east-1:123456789012:profile/xyz",
		"region":        "us-east-1",
		"expires_at":    "2099-01-01T00:00:00Z",
	})

	cfg := &config.Config{}
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadFromSQLite failed: %v", err)
	}

	if m.creds.AccessToken != "social-access" {
		t.Errorf("AccessToken: got %q, want social-access", m.creds.AccessToken)
	}
	if m.sqliteKeyRead != "kirocli:social:token" {
		t.Errorf("sqliteKeyRead: got %q, want kirocli:social:token", m.sqliteKeyRead)
	}
}

// TestLoadFromSQLite_KiroODICToken loads from kirocli:odic:token
func TestLoadFromSQLite_KiroODICToken(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	insertTokenRow(t, db, "kirocli:odic:token", map[string]any{
		"access_token":  "odic-access",
		"refresh_token": "odic-refresh",
		"region":        "us-west-2",
		"expires_at":    "2099-01-01T00:00:00Z",
	})

	cfg := &config.Config{}
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadFromSQLite failed: %v", err)
	}

	if m.creds.AccessToken != "odic-access" {
		t.Errorf("AccessToken: got %q, want odic-access", m.creds.AccessToken)
	}
	if m.sqliteKeyRead != "kirocli:odic:token" {
		t.Errorf("sqliteKeyRead: got %q, want kirocli:odic:token", m.sqliteKeyRead)
	}
}

// TestLoadFromSQLite_CodewhispererToken loads from codewhisperer:odic:token
func TestLoadFromSQLite_CodewhispererToken(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	insertTokenRow(t, db, "codewhisperer:odic:token", map[string]any{
		"access_token":  "cw-access",
		"refresh_token": "cw-refresh",
		"expires_at":    "2099-01-01T00:00:00Z",
	})

	cfg := &config.Config{}
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadFromSQLite failed: %v", err)
	}

	if m.creds.AccessToken != "cw-access" {
		t.Errorf("AccessToken: got %q, want cw-access", m.creds.AccessToken)
	}
}

// TestLoadFromSQLite_PriorityOrder verifies that the first matching key wins
func TestLoadFromSQLite_PriorityOrder(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	// Insert all three keys with different access tokens
	insertTokenRow(t, db, "kirocli:social:token", map[string]any{
		"access_token":  "social-access",
		"refresh_token": "social-refresh",
		"expires_at":    "2099-01-01T00:00:00Z",
	})
	insertTokenRow(t, db, "kirocli:odic:token", map[string]any{
		"access_token":  "odic-access",
		"refresh_token": "odic-refresh",
		"expires_at":    "2099-01-01T00:00:00Z",
	})
	insertTokenRow(t, db, "codewhisperer:odic:token", map[string]any{
		"access_token":  "cw-access",
		"refresh_token": "cw-refresh",
		"expires_at":    "2099-01-01T00:00:00Z",
	})

	cfg := &config.Config{}
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadFromSQLite failed: %v", err)
	}

	// kirocli:social:token should win (first in priority order)
	if m.creds.AccessToken != "social-access" {
		t.Errorf("AccessToken: got %q, want social-access", m.creds.AccessToken)
	}
	if m.sqliteKeyRead != "kirocli:social:token" {
		t.Errorf("sqliteKeyRead: got %q, want kirocli:social:token", m.sqliteKeyRead)
	}
}

// TestLoadFromSQLite_RegionDetectionFromState verifies region extraction from state table
func TestLoadFromSQLite_RegionDetectionFromState(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	insertTokenRow(t, db, "kirocli:odic:token", map[string]any{
		"access_token":  "access",
		"refresh_token": "refresh",
		"region":        "us-west-2", // SSO region
		"expires_at":    "2099-01-01T00:00:00Z",
	})

	// Insert profile ARN in state table
	insertStateRow(t, db, "api.codewhisperer.profile", map[string]any{
		"arn": "arn:aws:codewhisperer:eu-central-1:123456789012:profile/abc",
	})

	cfg := &config.Config{}
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadFromSQLite failed: %v", err)
	}

	// Region should be extracted from ARN
	if m.creds.Region != "eu-central-1" {
		t.Errorf("Region: got %q, want eu-central-1", m.creds.Region)
	}
	// SSORegion should be from token
	if m.creds.SSORegion != "us-west-2" {
		t.Errorf("SSORegion: got %q, want us-west-2", m.creds.SSORegion)
	}
}

// TestLoadFromSQLite_DeviceRegistration verifies device registration loading
func TestLoadFromSQLite_DeviceRegistration(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	insertTokenRow(t, db, "kirocli:odic:token", map[string]any{
		"access_token":  "access",
		"refresh_token": "refresh",
		"expires_at":    "2099-01-01T00:00:00Z",
	})

	insertDeviceRegistration(t, db, "kirocli:odic:device-registration", map[string]any{
		"client_id":     "test-client-id",
		"client_secret": "test-client-secret",
	})

	cfg := &config.Config{}
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadFromSQLite failed: %v", err)
	}

	if m.creds.ClientID != "test-client-id" {
		t.Errorf("ClientID: got %q, want test-client-id", m.creds.ClientID)
	}
	if m.creds.ClientSecret != "test-client-secret" {
		t.Errorf("ClientSecret: got %q, want test-client-secret", m.creds.ClientSecret)
	}
}
