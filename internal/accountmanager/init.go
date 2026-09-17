// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/auth"
)

// fallbackModels is the static model catalog used when ListAvailableModels is
// unavailable (runtime endpoint) or fetch fails (old endpoint).
//
// TEMPORARY: replicated from upstream config.py:276-290; move to internal/modelresolver
// in phase 6 per spec §6.12.
var fallbackModels = []string{
	"auto",
	"claude-sonnet-4",
	"claude-sonnet-4.5",
	"claude-sonnet-4.6",
	"claude-haiku-4.5",
	"claude-opus-4.5",
	"claude-opus-4.6",
	"claude-opus-4.7",
	"deepseek-3.2",
	"glm-5",
	"minimax-m2.1",
	"minimax-m2.5",
	"qwen3-coder-next",
}

const initConcurrency = 4

// Initialize starts each account: verifies auth, populates ModelAccountList
// (dynamic if the ListAvailableModels endpoint is available, static FALLBACK_MODELS
// otherwise). Runs in parallel with a small concurrency limit to avoid hammering OIDC.
func (m *Manager) Initialize(ctx context.Context) error {
	m.mu.RLock()
	accounts := m.accounts
	m.mu.RUnlock()

	if len(accounts) == 0 {
		return nil
	}

	// Semaphore to limit concurrency
	sem := make(chan struct{}, initConcurrency)
	wg := sync.WaitGroup{}

	for _, acc := range accounts {
		wg.Add(1)
		go func(account *Account) {
			defer wg.Done()

			// Acquire semaphore to limit concurrent goroutines to initConcurrency
			sem <- struct{}{}
			defer func() { <-sem }()

			// Validate credentials via AccessToken. Token comes from auth.Manager,
			// which validates it against OIDC/SQLite/refresh-token sources.
			_, err := account.Auth.AccessToken(ctx)
			if err != nil {
				// Mark account as disabled on auth failure
				m.mu.Lock()
				account.Enabled = false
				m.mu.Unlock()
				// Log at debug: auth validation failed for account
				return
			}

			// Determine endpoint type and fetch models.
			// Silent fallback; refreshAccountModels handles errors internally by using static list (matches upstream).
			_ = m.refreshAccountModels(ctx, account.ID)
		}(acc)
	}

	wg.Wait()
	return nil
}

// refreshAccountModels re-fetches the model list for one account if its TTL expired.
// No-op against runtime endpoints (which don't expose ListAvailableModels).
func (m *Manager) refreshAccountModels(ctx context.Context, accountID string) error {
	m.mu.Lock()
	idx := m.findAccountIndexByID(accountID)
	if idx == -1 {
		m.mu.Unlock()
		return nil
	}
	account := m.accounts[idx]

	// Check if TTL has expired
	now := m.clock()
	if account.Models.LoadedAt.Add(account.Models.TTL).After(now) {
		// TTL not expired, no-op
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	// Determine endpoint type via APIHost
	apiHost := account.Auth.APIHost()
	var isRuntime bool
	if m.isRuntimeEndpointOverride != nil {
		// Test override: use the injected endpoint detection
		isRuntime = m.isRuntimeEndpointOverride(apiHost)
	} else {
		// Production: check if URL contains "://runtime."
		isRuntime = strings.Contains(apiHost, "://runtime.")
	}

	if isRuntime {
		// Runtime endpoint: no ListAvailableModels available
		m.mu.Lock()
		defer m.mu.Unlock()

		// Use static fallback list
		models := append([]string(nil), fallbackModels...)
		account.Models = ModelAccountList{
			Models:   models,
			LoadedAt: now,
			TTL:      time.Duration(m.cfg.AccountCacheTTL) * time.Second,
		}
		return nil
	}

	// Old endpoint: fetch dynamic model list
	qhost := account.Auth.QHost()
	var listModelsURL string
	if m.listURLOverride != nil {
		// Test override: use the injected URL
		listModelsURL = m.listURLOverride(qhost)
	} else {
		// Production: append endpoint path to qhost
		listModelsURL = qhost + "/ListAvailableModels"
	}

	// Build request with query parameters per upstream account_manager.py:509-511
	req, err := http.NewRequestWithContext(ctx, "GET", listModelsURL, nil)
	if err != nil {
		// Fall back to static models on request creation error
		m.mu.Lock()
		defer m.mu.Unlock()
		account.Models = ModelAccountList{
			Models:   append([]string(nil), fallbackModels...),
			LoadedAt: now,
			TTL:      time.Duration(m.cfg.AccountCacheTTL) * time.Second,
		}
		return nil
	}

	// Add query parameters
	q := req.URL.Query()
	q.Set("origin", "AI_EDITOR")
	// Add profileArn if this is a KIRO_DESKTOP account with a profile ARN
	if account.Auth.Type() == auth.AuthTypeKiroDesktop && account.ProfileARN != "" {
		q.Set("profileArn", account.ProfileARN)
	}
	req.URL.RawQuery = q.Encode()

	// Attempt to fetch models using RequestWithRetry (handles 5xx retries)
	resp, err := m.httpClient.RequestWithRetry(ctx, req, account.Auth, false)
	var models []string

	if err == nil && resp.StatusCode == http.StatusOK {
		// Parse response body
		defer resp.Body.Close()
		bodyBytes, _ := io.ReadAll(resp.Body)

		var data map[string]interface{}
		if err := json.Unmarshal(bodyBytes, &data); err == nil {
			if modelList, ok := data["models"].([]interface{}); ok {
				models = make([]string, len(modelList))
				for i, m := range modelList {
					if s, ok := m.(string); ok {
						models[i] = s
					}
				}
			}
		}
	}

	// If we couldn't fetch models, fall back to static list
	if len(models) == 0 {
		models = append([]string(nil), fallbackModels...)
	}

	// Nota (fase 6a Task 3): esta cuenta guarda la unión CRUDA de modelos; el
	// filtrado de §6.11 (HIDDEN_FROM_LIST + aliases MODEL_ALIASES) se aplica
	// aguas abajo en routesopenai.Models vía modelresolver.GetAvailableModels
	// (model_resolver.py:370-397), no aquí — GetAllAvailableModels sigue
	// devolviendo la unión sin filtrar a propósito.

	m.mu.Lock()
	defer m.mu.Unlock()

	account.Models = ModelAccountList{
		Models:   models,
		LoadedAt: now,
		TTL:      time.Duration(m.cfg.AccountCacheTTL) * time.Second,
	}

	return nil
}

// GetAllAvailableModels returns the union of models from all enabled accounts.
func (m *Manager) GetAllAvailableModels() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	models := make(map[string]struct{})
	for _, acc := range m.accounts {
		if !acc.Enabled {
			continue
		}
		for _, model := range acc.Models.Models {
			models[model] = struct{}{}
		}
	}

	// Convert to sorted slice
	result := make([]string, 0, len(models))
	for model := range models {
		result = append(result, model)
	}
	sort.Strings(result)

	return result
}

// GetFirstAccount returns the first discovered account (legacy path for callers
// that don't want failover).
func (m *Manager) GetFirstAccount() *Account {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.accounts) > 0 {
		return m.accounts[0]
	}
	return nil
}
