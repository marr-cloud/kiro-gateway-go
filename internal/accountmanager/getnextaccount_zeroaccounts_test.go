// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"errors"
	"testing"
	"time"
)

// TestGetNextAccount_ZeroAccountsReturnsExhaustedNoPanic verifies that an
// empty account slice does not trigger the (stickyIdx+len-1) % len divide-by-
// zero panic and instead returns a typed 503, matching upstream
// get_next_account returning None for an empty account map.
func TestGetNextAccount_ZeroAccountsReturnsExhaustedNoPanic(t *testing.T) {
	m := &Manager{
		accounts:  []*Account{},
		stickyIdx: 0,
		clock:     func() time.Time { return time.Unix(0, 0) },
	}

	acc, err := m.GetNextAccount("claude-sonnet-4", nil)
	if acc != nil {
		t.Fatalf("expected nil account for 0 accounts, got %+v", acc)
	}
	var exhausted *ExhaustedAccountsError
	if !errors.As(err, &exhausted) {
		t.Fatalf("expected *ExhaustedAccountsError, got %T: %v", err, err)
	}
}
