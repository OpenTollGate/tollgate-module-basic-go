package merchant

// The #1222 red test: a definitive mint refusal (the mint ANSWERED with a
// NUT-19 error body — the Coinos shape "keyset <id> unknown", relayed by the
// gonuts wallet as "could not swap proofs: <mint's message>") must be
// classified as the mint refusing the note, NOT as the mint being
// unreachable. Three behaviors pin the doctrine:
//
//  1. the notice is payment-error-mint-refused with resubmittable guidance
//     (the note was NOT spent — a definitive 4xx consumed nothing);
//  2. the receive intent resolves ABANDONED, so a retry of the same note
//     reaches Receive again (it must not sit pending like an ambiguous
//     outcome — the #502 model: definitive refusal ⇒ abandoned);
//  3. the mint is NOT marked unreachable (it answered — condemning it
//     degrades the router's sales path for a mint-side data issue).
//
// Evidence shape from the LEG F rig (2026-11-12, module log):
//
//	PurchaseSession: Receive completed, amount=0,
//	err=could not swap proofs: keyset 007311aa2fa58cc8 unknown
//
// Before the fix this landed in isMintUnreachableError via the generic
// "could not" substring (meant for keyset-RESOLUTION failures), the
// tracker marked the mint down, and the still-spendable note was
// permanently refused (#1222).

import (
	"fmt"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

type mintRefusalToken struct {
	tollwallet.Token
}

func (mintRefusalToken) Mint() string   { return "https://refusing-mint.example.com" }
func (mintRefusalToken) Amount() uint64 { return 100 }
func (mintRefusalToken) Serialize() (string, error) {
	return "mintrefusal-stub-token", nil
}

type mintRefusalWallet struct {
	tollwallet.WalletPort
	receiveCalls int
}

func (w *mintRefusalWallet) DecodeToken(string) (tollwallet.Token, error) {
	return mintRefusalToken{}, nil
}
func (w *mintRefusalWallet) SwapFeeSats(tollwallet.Token) (uint64, error) {
	return 0, nil
}
func (w *mintRefusalWallet) CheckTokenSpent(tollwallet.Token) (bool, error) {
	return false, nil // the mint's NUT-07 keeps answering UNSPENT (#1153)
}
func (w *mintRefusalWallet) Receive(tollwallet.Token) (uint64, error) {
	w.receiveCalls++
	return 0, fmt.Errorf("could not swap proofs: keyset 007311aa2fa58cc8 unknown")
}

func TestPurchaseSessionDefinitiveMintRefusalIsNotUnreachable(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	m, _ := newRenewalMerchant(t, "milliseconds")
	w := &mintRefusalWallet{}
	m.tollwallet = w

	event, err := m.PurchaseSession("cashuAstub", renewalMAC)
	if err != nil {
		t.Fatalf("PurchaseSession: %v", err)
	}

	code := noticeErrorCode(t, event)
	if code != "payment-error-mint-refused" {
		t.Fatalf("a definitive mint refusal must be payment-error-mint-refused, got %q (content: %q)", code, event.Content)
	}
	if !strings.Contains(event.Content, "was not spent") {
		t.Fatalf("notice must state the note was not spent (safe to retry): %q", event.Content)
	}

	// The intent resolved abandoned and the mint was not condemned: the
	// SAME note, retried, must reach Receive again (no pending-intent
	// do-not-resubmit, no degraded short-circuit).
	_, err = m.PurchaseSession("cashuAstub", renewalMAC)
	if err != nil {
		t.Fatalf("retry PurchaseSession: %v", err)
	}
	if w.receiveCalls != 2 {
		t.Fatalf("the retry must reach Receive again (intent abandoned, mint not marked unreachable); Receive calls = %d", w.receiveCalls)
	}
}

// The sidecar (cdk backend) relay of the same Coinos refusal — the
// both-backend conformance claim (#1222): the wrapper differs
// ("Unknown error response: `code: 0, detail: ...`" vs the in-process
// "could not swap proofs: ..."), the doctrine must not.
func TestSidecarMintRefusalWrapperIsAlsoRefusedClass(t *testing.T) {
	if !isMintRefusalError(fmt.Errorf("tollwallet: sidecar receive: receive: Unknown error response: `code: 0, detail: keyset 007311aa2fa58cc8 unknown`")) {
		t.Fatal("the sidecar's mint-answer wrapper must classify as a definitive refusal")
	}
	if isMintRefusalError(fmt.Errorf("tollwallet: sidecar connect /var/run/cdk-walletd/w.sock: connection refused")) {
		t.Fatal("a sidecar CONNECTION failure is not a mint refusal (the mint was never asked)")
	}
}
