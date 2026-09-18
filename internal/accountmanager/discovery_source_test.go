// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// TestLoadCredentials_ReadsFromAccountsConfigFile fija el contrato de la
// simplificación de credenciales: el descubrimiento se dirige por
// AccountsConfigFile (ACCOUNTS_CONFIG_FILE) y NO por el campo KiroCredsFile
// (que ya no se lee del entorno global; solo lo usa auth por cuenta). El test
// pone un KiroCredsFile deliberadamente falso para probar que se ignora.
func TestLoadCredentials_ReadsFromAccountsConfigFile(t *testing.T) {
	dir := t.TempDir()
	credsPath := filepath.Join(dir, "credentials.json")
	// Una entrada refresh_token es autosuficiente: no necesita fichero externo.
	const arr = `[{"type":"refresh_token","enabled":true,"refresh_token":"tok-123"}]`
	if err := os.WriteFile(credsPath, []byte(arr), 0o600); err != nil {
		t.Fatalf("write creds: %v", err)
	}

	cfg := &config.Config{
		AccountsConfigFile:       credsPath,
		KiroCredsFile:            filepath.Join(dir, "no-existe-ignorado.json"),
		AccountsStateFile:        filepath.Join(dir, "state.json"),
		StateSaveIntervalSeconds: 1,
		AccountCacheTTL:          3600,
		KiroRegion:               "us-east-1",
	}

	m, err := NewManager(cfg)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	if err := m.LoadCredentials(context.Background()); err != nil {
		t.Fatalf("LoadCredentials: %v", err)
	}
	if got := len(m.Accounts()); got != 1 {
		t.Fatalf("cuentas cargadas = %d, quiero 1 (descubiertas desde AccountsConfigFile)", got)
	}
}
