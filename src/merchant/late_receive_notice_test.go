//go:build !cdk_wallet

package merchant

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The late-`Receive` notice.
//
// `Receive` is given a 30-second deadline while the HTTP client and the mint both
// use a 30-second transport timeout, so the module can abandon its own money leg
// at the instant it is about to learn the truth. When that happens the notice the
// customer receives says "Payment processing timed out after 30 seconds. Please
// try again." — and acting on that advice destroys their money: a `Receive` that
// lands at t=31s has already moved the proofs into the operator's wallet, so the
// retry fails as `ErrTokenAlreadySpent`. The module's own written rule forbids
// this ("decide refund vs late-grant explicitly; do not silently drop it").
//
// The journal/janitor that would collect the late result is a separate card. What
// this pins is the part that must not wait for it: the notice must describe the
// situation honestly (the outcome is unknown, not "failed"), must tell the
// customer not to submit the same note again, and must carry a reference the
// customer can quote and the operator can find in the log.

// lateReceiveToken is the note the customer submitted; only its serialized form
// matters here, and it is distinctive enough to assert absence in the log.
type lateReceiveToken struct{ tollwallet.Token }

func (lateReceiveToken) Mint() string               { return "https://late-receive.example.com" }
func (lateReceiveToken) Amount() uint64             { return 1 }
func (lateReceiveToken) Serialize() (string, error) { return lateReceiveSerialized, nil }
func (lateReceiveToken) Close()                     {}

const lateReceiveSerialized = "cashuB" + "late-receive-note-late-receive-note-late-receive-note-"

// blockingReceiveWallet reaches `Receive` and stays inside it: the point of the
// test is the window in which the module has sent a money-moving request and does
// not yet know the outcome.
type blockingReceiveWallet struct {
	tollwallet.WalletPort
	started chan struct{}
	release chan struct{}
}

func (w *blockingReceiveWallet) DecodeToken(string) (tollwallet.Token, error) {
	return lateReceiveToken{}, nil
}
func (w *blockingReceiveWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return 0, nil }
func (w *blockingReceiveWallet) Receive(tollwallet.Token) (uint64, error) {
	close(w.started)
	<-w.release
	return 1, nil
}

func TestLateReceiveNoticeIsHonestAndCarriesAReference(t *testing.T) {
	cm, _ := setupTestConfigManager(t)
	wallet := &blockingReceiveWallet{started: make(chan struct{}), release: make(chan struct{})}
	m := &Merchant{
		config:            cm.GetConfig(),
		configManager:     cm,
		tollwallet:        wallet,
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
	}
	// The Receive goroutine outlives the assertion by design; let it finish so
	// the test process does not carry a stuck goroutine into the next test.
	t.Cleanup(func() { close(wallet.release) })

	stubPreflightProbe(t, func(string) (valve.ClientState, error) {
		return valve.ClientState{Registered: true}, nil
	})

	// The production deadline is a var precisely so this window can be tested
	// without waiting 30 s (the same seam style as preflightRetryDelay).
	prevTimeout := receiveTimeout
	receiveTimeout = 25 * time.Millisecond
	t.Cleanup(func() { receiveTimeout = prevTimeout })

	logs := capturedStdLogs(t)

	event, err := m.PurchaseSession("cashuBsubmitted-note", "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("PurchaseSession returned an error instead of a notice: %v", err)
	}
	select {
	case <-wallet.started:
	default:
		t.Fatal("Receive was never reached, so the late-outcome window was not exercised")
	}

	if event == nil || event.Kind != 21023 {
		t.Fatalf("expected a kind-21023 notice, got %+v", event)
	}

	// The notice must not claim the payment failed, and must not instruct the
	// customer to spend the same note twice.
	// (Each check reports rather than stops, so one run shows every dishonest
	// thing the notice says.)
	if got := noticeCode(t, event); got != "payment-outcome-unknown" {
		t.Errorf("notice code = %q, want %q (the outcome is unknown, not a failed payment)", got, "payment-outcome-unknown")
	}
	content := strings.ToLower(event.Content)
	if strings.Contains(content, "please try again") || strings.Contains(content, "try again") {
		t.Errorf("notice still tells the customer to retry a note the mint may already hold: %q", event.Content)
	}
	if !strings.Contains(content, "do not") || !strings.Contains(content, "again") {
		t.Errorf("notice does not tell the customer to stop: %q", event.Content)
	}

	// The reference: the same value the operator finds in the log, derived from
	// the note without being the note.
	wantReference := utils.TokenFingerprint(lateReceiveSerialized)
	if wantReference == "" {
		t.Fatal("the fingerprint helper returned nothing for a real note")
	}
	if !strings.Contains(event.Content, wantReference) {
		t.Errorf("notice %q does not carry the reference %q the operator can search for", event.Content, wantReference)
	}
	if !regexp.MustCompile(`[0-9a-f]{16}`).MatchString(event.Content) {
		t.Errorf("notice %q carries no 16-hex reference at all", event.Content)
	}

	// The operator's side of the reference, and the assertion that the reference
	// is not the instrument: the log line is where an incident is diagnosed.
	out := logs.String()
	if !strings.Contains(out, wantReference) {
		t.Errorf("the log does not carry the reference %q the customer will quote:\n%s", wantReference, out)
	}
	if strings.Contains(out, lateReceiveSerialized) {
		t.Errorf("the spendable note is in the log output:\n%s", out)
	}
	if !strings.Contains(out, "mac=aa:bb:cc:dd:ee:ff") {
		// The canonical (lower-case) spelling: PurchaseSession normalises the MAC
		// on the way in, and the operator greps what the log holds.
		t.Errorf("the late-receive log line does not identify the device:\n%s", out)
	}
}

// The window itself: a `Receive` that is still in flight must leave the request
// answered (the customer is not left waiting) and must leave the goroutine free
// to record an outcome — the field this test cannot yet assert is the one the
// journal card adds.
func TestLateReceiveReturnsANoticeWhileReceiveIsStillInFlight(t *testing.T) {
	cm, _ := setupTestConfigManager(t)
	wallet := &blockingReceiveWallet{started: make(chan struct{}), release: make(chan struct{})}
	m := &Merchant{
		config:            cm.GetConfig(),
		configManager:     cm,
		tollwallet:        wallet,
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
	}
	t.Cleanup(func() { close(wallet.release) })

	stubPreflightProbe(t, func(string) (valve.ClientState, error) {
		return valve.ClientState{Registered: true}, nil
	})

	prevTimeout := receiveTimeout
	receiveTimeout = 20 * time.Millisecond
	t.Cleanup(func() { receiveTimeout = prevTimeout })

	started := time.Now()
	event, err := m.PurchaseSession("cashuBsubmitted-note", "AA:BB:CC:DD:EE:FF")
	elapsed := time.Since(started)

	if err != nil {
		t.Fatalf("PurchaseSession returned an error instead of a notice: %v", err)
	}
	if event == nil || event.Kind != 21023 {
		t.Fatalf("expected a kind-21023 notice, got %+v", event)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the request was not answered at the deadline (took %s)", elapsed)
	}
}
