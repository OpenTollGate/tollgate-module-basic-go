package merchant

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
)

// The receive-intent journal (#502): the business-transaction record that
// turns the two red conformance rows — killed between mint-acceptance and
// session-grant, and swap-response-dropped — into service-or-evidence.
// Contract under test, per the fault-lab lifecycle model:
//
//   persist-before-effect: the intent is durable BEFORE Receive starts;
//   evidence, never guesses: only the mint's own answer (late result or
//   NUT-07) resolves pending; ambiguous evidence keeps it pending;
//   convergence: a spent proof-set records the owed entitlement and the
//   owed-grant monitor delivers exactly one session — across restarts.

const intentMAC = "aa:bb:cc:dd:ee:78"

type intentNote struct{}

func (intentNote) Mint() string               { return renewalMintURL }
func (intentNote) Amount() uint64             { return renewalSats }
func (intentNote) Serialize() (string, error) { return "cashuBreceive-intent-fixture", nil }
func (intentNote) Secrets() []string          { return nil }
func (intentNote) Close()                     {}

// intentWallet scripts Receive (blocking on a channel when set) and
// CheckTokenSpent independently; both are counted.
type intentWallet struct {
	tollwallet.WalletPort
	receiveStarted chan struct{}
	receiveRelease chan struct{}
	receiveErr     error
	spent          atomic.Bool
	checkCalls     atomic.Int32
	receiveCalls   atomic.Int32
}

func (w *intentWallet) DecodeToken(string) (tollwallet.Token, error) { return intentNote{}, nil }
func (w *intentWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return 0, nil }
func (w *intentWallet) CheckTokenSpent(tollwallet.Token) (bool, error) {
	w.checkCalls.Add(1)
	if w.spent.Load() {
		return true, nil
	}
	return false, nil
}
func (w *intentWallet) Receive(tollwallet.Token) (uint64, error) {
	n := w.receiveCalls.Add(1)
	if w.receiveStarted != nil && n == 1 {
		close(w.receiveStarted)
	}
	if w.receiveRelease != nil && n == 1 {
		<-w.receiveRelease
	}
	if w.receiveErr != nil && n == 1 {
		return 0, w.receiveErr
	}
	return renewalSats, nil
}

func newIntentMerchant(t *testing.T, wallet *intentWallet, storeDir string) *Merchant {
	t.Helper()

	cm, _ := setupTestConfigManager(t)
	m := &Merchant{
		config: &config_manager.Config{
			Metric:   "milliseconds",
			StepSize: renewalStepMS,
			AcceptedMints: []config_manager.MintConfig{
				{URL: renewalMintURL, PricePerStep: 1, PriceUnit: "sat", MinPurchaseSteps: 1},
			},
		},
		configManager:     cm,
		tollwallet:        wallet,
		mintHealthTracker: newTestTracker(cm.GetConfig(), nil),
		customerSessions:  make(map[string]*CustomerSession),
		expiredSessions:   make(map[string]int64),
		receiveIntents:    make(map[string]*receiveIntentRecord),
	}
	if storeDir != "" {
		m.receiveIntentStore = newReceiveIntentStore(filepath.Join(storeDir, "receive-intents.json"))
	}
	// Compress the owed-grant monitor's retry clock (#733): the convergence
	// deadlines below must not be coupled to the production 5 s first-attempt
	// delay — under a loaded -race full-suite run that coupling is what made
	// this row time out once in three suite runs while never reproducing in
	// isolation.
	m.owedGrantRetryBase = 10 * time.Millisecond
	m.owedGrantRetryCap = 40 * time.Millisecond
	retireOwedMonitorsOnCleanup(t, m)
	_ = valve.CloseGate(intentMAC)
	return m
}

func readIntentStore(t *testing.T, dir string) map[string]*persistedReceiveIntent {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "receive-intents.json"))
	if err != nil {
		t.Fatalf("read receive-intents store: %v", err)
	}
	var intents map[string]*persistedReceiveIntent
	if err := jsonUnmarshalIntent(data, &intents); err != nil {
		t.Fatalf("decode receive-intents store: %v", err)
	}
	return intents
}

// TestReceiveIntentIsDurableBeforeTheMoneyMoves pins persist-before-effect:
// with Receive blocked mid-call, the journal already holds a pending record
// for the reference on disk — the crash window cannot orphan the attempt.
func TestReceiveIntentIsDurableBeforeTheMoneyMoves(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	dir := t.TempDir()
	w := &intentWallet{receiveStarted: make(chan struct{}), receiveRelease: make(chan struct{})}
	m := newIntentMerchant(t, w, dir)

	go func() { _, _ = m.PurchaseSession("cashuBreceive-intent-fixture", intentMAC) }()

	select {
	case <-w.receiveStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("Receive was never reached")
	}

	intents := readIntentStore(t, dir)
	if len(intents) != 1 {
		t.Fatalf("expected exactly one journaled intent while Receive is in flight, got %d", len(intents))
	}
	for ref, rec := range intents {
		if rec.State != intentStatePending {
			t.Fatalf("in-flight intent must be pending, got %q", rec.State)
		}
		if rec.TokenSerialized == "" {
			t.Fatal("the intent must carry the serialized token — reconciliation after a restart needs the proof secrets")
		}
		if ref == "" {
			t.Fatal("intent keyed by an empty reference")
		}
	}
	close(w.receiveRelease)
}

// TestKilledAttemptConvergesAfterRestart is the kill-boundary row
// (pay-kill-post-receive-pre-session) at unit scale: the process "dies"
// with the intent pending and the mint having taken the note; a fresh
// merchant over the same store reconciles on NUT-07 and grants exactly
// once.
func TestKilledAttemptConvergesAfterRestart(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	dir := t.TempDir()

	// First process: journal the attempt, "die" before the result.
	w1 := &intentWallet{receiveStarted: make(chan struct{}), receiveRelease: make(chan struct{})}
	m1 := newIntentMerchant(t, w1, dir)
	go func() { _, _ = m1.PurchaseSession("cashuBreceive-intent-fixture", intentMAC) }()
	<-w1.receiveStarted
	// No release: the attempt dies pending, exactly like docker kill.
	if got := len(readIntentStore(t, dir)); got != 1 {
		t.Fatalf("the journal must survive the kill with one pending intent, got %d", got)
	}

	// The mint took the note (the fault lab processed-then-dropped shape).
	second := &intentWallet{}
	second.spent.Store(true)
	m2 := newIntentMerchant(t, second, dir)
	m2.loadReceiveIntentsFromDisk()

	waitForOwed2(t, m2, receiveReferenceOf(t), 10*time.Second)
	m2.receiveIntentMu.Lock()
	var state string
	if rec, ok := m2.receiveIntents[receiveReferenceOf(t)]; ok && rec != nil {
		state = rec.State
	}
	m2.receiveIntentMu.Unlock()
	if state != intentStateOwed {
		t.Fatalf("a spent proof-set must resolve the intent to owed, got %q", state)
	}

	// And the owed machinery delivers exactly one session for one payment.
	waitForIntentSession(t, m2, renewalSats*renewalStepMS, 25*time.Second)
	if got := second.receiveCalls.Load(); got != 0 {
		t.Fatalf("reconciliation must never re-send the note to the mint; Receive called %d times on the second process", got)
	}
}

// TestResubmissionAfterAmbiguityConvergesWithoutASecondSpend: the customer
// retries a note whose first attempt died ambiguously; the resubmission
// reconciles on evidence (spent), refuses a second Receive, and the owed
// grant delivers the one session the note paid for.
func TestResubmissionAfterAmbiguityConvergesWithoutASecondSpend(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	dir := t.TempDir()

	w := &intentWallet{}
	w.spent.Store(true)
	m := newIntentMerchant(t, w, dir)
	// Simulate the dead attempt directly: pending on disk, no live call.
	reference := receiveReferenceOf(t)
	m.beginReceiveIntent(reference, intentMAC, renewalMintURL, renewalSats, "cashuBreceive-intent-fixture", "milliseconds")

	event, err := m.PurchaseSession("cashuBreceive-intent-fixture", intentMAC)
	if err != nil {
		t.Fatalf("PurchaseSession: %v", err)
	}
	if code := noticeErrorCode(t, event); code != "payment-received-grant-pending" {
		t.Fatalf("resubmission of a received note must answer grant-pending, got %q", code)
	}
	if got := w.receiveCalls.Load(); got != 0 {
		t.Fatalf("no second Receive may be issued for a note the mint already took; got %d", got)
	}
	if got := w.checkCalls.Load(); got == 0 {
		t.Fatal("the resubmission must reconcile on NUT-07 evidence")
	}
	waitForIntentSession(t, m, renewalSats*renewalStepMS, 25*time.Second)
}

// TestUnspentAmbiguityAbandonsAndAllowsAFreshSpend: when the evidence says
// the mint never took the note, the intent abandons and a resubmission
// proceeds as a fresh payment (the customer's note is still spendable).
func TestUnspentAmbiguityAbandonsAndAllowsAFreshSpend(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)
	dir := t.TempDir()

	// The prior attempt "died ambiguously" — the journal holds it pending;
	// the wallet's checkstate will answer unspent. The fresh Receive must
	// succeed normally.
	w := &intentWallet{}
	m := newIntentMerchant(t, w, dir)
	reference := receiveReferenceOf(t)
	m.beginReceiveIntent(reference, intentMAC, renewalMintURL, renewalSats, "cashuBreceive-intent-fixture", "milliseconds")

	m.reconcileReceiveIntent(reference)

	m.receiveIntentMu.Lock()
	rec := m.receiveIntents[reference]
	m.receiveIntentMu.Unlock()
	if rec.State != intentStateAbandoned {
		t.Fatalf("all-unspent evidence must abandon the intent, got %q", rec.State)
	}

	// The abandoned reference frees the resubmission: a fresh Receive runs.
	event, err := m.PurchaseSession("cashuBreceive-intent-fixture", intentMAC)
	if err != nil {
		t.Fatalf("PurchaseSession after abandon: %v", err)
	}
	if event.Kind != 1022 {
		t.Fatalf("the fresh payment must grant a session (kind 1022), got kind %d", event.Kind)
	}
	if got := w.receiveCalls.Load(); got != 1 {
		t.Fatalf("exactly one fresh Receive must run, got %d", got)
	}
}

// helpers -------------------------------------------------------------

func jsonUnmarshalIntent(data []byte, v *map[string]*persistedReceiveIntent) error {
	return json.Unmarshal(data, v)
}

func receiveReferenceOf(t *testing.T) string {
	t.Helper()
	ref := receiveReference(intentNote{})
	if ref == "" {
		t.Fatal("fixture note produced no reference")
	}
	return ref
}

func waitForOwed2(t *testing.T, m *Merchant, reference string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		state := func() string {
			m.receiveIntentMu.Lock()
			defer m.receiveIntentMu.Unlock()
			if rec, ok := m.receiveIntents[reference]; ok && rec != nil {
				return rec.State
			}
			return ""
		}()
		if state != "" && state != intentStatePending {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatalf("intent never resolved within %s", d)
}

func waitForIntentSession(t *testing.T, m *Merchant, allotment uint64, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if s, err := m.GetSession(intentMAC); err == nil {
			if s.Allotment != allotment {
				t.Fatalf("exactly one allotment expected: got %d, want %d", s.Allotment, allotment)
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the owed entitlement never granted a session within %s", d)
}
