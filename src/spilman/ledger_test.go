package spilman

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// The tests below exercise the durable CAS arithmetic in isolation, through the
// unexported helper that only RecordPayment may call after authenticating. They
// deliberately use a fake signature ("sig-N"): they are about the WRITE, not
// about authenticity, which forgery_test.go and (tagged) payment_auth_cgo_test.go
// cover against the real validator.
//
// A channel row is still required, because the helper does not invent one and
// because the real path only ever writes for a registered channel.
func openCASLedger(t *testing.T) *ChannelLedger {
	t.Helper()
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ledger.Close()) })
	require.NoError(t, ledger.RegisterChannel(context.Background(), ChannelRecord{
		ChannelID: "chan-1",
		Unit:      UnitSat,
		State:     ChannelStateOpen,
		Capacity:  1000,
		Expiry:    1_900_000_000,
		OpenedAt:  1_700_000_000,
	}))
	return ledger
}

func TestRecordPaymentAcceptsTheExactNextBalance(t *testing.T) {
	ledger := openCASLedger(t)

	outcome, err := ledger.recordAuthenticatedPayment("chan-1", 1, "sig-1")
	require.NoError(t, err)
	require.Equal(t, LedgerOutcomeAccepted, outcome)

	outcome, err = ledger.recordAuthenticatedPayment("chan-1", 2, "sig-2")
	require.NoError(t, err)
	require.Equal(t, LedgerOutcomeAccepted, outcome)

	latest, err := ledger.LatestPayment("chan-1")
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.Equal(t, uint64(2), latest.Balance)
	require.Equal(t, "sig-2", latest.Signature)
	require.Equal(t, uint64(2), latest.PaymentCount)
}

// A balance that SKIPS AHEAD (more than one step past the stored value) is
// ACCEPTED, and this test pins that deliberately rather than leaving it as
// prose in the ledger's doc comment.
//
// The guard is a strict-advance predicate (`stored < offered`), not a
// compare-and-swap against a caller-supplied "expected previous" balance: the
// caller never supplies one, and nothing in the SQL compares against a value
// the caller expected. So offering 5 when 1 is stored is accepted, not refused.
//
// That is the right trade for this rail — the offered balance is signed by the
// payer, so it is self-authenticating for the amount it names, and a higher
// balance is never worse for the seller than the stored one. It is NOT a licence
// to describe the guard as a CAS: a CAS would refuse this write instead.
//
// If this test ever fails because the write was refused, the guard became
// stricter than documented and the ledger doc comment (and any PR text calling
// it a monotonic guard) must be corrected with it.
func TestRecordPaymentAcceptsASkipAheadBalance(t *testing.T) {
	ledger := openCASLedger(t)

	outcome, err := ledger.recordAuthenticatedPayment("chan-1", 1, "sig-1")
	require.NoError(t, err)
	require.Equal(t, LedgerOutcomeAccepted, outcome)

	outcome, err = ledger.recordAuthenticatedPayment("chan-1", 5, "sig-5")
	require.NoError(t, err, "a skip-ahead balance is accepted by the strict-advance guard")
	require.Equal(t, LedgerOutcomeAccepted, outcome)

	latest, err := ledger.LatestPayment("chan-1")
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.Equal(t, uint64(5), latest.Balance)
	require.Equal(t, "sig-5", latest.Signature)
	require.Equal(t, uint64(2), latest.PaymentCount)
}

// A stale (lower) balance must be refused: honouring it would roll back credit
// the payee already served bytes for.
func TestRecordPaymentRejectsStaleBalance(t *testing.T) {
	ledger := openCASLedger(t)

	_, err := ledger.recordAuthenticatedPayment("chan-1", 5, "sig-5")
	require.NoError(t, err)

	_, err = ledger.recordAuthenticatedPayment("chan-1", 4, "sig-4")
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	var notAdvanced *BalanceNotAdvancedError
	require.True(t, errors.As(err, &notAdvanced))
	require.Equal(t, "chan-1", notAdvanced.ChannelID)
	require.Equal(t, uint64(5), notAdvanced.Stored)
	require.Equal(t, uint64(4), notAdvanced.Offered)

	latest, err := ledger.LatestPayment("chan-1")
	require.NoError(t, err)
	require.Equal(t, uint64(5), latest.Balance)
	require.Equal(t, uint64(1), latest.PaymentCount, "a refused payment is not accepted funds")
}

// Replay of the identical payment: same balance, same signature.
func TestRecordPaymentRejectsReplay(t *testing.T) {
	ledger := openCASLedger(t)

	_, err := ledger.recordAuthenticatedPayment("chan-1", 7, "sig-7")
	require.NoError(t, err)
	_, err = ledger.recordAuthenticatedPayment("chan-1", 7, "sig-7")
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)
}

// The decisive case the monotonic max in integration-kits/go/stores.go
// (WHERE balance < ?) gets wrong in the other direction: a *different* payment
// carrying the same balance must not be credited twice.
func TestRecordPaymentRejectsReplayedBalanceWithDifferentSignature(t *testing.T) {
	ledger := openCASLedger(t)

	_, err := ledger.recordAuthenticatedPayment("chan-1", 9, "sig-a")
	require.NoError(t, err)
	_, err = ledger.recordAuthenticatedPayment("chan-1", 9, "sig-b")
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	latest, err := ledger.LatestPayment("chan-1")
	require.NoError(t, err)
	require.Equal(t, "sig-a", latest.Signature)
	require.Equal(t, uint64(1), latest.PaymentCount)
}

func TestZeroBalanceIsNeverRecorded(t *testing.T) {
	ledger := openCASLedger(t)

	_, err := ledger.recordAuthenticatedPayment("chan-1", 0, "sig-0")
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	_, known, err := ledger.Balance("chan-1")
	require.NoError(t, err)
	require.False(t, known, "no record must stay distinct from a zero balance")
}

// Exactly one of two racing writers may advance from the same previous balance.
func TestConcurrentWritersCannotBothAdvance(t *testing.T) {
	ledger := openCASLedger(t)

	_, err := ledger.recordAuthenticatedPayment("chan-1", 1, "sig-1")
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, sig := range []string{"sig-2a", "sig-2b"} {
		wg.Add(1)
		go func(i int, sig string) {
			defer wg.Done()
			_, errs[i] = ledger.recordAuthenticatedPayment("chan-1", 2, sig)
		}(i, sig)
	}
	wg.Wait()

	accepted := 0
	for _, err := range errs {
		if err == nil {
			accepted++
		} else {
			require.ErrorIs(t, err, ErrBalanceNotAdvanced)
		}
	}
	require.Equal(t, 1, accepted, "exactly one writer may win the CAS")

	latest, err := ledger.LatestPayment("chan-1")
	require.NoError(t, err)
	require.Equal(t, uint64(2), latest.Balance)
	require.Equal(t, uint64(2), latest.PaymentCount)
}

// The latest signature must survive a process restart: that signature is the
// only thing that lets the payee claim the channel at settle time.
func TestLedgerIsDurableAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "spilman-ledger.db")

	ledger, err := OpenLedgerWithValidator(path, newProtocolValidator())
	require.NoError(t, err)
	require.NoError(t, ledger.RegisterChannel(context.Background(), ChannelRecord{
		ChannelID: "chan-1", Unit: UnitSat, State: ChannelStateOpen,
		Capacity: 1000, Expiry: 1_900_000_000, OpenedAt: 1_700_000_000,
	}))
	_, err = ledger.recordAuthenticatedPayment("chan-1", 21, "sig-21")
	require.NoError(t, err)
	_, err = ledger.recordAuthenticatedPayment("chan-1", 42, "sig-42")
	require.NoError(t, err)
	require.NoError(t, ledger.Close())

	reopened, err := OpenLedgerWithValidator(path, newProtocolValidator())
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()

	latest, err := reopened.LatestPayment("chan-1")
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.Equal(t, uint64(42), latest.Balance)
	require.Equal(t, "sig-42", latest.Signature)
	require.Equal(t, uint64(2), latest.PaymentCount)

	// The CAS must still be anchored to the persisted previous balance.
	_, err = reopened.recordAuthenticatedPayment("chan-1", 42, "sig-42-replay")
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	_, err = reopened.recordAuthenticatedPayment("chan-1", 43, "sig-43")
	require.NoError(t, err)
}

func TestAmountDueReadsTheStoredBalance(t *testing.T) {
	ledger := openCASLedger(t)

	due, err := ledger.AmountDue("chan-unknown")
	require.NoError(t, err)
	require.Equal(t, uint64(0), due)

	_, err = ledger.recordAuthenticatedPayment("chan-1", 3, "sig-3")
	require.NoError(t, err)
	due, err = ledger.AmountDue("chan-1")
	require.NoError(t, err)
	require.Equal(t, uint64(3), due)

	count, err := ledger.PaymentCount("chan-unknown")
	require.NoError(t, err)
	require.Equal(t, uint64(0), count)
}
