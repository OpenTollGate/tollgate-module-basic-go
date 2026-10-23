package spilman

import (
	"errors"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRecordPaymentAcceptsTheExactNextBalance(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()

	outcome, err := ledger.RecordPayment("chan-1", 1, "sig-1")
	require.NoError(t, err)
	require.Equal(t, LedgerOutcomeAccepted, outcome)

	outcome, err = ledger.RecordPayment("chan-1", 2, "sig-2")
	require.NoError(t, err)
	require.Equal(t, LedgerOutcomeAccepted, outcome)

	latest, err := ledger.LatestPayment("chan-1")
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.Equal(t, uint64(2), latest.Balance)
	require.Equal(t, "sig-2", latest.Signature)
	require.Equal(t, uint64(2), latest.PaymentCount)
}

// A stale (lower) balance must be refused: honouring it would roll back credit
// the payee already served bytes for.
func TestRecordPaymentRejectsStaleBalance(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()

	_, err = ledger.RecordPayment("chan-1", 5, "sig-5")
	require.NoError(t, err)

	_, err = ledger.RecordPayment("chan-1", 4, "sig-4")
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
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()

	_, err = ledger.RecordPayment("chan-1", 7, "sig-7")
	require.NoError(t, err)
	_, err = ledger.RecordPayment("chan-1", 7, "sig-7")
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)
}

// The decisive case the monotonic max in integration-kits/go/stores.go
// (WHERE balance < ?) gets wrong in the other direction: a *different* payment
// carrying the same balance must not be credited twice.
func TestRecordPaymentRejectsReplayedBalanceWithDifferentSignature(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()

	_, err = ledger.RecordPayment("chan-1", 9, "sig-a")
	require.NoError(t, err)
	_, err = ledger.RecordPayment("chan-1", 9, "sig-b")
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	latest, err := ledger.LatestPayment("chan-1")
	require.NoError(t, err)
	require.Equal(t, "sig-a", latest.Signature)
	require.Equal(t, uint64(1), latest.PaymentCount)
}

func TestZeroBalanceIsNeverRecorded(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()

	_, err = ledger.RecordPayment("chan-1", 0, "sig-0")
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	_, known, err := ledger.Balance("chan-1")
	require.NoError(t, err)
	require.False(t, known, "no record must stay distinct from a zero balance")
}

// Exactly one of two racing writers may advance from the same previous balance.
func TestConcurrentWritersCannotBothAdvance(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()

	_, err = ledger.RecordPayment("chan-1", 1, "sig-1")
	require.NoError(t, err)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, sig := range []string{"sig-2a", "sig-2b"} {
		wg.Add(1)
		go func(i int, sig string) {
			defer wg.Done()
			_, errs[i] = ledger.RecordPayment("chan-1", 2, sig)
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

	ledger, err := OpenLedger(path)
	require.NoError(t, err)
	_, err = ledger.RecordPayment("chan-1", 21, "sig-21")
	require.NoError(t, err)
	_, err = ledger.RecordPayment("chan-1", 42, "sig-42")
	require.NoError(t, err)
	require.NoError(t, ledger.Close())

	reopened, err := OpenLedger(path)
	require.NoError(t, err)
	defer func() { require.NoError(t, reopened.Close()) }()

	latest, err := reopened.LatestPayment("chan-1")
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.Equal(t, uint64(42), latest.Balance)
	require.Equal(t, "sig-42", latest.Signature)
	require.Equal(t, uint64(2), latest.PaymentCount)

	// The CAS must still be anchored to the persisted previous balance.
	_, err = reopened.RecordPayment("chan-1", 42, "sig-42-replay")
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	_, err = reopened.RecordPayment("chan-1", 43, "sig-43")
	require.NoError(t, err)
}

func TestAmountDueReadsTheStoredBalance(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()

	due, err := ledger.AmountDue("chan-unknown")
	require.NoError(t, err)
	require.Equal(t, uint64(0), due)

	_, err = ledger.RecordPayment("chan-1", 3, "sig-3")
	require.NoError(t, err)
	due, err = ledger.AmountDue("chan-1")
	require.NoError(t, err)
	require.Equal(t, uint64(3), due)

	count, err := ledger.PaymentCount("chan-unknown")
	require.NoError(t, err)
	require.Equal(t, uint64(0), count)
}
