// SPDX-License-Identifier: AGPL-3.0-or-later
// Port a Go de jwadow/kiro-gateway. Ver NOTICE.

package accountmanager

import (
	"testing"
	"time"

	"github.com/marr-cloud/kiro-gateway-go/internal/accounterrors"
	"github.com/marr-cloud/kiro-gateway-go/internal/config"
)

// TestReportFailureAsForcesClassification verifies that ReportFailureAs arms
// the circuit breaker using the SUPPLIED classification, even when
// accounterrors.Classify would independently call the same status code
// Fatal (the classification table treats any 5xx as Fatal). This is the
// primitive internal/routesopenai.handleTransportError relies on to fail
// over — and quarantine the failing account — on a transport-level 502/504,
// without going through accounterrors' response-classification table, which
// answers "was this HTTP response FROM Kiro bad" and not "should this
// account be quarantined for this specific transport failure" (see
// routes_openai.py:513-516, which hardcodes ErrorType.RECOVERABLE for that
// case; account_manager.py:809-816 takes error_type as a caller parameter
// for exactly this reason).
func TestReportFailureAsForcesClassification(t *testing.T) {
	m := &Manager{
		accounts:  make([]*Account, 2),
		stickyIdx: 0,
		clock:     time.Now,
		randFloat: func() float64 { return 0.5 },
		cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
	}
	for i := 0; i < 2; i++ {
		m.accounts[i] = &Account{ID: testAccountID(i), Enabled: true, Stats: AccountStats{}}
	}

	// Sanity-check the premise this test exists to guard against: on its
	// own, Classify(500, "") is Fatal, which would NOT arm the breaker if
	// ReportFailureAs re-derived the classification instead of using the
	// one it was given.
	if got := accounterrors.Classify(500, ""); got != accounterrors.Fatal {
		t.Fatalf("premise broken: accounterrors.Classify(500, \"\") = %s, want Fatal", got)
	}

	kind := m.ReportFailureAs(testAccountID(0), "gpt-4", accounterrors.Recoverable, 500, "", "transport error: connection refused")
	if kind != accounterrors.Recoverable {
		t.Fatalf("ReportFailureAs returned %s, want Recoverable", kind)
	}

	acc := m.accounts[0]
	if acc.Stats.ConsecutiveFailures != 1 {
		t.Fatalf("ConsecutiveFailures = %d, want 1 (breaker should have armed)", acc.Stats.ConsecutiveFailures)
	}
	if acc.Stats.LastFailure.IsZero() {
		t.Fatal("LastFailure was not set — breaker did not arm")
	}
	if acc.Stats.LastFailureMsg != "transport error: connection refused" {
		t.Fatalf("LastFailureMsg = %q, want the supplied msg", acc.Stats.LastFailureMsg)
	}

	// GetNextAccount should now skip account 0 (quarantined) and return
	// account 1, proving the breaker actually excludes it from selection.
	got, err := m.GetNextAccount("gpt-4", nil)
	if err != nil {
		t.Fatalf("GetNextAccount: %v", err)
	}
	if got.ID != testAccountID(1) {
		t.Fatalf("GetNextAccount = %s, want account 1 (account 0 should be quarantined)", got.ID)
	}
}

// TestReportFailureDelegatesToReportFailureAs is a narrow regression guard:
// ReportFailure must keep self-classifying via accounterrors.Classify and
// produce IDENTICAL side effects to calling ReportFailureAs with that same
// pre-computed classification — the refactor that introduced ReportFailureAs
// must not change ReportFailure's existing, already-tested behavior.
func TestReportFailureDelegatesToReportFailureAs(t *testing.T) {
	newManager := func() *Manager {
		m := &Manager{
			accounts:  make([]*Account, 1),
			stickyIdx: 0,
			clock:     time.Now,
			randFloat: func() float64 { return 0.5 },
			cfg:       &config.Config{AccountRecoveryTimeout: 60, AccountMaxBackoffMultiplier: 1440, AccountProbabilisticRetryChance: 0.1},
		}
		m.accounts[0] = &Account{ID: testAccountID(0), Enabled: true, Stats: AccountStats{}}
		return m
	}

	// Recoverable path (429).
	mViaReportFailure := newManager()
	kindA := mViaReportFailure.ReportFailure(testAccountID(0), "gpt-4", 429, "", "rate limited")

	mViaReportFailureAs := newManager()
	kindB := mViaReportFailureAs.ReportFailureAs(testAccountID(0), "gpt-4", accounterrors.Classify(429, ""), 429, "", "rate limited")

	if kindA != kindB {
		t.Fatalf("classification mismatch: ReportFailure=%s ReportFailureAs=%s", kindA, kindB)
	}
	if mViaReportFailure.accounts[0].Stats != mViaReportFailureAs.accounts[0].Stats {
		t.Fatalf("stats mismatch: ReportFailure=%+v ReportFailureAs=%+v",
			mViaReportFailure.accounts[0].Stats, mViaReportFailureAs.accounts[0].Stats)
	}
}
