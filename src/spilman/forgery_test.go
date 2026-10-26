package spilman

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// A forged payment must be REFUSED, and nothing may be recorded.
//
// This is the exact hole the rail shipped with: RecordPayment's only guard was
// the SQL CAS (`WHERE balance < excluded.balance`), i.e. monotonicity, which is
// not authenticity. Anyone could hand the ledger a higher balance with a
// signature they invented and it became durable, creditable state — the same
// root cause as the bug found and fixed on the Rust side.
//
// The validator here is a FAKE (the real one needs the Rust core), so this test
// pins the ORDERING rule instead of the cryptography: the ledger must ask, and
// must obey "no". The cryptography is covered by the tagged tests against
// upstream's real validator (payment_auth_cgo_test.go).

// fakeValidator is a PaymentValidator whose verdict the test controls.
type fakeValidator struct {
	accept   func(funding FundingRecord, balance uint64, signature string) bool
	calls    int
	lastSeen FundingRecord
	lastBal  uint64
	lastSig  string
	err      error
}

func (v *fakeValidator) Name() string { return "fake" }

func (v *fakeValidator) ValidatePayment(_ context.Context, funding FundingRecord, balance uint64, signature string) error {
	v.calls++
	v.lastSeen = funding
	v.lastBal = balance
	v.lastSig = signature
	if v.err != nil {
		return v.err
	}
	if v.accept != nil && v.accept(funding, balance, signature) {
		return nil
	}
	return errors.New("signature verification failed")
}

// genuineLooking accepts only the signatures a test declares genuine.
func genuineLooking(genuine ...string) *fakeValidator {
	accept := make(map[string]bool, len(genuine))
	for _, signature := range genuine {
		accept[signature] = true
	}
	return &fakeValidator{accept: func(_ FundingRecord, _ uint64, signature string) bool {
		return accept[signature]
	}}
}

// registeredLedger returns a ledger over the given validator with one channel
// registered, carrying a complete funding record.
func registeredLedger(t *testing.T, validator PaymentValidator) *ChannelLedger {
	t.Helper()
	ledger, err := OpenLedgerInMemoryWithValidator(validator)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ledger.Close()) })
	require.NoError(t, ledger.RegisterChannel(context.Background(), ChannelRecord{
		ChannelID:         "chan-1",
		Unit:              UnitSat,
		State:             ChannelStateOpen,
		Capacity:          100,
		Expiry:            1_900_000_000,
		OpenedAt:          1_700_000_000,
		ReceiverPubkey:    "02aa",
		MintURL:           "https://mint.example",
		KeysetID:          "00aa",
		FundingTxID:       "tx-1",
		ParamsJSON:        `{"capacity":100}`,
		FundingProofsJSON: `[{"amount":100}]`,
		ChannelSecretHex:  "11" + "22",
		KeysetInfoJSON:    `{"keysetId":"00aa"}`,
	}))
	return ledger
}

func TestForgedBalanceWithAnInventedSignatureIsRefused(t *testing.T) {
	validator := genuineLooking("the-real-signature")
	ledger := registeredLedger(t, validator)
	ctx := context.Background()

	// A balance far inside capacity, so the capacity guard is not what refuses
	// this: the only thing that can refuse it is authenticity.
	_, err := ledger.RecordPayment(ctx, "chan-1", 50, "an-invented-signature")
	require.Error(t, err, "a forged balance must never be recorded")
	require.ErrorIs(t, err, ErrPaymentNotAuthentic)

	_, known, err := ledger.Balance("chan-1")
	require.NoError(t, err)
	require.False(t, known, "a refused payment must leave no balance behind")
	require.Equal(t, 1, validator.calls, "the ledger must have consulted the validator")

	// The positive control: the SAME balance with the genuine signature is
	// accepted. Without this, "refuse everything" would pass the test above.
	outcome, err := ledger.RecordPayment(ctx, "chan-1", 50, "the-real-signature")
	require.NoError(t, err)
	require.Equal(t, LedgerOutcomeAccepted, outcome)

	_, known, err = ledger.Balance("chan-1")
	require.NoError(t, err)
	require.True(t, known)
}

func TestPaymentForAnUnregisteredChannelIsRefusedWithoutVerifying(t *testing.T) {
	validator := genuineLooking("sig")
	ledger := registeredLedger(t, validator)

	_, err := ledger.RecordPayment(context.Background(), "chan-unknown", 1, "sig")
	require.ErrorIs(t, err, ErrChannelUnknown)
	require.Zero(t, validator.calls, "there is nothing to verify against, so do not try")
}

// A channel registered WITHOUT its funding record cannot authenticate anything.
// It must refuse rather than fall through to the CAS.
func TestPaymentIsRefusedWhenTheFundingRecordIsIncomplete(t *testing.T) {
	validator := genuineLooking("sig")
	ledger, err := OpenLedgerInMemoryWithValidator(validator)
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()
	ctx := context.Background()

	require.NoError(t, ledger.RegisterChannel(ctx, ChannelRecord{
		ChannelID: "chan-bare",
		Unit:      UnitSat,
		State:     ChannelStateOpen,
		Capacity:  100,
		Expiry:    1_900_000_000,
		OpenedAt:  1_700_000_000,
	}))

	_, err = ledger.RecordPayment(ctx, "chan-bare", 10, "sig")
	require.ErrorIs(t, err, ErrFundingRecordMissing)
	require.Zero(t, validator.calls)

	_, known, err := ledger.Balance("chan-bare")
	require.NoError(t, err)
	require.False(t, known)
}

// Every validator error is fatal, including one that is not obviously about a
// signature (a decode failure, a capacity rejection from upstream, a bug). The
// ledger must not distinguish "definitely forged" from "could not check": both
// mean the balance is unproven.
func TestAnyValidatorErrorRefusesThePayment(t *testing.T) {
	for _, cause := range []error{
		errors.New("Invalid signature: Alice did not authorize this balance update"),
		errors.New("UnknownChannel"),
		errors.New("BalanceExceedsCapacity"),
		errors.New("Internal"),
		ErrNotCompiled,
	} {
		validator := &fakeValidator{err: cause}
		ledger := registeredLedger(t, validator)

		_, err := ledger.RecordPayment(context.Background(), "chan-1", 10, "sig")
		require.ErrorIs(t, err, ErrPaymentNotAuthentic, "cause %v", cause)
		require.ErrorContains(t, err, cause.Error())

		_, known, err := ledger.Balance("chan-1")
		require.NoError(t, err)
		require.False(t, known, "cause %v must not write", cause)
	}
}

// The validator must be handed the funding record the payee persisted, not
// something the caller supplied: that is the whole point of persisting it.
func TestValidatorSeesThePersistedFundingRecord(t *testing.T) {
	validator := genuineLooking("sig")
	ledger := registeredLedger(t, validator)

	_, err := ledger.RecordPayment(context.Background(), "chan-1", 42, "sig")
	require.NoError(t, err)

	require.Equal(t, "chan-1", validator.lastSeen.ChannelID)
	require.Equal(t, `{"capacity":100}`, validator.lastSeen.ParamsJSON)
	require.Equal(t, `[{"amount":100}]`, validator.lastSeen.FundingProofsJSON)
	require.Equal(t, "1122", validator.lastSeen.ChannelSecretHex)
	require.Equal(t, `{"keysetId":"00aa"}`, validator.lastSeen.KeysetInfoJSON)
	require.Equal(t, uint64(42), validator.lastBal)
	require.Equal(t, "sig", validator.lastSig)
}

// A ledger with no validator cannot tell a payment from a forgery, so it must not
// be constructible.
func TestALedgerWithoutAValidatorIsRefused(t *testing.T) {
	_, err := OpenLedgerInMemoryWithValidator(nil)
	require.ErrorIs(t, err, ErrLedger)

	_, err = OpenLedgerWithValidator(t.TempDir()+"/ledger.db", nil)
	require.ErrorIs(t, err, ErrLedger)
}

// The rail is the caller-facing seam; it must not have a path that bypasses the
// ledger's authentication either.
func TestSellerRailRefusesAForgedPaymentEndToEnd(t *testing.T) {
	validator := genuineLooking("sig")
	ledger := registeredLedger(t, validator)
	rail := NewSellerRail(ledger)
	ctx := context.Background()

	require.NoError(t, ledger.RegisterChannel(ctx, ChannelRecord{
		ChannelID: "chan-2", Unit: UnitSat, State: ChannelStateOpen, Capacity: 100,
		Expiry: 1_900_000_000, OpenedAt: 1_700_000_000,
		ParamsJSON: `{"capacity":100}`, FundingProofsJSON: `[{"amount":100}]`,
		ChannelSecretHex: "1122", KeysetInfoJSON: `{"keysetId":"00aa"}`,
	}))

	_, err := rail.AcceptPayment(ctx, ChannelPayment{ChannelID: "chan-2", Balance: 60, Signature: "forged"})
	require.ErrorIs(t, err, ErrPaymentNotAuthentic)

	status, err := rail.Status(ctx, "chan-2")
	require.NoError(t, err)
	require.Equal(t, uint64(0), status.Balance)
	require.Equal(t, uint64(0), status.PaymentCount)

	outcome, err := rail.AcceptPayment(ctx, ChannelPayment{ChannelID: "chan-2", Balance: 60, Signature: "sig"})
	require.NoError(t, err)
	require.Equal(t, LedgerOutcomeAccepted, outcome)
}
