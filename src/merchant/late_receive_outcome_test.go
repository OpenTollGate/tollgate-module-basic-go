package merchant

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/utils"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/valve"
	"github.com/nbd-wtf/go-nostr"
)

// The outcome of a `Receive` that returns AFTER its deadline.
//
// `PurchaseSession` answers the customer at the deadline and moves on: a
// money-moving `Receive` is deliberately not cancelled, because a timeout is an
// ambiguous outcome rather than a failure — the mint may still complete the swap
// and take the note. The notice says so honestly and hands the customer a
// reference (the salted fingerprint of the note) to quote.
//
// Nothing read the Receive channel once the deadline had fired, so the answer
// was dropped in silence: a late success (the mint took the note, the customer
// has no session) and a late failure (the note is untouched) looked identical to
// an operator, and the reference the notice gave out led to a log line that says
// only "outcome unknown". These tests pin the record that makes the reference
// worth quoting.
//
// What they do NOT pin is the money: nothing here grants or refunds a late
// outcome, which is the journal card's job. The record must therefore also not
// read as if the customer had been served.

// lateOutcomeNote is the note the customer submitted. Only its serialized form
// matters here; it is deliberately low-entropy — a fixture must not look like a
// live credential — and distinctive enough to assert its absence from the log.
type lateOutcomeNote struct{ tollwallet.Token }

func (lateOutcomeNote) Mint() string               { return "https://late-outcome.example.com" }
func (lateOutcomeNote) Amount() uint64             { return 1 }
func (lateOutcomeNote) Serialize() (string, error) { return lateOutcomeSerialized, nil }
func (lateOutcomeNote) Close()                     {}

const lateOutcomeSerialized = "cashuB" + "late-outcome-note-late-outcome-note-late-outcome-note-"

// completingReceiveWallet reaches `Receive` and does not answer until the test
// releases it, so the completion lands strictly after the response deadline.
type completingReceiveWallet struct {
	tollwallet.WalletPort
	started chan struct{}
	release chan struct{}
	amount  uint64
	err     error
}

func (w *completingReceiveWallet) DecodeToken(string) (tollwallet.Token, error) {
	return lateOutcomeNote{}, nil
}

// CheckTokenSpent answers unspent: the late-outcome harness drives evidence
// through the Receive result, not through NUT-07, and the nil embedded
// WalletPort would panic on the compiler-generated wrapper.
func (w *completingReceiveWallet) CheckTokenSpent(tollwallet.Token) (bool, error) {
	return false, nil
}
func (w *completingReceiveWallet) SwapFeeSats(tollwallet.Token) (uint64, error) { return 0, nil }
func (w *completingReceiveWallet) Receive(tollwallet.Token) (uint64, error) {
	close(w.started)
	<-w.release
	return w.amount, w.err
}

// syncLogs is a concurrency-safe log sink: the record under test is written by
// the goroutine that is still inside `Receive`, so the test reads the buffer
// while another goroutine may be appending to it.
type syncLogs struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *syncLogs) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *syncLogs) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// Len reports the number of buffered bytes under the same lock as Write, so a
// test can mark a position in the capture while goroutines may still be
// logging (bytes read this way cannot race with the writers).
func (l *syncLogs) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Len()
}

func captureSyncLogs(t *testing.T) *syncLogs {
	t.Helper()

	logs := &syncLogs{}
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(logs)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})

	return logs
}

// waitForLogLines waits for at least `want` log lines containing substr and
// returns them. Waiting for a count, not for the first match, matters here: the
// deadline record already names the reference, so a first-match wait would
// return at the deadline and never observe the late outcome — the very record
// under test, which is written afterwards.
func waitForLogLines(t *testing.T, logs *syncLogs, substr string, want int, within time.Duration) []string {
	t.Helper()

	deadline := time.Now().Add(within)
	var lines []string
	for {
		lines = nil
		for _, line := range strings.Split(logs.String(), "\n") {
			if strings.Contains(line, substr) {
				lines = append(lines, line)
			}
		}
		if len(lines) >= want || !time.Now().Before(deadline) {
			return lines
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// lateOutcomeCase wires a merchant whose `Receive` blocks until release() is
// called, so the test owns the moment the money-moving call answers.
func lateOutcomeCase(t *testing.T, amount uint64, receiveErr error) (*Merchant, *completingReceiveWallet, func(), *syncLogs) {
	t.Helper()

	cm, _ := setupTestConfigManager(t)
	wallet := &completingReceiveWallet{
		started: make(chan struct{}),
		release: make(chan struct{}),
		amount:  amount,
		err:     receiveErr,
	}
	cfg := cm.GetConfig()
	cfg.AcceptedMints = append(cfg.AcceptedMints, config_manager.MintConfig{
		URL: "https://late-outcome.example.com", PricePerStep: 1, PriceUnit: "sat", MinPurchaseSteps: 1,
	})
	m := &Merchant{
		config:             cfg,
		configManager:      cm,
		tollwallet:         wallet,
		mintHealthTracker:  newTestTracker(cm.GetConfig(), nil),
		receiveIntents:     make(map[string]*receiveIntentRecord),
		receiveIntentStore: newReceiveIntentStore(filepath.Join(t.TempDir(), "receive-intents.json")),
	}
	var once sync.Once
	release := func() { once.Do(func() { close(wallet.release) }) }

	stubPreflightProbe(t, m, func(string) (valve.ClientState, error) {
		return valve.ClientState{Registered: true}, nil
	})
	prevTimeout := receiveTimeout
	receiveTimeout = 150 * time.Millisecond
	t.Cleanup(func() { receiveTimeout = prevTimeout })
	drainOwedGrantMonitors(t, m)

	logs := captureSyncLogs(t)

	// A late SUCCESS owes the customer their grant, and the recorder pays that
	// debt on its own goroutine: log the COMPLETED record, then attempt the
	// grant — a 5-attempt auth retry storm taking ~2 s. Release is deferred to
	// cleanup for tests that forget it, and once it fires the storm must be
	// DRAINED HERE, inside this test's cleanup: no later test's PATH-scoped
	// fake is live yet, so the failing execs resolve nothing and write no AUTH
	// lines anywhere. Without the drain the storm ran on into the next tests
	// and its `ndsctl auth` attempts landed in whichever fake was current —
	// the 1-in-8 stray-AUTH failure of TestFirstTimePurchaseStill... in the
	// #733 soak ledger. The owed-grant ERROR line is logged after the grant
	// attempt fails, so it is the storm-has-ended signal (success variants
	// only; a late failure owes nothing and records nothing).
	if amount >= 1 {
		t.Cleanup(func() {
			release()
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if strings.Contains(logs.String(), "could not be granted") {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		})
	} else {
		t.Cleanup(release)
	}

	return m, wallet, release, logs
}

func awaitReceiveStarted(t *testing.T, wallet *completingReceiveWallet) {
	t.Helper()

	select {
	case <-wallet.started:
	case <-time.After(5 * time.Second):
		t.Fatal("Receive was never reached, so the late-outcome window was not exercised")
	}
}

// lateOutcomeRecord picks the record that answers "what did the mint do with the
// note?" out of the lines naming the reference. The "calling Receive" record and
// the deadline record name the reference too; neither is an outcome.
func lateOutcomeRecord(lines []string) string {
	var record string
	for _, line := range lines {
		if strings.Contains(line, "token_amount=") || strings.Contains(line, "outcome unknown") {
			continue
		}
		record = line
	}
	return record
}

// submitLateReceive drives the real payment path while `Receive` is still in
// flight and returns the notice the customer received.
func submitLateReceive(t *testing.T, m *Merchant, wallet *completingReceiveWallet) *nostr.Event {
	t.Helper()

	event, err := m.PurchaseSession("cashuBsubmitted-note", "AA:BB:CC:DD:EE:FF")
	if err != nil {
		t.Fatalf("PurchaseSession returned an error instead of a notice: %v", err)
	}
	if event == nil || event.Kind != 21023 {
		t.Fatalf("expected a kind-21023 notice, got %+v", event)
	}
	awaitReceiveStarted(t, wallet)
	return event
}

func TestLateReceiveOutcomeIsRecordedWhenReceiveCompletesAfterTheDeadline(t *testing.T) {
	m, wallet, release, logs := lateOutcomeCase(t, 1, nil)

	event := submitLateReceive(t, m, wallet)

	reference := utils.TokenFingerprint(lateOutcomeSerialized)
	if reference == "" {
		t.Fatal("the fingerprint helper returned nothing for a real note")
	}
	if !strings.Contains(event.Content, reference) {
		t.Errorf("notice %q does not carry the reference %q the operator is asked to act on", event.Content, reference)
	}
	if !regexp.MustCompile(`[0-9a-f]{16}`).MatchString(event.Content) {
		t.Errorf("notice %q carries no 16-hex reference at all", event.Content)
	}

	// The customer has been answered at the deadline; the money-moving call is
	// still in flight at this instant, which is the whole difficulty. Completing
	// it now is the case the notice warns about.
	release()

	lines := waitForLogLines(t, logs, reference, 2, 5*time.Second)
	if len(lines) < 2 {
		t.Fatalf("the reference the notice carries appears on %d log line(s); the deadline record exists but the late outcome was dropped in silence, so the reference leads the operator nowhere:\n%s",
			len(lines), logs.String())
	}
	record := lateOutcomeRecord(lines)
	if record == "" {
		t.Fatalf("no log line records what the mint did with the note late:\n%s", strings.Join(lines, "\n"))
	}
	t.Logf("late outcome record: %s", record)

	if !strings.Contains(record, "amount=") {
		t.Errorf("the late record does not say what the mint credited: %q", record)
	}
	if !strings.Contains(record, "owed entitlement") {
		t.Errorf("the late record does not state the #502 contract — a late success records the owed entitlement: %q", record)
	}
	if strings.Contains(record, "FAILED") {
		t.Errorf("a successful late Receive is recorded as a failure: %q", record)
	}
	if strings.Contains(logs.String(), lateOutcomeSerialized) {
		t.Errorf("the spendable note is in the log output:\n%s", logs.String())
	}
}

func TestLateReceiveFailureAfterTheDeadlineIsRecordedAsAFailure(t *testing.T) {
	m, wallet, release, logs := lateOutcomeCase(t, 0, errors.New("mint refused the swap"))

	event := submitLateReceive(t, m, wallet)
	reference := utils.TokenFingerprint(lateOutcomeSerialized)
	if !strings.Contains(event.Content, reference) {
		t.Errorf("notice %q does not carry the reference %q", event.Content, reference)
	}

	release()

	lines := waitForLogLines(t, logs, reference, 2, 5*time.Second)
	if len(lines) < 2 {
		t.Fatalf("the reference the notice carries appears on %d log line(s); a late failure and a late success must not look the same to an operator:\n%s",
			len(lines), logs.String())
	}
	record := lateOutcomeRecord(lines)
	if record == "" {
		t.Fatalf("no log line records the late failure:\n%s", strings.Join(lines, "\n"))
	}
	t.Logf("late outcome record: %s", record)

	if !strings.Contains(record, "mint refused the swap") {
		t.Errorf("the late failure record does not carry the mint's error: %q", record)
	}
	if !strings.Contains(record, "reconciling on NUT-07 evidence") {
		t.Errorf("the late failure record does not state the #502 contract — ambiguous failures reconcile on evidence: %q", record)
	}
	// And the reconciliation decided: the stub answers unspent, so the
	// intent must leave pending (abandoned) within the window.
	waitForIntentResolved(t, m, reference, 5*time.Second)
}

// The other half of the same question, and the finding this test was added for:
// an error that surfaces *after* the deadline is not automatically evidence that
// the mint refused the note. The module's deadline and the wallet's own HTTP
// client timeout are both 30 s, so the first answer to arrive late is normally
// the client-side one (`context deadline exceeded`), and a mint 5xx answered
// after it processed the swap is ambiguous the same way. Both mean the mint may
// well have taken the note. Stamping "the mint did not take the note" there is
// exactly the confidently-wrong label on the case the record exists to
// disambiguate: the operator tells the customer to resubmit, the retry is
// refused as already-spent, and the value is gone with no session — the loss
// this whole record was written to prevent.
func TestLateReceiveTimeoutIsRecordedAsAmbiguousNotAsDefinitelyNotTaken(t *testing.T) {
	// The shape the wallet answers with when its 30 s client gives up on a swap
	// POST that the mint may already have processed.
	timeoutErr := fmt.Errorf("Post %q: %w (Client.Timeout exceeded while awaiting headers)",
		"https://late-outcome.example.com/v1/swap", context.DeadlineExceeded)
	m, wallet, release, logs := lateOutcomeCase(t, 0, timeoutErr)

	event := submitLateReceive(t, m, wallet)
	reference := utils.TokenFingerprint(lateOutcomeSerialized)
	if !strings.Contains(event.Content, reference) {
		t.Errorf("notice %q does not carry the reference %q", event.Content, reference)
	}

	release()

	lines := waitForLogLines(t, logs, reference, 2, 5*time.Second)
	if len(lines) < 2 {
		t.Fatalf("the reference the notice carries appears on %d log line(s); the late outcome was dropped in silence:\n%s",
			len(lines), logs.String())
	}
	record := lateOutcomeRecord(lines)
	if record == "" {
		t.Fatalf("no log line records the late timeout:\n%s", strings.Join(lines, "\n"))
	}
	t.Logf("late outcome record: %s", record)

	if !strings.Contains(record, "deadline exceeded") {
		t.Errorf("the late record does not carry the wallet's error: %q", record)
	}
	if !strings.Contains(record, "ambiguous") {
		t.Errorf("a timeout-class late error leaves the note's fate undecided, yet the record does not say ambiguous: %q", record)
	}
	if !strings.Contains(record, "reconciling on NUT-07 evidence") {
		t.Errorf("the late record does not state the #502 contract — ambiguous outcomes reconcile on evidence, not operator guesswork: %q", record)
	}
}

// The mirror: when the mint *did* answer and the answer is a refusal that no
// outage can produce (the note is already spent, it sits on a retired keyset,
// or it cannot cover the swap fee), the note really is untouched and the
// original wording is the honest one — the customer can submit it again.
func TestLateReceiveDefinitiveRejectionKeepsTheNotTakenWording(t *testing.T) {
	m, wallet, release, logs := lateOutcomeCase(t, 0,
		fmt.Errorf("swap rejected: %w", tollwallet.ErrTokenAlreadySpent))

	event := submitLateReceive(t, m, wallet)
	reference := utils.TokenFingerprint(lateOutcomeSerialized)
	if !strings.Contains(event.Content, reference) {
		t.Errorf("notice %q does not carry the reference %q", event.Content, reference)
	}

	release()

	lines := waitForLogLines(t, logs, reference, 2, 5*time.Second)
	if len(lines) < 2 {
		t.Fatalf("the reference the notice carries appears on %d log line(s):\n%s", len(lines), logs.String())
	}
	record := lateOutcomeRecord(lines)
	if record == "" {
		t.Fatalf("no log line records the late rejection:\n%s", strings.Join(lines, "\n"))
	}
	t.Logf("late outcome record: %s", record)

	if !strings.Contains(record, "did not take the note") {
		t.Errorf("a definitive mint refusal should keep the 'the mint did not take the note' wording: %q", record)
	}
	if strings.Contains(record, "ambiguous") {
		t.Errorf("a definitive mint refusal is hedged as ambiguous, so the operator cannot safely tell the customer to resubmit: %q", record)
	}
	// The #502 contract: a definitive mint refusal abandons the attempt —
	// no session, the note stays spendable, a resubmission proceeds fresh.
	waitForIntentResolved(t, m, reference, 5*time.Second)
	m.receiveIntentMu.Lock()
	state := m.receiveIntents[reference].State
	m.receiveIntentMu.Unlock()
	if state != intentStateAbandoned {
		t.Errorf("a definitive late rejection must abandon the intent, got %q", state)
	}
}

// waitForIntentResolved fails when the reference's intent is still pending
// after the window — the NUT-07 reconciler must decide, not leave the
// attempt hanging.
func waitForIntentResolved(t *testing.T, m *Merchant, reference string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		m.receiveIntentMu.Lock()
		rec, ok := m.receiveIntents[reference]
		m.receiveIntentMu.Unlock()
		if ok && rec != nil && rec.State != intentStatePending {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("intent %s never left pending — the NUT-07 reconciler did not decide", reference)
}
