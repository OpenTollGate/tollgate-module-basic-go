//go:build !spilman

package spilman

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// In the default CGO_ENABLED=0 build the protocol operations must refuse
// honestly: the rail must not report a settle or a close it did not perform.
func TestDefaultBuildRefusesProtocolOperations(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()
	rail := NewSellerRail(ledger)
	openTestChannel(t, rail)
	ctx := context.Background()

	_, err = rail.Settle(ctx, ChannelSettleRequest{ChannelID: "chan-1"})
	require.ErrorIs(t, err, ErrNotCompiled)

	err = rail.Close(ctx, "chan-1")
	require.ErrorIs(t, err, ErrNotCompiled)

	status, err := rail.Status(ctx, "chan-1")
	require.NoError(t, err)
	require.Equal(t, ChannelStateOpen, status.State, "a refused close must not move the state")

	// The default build has NO payment validator: the Schnorr check lives in the
	// Rust core, and a build without it cannot tell a payment from a forgery.
	// So the "pure-Go hot path" is NOT a payment path here any more — accepting
	// the balance would be exactly the forgeable behaviour this rail must not
	// have. Either refusal is correct and closed; which one fires depends on
	// whether the channel carries a funding record, and openTestChannel does not
	// give it one.
	_, err = rail.AcceptPayment(ctx, ChannelPayment{ChannelID: "chan-1", Balance: 1, Signature: "sig-1"})
	require.ErrorIs(t, err, ErrFundingRecordMissing)

	// Give the channel a funding record and the refusal becomes the validator's:
	// without the Rust core there is no way to check the signature.
	require.NoError(t, ledger.SetFundingRecord(ctx, FundingRecord{
		ChannelID:         "chan-1",
		ParamsJSON:        `{"capacity":100}`,
		FundingProofsJSON: `[{"amount":100}]`,
		ChannelSecretHex:  "1122",
		KeysetInfoJSON:    `{"keysetId":"00aa"}`,
	}))
	_, err = rail.AcceptPayment(ctx, ChannelPayment{ChannelID: "chan-1", Balance: 1, Signature: "sig-1"})
	require.ErrorIs(t, err, ErrPaymentNotAuthentic)
	require.ErrorIs(t, err, ErrNotCompiled)

	status, err = rail.Status(ctx, "chan-1")
	require.NoError(t, err)
	require.Equal(t, uint64(0), status.Balance, "no balance may be recorded without authentication")
	require.Equal(t, uint64(0), status.PaymentCount)
	require.Equal(t, "not-compiled", rail.Validator())
}

// The protocol primitives must refuse in the default build rather than
// returning zero values that look like real answers.
func TestDefaultBuildRefusesSellerPrimitives(t *testing.T) {
	_, err := SecretKeyToPubkey("aa")
	require.ErrorIs(t, err, ErrNotCompiled)
	_, err = ComputeChannelSecret("aa", "bb")
	require.ErrorIs(t, err, ErrNotCompiled)
	_, err = ComputeFundingTokenAmount(100, "{}", 100)
	require.ErrorIs(t, err, ErrNotCompiled)
	_, err = CreateSignedBalanceUpdate("{}", "{}", "aa", "[]", 1)
	require.ErrorIs(t, err, ErrNotCompiled)
	_, err = ChannelIDFromParams("{}", "aa", "{}")
	require.ErrorIs(t, err, ErrNotCompiled)

	require.Equal(t, "not-compiled", newProtocolBridge().Name())
}
