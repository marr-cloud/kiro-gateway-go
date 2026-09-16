// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"fmt"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/accounterrors"
)

// GetNextAccount returns the next enabled account, skipping quarantined ones.
// The sticky index only advances on failure (in ReportFailure).
//
// For single-account deployments, returns that account's real error if it's
// disabled/excluded/quarantined (no 503 synthesis).
// For multi-account, if all accounts are unavailable, returns a synthesized
// 503 error with the last attempted account's error message.
func (m *Manager) GetNextAccount(model string, exclude map[string]struct{}) (*Account, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := m.clock()

	// Single-account mode: bypass circuit breaker, return the account or nil
	if len(m.accounts) == 1 {
		acc := m.accounts[0]

		// If already tried in current failover loop, return nil
		if _, ok := exclude[acc.ID]; ok {
			return nil, nil
		}

		// Return the single account (ignore disabled/quarantine/failures)
		return acc, nil
	}

	// Multi-account mode: find next enabled, non-excluded, non-quarantined
	_, acc := nextEnabledIdx(m.stickyIdx, m.accounts, exclude, now, m.isInQuarantine)

	if acc != nil {
		return acc, nil
	}

	// All accounts unavailable: synthesize a 503 error
	// Use the last attempted account's error message
	lastAccountIdx := (m.stickyIdx + len(m.accounts) - 1) % len(m.accounts)
	lastAcc := m.accounts[lastAccountIdx]
	errMsg := lastAcc.Stats.LastFailureMsg
	if errMsg == "" {
		errMsg = "no accounts available"
	}

	return nil, fmt.Errorf("503 Service Unavailable: %s", errMsg)
}

// ReportSuccess resets the failure counter for the account.
// Does NOT move the sticky index (sticky only advances on failure).
func (m *Manager) ReportSuccess(accountID, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the account by ID
	for _, acc := range m.accounts {
		if acc.ID == accountID {
			// Reset failure state
			acc.Stats.ConsecutiveFailures = 0
			acc.Stats.LastFailure = time.Time{}
			acc.Stats.LastFailureMsg = ""
			return
		}
	}
}

// ReportFailure classifies the error and decides quarantine vs propagation.
// Classifies via accounterrors.Classify(statusCode, reason).
// If Recoverable: increments ConsecutiveFailures, sets LastFailure, advances stickyIdx.
// If Fatal: only updates LastFailureMsg, does NOT advance stickyIdx.
// Returns the classification (Fatal or Recoverable).
func (m *Manager) ReportFailure(accountID, model string, statusCode int, reason string, msg string) accounterrors.Type {
	classification := accounterrors.Classify(statusCode, reason)

	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the account by ID
	var acc *Account
	for _, a := range m.accounts {
		if a.ID == accountID {
			acc = a
			break
		}
	}

	if acc == nil {
		// Account not found, just return the classification
		return classification
	}

	// Update the failure message in all cases
	acc.Stats.LastFailureMsg = msg

	if classification == accounterrors.Recoverable {
		// Increment failure counter, set timestamp, and advance sticky
		acc.Stats.ConsecutiveFailures++
		acc.Stats.LastFailure = m.clock()

		// Advance sticky index to next account for next attempt
		m.stickyIdx = (m.stickyIdx + 1) % len(m.accounts)
	}
	// If Fatal, do NOT advance sticky or open quarantine; just updated LastFailureMsg above

	return classification
}
