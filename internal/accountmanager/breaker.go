// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"time"
)

// quarantineWindow computes the exponential backoff window for a quarantined account.
// Formula: base * min(2^(fails-1), cap)
// Handles overflow at high fail counts (cap kicks in before shift overflows).
func quarantineWindow(fails int, base time.Duration, cap int) time.Duration {
	if fails == 0 {
		return 0
	}

	// Compute 2^(fails-1), capped at cap multiplier
	multiplier := 1
	for i := 1; i < fails; i++ {
		if multiplier > cap {
			// Already at or exceeded cap, no need to multiply further
			multiplier = cap
			break
		}
		if multiplier > cap/2 {
			// Next multiplication would exceed cap
			multiplier = cap
			break
		}
		multiplier *= 2
	}
	if multiplier > cap {
		multiplier = cap
	}

	return base * time.Duration(multiplier)
}

// isInQuarantine checks whether an account is quarantined at the given time.
// If no failures (ConsecutiveFailures == 0), returns false.
// Otherwise computes the quarantine window and checks if now.Sub(LastFailure) < window.
// If quarantined AND probabilistic retry fires, returns false (probabilistic exemption).
func (m *Manager) isInQuarantine(a *Account, now time.Time) bool {
	if a.Stats.ConsecutiveFailures == 0 {
		return false
	}

	// Compute quarantine window
	baseTimeout := time.Duration(m.cfg.AccountRecoveryTimeout) * time.Second
	cap := m.cfg.AccountMaxBackoffMultiplier
	window := quarantineWindow(a.Stats.ConsecutiveFailures, baseTimeout, cap)

	// Check if still within quarantine window
	timeSinceFailure := now.Sub(a.Stats.LastFailure)
	if timeSinceFailure >= window {
		// Recovery timeout passed
		return false
	}

	// Still quarantined; check for probabilistic retry
	if m.randFloat() < m.cfg.AccountProbabilisticRetryChance {
		// Probabilistic retry fires
		return false
	}

	return true
}
