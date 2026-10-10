package merchant

import (
	"testing"
	"time"
)

// The boot-ordering race the crash lane proved (#793 rel-verify,
// Appendix B intervention): the merchant's NUT-07 receive-intent reconcile
// and the wallet's boot pending-swap replay run concurrently after a
// restart. For an intent whose proofs the wallet journaled a pending swap
// for, a checkstate that wins the race answers UNSPENT (the replay has not
// re-POSTed yet) and the intent abandons — the customer keeps a dead note,
// gets nothing, while the operator's replay recovers the value moments
// later. The fix under test: the reconcile sequences itself after the
// wallet's boot replay pass whenever the wallet exposes the completion
// channel. The matrix the review demanded:
//
//	succeeded replay   -> checkstate spent   -> owed (customer converges)
//	replay done, unspent evidence            -> abandoned (never a false owed)
//	no replay seam (cdk/sidecar shape)       -> decides immediately, as before

type replayGateWallet struct {
	intentWallet
	replayDone chan struct{}
}

func (w *replayGateWallet) BootSwapReplayDone() <-chan struct{} { return w.replayDone }

func TestReconcileSequencesAfterBootSwapReplay(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)

	w := &replayGateWallet{replayDone: make(chan struct{})}
	m := newIntentMerchant(t, w, t.TempDir())
	reference := receiveReferenceOf(t)
	m.beginReceiveIntent(reference, intentMAC, renewalMintURL, renewalSats, "cashuBreceive-intent-fixture", "milliseconds")

	done := make(chan struct{})
	go func() {
		m.reconcileReceiveIntent(reference)
		close(done)
	}()

	// While the boot replay pass is in flight, the reconcile must not ask
	// the mint at all: an unspent answer in this window is not stable
	// evidence, and deciding on it is exactly the shipped race.
	select {
	case <-done:
		t.Fatal("reconcile decided while the boot swap replay was still in flight")
	case <-time.After(150 * time.Millisecond):
	}
	if got := w.checkCalls.Load(); got != 0 {
		t.Fatalf("checkstate must not fire before the replay pass completes, got %d call(s)", got)
	}

	// The replay lands (the mint takes the note) and the pass completes:
	// now the checkstate is evidence, and it must see the post-replay
	// state — spent — resolving owed and converging the customer.
	w.spent.Store(true)
	close(w.replayDone)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("reconcile never decided after the replay pass completed")
	}
	if got := w.checkCalls.Load(); got != 1 {
		t.Fatalf("exactly one post-replay checkstate expected, got %d", got)
	}
	assertIntentState(t, m, reference, intentStateOwed)
	waitForIntentSession(t, m, renewalSats*renewalStepMS, 25*time.Second)
}

func TestReconcileAbandonsOnUnspentOnlyAfterReplayDone(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)

	// The pass has finished and the evidence is genuinely unspent (the
	// beyond-window / never-processed shapes): abandoned is the right
	// resolution and the sequencing must not manufacture a false owed.
	w := &replayGateWallet{replayDone: make(chan struct{})}
	close(w.replayDone)
	m := newIntentMerchant(t, w, t.TempDir())
	reference := receiveReferenceOf(t)
	m.beginReceiveIntent(reference, intentMAC, renewalMintURL, renewalSats, "cashuBreceive-intent-fixture", "milliseconds")

	m.reconcileReceiveIntent(reference)

	if got := w.checkCalls.Load(); got != 1 {
		t.Fatalf("one checkstate expected once the pass is done, got %d", got)
	}
	assertIntentState(t, m, reference, intentStateAbandoned)
}

func TestReconcileWithoutReplaySeamDecidesImmediately(t *testing.T) {
	ndsctl := installRenewalNdsctl(t)
	ndsctl.setRegistered(t, true)

	// A wallet that does not expose the channel (the cdk and sidecar
	// shapes, which resume at open or own their intents) keeps the prior
	// behavior: the evidence decides, with no sequencing gate.
	w := &intentWallet{}
	m := newIntentMerchant(t, w, t.TempDir())
	reference := receiveReferenceOf(t)
	m.beginReceiveIntent(reference, intentMAC, renewalMintURL, renewalSats, "cashuBreceive-intent-fixture", "milliseconds")

	m.reconcileReceiveIntent(reference)

	if got := w.checkCalls.Load(); got != 1 {
		t.Fatalf("one checkstate expected, got %d", got)
	}
	assertIntentState(t, m, reference, intentStateAbandoned)
}

func assertIntentState(t *testing.T, m *Merchant, reference, want string) {
	t.Helper()
	m.receiveIntentMu.Lock()
	rec, ok := m.receiveIntents[reference]
	m.receiveIntentMu.Unlock()
	if !ok || rec == nil {
		t.Fatalf("intent %s not found in the journal", reference)
	}
	if rec.State != want {
		t.Fatalf("intent %s state = %q, want %q", reference, rec.State, want)
	}
}
