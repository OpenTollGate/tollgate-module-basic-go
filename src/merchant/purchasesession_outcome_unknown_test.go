package merchant

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// outcomeUnknownWallet stubs the wallet with a Receive that fails exactly the
// way the gonuts v0.12.2 mint client does when the mint processed a swap and
// dropped the response: an error wrapping tollwallet.ErrOutcomeUnknown.
type outcomeUnknownWallet struct {
	tollwallet.WalletPort
}

func (w *outcomeUnknownWallet) DecodeToken(string) (tollwallet.Token, error) {
	return preflightToken{}, nil
}
func (w *outcomeUnknownWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return 0, nil }
func (w *outcomeUnknownWallet) Receive(tollwallet.Token) (uint64, error) {
	return 0, fmt.Errorf("could not swap proofs: %w: mint did not answer", tollwallet.ErrOutcomeUnknown)
}

// TestPurchaseSessionAmbiguousOutcomeNoticeTellsTheCustomerNotToResend pins
// the customer-facing half of #640 at the merchant layer: a swap whose
// response was dropped may already have been processed, so the notice must
// carry payment-outcome-unknown and its do-not-resend guidance — never a
// retry-flavoured code that invites a second submission of the same note.
func TestPurchaseSessionAmbiguousOutcomeNoticeTellsTheCustomerNotToResend(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	m, _ := newRenewalMerchant(t, "milliseconds")
	m.tollwallet = &outcomeUnknownWallet{}

	event, err := m.PurchaseSession("cashuAstub", renewalMAC)
	if err != nil {
		t.Fatalf("PurchaseSession: %v", err)
	}

	if code := noticeErrorCode(t, event); code != "payment-outcome-unknown" {
		t.Fatalf("notice code: want payment-outcome-unknown, got %q", code)
	}

	message := event.Content
	if !strings.Contains(message, "Do not send this e-cash note again") {
		t.Fatalf("notice must tell the customer not to resubmit the note; got %q", message)
	}
	if !strings.Contains(message, "reference:") {
		t.Fatalf("notice must carry the operator-quotable reference; got %q", message)
	}
}

// TestPurchaseSessionAmbiguousOutcomeDoesNotCondemnTheMint pins the
// health-tracker half: an unanswered request is not evidence the mint is
// down (it may be processing the very swap whose response was dropped), so a
// single dropped response must not empty the reachable set — the same
// revenue-DoS rule the rate-limit class already follows.
func TestPurchaseSessionAmbiguousOutcomeDoesNotCondemnTheMint(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	m, _ := newRenewalMerchant(t, "milliseconds")
	m.tollwallet = &outcomeUnknownWallet{}

	const mintURL = "https://preflight-mint.example.com"
	m.mintHealthTracker.mu.Lock()
	m.mintHealthTracker.reachableMints[mintURL] = true
	m.mintHealthTracker.mu.Unlock()

	if _, err := m.PurchaseSession("cashuAstub", renewalMAC); err != nil {
		t.Fatalf("PurchaseSession: %v", err)
	}

	if !m.mintHealthTracker.IsReachable(mintURL) {
		t.Fatal("an unanswered swap response must not condemn the mint")
	}
}

// TestErrOutcomeUnknownWrapsClientAmbiguity pins the tollwallet boundary
// mapping end to end at the unit level: the fork's AmbiguousResponseError
// (surfaced through gonuts) maps to the exported sentinel callers classify
// on.
func TestErrOutcomeUnknownWrapsClientAmbiguity(t *testing.T) {
	wrapped := fmt.Errorf("outer: %w", tollwallet.ErrOutcomeUnknown)
	if !errors.Is(wrapped, tollwallet.ErrOutcomeUnknown) {
		t.Fatal("errors.Is must see the sentinel through wrapping")
	}
}
