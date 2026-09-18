// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/accounterrors"
)

// ExhaustedAccountsError is returned by GetNextAccount when a multi-account
// setup has no candidate left (all disabled/quarantined/excluded).
type ExhaustedAccountsError struct {
	LastMsg string
}

func (e *ExhaustedAccountsError) Error() string {
	return "503 Service Unavailable: " + e.LastMsg
}

// findAccountIndexByID returns the index of the account with the given ID,
// or -1 if not found. Requires m.mu to be held.
func (m *Manager) findAccountIndexByID(accountID string) int {
	for i, acc := range m.accounts {
		if acc.ID == accountID {
			return i
		}
	}
	return -1
}

// GetNextAccount returns the next enabled account, starting from the sticky index.
// The sticky index is updated by ReportSuccess to point to the last successful account.
// For single-account deployments, returns that account (bypassing circuit breaker),
// or an error if it's been excluded in the current failover loop.
// For multi-account, if all accounts are unavailable, returns a typed
// ExhaustedAccountsError containing the last attempted account's error message.
func (m *Manager) GetNextAccount(model string, exclude map[string]struct{}) (*Account, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Zero-account guard: with no accounts, the len==1 fast path is skipped,
	// nextEnabledIdx returns (-1,nil), and the "last attempted" index below
	// would compute (stickyIdx+len-1) % len — a divide-by-zero panic. Upstream
	// get_next_account (account_manager.py:645-720) returns None for an empty
	// account map; the Go idiom is the typed 503, same as the exhausted path.
	if len(m.accounts) == 0 {
		return nil, &ExhaustedAccountsError{LastMsg: "no accounts available"}
	}

	now := m.clock()

	// Single-account mode: bypass circuit breaker, return the account
	if len(m.accounts) == 1 {
		acc := m.accounts[0]

		// If already tried in current failover loop, return an error
		if _, ok := exclude[acc.ID]; ok {
			errMsg := acc.Stats.LastFailureMsg
			if errMsg == "" {
				errMsg = "account already tried"
			}
			return nil, &ExhaustedAccountsError{LastMsg: errMsg}
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

	return nil, &ExhaustedAccountsError{LastMsg: errMsg}
}

// ReportSuccess resets the failure counter for the account and updates the sticky index.
// The sticky index is set to point to the account that just succeeded.
// This is the GLOBAL sticky behavior: next request prefers this successful account.
// Upstream: account_manager.py:801-804.
func (m *Manager) ReportSuccess(accountID, model string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the account by ID
	idx := m.findAccountIndexByID(accountID)
	if idx == -1 {
		// Silently ignore unknown accountID; a future logging framework should warn.
		// Deferred to observability wire (matches upstream's blanket try/except).
		return
	}

	acc := m.accounts[idx]

	// Reset failure state
	acc.Stats.ConsecutiveFailures = 0
	acc.Stats.LastFailure = time.Time{}
	acc.Stats.LastFailureMsg = ""

	// GLOBAL STICKY: update to point to the successful account
	if m.stickyIdx != idx {
		m.stickyIdx = idx
	}
}

// ReportFailure classifies the error and updates the failure state.
// Classifies via accounterrors.Classify(statusCode, reason).
// If Recoverable: increments ConsecutiveFailures, sets LastFailure (enters quarantine).
// If Fatal: only updates LastFailureMsg, does NOT increment failure counter.
// The sticky index is NEVER changed on failure; it only moves in ReportSuccess.
// Upstream: account_manager.py:864-865 — failover happens via the exclude set and
// GetNextAccount's round-robin walk, not via sticky rotation.
// Returns the classification (Fatal or Recoverable).
//
// ReportFailure self-classifies via accounterrors.Classify(statusCode, reason).
// Callers that already know the classification they want to record — e.g. a
// caller distinguishing a real Kiro HTTP response from a transport-level
// failure with the same nominal status code, which must be classified
// differently — should call ReportFailureAs instead. This delegates to it
// with the self-computed classification, so the two never drift apart.
func (m *Manager) ReportFailure(accountID, model string, statusCode int, reason string, msg string) accounterrors.Type {
	return m.ReportFailureAs(accountID, model, accounterrors.Classify(statusCode, reason), statusCode, reason, msg)
}

// ReportFailureAs records a failure using the SUPPLIED classification kind,
// skipping accounterrors.Classify entirely. Upstream's report_failure takes
// error_type as a caller-supplied parameter (account_manager.py:809-816),
// not something it derives itself — callers like routes_openai.py:513-516
// pass ErrorType.RECOVERABLE for a 502/504 transport failure even though
// classify_error(502-or-504, None) would independently call it Fatal (any
// 5xx defaults to Fatal in the classification table, account_errors.py) —
// that table answers "was this a bad response FROM Kiro", not "should this
// specific transport failure open the circuit breaker for this account".
// ReportFailure (above) is the self-classifying convenience wrapper most
// callers want; this is the primitive it delegates to, exposed for callers
// that need to override the classification.
//
// Same side effects as ReportFailure, keyed off kind instead of a freshly
// computed classification: Recoverable increments ConsecutiveFailures and
// sets LastFailure (opens quarantine); Fatal only updates LastFailureMsg.
// The sticky index is never touched here either.
func (m *Manager) ReportFailureAs(accountID, model string, kind accounterrors.Type, statusCode int, reason string, msg string) accounterrors.Type {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Find the account by ID
	idx := m.findAccountIndexByID(accountID)
	if idx == -1 {
		// Silently ignore unknown accountID; a future logging framework should warn.
		// Deferred to observability wire (matches upstream's blanket try/except).
		return kind
	}

	acc := m.accounts[idx]

	// Update the failure message in all cases
	acc.Stats.LastFailureMsg = msg

	if kind == accounterrors.Recoverable {
		// Increment failure counter and set timestamp (opens quarantine)
		acc.Stats.ConsecutiveFailures++
		acc.Stats.LastFailure = m.clock()
	}
	// If Fatal, do NOT increment failure counter or open quarantine; just updated LastFailureMsg above

	// GLOBAL STICKY: never changed on failure (upstream auth.py:864-865)
	// Failover happens through the exclude_accounts loop and nextEnabledIdx walk,
	// not through sticky rotation.

	return kind
}
