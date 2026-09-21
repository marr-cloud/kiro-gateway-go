// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

func TestStateRoundTrip(t *testing.T) {
	tmpDir := t.TempDir()

	// Crear una cuenta simple
	jsonPath := filepath.Join(tmpDir, "creds.json")
	jsonCreds := map[string]interface{}{"refreshToken": "test-token"}
	jsonData, _ := json.Marshal(jsonCreds)
	_ = os.WriteFile(jsonPath, jsonData, 0644)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	creds := []map[string]interface{}{
		{
			"type":    "json",
			"enabled": true,
			"path":    jsonPath,
		},
	}
	credsData, _ := json.Marshal(creds)
	_ = os.WriteFile(credsPath, credsData, 0644)

	stateFile := filepath.Join(tmpDir, "state.json")

	cfg := &config.Config{
		AccountsConfigFile:       credsPath,
		AccountsStateFile:        stateFile,
		StateSaveIntervalSeconds: 1,
	}

	// Crear y cargar first manager
	mgr1, _ := NewManager(cfg)
	_ = mgr1.LoadCredentials(context.Background())
	_ = mgr1.LoadState()

	// Mutar el estado
	if len(mgr1.accounts) > 0 {
		mgr1.accounts[0].Stats.ConsecutiveFailures = 3
		mgr1.accounts[0].Stats.LastFailureMsg = "test failure"
		mgr1.accounts[0].Stats.LastFailure = time.Now()
	}

	// Guardar estado
	_ = mgr1.SaveState()

	// Crear nuevo manager y cargar el estado
	mgr2, _ := NewManager(cfg)
	_ = mgr2.LoadCredentials(context.Background())
	_ = mgr2.LoadState()

	// Verificar que el estado se recuperó
	if len(mgr2.accounts) > 0 {
		acc := mgr2.accounts[0]
		if acc.Stats.ConsecutiveFailures != 3 {
			t.Errorf("Expected ConsecutiveFailures=3, got %d", acc.Stats.ConsecutiveFailures)
		}
		if acc.Stats.LastFailureMsg != "test failure" {
			t.Errorf("Expected LastFailureMsg='test failure', got '%s'", acc.Stats.LastFailureMsg)
		}
	}
}

// TestSaveStatePeriodicallyCancel verifica que SaveStatePeriodically se cancela
// limpiamente y guarda el estado final.
func TestSaveStatePeriodicallyCancel(t *testing.T) {
	tmpDir := t.TempDir()

	// Crear una cuenta simple
	jsonPath := filepath.Join(tmpDir, "creds.json")
	jsonCreds := map[string]interface{}{"refreshToken": "test-token"}
	jsonData, _ := json.Marshal(jsonCreds)
	_ = os.WriteFile(jsonPath, jsonData, 0644)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	creds := []map[string]interface{}{
		{
			"type":    "json",
			"enabled": true,
			"path":    jsonPath,
		},
	}
	credsData, _ := json.Marshal(creds)
	_ = os.WriteFile(credsPath, credsData, 0644)

	stateFile := filepath.Join(tmpDir, "state.json")

	cfg := &config.Config{
		AccountsConfigFile:       credsPath,
		AccountsStateFile:        stateFile,
		StateSaveIntervalSeconds: 1,
	}

	mgr, _ := NewManager(cfg)
	mgr.saveInterval = 20 * time.Millisecond // Intervalo corto para prueba
	_ = mgr.LoadCredentials(context.Background())
	_ = mgr.LoadState()

	// Mutar el estado
	if len(mgr.accounts) > 0 {
		mgr.accounts[0].Stats.ConsecutiveFailures = 5
	}

	// Iniciar SaveStatePeriodically en goroutine
	ctx, cancel := context.WithCancel(context.Background())
	go mgr.SaveStatePeriodically(ctx)

	// Esperar un poco para que se guarde
	time.Sleep(100 * time.Millisecond)

	// Cancelar
	cancel()

	// Esperar a que la goroutine termine
	time.Sleep(100 * time.Millisecond)

	// Verificar que el estado se guardó
	mgr2, _ := NewManager(cfg)
	_ = mgr2.LoadCredentials(context.Background())
	_ = mgr2.LoadState()

	if len(mgr2.accounts) > 0 {
		acc := mgr2.accounts[0]
		if acc.Stats.ConsecutiveFailures != 5 {
			t.Errorf("Expected ConsecutiveFailures=5, got %d", acc.Stats.ConsecutiveFailures)
		}
	}
}

// TestAtomicRenameUnderLoad verifica que el rename es atómico bajo carga.
func TestAtomicRenameUnderLoad(t *testing.T) {
	tmpDir := t.TempDir()

	// Crear una cuenta simple
	jsonPath := filepath.Join(tmpDir, "creds.json")
	jsonCreds := map[string]interface{}{"refreshToken": "test-token"}
	jsonData, _ := json.Marshal(jsonCreds)
	_ = os.WriteFile(jsonPath, jsonData, 0644)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	creds := []map[string]interface{}{
		{
			"type":    "json",
			"enabled": true,
			"path":    jsonPath,
		},
	}
	credsData, _ := json.Marshal(creds)
	_ = os.WriteFile(credsPath, credsData, 0644)

	stateFile := filepath.Join(tmpDir, "state.json")

	cfg := &config.Config{
		AccountsConfigFile:       credsPath,
		AccountsStateFile:        stateFile,
		StateSaveIntervalSeconds: 1,
	}

	mgr, _ := NewManager(cfg)
	_ = mgr.LoadCredentials(context.Background())

	// Hacer 100 saves en bucle apretado
	for i := 0; i < 100; i++ {
		_ = mgr.SaveState()
		// Intentar leer el archivo entre saves
		if _, err := os.Stat(stateFile); err == nil {
			data, err := os.ReadFile(stateFile)
			if err == nil && len(data) > 0 {
				// Intentar parsear como JSON para verificar que es válido
				var stateData map[string]interface{}
				if err := json.Unmarshal(data, &stateData); err != nil {
					t.Errorf("Partial JSON detected at iteration %d: %v", i, err)
				}
			}
		}
	}
}

// TestRenameRetry verifica que el rename se reintenta 3 veces con backoff.
func TestRenameRetry(t *testing.T) {
	tmpDir := t.TempDir()

	// Crear una cuenta simple
	jsonPath := filepath.Join(tmpDir, "creds.json")
	jsonCreds := map[string]interface{}{"refreshToken": "test-token"}
	jsonData, _ := json.Marshal(jsonCreds)
	_ = os.WriteFile(jsonPath, jsonData, 0644)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	creds := []map[string]interface{}{
		{
			"type":    "json",
			"enabled": true,
			"path":    jsonPath,
		},
	}
	credsData, _ := json.Marshal(creds)
	_ = os.WriteFile(credsPath, credsData, 0644)

	stateFile := filepath.Join(tmpDir, "state.json")

	cfg := &config.Config{
		AccountsConfigFile:       credsPath,
		AccountsStateFile:        stateFile,
		StateSaveIntervalSeconds: 1,
	}

	mgr, _ := NewManager(cfg)
	_ = mgr.LoadCredentials(context.Background())

	// Inyectar una función de rename que falla 2 veces y luego tiene éxito
	attemptCount := 0
	originalRename := os.Rename
	mgr.renameFn = func(old, new string) error {
		attemptCount++
		if attemptCount < 3 {
			return fmt.Errorf("simulated error")
		}
		return originalRename(old, new)
	}

	// Hacer un save
	err := mgr.SaveState()
	if err != nil {
		t.Errorf("SaveState failed: %v", err)
	}

	// Verificar que se intentó 3 veces
	if attemptCount != 3 {
		t.Errorf("Expected 3 rename attempts, got %d", attemptCount)
	}

	// Verificar que el archivo existe
	if _, err := os.Stat(stateFile); err != nil {
		t.Errorf("State file not created: %v", err)
	}
}

// TestSaveStatePeriodically_SkipsSavesWhenClean verifica que SaveStatePeriodically
// solo guarda cuando el estado cambió (dirty-state optimization).
func TestSaveStatePeriodically_SkipsSavesWhenClean(t *testing.T) {
	tmpDir := t.TempDir()

	// Crear una cuenta simple
	jsonPath := filepath.Join(tmpDir, "creds.json")
	jsonCreds := map[string]interface{}{"refreshToken": "test-token"}
	jsonData, _ := json.Marshal(jsonCreds)
	_ = os.WriteFile(jsonPath, jsonData, 0644)

	credsPath := filepath.Join(tmpDir, "credentials.json")
	creds := []map[string]interface{}{
		{
			"type":    "json",
			"enabled": true,
			"path":    jsonPath,
		},
	}
	credsData, _ := json.Marshal(creds)
	_ = os.WriteFile(credsPath, credsData, 0644)

	stateFile := filepath.Join(tmpDir, "state.json")

	cfg := &config.Config{
		AccountsConfigFile:       credsPath,
		AccountsStateFile:        stateFile,
		StateSaveIntervalSeconds: 1,
	}

	mgr, _ := NewManager(cfg)
	mgr.saveInterval = 20 * time.Millisecond // Intervalo corto para prueba
	_ = mgr.LoadCredentials(context.Background())
	_ = mgr.LoadState()

	// Inyectar un contador de rename para detectar saves
	renameCount := 0
	originalRename := os.Rename
	mgr.renameFn = func(old, new string) error {
		renameCount++
		return originalRename(old, new)
	}

	// Iniciar SaveStatePeriodically en goroutine
	ctx, cancel := context.WithCancel(context.Background())
	go mgr.SaveStatePeriodically(ctx)

	// Dejar que corra por ~100ms (5 ticks)
	time.Sleep(100 * time.Millisecond)

	// Cancelar
	cancel()

	// Esperar a que la goroutine termine
	time.Sleep(50 * time.Millisecond)

	// Con dirty-state detection funcionando correctamente:
	// - El primer tick verá estado limpio (no mutamos nada) → no guarda
	// - Todos los ticks subsecuentes lo mismo → no guardan
	// - El cancel causa un SaveState() final, pero el estado sigue limpio → no guarda
	// Total: 0 saves
	// Si dirty-state está ROTO (como era antes del fix):
	// - Cada tick vería "dirty" → guardaría 5+ veces
	if renameCount != 0 {
		t.Errorf("Expected 0 renames for clean state, got %d", renameCount)
	}
}

// Helper function para crear una SQLite DB con tabla auth_kv
func setupSQLiteDB(t *testing.T, dbPath string) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to create SQLite DB: %v", err)
	}
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS auth_kv (
			key TEXT PRIMARY KEY,
			value TEXT
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create auth_kv table: %v", err)
	}

	// Insertar un token de prueba
	tokenData := map[string]string{
		"access_token":  "test-access-token",
		"refresh_token": "test-refresh-token",
		"profile_arn":   "arn:aws:iam::123456789:role/test",
	}
	tokenJSON, _ := json.Marshal(tokenData)
	_, err = db.Exec("INSERT INTO auth_kv (key, value) VALUES (?, ?)",
		"kirocli:social:token", string(tokenJSON))
	if err != nil {
		t.Fatalf("Failed to insert token: %v", err)
	}
}
