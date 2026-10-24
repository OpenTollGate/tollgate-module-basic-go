//go:build spilman

package spilman

// Payment authentication against the REAL upstream validator and REAL
// signatures.
//
// The unit tests in forgery_test.go pin the ordering with a fake validator: they
// prove RecordPayment asks before it writes, and that any error refuses. These
// tests prove the far more important half — that the accounting rail actually
// ACCEPTS a genuine payment. A "refuse everything" rail passes every negative
// test, so without these, the fix could be a lockout and still look green.

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUpstreamValidatorAcceptsAGenuineSignatureAndRefusesForgeries(t *testing.T) {
	fixture := newFixtureChannel(t, 8)
	validator := newProtocolValidator()
	ctx := context.Background()

	signature := fixture.sign(t, 4)
	require.NoError(t,
		validator.ValidatePayment(ctx, fixture.Funding, 4, signature),
		"a genuine sender signature over the channel commitment must verify")

	// A signature that is syntactically valid hex but was not produced by the
	// sender over this commitment.
	require.Error(t, validator.ValidatePayment(ctx, fixture.Funding, 4, strings.Repeat("ab", 64)))
	// A signature that is not a signature at all.
	require.Error(t, validator.ValidatePayment(ctx, fixture.Funding, 4, "an-invented-signature"))
	// Empty.
	require.Error(t, validator.ValidatePayment(ctx, fixture.Funding, 4, ""))
	// The genuine signature for 4 does not authorise 5.
	require.Error(t, validator.ValidatePayment(ctx, fixture.Funding, 5, signature))

	// A genuine signature from one channel is worthless on ANOTHER channel. The
	// anchor is the funding record, so the second channel must differ in its
	// material (channel secret / proofs), not merely its id string: upstream
	// re-derives the commitment from the record and the id is not part of it.
	otherChannel := newFixtureChannelWith(t, 8, 3)
	require.NotEqual(t, fixture.ChannelID, otherChannel.ChannelID)
	require.NotEqual(t, fixture.Funding.ChannelSecretHex, otherChannel.Funding.ChannelSecretHex)
	require.Error(t, validator.ValidatePayment(ctx, otherChannel.Funding, 4, signature),
		"a signature must not transfer to a channel with different funding material")

	// And the record is what is bound, not the caller's id: substituting a
	// DIFFERENT record for the same request re-derives a different commitment and
	// the signature stops verifying.
	tampered := fixture.Funding
	tampered.FundingProofsJSON = fmt.Sprintf(
		`[{"amount":8,"id":"009a1f293253e41e","secret":"different","C":%q}]`, fixture.SenderPubkey)
	require.Error(t, validator.ValidatePayment(ctx, tampered, 4, signature))
}

func TestRailRefusesAForgedBalanceAndAcceptsTheGenuineOne(t *testing.T) {
	fixture := newFixtureChannel(t, 8)
	ctx := context.Background()

	ledger, err := OpenLedgerInMemoryWithValidator(newProtocolValidator())
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()
	require.NoError(t, ledger.RegisterChannel(ctx, ChannelRecord{
		ChannelID:         fixture.ChannelID,
		Unit:              UnitSat,
		State:             ChannelStateOpen,
		Capacity:          fixture.Capacity,
		Expiry:            1_900_000_000,
		OpenedAt:          1_700_000_000,
		ParamsJSON:        fixture.Funding.ParamsJSON,
		FundingProofsJSON: fixture.Funding.FundingProofsJSON,
		ChannelSecretHex:  fixture.Funding.ChannelSecretHex,
		KeysetInfoJSON:    fixture.Funding.KeysetInfoJSON,
	}))

	// FORGERY: a high balance with a signature that is merely hex-shaped.
	_, err = ledger.RecordPayment(ctx, fixture.ChannelID, 7, strings.Repeat("cd", 64))
	require.ErrorIs(t, err, ErrPaymentNotAuthentic)
	_, known, err := ledger.Balance(fixture.ChannelID)
	require.NoError(t, err)
	require.False(t, known, "a forged payment must leave NO balance behind")

	// GENUINE: the sender really did authorise 3, then 6.
	for _, step := range []uint64{3, 6} {
		outcome, err := ledger.RecordPayment(ctx, fixture.ChannelID, step, fixture.sign(t, step))
		require.NoError(t, err, "a genuine payment for %d must be accepted", step)
		require.Equal(t, LedgerOutcomeAccepted, outcome)
	}

	latest, err := ledger.LatestPayment(fixture.ChannelID)
	require.NoError(t, err)
	require.NotNil(t, latest)
	require.Equal(t, uint64(6), latest.Balance)
	require.Equal(t, uint64(2), latest.PaymentCount)

	// REPLAY of the genuine 6: authentic, but not an advance.
	_, err = ledger.RecordPayment(ctx, fixture.ChannelID, 6, fixture.sign(t, 6))
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	// ROLLBACK to 3: authentic, but older.
	_, err = ledger.RecordPayment(ctx, fixture.ChannelID, 3, fixture.sign(t, 3))
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	// Not a single forged balance reached the durable record.
	_, known, err = ledger.Balance(fixture.ChannelID)
	require.NoError(t, err)
	require.True(t, known)
	require.Equal(t, uint64(6), latest.Balance)
}

// A balance above capacity must be refused even when the signature is genuine.
//
// Upstream's SIGNER will not mint such a signature ("Receiver balance 16 exceeds
// channel capacity 8"), so the above-capacity-with-valid-signature state cannot
// be produced by the protocol — but a hostile payer controls the wire, so the
// rail must still refuse it rather than trusting that. Here the signature is for
// 4 (the maximum the channel can authorise in this fixture's keyset terms) and
// the claimed balance is 9: the seller rail's capacity guard refuses before
// anything is recorded, and the ledger's validator refuses on capacity too.
func TestRailRefusesAnAboveCapacityClaim(t *testing.T) {
	fixture := newFixtureChannel(t, 8)
	ctx := context.Background()

	ledger, err := OpenLedgerInMemoryWithValidator(newProtocolValidator())
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()
	require.NoError(t, ledger.RegisterChannel(ctx, ChannelRecord{
		ChannelID:         fixture.ChannelID,
		Unit:              UnitSat,
		State:             ChannelStateOpen,
		Capacity:          fixture.Capacity,
		Expiry:            1_900_000_000,
		OpenedAt:          1_700_000_000,
		ParamsJSON:        fixture.Funding.ParamsJSON,
		FundingProofsJSON: fixture.Funding.FundingProofsJSON,
		ChannelSecretHex:  fixture.Funding.ChannelSecretHex,
		KeysetInfoJSON:    fixture.Funding.KeysetInfoJSON,
	}))

	// The seller rail refuses it on its own capacity guard first.
	rail := NewSellerRail(ledger)
	_, err = rail.AcceptPayment(ctx, ChannelPayment{ChannelID: fixture.ChannelID, Balance: 9, Signature: "sig"})
	require.ErrorIs(t, err, ErrCapacityExceeded)

	// The ledger refuses it too, and cannot be talked into it with the genuine
	// signature for a legal balance: the balance is part of the signed message, so
	// a signature for 4 does not authorise 9 anywhere in the path.
	_, err = ledger.RecordPayment(ctx, fixture.ChannelID, 9, fixture.sign(t, 4))
	require.ErrorIs(t, err, ErrPaymentNotAuthentic)

	_, known, err := ledger.Balance(fixture.ChannelID)
	require.NoError(t, err)
	require.False(t, known)
}
