// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"database/sql"

	_ "modernc.org/sqlite"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// TestDiscoveryThreeTypes verifica que el descubrimiento funciona con JSON, SQLite
// y refresh_token en la misma credentials.json.
func TestDiscoveryThreeTypes(t *testing.T) {
	tmpDir := t.TempDir()

	// Crear un archivo JSON válido (con refreshToken)
	jsonPath := filepath.Join(tmpDir, "creds.json")
	jsonCreds := map[string]interface{}{
		"refreshToken": "test-refresh-token-12345",
		"region":       "us-east-1",
	}
	jsonData, _ := json.Marshal(jsonCreds)
	_ = os.WriteFile(jsonPath, jsonData, 0644)

	// Crear un archivo SQLite válido (con tabla auth_kv)
	sqlitePath := filepath.Join(tmpDir, "kiro-cli.db")
	setupSQLiteDB(t, sqlitePath)

	// Crear credentials.json con los tres tipos
	credsPath := filepath.Join(tmpDir, "credentials.json")
	creds := []map[string]interface{}{
		{
			"type":        "json",
			"enabled":     true,
			"path":        jsonPath,
			"profile_arn": "arn:aws:iam::123456789:role/kiro",
			"region":      "us-west-2",
			"api_region":  "eu-west-1",
		},
		{
			"type":    "sqlite",
			"enabled": true,
			"path":    sqlitePath,
			"region":  "us-east-1",
		},
		{
			"type":          "refresh_token",
			"enabled":       true,
			"refresh_token": "test-refresh-token-xyz",
			"profile_arn":   "arn:aws:iam::987654321:role/other",
		},
	}
	credsData, _ := json.Marshal(creds)
	_ = os.WriteFile(credsPath, credsData, 0644)

	// Crear config
	cfg := &config.Config{
		AccountsConfigFile:       credsPath,
		AccountsStateFile:        filepath.Join(tmpDir, "state.json"),
		StateSaveIntervalSeconds: 1,
		AccountCacheTTL:          3600,
	}

	// Crear y cargar manager
	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	err = mgr.LoadCredentials(context.Background())
	if err != nil {
		t.Fatalf("LoadCredentials failed: %v", err)
	}

	// Verificar que hay exactamente 3 cuentas
	if len(mgr.accounts) != 3 {
		t.Errorf("Expected 3 accounts, got %d", len(mgr.accounts))
	}

	// Verificar tipos y IDs
	foundJSON := false
	foundSQLite := false
	foundRefreshToken := false

	for _, acc := range mgr.accounts {
		switch acc.Type {
		case "json":
			if acc.ID != jsonPath {
				t.Errorf("JSON account ID mismatch: expected %s, got %s", jsonPath, acc.ID)
			}
			if acc.Auth == nil {
				t.Errorf("JSON account Auth is nil")
			}
			if acc.APIRegion != "eu-west-1" {
				t.Errorf("JSON account APIRegion: expected eu-west-1, got %s", acc.APIRegion)
			}
			foundJSON = true
		case "sqlite":
			if acc.ID != sqlitePath {
				t.Errorf("SQLite account ID mismatch: expected %s, got %s", sqlitePath, acc.ID)
			}
			if acc.Auth == nil {
				t.Errorf("SQLite account Auth is nil")
			}
			foundSQLite = true
		case "refresh_token":
			// Verify refresh_token ID format
			hash := sha256.Sum256([]byte("test-refresh-token-xyz"))
			hashStr := hex.EncodeToString(hash[:])[:16]
			expectedID := "refresh_token_" + hashStr
			if acc.ID != expectedID {
				t.Errorf("Refresh_token ID mismatch: expected %s, got %s", expectedID, acc.ID)
			}
			if acc.Auth == nil {
				t.Errorf("Refresh_token account Auth is nil")
			}
			if acc.Path != "" {
				t.Errorf("Refresh_token account Path should be empty, got %s", acc.Path)
			}
			foundRefreshToken = true
		}
	}

	if !foundJSON {
		t.Error("JSON account not found")
	}
	if !foundSQLite {
		t.Error("SQLite account not found")
	}
	if !foundRefreshToken {
		t.Error("Refresh_token account not found")
	}
}

// TestDiscoveryDirectoryNonRecursive verifica que el escaneo de directorios
// no es recursivo.
func TestDiscoveryDirectoryNonRecursive(t *testing.T) {
	tmpDir := t.TempDir()

	// Crear estructura de directorios
	mainDir := filepath.Join(tmpDir, "creds_dir")
	_ = os.MkdirAll(mainDir, 0755)
	subDir := filepath.Join(mainDir, "subdir")
	_ = os.MkdirAll(subDir, 0755)

	// Crear JSON en el directorio principal
	mainJSON := filepath.Join(mainDir, "main.json")
	mainCreds := map[string]interface{}{"refreshToken": "token1"}
	mainData, _ := json.Marshal(mainCreds)
	_ = os.WriteFile(mainJSON, mainData, 0644)

	// Crear JSON en el subdirectorio (NO debe ser detectado)
	subJSON := filepath.Join(subDir, "sub.json")
	subCreds := map[string]interface{}{"refreshToken": "token2"}
	subData, _ := json.Marshal(subCreds)
	_ = os.WriteFile(subJSON, subData, 0644)

	// Crear credentials.json que apunta al directorio principal
	credsPath := filepath.Join(tmpDir, "credentials.json")
	creds := []map[string]interface{}{
		{
			"type":    "json",
			"enabled": true,
			"path":    mainDir,
		},
	}
	credsData, _ := json.Marshal(creds)
	_ = os.WriteFile(credsPath, credsData, 0644)

	cfg := &config.Config{
		AccountsConfigFile: credsPath,
		AccountsStateFile:  filepath.Join(tmpDir, "state.json"),
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	err = mgr.LoadCredentials(context.Background())
	if err != nil {
		t.Fatalf("LoadCredentials failed: %v", err)
	}

	// Debe haber exactamente 1 cuenta (solo la del directorio principal)
	if len(mgr.accounts) != 1 {
		t.Errorf("Expected 1 account, got %d", len(mgr.accounts))
	}

	if len(mgr.accounts) > 0 {
		acc := mgr.accounts[0]
		if acc.ID != mainJSON {
			t.Errorf("Expected account ID %s, got %s", mainJSON, acc.ID)
		}
	}
}

// TestJSONValidityCheck verifica que solo JSON con refreshToken o clientId son válidos.
func TestJSONValidityCheck(t *testing.T) {
	tmpDir := t.TempDir()

	// JSON válido con refreshToken
	validJSON1 := filepath.Join(tmpDir, "valid1.json")
	_ = os.WriteFile(validJSON1, []byte(`{"refreshToken":"token1"}`), 0644)

	// JSON válido con clientId
	validJSON2 := filepath.Join(tmpDir, "valid2.json")
	_ = os.WriteFile(validJSON2, []byte(`{"clientId":"id1"}`), 0644)

	// JSON inválido (sin refreshToken ni clientId)
	invalidJSON := filepath.Join(tmpDir, "invalid.json")
	_ = os.WriteFile(invalidJSON, []byte(`{"region":"us-east-1"}`), 0644)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	creds := []map[string]interface{}{
		{
			"type":    "json",
			"enabled": true,
			"path":    tmpDir,
		},
	}
	credsData, _ := json.Marshal(creds)
	_ = os.WriteFile(credsPath, credsData, 0644)

	cfg := &config.Config{
		AccountsConfigFile: credsPath,
		AccountsStateFile:  filepath.Join(tmpDir, "state.json"),
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	err = mgr.LoadCredentials(context.Background())
	if err != nil {
		t.Fatalf("LoadCredentials failed: %v", err)
	}

	// Debe haber exactamente 2 cuentas (los dos JSON válidos)
	if len(mgr.accounts) != 2 {
		t.Errorf("Expected 2 valid JSON accounts, got %d", len(mgr.accounts))
	}

	// Verificar que los archivos válidos están presentes
	foundValid1 := false
	foundValid2 := false
	for _, acc := range mgr.accounts {
		if acc.ID == validJSON1 {
			foundValid1 = true
		}
		if acc.ID == validJSON2 {
			foundValid2 = true
		}
	}

	if !foundValid1 {
		t.Error("validJSON1 not found in accounts")
	}
	if !foundValid2 {
		t.Error("validJSON2 not found in accounts")
	}
}

// TestSQLiteValidityCheck verifica que solo SQLite con tabla auth_kv son válidos.
func TestSQLiteValidityCheck(t *testing.T) {
	tmpDir := t.TempDir()

	// SQLite válido (con tabla auth_kv)
	validDB := filepath.Join(tmpDir, "valid.db")
	setupSQLiteDB(t, validDB)

	// SQLite inválido (sin tabla auth_kv)
	invalidDB := filepath.Join(tmpDir, "invalid.db")
	db, err := sql.Open("sqlite", invalidDB)
	if err != nil {
		t.Fatalf("Failed to create invalid SQLite DB: %v", err)
	}
	db.Close()

	credsPath := filepath.Join(tmpDir, "credentials.json")
	creds := []map[string]interface{}{
		{
			"type":    "sqlite",
			"enabled": true,
			"path":    tmpDir,
		},
	}
	credsData, _ := json.Marshal(creds)
	_ = os.WriteFile(credsPath, credsData, 0644)

	cfg := &config.Config{
		AccountsConfigFile: credsPath,
		AccountsStateFile:  filepath.Join(tmpDir, "state.json"),
	}

	mgr, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager failed: %v", err)
	}

	err = mgr.LoadCredentials(context.Background())
	if err != nil {
		t.Fatalf("LoadCredentials failed: %v", err)
	}

	// Debe haber exactamente 1 cuenta (solo el DB válido)
	if len(mgr.accounts) != 1 {
		t.Errorf("Expected 1 valid SQLite account, got %d", len(mgr.accounts))
	}

	if len(mgr.accounts) > 0 {
		acc := mgr.accounts[0]
		if acc.ID != validDB {
			t.Errorf("Expected account ID %s, got %s", validDB, acc.ID)
		}
	}
}

// TestStateRoundTrip verifica que el estado se guarda y recupera correctamente.
