// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"github.com/marr-cloud/kiro-gateway-go/internal/auth"
)

// credentialEntry representa una entrada en credentials.json.
type credentialEntry struct {
	Type         string `json:"type"`
	Enabled      bool   `json:"enabled"`
	Path         string `json:"path"`
	ProfileARN   string `json:"profile_arn"`
	Region       string `json:"region"`
	APIRegion    string `json:"api_region"`
	RefreshToken string `json:"refresh_token"`
}

// loadCredentials lee credentials.json y descubre todas las cuentas válidas.
func (m *Manager) loadCredentials(ctx context.Context) error {
	credsFilePath := m.cfg.AccountsConfigFile
	if credsFilePath == "" {
		// Sin archivo de credenciales configurado
		return nil
	}

	// Leer credentials.json
	data, err := os.ReadFile(credsFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Archivo no existe — no es un error fatal
			return nil
		}
		return fmt.Errorf("failed to read credentials file: %w", err)
	}

	var entries []credentialEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("failed to parse credentials file: %w", err)
	}

	// Procesar cada entrada
	for _, entry := range entries {
		if !entry.Enabled {
			continue
		}

		// Validar campos requeridos según el tipo
		if entry.Type == "" {
			// Log at debug: missing type
			continue
		}

		if entry.Type != "refresh_token" && entry.Path == "" {
			// Log at debug: json/sqlite requires path
			continue
		}

		if entry.Type == "refresh_token" && entry.RefreshToken == "" {
			// Log at debug: refresh_token requires refresh_token field
			continue
		}

		// Procesar según tipo
		switch entry.Type {
		case "refresh_token":
			m.processRefreshTokenEntry(entry)
		case "json", "sqlite":
			m.processFileEntry(entry)
		}
	}

	return nil
}

// processRefreshTokenEntry crea una cuenta para una entrada de tipo refresh_token.
func (m *Manager) processRefreshTokenEntry(entry credentialEntry) {
	// Generar ID como "refresh_token_{sha256[:16]}"
	hash := sha256.Sum256([]byte(entry.RefreshToken))
	hashStr := hex.EncodeToString(hash[:])[:16]
	accountID := fmt.Sprintf("refresh_token_%s", hashStr)

	// Crear config por cuenta
	accountCfg := *m.cfg
	// Para refresh_token, pasamos el token directamente al config
	accountCfg.RefreshToken = entry.RefreshToken
	accountCfg.ProfileARN = entry.ProfileARN
	// Asegurar que no se intenta cargar de archivos
	accountCfg.KiroCredsFile = ""
	accountCfg.KiroCLIDBFile = ""

	// Crear auth.Manager para esta cuenta
	authMgr, err := auth.NewManagerForAccount(&accountCfg, entry.APIRegion)
	if err != nil {
		// Log at debug: failed to create auth manager
		return
	}

	account := &Account{
		ID:         accountID,
		Type:       "refresh_token",
		Path:       "",
		Enabled:    true,
		ProfileARN: entry.ProfileARN,
		Region:     entry.Region,
		APIRegion:  entry.APIRegion,
		Auth:       authMgr,
		Stats:      AccountStats{},
		Models:     ModelAccountList{},
	}

	m.accounts = append(m.accounts, account)
}

// processFileEntry procesa una entrada de tipo json o sqlite.
// Si es un directorio, lo escanea sin recursión.
func (m *Manager) processFileEntry(entry credentialEntry) {
	expandedPath := os.ExpandEnv(entry.Path)

	// Verificar si es un directorio
	info, err := os.Stat(expandedPath)
	if err != nil {
		// Log at debug: path not found
		return
	}

	if info.IsDir() {
		// Escanear directorio sin recursión
		m.scanDirectory(expandedPath, entry)
	} else {
		// Procesar como archivo individual
		m.processFile(expandedPath, entry)
	}
}

// scanDirectory escanea un directorio sin recursión y valida/procesa cada archivo.
func (m *Manager) scanDirectory(dirPath string, entry credentialEntry) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		// Log at debug: failed to read directory
		return
	}

	for _, e := range entries {
		// Solo procesar archivos, no directorios
		if e.IsDir() {
			continue
		}

		filePath := filepath.Join(dirPath, e.Name())
		m.processFile(filePath, entry)
	}
}

// processFile valida un archivo JSON o SQLite y lo añade como cuenta si es válido.
func (m *Manager) processFile(filePath string, entry credentialEntry) {
	var isValid bool

	switch entry.Type {
	case "json":
		isValid = m.isValidJSONFile(filePath)
	case "sqlite":
		isValid = m.isValidSQLiteFile(filePath)
	default:
		return
	}

	if !isValid {
		// Log at debug: invalid credentials file
		return
	}

	// Resolver ruta absoluta
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		// Log at debug: failed to get absolute path
		return
	}

	// Crear config por cuenta con el path correcto
	accountCfg := *m.cfg

	if entry.Type == "json" {
		accountCfg.KiroCredsFile = absPath
		accountCfg.KiroCLIDBFile = "" // Asegurar que no se intenta cargar SQLite
	} else if entry.Type == "sqlite" {
		accountCfg.KiroCLIDBFile = absPath
		accountCfg.KiroCredsFile = "" // Asegurar que no se intenta cargar JSON
	}

	// Crear auth.Manager para esta cuenta
	authMgr, err := auth.NewManagerForAccount(&accountCfg, entry.APIRegion)
	if err != nil {
		// Log at debug: failed to create auth manager
		return
	}

	account := &Account{
		ID:         absPath,
		Type:       entry.Type,
		Path:       absPath,
		Enabled:    true,
		ProfileARN: entry.ProfileARN,
		Region:     entry.Region,
		APIRegion:  entry.APIRegion,
		Auth:       authMgr,
		Stats:      AccountStats{},
		Models:     ModelAccountList{},
	}

	m.accounts = append(m.accounts, account)
}

// isValidJSONFile verifica que un archivo JSON contiene refreshToken o clientId.
func (m *Manager) isValidJSONFile(filePath string) bool {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return false
	}

	var creds map[string]interface{}
	if err := json.Unmarshal(data, &creds); err != nil {
		return false
	}

	// Válido si tiene refreshToken o clientId
	_, hasRefreshToken := creds["refreshToken"]
	_, hasClientID := creds["clientId"]

	return hasRefreshToken || hasClientID
}

// isValidSQLiteFile verifica que un archivo SQLite contiene la tabla auth_kv.
func (m *Manager) isValidSQLiteFile(filePath string) bool {
	db, err := sql.Open("sqlite", filePath)
	if err != nil {
		return false
	}
	defer db.Close()

	// Verificar si la tabla auth_kv existe
	row := db.QueryRow(`
		SELECT name FROM sqlite_master
		WHERE type='table' AND name='auth_kv'
	`)

	var name string
	err = row.Scan(&name)
	if err != nil {
		return false
	}

	return name == "auth_kv"
}
