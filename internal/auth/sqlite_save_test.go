// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package auth

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// TestLoadFromSQLite_ReadMergeWritePreservesUnknown verifies unknown fields are preserved
func TestLoadFromSQLite_ReadMergeWritePreservesUnknown(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	// Insert token with unknown fields
	insertTokenRow(t, db, "kirocli:odic:token", map[string]any{
		"access_token":          "access",
		"refresh_token":         "refresh",
		"expires_at":            "2099-01-01T00:00:00Z",
		"extraField":            "keep-me",
		"registrationExpiresAt": "2100-01-01T00:00:00Z",
	})

	cfg := &config.Config{SQLiteReadOnly: false}
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadFromSQLite failed: %v", err)
	}

	// Update token
	m.creds.AccessToken = "new-access"

	// Save back
	err = m.saveToSQLite()
	if err != nil {
		t.Fatalf("saveToSQLite failed: %v", err)
	}

	// Read back and verify unknown fields are preserved
	var result string
	err = db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", "kirocli:odic:token").Scan(&result)
	if err != nil {
		t.Fatalf("failed to read back token: %v", err)
	}

	var data map[string]any
	err = json.Unmarshal([]byte(result), &data)
	if err != nil {
		t.Fatalf("failed to unmarshal saved data: %v", err)
	}

	if extra, ok := data["extraField"]; !ok || extra != "keep-me" {
		t.Errorf("extraField not preserved: got %v", data["extraField"])
	}
	if regExpires, ok := data["registrationExpiresAt"]; !ok || regExpires != "2100-01-01T00:00:00Z" {
		t.Errorf("registrationExpiresAt not preserved: got %v", data["registrationExpiresAt"])
	}
}

// TestLoadFromSQLite_SaveToSameKey verifies save goes to the correct key
func TestLoadFromSQLite_SaveToSameKey(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	// Only insert codewhisperer token (lowest priority)
	insertTokenRow(t, db, "codewhisperer:odic:token", map[string]any{
		"access_token":  "cw-access",
		"refresh_token": "cw-refresh",
		"expires_at":    "2099-01-01T00:00:00Z",
	})

	cfg := &config.Config{SQLiteReadOnly: false, KiroCLIDBFile: dbPath}
	m, _ := NewManagerForAccount(cfg, "")

	// Verify it loaded from the correct key
	if m.sqliteKeyRead != "codewhisperer:odic:token" {
		t.Fatalf("expected to load from codewhisperer:odic:token, got %q", m.sqliteKeyRead)
	}

	// Modify and save
	m.creds.AccessToken = "new-access"
	err := m.saveToSQLite()
	if err != nil {
		t.Fatalf("saveToSQLite failed: %v", err)
	}

	// Verify the update went to codewhisperer:odic:token, not a higher-priority key
	var rows int
	err = db.QueryRow("SELECT COUNT(*) FROM auth_kv WHERE key = ? AND value LIKE ?",
		"codewhisperer:odic:token", "%new-access%").Scan(&rows)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}
	if rows != 1 {
		t.Errorf("expected 1 row with updated token, got %d", rows)
	}

	// Verify higher-priority keys were NOT updated
	var kiroRows int
	err = db.QueryRow("SELECT COUNT(*) FROM auth_kv WHERE key = ?", "kirocli:social:token").Scan(&kiroRows)
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("failed to query kiro key: %v", err)
	}
	if kiroRows > 0 {
		t.Errorf("kirocli:social:token should not exist, but found %d rows", kiroRows)
	}
}

// TestLoadFromSQLite_SQLiteReadOnlyNoOp verifies save is no-op when SQLITE_READONLY is true
func TestLoadFromSQLite_SQLiteReadOnlyNoOp(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	insertTokenRow(t, db, "kirocli:odic:token", map[string]any{
		"access_token":  "original-access",
		"refresh_token": "refresh",
		"expires_at":    "2099-01-01T00:00:00Z",
	})

	cfg := &config.Config{SQLiteReadOnly: true} // Set read-only mode
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadFromSQLite failed: %v", err)
	}

	// Modify token
	m.creds.AccessToken = "modified-access"

	// Try to save
	err = m.saveToSQLite()
	if err != nil {
		t.Fatalf("saveToSQLite failed: %v", err)
	}

	// Verify the original value is still in the database (save was no-op)
	var result string
	err = db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", "kirocli:odic:token").Scan(&result)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}

	var data map[string]any
	err = json.Unmarshal([]byte(result), &data)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if data["access_token"] != "original-access" {
		t.Errorf("access_token was modified despite SQLITE_READONLY: got %v", data["access_token"])
	}
}

// TestLoadFromSQLite_ExpiresAtRFC3339Nano verifies RFC3339Nano parsing
func TestLoadFromSQLite_ExpiresAtRFC3339Nano(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	// 9-digit fractional seconds
	expires := "2099-12-31T23:59:59.999999999Z"
	insertTokenRow(t, db, "kirocli:odic:token", map[string]any{
		"access_token":  "access",
		"refresh_token": "refresh",
		"expires_at":    expires,
	})

	cfg := &config.Config{}
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadFromSQLite failed: %v", err)
	}

	if m.creds.ExpiresAt.IsZero() {
		t.Errorf("ExpiresAt was not parsed")
	}
	// Verify it's in the year 2099
	if m.creds.ExpiresAt.Year() != 2099 {
		t.Errorf("ExpiresAt year: got %d, want 2099", m.creds.ExpiresAt.Year())
	}
}

// TestLoadFromSQLite_MissingDeviceRegistration is non-fatal
func TestLoadFromSQLite_MissingDeviceRegistration(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	insertTokenRow(t, db, "kirocli:odic:token", map[string]any{
		"access_token":  "access",
		"refresh_token": "refresh",
		"expires_at":    "2099-01-01T00:00:00Z",
	})
	// No device registration row

	cfg := &config.Config{}
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite(dbPath)
	if err != nil {
		t.Fatalf("loadFromSQLite should not fail when device registration is missing: %v", err)
	}

	// ClientID and ClientSecret should be empty (non-fatal)
	if m.creds.ClientID != "" {
		t.Errorf("ClientID should be empty when device registration is missing, got %q", m.creds.ClientID)
	}
}

// TestLoadFromSQLite_NonexistentDatabase doesn't fail
func TestLoadFromSQLite_NonexistentDatabase(t *testing.T) {
	cfg := &config.Config{}
	m, _ := NewManagerForAccount(cfg, "")

	err := m.loadFromSQLite("/nonexistent/path/to/db.sqlite")
	if err != nil {
		t.Fatalf("loadFromSQLite should not fail on nonexistent database: %v", err)
	}
}

// TestSaveToSQLite_UpdatesCorrectly verifies basic save functionality
func TestSaveToSQLite_UpdatesCorrectly(t *testing.T) {
	db, dbPath := createTestDB(t)
	defer db.Close()

	insertTokenRow(t, db, "kirocli:odic:token", map[string]any{
		"access_token":  "old-access",
		"refresh_token": "old-refresh",
		"expires_at":    "2099-01-01T00:00:00Z",
	})

	cfg := &config.Config{SQLiteReadOnly: false, KiroCLIDBFile: dbPath}
	m, _ := NewManagerForAccount(cfg, "")

	// Update
	m.creds.AccessToken = "new-access"
	expiresAt := time.Date(2099, 6, 15, 12, 30, 45, 0, time.UTC)
	m.creds.ExpiresAt = expiresAt
	m.tokens.AccessToken = "new-access"
	m.tokens.ExpiresAt = expiresAt

	err := m.saveToSQLite()
	if err != nil {
		t.Fatalf("saveToSQLite failed: %v", err)
	}

	// Verify
	var result string
	err = db.QueryRow("SELECT value FROM auth_kv WHERE key = ?", "kirocli:odic:token").Scan(&result)
	if err != nil {
		t.Fatalf("failed to query: %v", err)
	}

	var data map[string]any
	err = json.Unmarshal([]byte(result), &data)
	if err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	if data["access_token"] != "new-access" {
		t.Errorf("access_token: got %v, want new-access", data["access_token"])
	}
}
