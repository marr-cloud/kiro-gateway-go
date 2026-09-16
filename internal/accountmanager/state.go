// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"
)

// accountStatsJSON es la estructura de estadísticas para serializar.
type accountStatsJSON struct {
	ConsecutiveFailures int    `json:"consecutive_failures"`
	LastFailure         string `json:"last_failure"` // RFC3339 timestamp
	LastFailureMsg      string `json:"last_failure_msg"`
}

// stateFileFormat es la estructura serializada a state.json.
type stateFileFormat struct {
	Accounts map[string]accountStatsJSON `json:"accounts"`
}

// LoadState lee state.json y fusiona el estado guardado con las cuentas cargadas.
// Si el archivo no existe, es un no-error (empieza con estado vacío).
func (m *Manager) LoadState() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	stateFilePath := m.stateFile
	if stateFilePath == "" {
		return nil
	}

	data, err := os.ReadFile(stateFilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Archivo no existe — no es error
			return nil
		}
		// Otro error de lectura — registrar pero no fallar
		return nil
	}

	var stateData stateFileFormat
	if err := json.Unmarshal(data, &stateData); err != nil {
		// JSON inválido — registrar pero no fallar
		return nil
	}

	// Fusionar el estado guardado con las cuentas cargadas
	for accountID, savedStats := range stateData.Accounts {
		// Buscar la cuenta correspondiente
		var acc *Account
		for _, a := range m.accounts {
			if a.ID == accountID {
				acc = a
				break
			}
		}

		if acc == nil {
			continue
		}

		// Restaurar estadísticas
		acc.Stats.ConsecutiveFailures = savedStats.ConsecutiveFailures
		acc.Stats.LastFailureMsg = savedStats.LastFailureMsg

		// Parsear timestamp
		if savedStats.LastFailure != "" {
			if t, err := time.Parse(time.RFC3339Nano, savedStats.LastFailure); err == nil {
				acc.Stats.LastFailure = t
			}
		}
	}

	return nil
}

// SaveState guarda el estado actual a state.json de forma atómica.
// Usa tmp + rename con reintentos (3x, 100ms apart).
func (m *Manager) SaveState() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	stateFilePath := m.stateFile
	if stateFilePath == "" {
		return nil
	}

	// Construir state data
	stateData := stateFileFormat{
		Accounts: make(map[string]accountStatsJSON),
	}

	for _, acc := range m.accounts {
		lastFailureStr := ""
		if !acc.Stats.LastFailure.IsZero() {
			lastFailureStr = acc.Stats.LastFailure.Format(time.RFC3339Nano)
		}

		stateData.Accounts[acc.ID] = accountStatsJSON{
			ConsecutiveFailures: acc.Stats.ConsecutiveFailures,
			LastFailure:         lastFailureStr,
			LastFailureMsg:      acc.Stats.LastFailureMsg,
		}
	}

	// Serializar a JSON
	jsonData, err := json.MarshalIndent(stateData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal state: %w", err)
	}

	// Escribir a tmp file
	tmpPath := stateFilePath + ".tmp"
	if err := os.WriteFile(tmpPath, jsonData, 0644); err != nil {
		return fmt.Errorf("failed to write tmp state file: %w", err)
	}

	// Rename con reintentos
	err = m.renameWithRetry(tmpPath, stateFilePath)
	if err != nil {
		// Intentar limpiar tmp file
		_ = os.Remove(tmpPath)
		return err
	}

	return nil
}

// renameWithRetry intenta hacer rename 3 veces con backoff de 100ms.
func (m *Manager) renameWithRetry(oldPath, newPath string) error {
	const maxAttempts = 3
	const retryDelay = 100 * time.Millisecond

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := m.renameFn(oldPath, newPath)
		if err == nil {
			return nil
		}

		lastErr = err
		if attempt < maxAttempts {
			time.Sleep(retryDelay)
		}
	}

	return fmt.Errorf("failed to rename state file after %d attempts: %w", maxAttempts, lastErr)
}
