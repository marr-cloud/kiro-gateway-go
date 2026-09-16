// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
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

			// Acquire semaphore
			sem <- struct{}{}
			defer func() { <-sem }()

			// Validate credentials
			_, err := account.Auth.AccessToken(ctx)
			if err != nil {
				// Mark account as disabled on auth failure
				m.mu.Lock()
				account.Enabled = false
				m.mu.Unlock()
				// Log at debug: auth validation failed for account
				return
			}

			// Determine endpoint type and fetch models
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
	isRuntime := strings.Contains(apiHost, "://runtime.")

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
	// Build request
	qhost := account.Auth.QHost()
	listModelsURL := qhost + "/ListAvailableModels"

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

	// Attempt to fetch models
	models, fetchErr := fetchListAvailableModels(ctx, req, account.Auth)
	if fetchErr != nil {
		// Fall back to static models
		models = append([]string(nil), fallbackModels...)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	account.Models = ModelAccountList{
		Models:   models,
		LoadedAt: now,
		TTL:      time.Duration(m.cfg.AccountCacheTTL) * time.Second,
	}

	return nil
}

// fetchListAvailableModels makes a GET request to {qhost}/ListAvailableModels
// and parses the response. Returns the model list or an error.
func fetchListAvailableModels(ctx context.Context, req *http.Request, auth interface {
	AccessToken(context.Context) (string, error)
}) ([]string, error) {
	// Get access token
	token, err := auth.AccessToken(ctx)
	if err != nil {
		return nil, err
	}

	// Set authorization header
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	// Create a simple HTTP client with retry logic
	client := &http.Client{
		Timeout: 30 * time.Second,
	}
	defer client.CloseIdleConnections()

	// Retry logic: 3 attempts with exponential backoff
	const maxRetries = 3
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(1<<uint(attempt-1)) * time.Second):
				// Continue
			}
		}

		resp, err := client.Do(req.Clone(ctx))
		if err != nil {
			lastErr = err
			continue
		}
		defer resp.Body.Close()

		// Success: parse response
		if resp.StatusCode == http.StatusOK {
			bodyBytes, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, err
			}

			var data map[string]interface{}
			if err := json.Unmarshal(bodyBytes, &data); err != nil {
				return nil, err
			}

			models, ok := data["models"].([]interface{})
			if !ok {
				return nil, nil
			}

			result := make([]string, len(models))
			for i, m := range models {
				if s, ok := m.(string); ok {
					result[i] = s
				}
			}
			return result, nil
		}

		// On 5xx, retry
		if resp.StatusCode >= 500 && resp.StatusCode < 600 {
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}

		// On other errors, return immediately
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	return nil, lastErr
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
