package main

import (
	"strings"
	"testing"
)

// TestDegradedBannerNamesTheRealCause is the regression test the original
// change lacked. The inline version compared WalletDegradedInfo()'s reason,
// which can only ever be "no reachable mints" in that branch (the storage case
// is handled by the caller), so the branch that named a wallet-init failure was
// unreachable and the #1135 bug the PR set out to fix was still present.
//
// A test that only asserted the no-reachable-mints wording would pass on both
// the broken and the fixed version, which is exactly how the dead branch got
// through. This one asserts the other side.
func TestDegradedBannerNamesTheRealCause(t *testing.T) {
	// The reason merchant.go records when every mint probes 200 OK but the
	// wallet cannot initialize (#583/#1035).
	cause := "wallet initialization failed: InitStorage: error setting bolt db: invalid argument"

	got := degradedBanner(cause)

	if !strings.Contains(got, cause) {
		t.Fatalf("banner must name the recorded cause, got: %q", got)
	}
	if strings.Contains(got, "will initialize when a mint becomes reachable") {
		t.Fatalf("banner must not promise a mint transition when the mints are fine, got: %q", got)
	}
	if !strings.Contains(got, "retried automatically") {
		t.Fatalf("banner must state the retry loop owns recovery, got: %q", got)
	}
}

// TestDegradedBannerKeepsHistoricWording pins the no-reachable-mints case: the
// first-reachable trigger really does own recovery there, so the old sentence
// is accurate and must not change.
func TestDegradedBannerKeepsHistoricWording(t *testing.T) {
	got := degradedBanner("no reachable mints")

	want := "Merchant started in degraded mode — wallet will initialize when a mint becomes reachable"
	if got != want {
		t.Fatalf("no-reachable-mints wording changed\n got: %q\nwant: %q", got, want)
	}
}

// TestDegradedBannerEmptyReasonStillNamesRecovery guards the DegradeReason()
// fallback path: an unset reason must not produce a banner claiming a mint
// transition, and must not render an empty "()" either.
func TestDegradedBannerEmptyReasonStillNamesRecovery(t *testing.T) {
	got := degradedBanner("")

	if strings.Contains(got, "will initialize when a mint becomes reachable") {
		t.Fatalf("an unset reason must not promise a mint transition, got: %q", got)
	}
	if strings.Contains(got, "()") {
		t.Fatalf("banner must not render an empty reason, got: %q", got)
	}
}
