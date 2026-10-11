package merchant

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/nbd-wtf/go-nostr"
)

// The in-flight guard around the money-moving Receive (#639). The mint's
// spend-state is the only sequential-duplicate guard the payment path has, and
// two concurrent Receives of the same note both pass it before either swap
// settles — measured by the #535 conformance lane as a double grant. These
// tests pin the properties the guard must hold:
//
//   - a concurrent duplicate is refused BEFORE the mint is touched;
//   - the guard releases once the outcome is consumed, so a sequential
//     resubmit reaches the mint and gets its already-spent refusal;
//   - through the outcome-unknown window (deadline fired, Receive still on the
//     wire) the note stays guarded until the late recorder consumes the result;
//   - a note whose reference cannot be computed is never refused by the guard.
//
// duplicateNote reuses the preflight harness's mint (it is the one the test
// config prices) with a serialized form of its own: the reference — the guard's
// key — is the fingerprint of the serialized note, so the fixture controls it
// independently of the mint. Low-entropy on purpose: a fixture must not look
// like a live credential.

type duplicateNote struct{ tollwallet.Token }

func (duplicateNote) Mint() string { return "https://preflight-mint.example.com" }
func (duplicateNote) Amount() uint64 {
	return 1
}
func (duplicateNote) Serialize() (string, error) { return duplicateNoteSerialized, nil }
func (duplicateNote) Close()                     {}

const duplicateNoteSerialized = "cashuB" + "duplicate-guard-note-duplicate-guard-note-duplicate-guard-note-"

// unserializableNote is the guard's fail-open case: no reference can be
// computed, so the guard must not refuse the payment (the mint still refuses a
// sequential resubmit as spent).
type unserializableNote struct{ tollwallet.Token }

func (unserializableNote) Mint() string   { return "https://preflight-mint.example.com" }
func (unserializableNote) Amount() uint64 { return 1 }
func (unserializableNote) Serialize() (string, error) {
	return "", errors.New("fixture: unserializable note")
}
func (unserializableNote) Close() {}

// scriptedReceiveWallet gates Receive on a channel when one is set, answers
// from a scripted list of outcomes, and counts how many Receives reached it —
// the count is the "did a second submission touch the mint" probe.
type scriptedReceiveWallet struct {
	tollwallet.WalletPort
	token    tollwallet.Token
	started  chan struct{}
	release  chan struct{}
	outcomes []receiveResult
	calls    atomic.Int32
}

func (w *scriptedReceiveWallet) DecodeToken(string) (tollwallet.Token, error) {
	if w.token != nil {
		return w.token, nil
	}
	return duplicateNote{}, nil
}
func (w *scriptedReceiveWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return 0, nil }
func (w *scriptedReceiveWallet) CheckTokenSpent(tollwallet.Token) (bool, error) {
	return false, nil
}

func (w *scriptedReceiveWallet) Receive(tollwallet.Token) (uint64, error) {
	n := w.calls.Add(1)
	// Only the FIRST call signals start and blocks on the release; later
	// calls answer immediately (the fields stay non-nil so the test's
	// receive on `started` never races a nil-write here).
	if n == 1 {
		if w.started != nil {
			close(w.started)
		}
		if w.release != nil {
			<-w.release
		}
	}
	res := w.outcomes[(int(n)-1)%len(w.outcomes)]
	return res.amount, res.err
}

func newDuplicateGuardMerchant(t *testing.T, wallet *scriptedReceiveWallet, timeout time.Duration) *Merchant {
	t.Helper()

	// The grant at the end of the outcome-unknown window runs the real valve
	// auth (ndsctl), and nothing else in this file installs a fake — so on a
	// host without ndsctl the granted-session assertion died as a
	// grant-pending notice instead (#822). Same harness the other
	// grant-exercising tests use, scoped to this test's lifetime.
	installRenewalNdsctl(t)

	cm, _ := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = append(cfg.AcceptedMints, config_manager.MintConfig{
		URL: "https://preflight-mint.example.com", PricePerStep: 1, PriceUnit: "sat", MinPurchaseSteps: 1,
	})
	m := &Merchant{
		config:            cfg,
		configManager:     cm,
		tollwallet:        wallet,
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
	}
	stubPreflightProbe(t, m, func(string) (valve.ClientState, error) {
		return valve.ClientState{Registered: true}, nil
	})
	prevTimeout := receiveTimeout
	receiveTimeout = timeout
	t.Cleanup(func() { receiveTimeout = prevTimeout })
	return m
}

// receiveGuardHeld is the white-box view of the guard map for the tests: the
// refusal behaviour is observable through PurchaseSession, but the release
// timing (after the outcome is consumed, not after the call returns) is what
// the map itself says, so reading it under its own lock is the deterministic
// check.
func receiveGuardHeld(m *Merchant, reference string) bool {
	m.receiveInFlightMu.Lock()
	defer m.receiveInFlightMu.Unlock()
	_, held := m.receiveInFlight[reference]
	return held
}

func duplicateGuardReference(t *testing.T) string {
	t.Helper()
	reference := utils.TokenFingerprint(duplicateNoteSerialized)
	if reference == "" {
		t.Fatal("the fingerprint helper returned nothing for the fixture note")
	}
	return reference
}

func TestPurchaseSessionRefusesConcurrentDuplicateBeforeTheMint(t *testing.T) {
	// Receive blocks until released, with a deadline the test never reaches:
	// the first submission stays inside the money-moving window indefinitely.
	wallet := &scriptedReceiveWallet{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		outcomes: []receiveResult{{amount: 1}},
	}
	m := newDuplicateGuardMerchant(t, wallet, 5*time.Second)

	first := make(chan *nostr.Event, 1)
	firstErr := make(chan error, 1)
	go func() {
		event, err := m.PurchaseSession("cashuBduplicate-note", "AA:BB:CC:DD:EE:01")
		first <- event
		firstErr <- err
	}()
	select {
	case <-wallet.started:
	case <-time.After(5 * time.Second):
		t.Fatal("the first submission never reached Receive, so the concurrent window was not exercised")
	}

	// The duplicate arrives while the note is in flight. It must be refused
	// locally: no second Receive (the mint's spend-state cannot arbitrate two
	// concurrent swaps of the same proofs), and the refusal must say the note
	// is being processed rather than that it failed.
	second, err := m.PurchaseSession("cashuBduplicate-note", "AA:BB:CC:DD:EE:02")
	if err != nil {
		t.Fatalf("the concurrent duplicate returned an error instead of a notice: %v", err)
	}
	if second == nil || second.Kind != 21023 {
		t.Fatalf("expected a kind-21023 notice for the concurrent duplicate, got %+v", second)
	}
	if code := noticeCode(t, second); code != "payment-duplicate-inflight" {
		t.Fatalf("expected notice code payment-duplicate-inflight, got %q", code)
	}
	if got := wallet.calls.Load(); got != 1 {
		t.Fatalf("the concurrent duplicate reached the mint: Receive was called %d times, want 1", got)
	}

	// Release the first submission and let it settle. Whatever the post-Receive
	// path returns, the outcome has been consumed and the guard must be free.
	close(wallet.release)
	select {
	case <-first:
	case <-time.After(5 * time.Second):
		t.Fatal("the first submission never settled after release")
	}
	<-firstErr
	if held := receiveGuardHeld(m, duplicateGuardReference(t)); held {
		t.Fatal("the guard is still held after the first submission consumed its outcome")
	}
}

func TestPurchaseSessionGuardReleasesSoSequentialResubmitReachesTheMint(t *testing.T) {
	// First submission succeeds; a resubmit of the same note afterwards must
	// NOT be refused by the guard — it proceeds to the mint and gets the mint's
	// own already-spent refusal, which is the sequential path's contract.
	wallet := &scriptedReceiveWallet{
		outcomes: []receiveResult{
			{amount: 1},
			{err: tollwallet.ErrTokenAlreadySpent},
		},
	}
	m := newDuplicateGuardMerchant(t, wallet, 5*time.Second)

	first, err := m.PurchaseSession("cashuBduplicate-note", "AA:BB:CC:DD:EE:01")
	if err != nil {
		t.Fatalf("first submission returned an error instead of an event: %v", err)
	}
	if first != nil && first.Kind == 21023 && noticeCode(t, first) == "payment-duplicate-inflight" {
		t.Fatal("the first submission was refused as in-flight; nothing else held the note")
	}

	second, err := m.PurchaseSession("cashuBduplicate-note", "AA:BB:CC:DD:EE:01")
	if err != nil {
		t.Fatalf("sequential resubmit returned an error instead of a notice: %v", err)
	}
	if second == nil || second.Kind != 21023 {
		t.Fatalf("expected a refusal notice for the sequential resubmit, got %+v", second)
	}
	// Under #502 the journal answers the sequential duplicate LOCALLY: the
	// first attempt's intent resolved granted, so the resubmit is refused
	// with grant-pending before the mint is touched — the same economic
	// refusal the mint's already-spent answer used to carry, one round trip
	// sooner. The mint-refusal path remains for notes the journal never saw
	// (pre-upgrade attempts, cleared journals).
	if code := noticeCode(t, second); code != "payment-received-grant-pending" && code != "payment-error-token-spent" {
		t.Fatalf("expected the journal's grant-pending refusal (or the mint's spent refusal), got %q", code)
	}
	if got := wallet.calls.Load(); got != 1 {
		t.Fatalf("Receive was called %d times, want 1 (the journal refuses the duplicate without a second mint call)", got)
	}
}

func TestPurchaseSessionGuardHoldsThroughTheOutcomeUnknownWindow(t *testing.T) {
	// The deadline fires while Receive is still on the wire: the customer is
	// answered "outcome unknown", but the note must STAY guarded — a
	// resubmission arriving now is exactly the "do not send this note again"
	// case the notice warns about, and letting it through re-opens the
	// concurrent race from the wrong side. Only the late recorder's consumption
	// of the result may release the guard.
	// The late outcome is the mint's own already-spent refusal: a definitive
	// answer that (under #502) abandons the journal intent, freeing a later
	// resubmission to proceed as a fresh payment. A late SUCCESS would owe
	// the entitlement instead — that contract is pinned by the journal's own
	// suite.
	wallet := &scriptedReceiveWallet{
		started:  make(chan struct{}),
		release:  make(chan struct{}),
		outcomes: []receiveResult{{err: tollwallet.ErrTokenAlreadySpent}, {amount: 1}},
	}
	m := newDuplicateGuardMerchant(t, wallet, 150*time.Millisecond)

	first, err := m.PurchaseSession("cashuBduplicate-note", "AA:BB:CC:DD:EE:01")
	if err != nil {
		t.Fatalf("first submission returned an error instead of a notice: %v", err)
	}
	if first == nil || first.Kind != 21023 || noticeCode(t, first) != "payment-outcome-unknown" {
		t.Fatalf("expected the outcome-unknown notice, got %+v", first)
	}
	if !receiveGuardHeld(m, duplicateGuardReference(t)) {
		t.Fatal("the guard was released at the deadline; it must hold until the late outcome is consumed")
	}

	during, err := m.PurchaseSession("cashuBduplicate-note", "AA:BB:CC:DD:EE:02")
	if err != nil {
		t.Fatalf("resubmit during the unknown window returned an error instead of a notice: %v", err)
	}
	if during == nil || during.Kind != 21023 || noticeCode(t, during) != "payment-duplicate-inflight" {
		t.Fatalf("a resubmit during the outcome-unknown window must be refused as in-flight, got %+v", during)
	}
	if got := wallet.calls.Load(); got != 1 {
		t.Fatalf("the resubmit during the window reached the mint: Receive was called %d times, want 1", got)
	}

	// The mint finally answers; the recorder consumes the result and the guard
	// releases. Under #502 the late answer also resolves the journal: this
	// wallet's outcomes[0] is a definitive mint refusal, so the intent
	// ABANDONS — stable evidence the note was never taken. A third
	// submission then proceeds to the mint exactly like a fresh payment.
	close(wallet.release)
	reference := duplicateGuardReference(t)
	deadline := time.Now().Add(5 * time.Second)
	for receiveGuardHeld(m, reference) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if receiveGuardHeld(m, reference) {
		t.Fatal("the guard never released after the late outcome was consumed")
	}

	third, err := m.PurchaseSession("cashuBduplicate-note", "AA:BB:CC:DD:EE:03")
	if err != nil {
		t.Fatalf("third submission returned an error instead of an event: %v", err)
	}
	if third != nil && third.Kind == 21023 && noticeCode(t, third) == "payment-duplicate-inflight" {
		t.Fatal("the third submission was refused as in-flight after the guard should have been released")
	}
	// The wallet's second outcome is a success: the fresh attempt grants.
	if got := wallet.calls.Load(); got != 2 {
		t.Fatalf("Receive was called %d times, want 2 (the abandoned first attempt frees the third submission to the mint)", got)
	}
	if third == nil || third.Kind != 1022 {
		t.Fatalf("the third submission must grant a session, got %+v", third)
	}
}

func TestPurchaseSessionGuardFailsOpenOnUnserializableNote(t *testing.T) {
	// A note whose reference cannot be computed must not be refused by the
	// guard: the guard is defence in depth, not a tollgate on the payment path,
	// and the mint still refuses a sequential resubmit as spent.
	wallet := &scriptedReceiveWallet{
		token: unserializableNote{},
		outcomes: []receiveResult{
			{amount: 1},
			{err: tollwallet.ErrTokenAlreadySpent},
		},
	}
	m := newDuplicateGuardMerchant(t, wallet, 5*time.Second)

	first, err := m.PurchaseSession("cashuBunserializable", "AA:BB:CC:DD:EE:01")
	if err != nil {
		t.Fatalf("first submission returned an error instead of an event: %v", err)
	}
	if first != nil && first.Kind == 21023 && noticeCode(t, first) == "payment-duplicate-inflight" {
		t.Fatal("an unserializable note was refused by the guard; the guard must fail open")
	}

	second, err := m.PurchaseSession("cashuBunserializable", "AA:BB:CC:DD:EE:01")
	if err != nil {
		t.Fatalf("second submission returned an error instead of a notice: %v", err)
	}
	if second == nil || second.Kind != 21023 || noticeCode(t, second) != "payment-error-token-spent" {
		t.Fatalf("an unserializable note must fall through to the mint's own refusal, got %+v", second)
	}
}
