// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/accounterrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// TestStickySelection verifies sticky selection: 3 accounts, 5 successful calls
// all go to the same account (sticky index never moves without failure).
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

	// 5 successful calls should all return the same account (sticky index doesn't move)
	for i := 0; i < 5; i++ {
		acc, err := m.GetNextAccount("gpt-4", nil)
		if err != nil {
			t.Fatalf("GetNextAccount failed: %v", err)
		}
		if acc.ID != testAccountID(0) {
			t.Fatalf("Call %d: expected account 0, got %s", i, acc.ID)
		}
	}

	// Verify sticky index is still 0
	if m.stickyIdx != 0 {
		t.Fatalf("Expected stickyIdx=0, got %d", m.stickyIdx)
	}
}

// TestFailoverOnRecoverableFailure verifies that on a Recoverable failure,
// the next call goes to the next account.
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

	// Get first account
	acc1, _ := m.GetNextAccount("gpt-4", nil)
	if acc1.ID != testAccountID(0) {
		t.Fatalf("Expected account 0, got %s", acc1.ID)
	}

	// Report a Recoverable failure from account 0
	errType := m.ReportFailure(testAccountID(0), "gpt-4", 429, "rate_limit", "Rate limited")
	if errType != accounterrors.Recoverable {
		t.Fatalf("Expected Recoverable, got %s", errType)
	}

	// Next call should return account 1 (sticky advanced)
	acc2, _ := m.GetNextAccount("gpt-4", nil)
	if acc2.ID != testAccountID(1) {
		t.Fatalf("Expected account 1, got %s", acc2.ID)
	}

	// Verify sticky index advanced
	if m.stickyIdx != 1 {
		t.Fatalf("Expected stickyIdx=1, got %d", m.stickyIdx)
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
func TestClassificationRecoverable(t *testing.T) {
	m := &Manager{
		accounts:  make([]*Account, 1),
		stickyIdx: 0,
		clock:     time.Now,
		randFloat: func() float64 { return 0.5 },
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}

	m.accounts[0] = &Account{
		ID:      testAccountID(0),
		Enabled: true,
		Stats:   AccountStats{},
	}

	// 402 should be Recoverable
	errType := m.ReportFailure(testAccountID(0), "gpt-4", 402, "", "Payment required")
	if errType != accounterrors.Recoverable {
		t.Fatalf("402 should be Recoverable, got %s", errType)
	}
}

// TestClassificationFatal verifies 5xx is classified as Fatal.
func TestClassificationFatal(t *testing.T) {
	m := &Manager{
		accounts:  make([]*Account, 1),
		stickyIdx: 0,
		clock:     time.Now,
		randFloat: func() float64 { return 0.5 },
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}

	m.accounts[0] = &Account{
		ID:      testAccountID(0),
		Enabled: true,
		Stats:   AccountStats{},
	}

	// 500 should be Fatal
	errType := m.ReportFailure(testAccountID(0), "gpt-4", 500, "", "Internal server error")
	if errType != accounterrors.Fatal {
		t.Fatalf("500 should be Fatal, got %s", errType)
	}
}

// TestSingleAccountFatal verifies that with 1 account, even if Fatal,
// GetNextAccount still returns it.
func TestSingleAccountFatal(t *testing.T) {
	m := &Manager{
		accounts:  make([]*Account, 1),
		stickyIdx: 0,
		clock:     time.Now,
		randFloat: func() float64 { return 0.5 },
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}

	m.accounts[0] = &Account{
		ID:      testAccountID(0),
		Enabled: true,
		Stats:   AccountStats{},
	}

	// Report a Fatal failure
	m.ReportFailure(testAccountID(0), "gpt-4", 500, "", "Internal server error")

	// GetNextAccount should still return the single account
	acc, err := m.GetNextAccount("gpt-4", nil)
	if err != nil {
		t.Fatalf("Single account should be returned, got error: %v", err)
	}
	if acc.ID != testAccountID(0) {
		t.Fatalf("Expected single account, got %s", acc.ID)
	}
}

// TestMultiAccountNoAvailable verifies 503 synthesis when all accounts are unavailable.
func TestMultiAccountNoAvailable(t *testing.T) {
	m := &Manager{
		accounts:  make([]*Account, 2),
		stickyIdx: 0,
		clock:     time.Now,
		randFloat: func() float64 { return 0.5 },
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}

	for i := 0; i < 2; i++ {
		m.accounts[i] = &Account{
			ID:      testAccountID(i),
			Enabled: true,
			Stats:   AccountStats{},
		}
	}

	// Make both accounts fail with Fatal
	m.ReportFailure(testAccountID(0), "gpt-4", 500, "", "Internal server error")
	m.ReportFailure(testAccountID(1), "gpt-4", 500, "", "Internal server error")

	// Try to get an account, excluding both
	exclude := map[string]struct{}{
		testAccountID(0): {},
		testAccountID(1): {},
	}
	acc, err := m.GetNextAccount("gpt-4", exclude)
	if acc != nil {
		t.Fatalf("Expected nil account when all unavailable")
	}
	if err == nil {
		t.Fatalf("Expected error when all accounts unavailable")
	}
}

// TestExcludeSkipping verifies that exclude parameter skips accounts.
func TestExcludeSkipping(t *testing.T) {
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

	// Exclude account 0
	exclude := map[string]struct{}{
		testAccountID(0): {},
	}
	acc, err := m.GetNextAccount("gpt-4", exclude)
	if err != nil {
		t.Fatalf("GetNextAccount failed: %v", err)
	}
	if acc.ID != testAccountID(1) {
		t.Fatalf("Expected account 1 (skipping 0), got %s", acc.ID)
	}
}

// TestReportSuccessResetsFailures verifies that ReportSuccess resets the failure count.
func TestReportSuccessResetsFailures(t *testing.T) {
	m := &Manager{
		accounts:  make([]*Account, 1),
		stickyIdx: 0,
		clock:     time.Now,
		randFloat: func() float64 { return 0.5 },
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}

	m.accounts[0] = &Account{
		ID:      testAccountID(0),
		Enabled: true,
		Stats:   AccountStats{},
	}

	// Report 3 failures
	for i := 0; i < 3; i++ {
		m.ReportFailure(testAccountID(0), "gpt-4", 429, "rate_limit", "Rate limited")
	}

	if m.accounts[0].Stats.ConsecutiveFailures != 3 {
		t.Fatalf("Expected 3 failures, got %d", m.accounts[0].Stats.ConsecutiveFailures)
	}

	// Report success
	m.ReportSuccess(testAccountID(0), "gpt-4")

	// Verify failures reset
	if m.accounts[0].Stats.ConsecutiveFailures != 0 {
		t.Fatalf("Expected 0 failures after success, got %d", m.accounts[0].Stats.ConsecutiveFailures)
	}
	if !m.accounts[0].Stats.LastFailure.IsZero() {
		t.Fatalf("Expected LastFailure to be zeroed")
	}
	if m.accounts[0].Stats.LastFailureMsg != "" {
		t.Fatalf("Expected LastFailureMsg to be empty")
	}
}

// Helper function to generate test account IDs
func testAccountID(idx int) string {
	return "account-" + string(rune('0'+idx))
}
