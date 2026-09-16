// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"time"
)

// nextEnabledIdx finds the next enabled, non-excluded, non-quarantined account
// starting from startIdx and iterating circularly through the accounts.
// Returns the index and the account, or (-1, nil) if no suitable account is found
// after a full loop.
func nextEnabledIdx(
	startIdx int,
	accounts []*Account,
	exclude map[string]struct{},
	now time.Time,
	isInQuarantine func(*Account, time.Time) bool,
) (int, *Account) {
	if len(accounts) == 0 {
		return -1, nil
	}

	for i := 0; i < len(accounts); i++ {
		idx := (startIdx + i) % len(accounts)
		acc := accounts[idx]

		// Skip disabled accounts
		if !acc.Enabled {
			continue
		}

		// Skip excluded accounts
		if _, ok := exclude[acc.ID]; ok {
			continue
		}

		// Skip quarantined accounts
		if isInQuarantine(acc, now) {
			continue
		}

		return idx, acc
	}

	return -1, nil
}
