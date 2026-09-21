// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/accounterrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// TestStickySelection verifies sticky selection: 3 accounts, 5 successful calls.
// Sticky points to the last successful account (upstream auth.py:801-804).
func TestStickySelection(t *testing.T) {
	m := &Manager{
		accounts:  make([]*Account, 3),
		stickyIdx: 0,
		clock:     time.Now,
		randFloat: func() float64 { return 0.5 },
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}

	// Create 3 test accounts
	for i := 0; i < 3; i++ {
		m.accounts[i] = &Account{
			ID:      testAccountID(i),
			Enabled: true,
			Stats:   AccountStats{},
		}
	}

	// 5 successful calls on account 0 should keep sticky at 0
	for i := 0; i < 5; i++ {
		acc, err := m.GetNextAccount("gpt-4", nil)
		if err != nil {
			t.Fatalf("GetNextAccount failed: %v", err)
		}
		if acc.ID != testAccountID(0) {
			t.Fatalf("Call %d: expected account 0, got %s", i, acc.ID)
		}
		// Report success to update sticky
		m.ReportSuccess(acc.ID, "gpt-4")
	}

	// Verify sticky index is still 0 (pointing to last successful account)
	if m.stickyIdx != 0 {
		t.Fatalf("Expected stickyIdx=0 (pointing to last successful), got %d", m.stickyIdx)
	}
}

// TestFailoverOnRecoverableFailure verifies failover behavior on Recoverable failure.
// Sticky follows the last successful account, not failures.
// When account 0 fails, it quarantines but sticky stays at 0.
// GetNextAccount finds account 1 (quarantined account 0 is skipped).
// When account 1 succeeds, sticky moves to 1.
func TestFailoverOnRecoverableFailure(t *testing.T) {
	m := &Manager{
		accounts:  make([]*Account, 3),
		stickyIdx: 0,
		clock:     time.Now,
		randFloat: func() float64 { return 0.5 },
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}

	for i := 0; i < 3; i++ {
		m.accounts[i] = &Account{
			ID:      testAccountID(i),
			Enabled: true,
			Stats:   AccountStats{},
		}
	}

	// Get account 0 (sticky=0)
	acc1, _ := m.GetNextAccount("gpt-4", nil)
	if acc1.ID != testAccountID(0) {
		t.Fatalf("Expected account 0, got %s", acc1.ID)
	}
	// Report success to set sticky to 0
	m.ReportSuccess(testAccountID(0), "gpt-4")

	// Report a Recoverable failure from account 0 (enters quarantine)
	errType := m.ReportFailure(testAccountID(0), "gpt-4", 429, "rate_limit", "Rate limited")
	if errType != accounterrors.Recoverable {
		t.Fatalf("Expected Recoverable, got %s", errType)
	}

	// Sticky should NOT move on failure (upstream auth.py:864-865)
	if m.stickyIdx != 0 {
		t.Fatalf("After failure, stickyIdx should NOT change; expected 0, got %d", m.stickyIdx)
	}

	// Next call: sticky is 0, but account 0 is quarantined, so nextEnabledIdx finds account 1
	acc2, _ := m.GetNextAccount("gpt-4", nil)
	if acc2.ID != testAccountID(1) {
		t.Fatalf("Expected account 1 (acc 0 quarantined), got %s", acc2.ID)
	}

	// Report success on account 1 → sticky moves to 1
	m.ReportSuccess(testAccountID(1), "gpt-4")
	if m.stickyIdx != 1 {
		t.Fatalf("After success on account 1, expected stickyIdx=1, got %d", m.stickyIdx)
	}

	// Next call should prefer account 1 (sticky=1)
	acc3, _ := m.GetNextAccount("gpt-4", nil)
	if acc3.ID != testAccountID(1) {
		t.Fatalf("Expected account 1 (sticky=1), got %s", acc3.ID)
	}
}

// TestCircuitBreakerBackoff verifies exponential backoff: 60s / 120s / 240s.
func TestCircuitBreakerBackoff(t *testing.T) {
	baseTime := time.Unix(1000, 0)
	m := &Manager{
		accounts:  make([]*Account, 2),
		stickyIdx: 0,
		clock: func() time.Time {
			return baseTime
		},
		randFloat: func() float64 { return 0.5 }, // No probabilistic retry
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}

	for i := 0; i < 2; i++ {
		m.accounts[i] = &Account{
			ID:      testAccountID(i),
			Enabled: true,
			Stats:   AccountStats{},
		}
	}

	// Make account 0 fail 3 times
	for i := 1; i <= 3; i++ {
		m.ReportFailure(testAccountID(0), "gpt-4", 429, "rate_limit", "Rate limited")
	}

	// At baseTime + 100s, account 0 should still be quarantined (1st backoff is 60s)
	baseTime = baseTime.Add(100 * time.Second)
	if !m.isInQuarantine(m.accounts[0], baseTime) {
		t.Fatalf("At +100s from 3rd failure, account should be quarantined")
	}

	// At baseTime + 300s from initial, account should be recovered
	// (3rd failure at 1000+t, backoff = 60 * 2^(3-1) = 60*4 = 240s)
	baseTime = time.Unix(1000+300, 0)
	if m.isInQuarantine(m.accounts[0], baseTime) {
		t.Fatalf("At +300s, account should be recovered")
	}
}

// TestBackoffCap verifies saturation at 1440 multiplier.
func TestBackoffCap(t *testing.T) {
	baseTime := time.Unix(1000, 0)
	m := &Manager{
		accounts:  make([]*Account, 1),
		stickyIdx: 0,
		clock: func() time.Time {
			return baseTime
		},
		randFloat: func() float64 { return 0.5 },
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440},
	}

	m.accounts[0] = &Account{
		ID:      testAccountID(0),
		Enabled: true,
		Stats:   AccountStats{},
	}

	// Simulate 1500 failures to trigger the cap
	for i := 0; i < 1500; i++ {
		m.ReportFailure(testAccountID(0), "gpt-4", 429, "rate_limit", "Rate limited")
	}

	// At baseTime + 86400 seconds exactly, should be recovered (at the threshold)
	// (60 * 1440 = 86400 seconds = 24 hours)
	baseTime = time.Unix(1000+86400, 0)
	if m.isInQuarantine(m.accounts[0], baseTime) {
		t.Fatalf("At cap backoff, account should be recovered")
	}

	// At baseTime + 86399 seconds, should still be quarantined (just before threshold)
	baseTime = time.Unix(1000+86399, 0)
	if !m.isInQuarantine(m.accounts[0], baseTime) {
		t.Fatalf("At cap backoff - 1s, account should still be quarantined")
	}
}

// TestProbabilisticRetry verifies that probabilistic retry fires.
func TestProbabilisticRetry(t *testing.T) {
	baseTime := time.Unix(1000, 0)
	m := &Manager{
		accounts:  make([]*Account, 1),
		stickyIdx: 0,
		clock: func() time.Time {
			return baseTime
		},
		randFloat: func() float64 { return 0.05 }, // < 0.1, so should retry
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}

	m.accounts[0] = &Account{
		ID:      testAccountID(0),
		Enabled: true,
		Stats:   AccountStats{},
	}

	// One failure
	m.ReportFailure(testAccountID(0), "gpt-4", 429, "rate_limit", "Rate limited")

	// At baseTime + 10s, still within quarantine window but should retry probabilistically
	baseTime = baseTime.Add(10 * time.Second)
	// With randFloat = 0.05 < 0.1 (chance), should NOT be quarantined
	if m.isInQuarantine(m.accounts[0], baseTime) {
		t.Fatalf("Probabilistic retry should have triggered")
	}
}

// TestProbabilisticRetrySkip verifies probabilistic retry can be skipped.
func TestProbabilisticRetrySkip(t *testing.T) {
	baseTime := time.Unix(1000, 0)
	m := &Manager{
		accounts:  make([]*Account, 1),
		stickyIdx: 0,
		clock: func() time.Time {
			return baseTime
		},
		randFloat: func() float64 { return 0.99 }, // > 0.1, so should NOT retry
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}

	m.accounts[0] = &Account{
		ID:      testAccountID(0),
		Enabled: true,
		Stats:   AccountStats{},
	}

	// One failure
	m.ReportFailure(testAccountID(0), "gpt-4", 429, "rate_limit", "Rate limited")

	// At baseTime + 10s, still within quarantine window and probabilistic retry should NOT fire
	baseTime = baseTime.Add(10 * time.Second)
	// With randFloat = 0.99 > 0.1 (chance), should be quarantined
	if !m.isInQuarantine(m.accounts[0], baseTime) {
		t.Fatalf("Probabilistic retry should NOT have triggered")
	}
}

// TestClassificationRecoverable verifies 402 is classified as Recoverable.
