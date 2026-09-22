// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/config"
	"github.com/marr-cloud/kiro-gateway-go/internal/httpclient"
)

// Manager gestiona múltiples cuentas de Kiro con descubrimiento, persistencia
// de estado y preparación para failover/circuit breaker.
type Manager struct {
	cfg          *config.Config
	accounts     []*Account
	stickyIdx    int // Current sticky account index for load balancing
	mu           sync.RWMutex
	stateFile    string
	saveInterval time.Duration
	httpClient   *httpclient.Client

	// renameFn es la función de rename, inyectable para tests.
	// Por defecto es os.Rename.
	renameFn func(string, string) error

	// lastSaved es el último estado guardado, para detectar cambios dirty.
	lastSaved map[string]AccountStats

	// clock es la función para obtener la hora actual.
	// Inyectable para tests. Por defecto es time.Now.
	clock func() time.Time

	// randFloat es la función para obtener un número aleatorio en [0, 1).
	// Inyectable para tests. Por defecto es rand.Float64.
	randFloat func() float64

	// listURLOverride es una función inyectable para tests que reemplaza el
	// cálculo del URL de ListAvailableModels. Por defecto es nil (sin override).
	// Usado para apuntar a servidores mock en tests.
	listURLOverride func(qhost string) string

	// isRuntimeEndpointOverride es una función inyectable para tests que
	// reemplaza la detección de endpoint runtime. Por defecto es nil (sin override).
	// Cuando es nil, se usa la detección estándar (apiHost contiene "://runtime.").
	// Usado para forzar tests a tomar la rama de old-endpoint en refreshAccountModels.
	isRuntimeEndpointOverride func(apiHost string) bool

	// modelsOverride es la lista de MODELS_CONFIG_FILE (models.json) si existe.
	// Se carga una vez en Initialize (antes de arrancar las goroutines, así que
	// las lecturas posteriores son seguras) y, si es no vacía, es la lista
	// autoritativa de modelos para toda cuenta (corto-circuita fetch y fallback).
	modelsOverride []string

	// managementURLOverride es una función inyectable para tests que reemplaza
	// el URL base de management.<region>.kiro.dev (descubrimiento dinámico de
	// modelos). Por defecto nil (se construye el host real).
	managementURLOverride func(region string) string
}

// NewManager crea un nuevo Manager.
// No carga las cuentas automáticamente — eso requiere llamar a LoadCredentials.
func NewManager(cfg *config.Config) (*Manager, error) {
	if cfg == nil {
		return nil, fmt.Errorf("accountmanager: nil config")
	}

	stateFile := cfg.AccountsStateFile
	if stateFile == "" {
		// Default: junto a credentials.json
		if cfg.AccountsConfigFile != "" {
			stateFile = filepath.Join(
				filepath.Dir(cfg.AccountsConfigFile),
				"state.json",
			)
		} else {
			stateFile = "state.json"
		}
	}

	saveInterval := time.Duration(cfg.StateSaveIntervalSeconds) * time.Second
	if saveInterval == 0 {
		saveInterval = 10 * time.Second
	}

	httpCli, err := httpclient.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("accountmanager: create httpclient: %w", err)
	}

	m := &Manager{
		cfg:          cfg,
		accounts:     make([]*Account, 0),
		stickyIdx:    0,
		stateFile:    stateFile,
		saveInterval: saveInterval,
		httpClient:   httpCli,
		renameFn:     os.Rename,
		lastSaved:    make(map[string]AccountStats),
		clock:        time.Now,
		randFloat:    rand.Float64,
	}

	return m, nil
}

// LoadCredentials lee credentials.json y crea todas las cuentas necesarias.
// Esto es una operación potencialmente lenta que inicializa los auth.Manager.
func (m *Manager) LoadCredentials(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.loadCredentials(ctx)
}

// SaveStatePeriodically es un bucle que guarda el estado periódicamente.
// Se ejecuta como una goroutine y se cancela vía ctx.Done().
// Guarda el estado final al ser cancelado si está dirty.
func (m *Manager) SaveStatePeriodically(ctx context.Context) {
	ticker := time.NewTicker(m.saveInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Cancelado — guardar estado final si está dirty
			m.mu.RLock()
			isDirty := m.hasStateChanged()
			m.mu.RUnlock()

			if isDirty {
				_ = m.SaveState()
			}
			return
		case <-ticker.C:
			// Verificar si el estado cambió
			m.mu.RLock()
			isDirty := m.hasStateChanged()
			m.mu.RUnlock()

			if isDirty {
				_ = m.SaveState()
			}
		}
	}
}

// hasStateChanged verifica si el estado en memoria difiere del último guardado.
// Debe llamarse con m.mu held.
func (m *Manager) hasStateChanged() bool {
	// Comparar cada cuenta
	for _, acc := range m.accounts {
		saved, exists := m.lastSaved[acc.ID]
		if !exists {
			// Cuenta nueva
			if acc.Stats.ConsecutiveFailures != 0 ||
				!acc.Stats.LastFailure.IsZero() ||
				acc.Stats.LastFailureMsg != "" {
				return true
			}
		} else {
			// Comparar estadísticas
			if saved.ConsecutiveFailures != acc.Stats.ConsecutiveFailures ||
				saved.LastFailureMsg != acc.Stats.LastFailureMsg ||
				!saved.LastFailure.Equal(acc.Stats.LastFailure) {
				return true
			}
		}
	}

	// Verificar si hay cuentas que desaparecieron
	if len(m.lastSaved) != len(m.accounts) {
		return true
	}

	return false
}

// Accounts devuelve la lista de cuentas cargadas.
// Requiere haber llamado a LoadCredentials antes.
func (m *Manager) Accounts() []*Account {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Devolver una copia de la lista
	result := make([]*Account, len(m.accounts))
	copy(result, m.accounts)
	return result
}
