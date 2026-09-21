// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/accounterrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

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

// TestSingleAccountExcluded verifies that single-account mode with exclude returns error.
func TestSingleAccountExcluded(t *testing.T) {
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

	// Exclude the single account
	exclude := map[string]struct{}{
		testAccountID(0): {},
	}
	acc, err := m.GetNextAccount("gpt-4", exclude)
	if acc != nil {
		t.Fatalf("Expected nil account when single account is excluded")
	}
	var exhausted *ExhaustedAccountsError
	if !errors.As(err, &exhausted) {
		t.Fatalf("Expected ExhaustedAccountsError, got %T: %v", err, err)
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
	var exhausted *ExhaustedAccountsError
	if !errors.As(err, &exhausted) {
		t.Fatalf("Expected ExhaustedAccountsError, got %T: %v", err, err)
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
	return fmt.Sprintf("account-%d", idx)
}
