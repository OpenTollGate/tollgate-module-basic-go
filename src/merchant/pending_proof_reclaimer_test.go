package merchant

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// fakeReclaimer embeds a nil WalletPort so it satisfies the merchant's
// wallet field type; only the reclaimer surface is ever exercised here.
type fakeReclaimer struct {
	tollwallet.WalletPort
	pending         atomic.Uint64
	reclaimCalls    atomic.Int32
	failNextReclaim atomic.Bool
}

func (f *fakeReclaimer) PendingBalance() uint64 { return f.pending.Load() }

func (f *fakeReclaimer) ReclaimPendingProofs() (uint64, error) {
	f.reclaimCalls.Add(1)
	if f.failNextReclaim.Swap(false) {
		return 0, errors.New("mint unreachable (simulated)")
	}
	reclaimed := f.pending.Swap(0)
	return reclaimed, nil
}

func newReclaimerTestMerchant(t *testing.T, wallet *fakeReclaimer) *Merchant {
	t.Helper()
	return &Merchant{tollwallet: wallet}
}

func TestReclaimerReclaimsPendingProofsAfterBoot(t *testing.T) {
	oldDelay, oldInterval := reclaimerBootDelay, reclaimerInterval
	reclaimerBootDelay, reclaimerInterval = 5*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { reclaimerBootDelay, reclaimerInterval = oldDelay, oldInterval })

	fake := &fakeReclaimer{}
	fake.pending.Store(64)
	m := newReclaimerTestMerchant(t, fake)

	m.StartPendingProofReclaimer()

	if !waitFor(t, 2*time.Second, func() bool { return fake.pending.Load() == 0 }) {
		t.Fatalf("pending proofs were not reclaimed; pending=%d calls=%d",
			fake.pending.Load(), fake.reclaimCalls.Load())
	}
	if got := fake.reclaimCalls.Load(); got < 1 {
		t.Fatalf("expected at least one reclaim call, got %d", got)
	}
}

func TestReclaimerSkipsWhenNothingPending(t *testing.T) {
	oldDelay, oldInterval := reclaimerBootDelay, reclaimerInterval
	reclaimerBootDelay, reclaimerInterval = 5*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { reclaimerBootDelay, reclaimerInterval = oldDelay, oldInterval })

	fake := &fakeReclaimer{}
	m := newReclaimerTestMerchant(t, fake)

	m.StartPendingProofReclaimer()
	time.Sleep(50 * time.Millisecond)

	if got := fake.reclaimCalls.Load(); got != 0 {
		t.Fatalf("reclaim must not fire when nothing is pending; calls=%d", got)
	}
}

func TestReclaimerSurvivesErrorsAndRetries(t *testing.T) {
	oldDelay, oldInterval := reclaimerBootDelay, reclaimerInterval
	reclaimerBootDelay, reclaimerInterval = 5*time.Millisecond, 10*time.Millisecond
	t.Cleanup(func() { reclaimerBootDelay, reclaimerInterval = oldDelay, oldInterval })

	fake := &fakeReclaimer{}
	fake.pending.Store(21)
	fake.failNextReclaim.Store(true)
	m := newReclaimerTestMerchant(t, fake)

	m.StartPendingProofReclaimer()

	if !waitFor(t, 2*time.Second, func() bool { return fake.pending.Load() == 0 }) {
		t.Fatalf("reclaimer did not converge after an error; pending=%d calls=%d",
			fake.pending.Load(), fake.reclaimCalls.Load())
	}
	if got := fake.reclaimCalls.Load(); got < 2 {
		t.Fatalf("expected a retry after the failed attempt; calls=%d", got)
	}
}

func TestReclaimerNoopOnUnsupportedBackend(t *testing.T) {
	m := &Merchant{}
	m.StartPendingProofReclaimer()
	time.Sleep(20 * time.Millisecond)
}
