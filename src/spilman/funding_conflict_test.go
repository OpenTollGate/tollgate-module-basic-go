package spilman

// A channel's funding record is the anchor for EVERY later payment signature.
//
// Registering a second, DIFFERENT funding record under the same channel id would
// re-point the channel at funding the payee never agreed to serve against — a payer
// could swap in their own proofs and then sign balances the original commitment
// never covered. So a conflicting registration must be REFUSED, and refused
// distinguishably: `ErrChannelConflict` wraps `ErrChannelExists` so a caller can
// tell "you already told me this" from "you are telling me something else".
//
// # Why this file exists
//
// `ErrChannelConflict` was introduced with the refusal paths but carried no test,
// so nothing observed the difference between "idempotent re-registration" and
// "conflicting re-registration". Those are opposite outcomes for the same shape of
// call, which is exactly the kind of pair that silently collapses when untested.

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// The channel must be registered for its funding record to be set: a record for an
// unknown id is refused as `unknown channel`, which is a different guard and would
// make these tests pass while observing nothing.
func registerFor(t *testing.T, ledger *ChannelLedger, id string) {
	t.Helper()
	require.NoError(t, ledger.RegisterChannel(context.Background(), ChannelRecord{
		ChannelID: id, Unit: UnitSat, State: ChannelStateOpen,
		Capacity: 1000, Expiry: 1_900_000_000, OpenedAt: 1_700_000_000,
	}))
}

func fundingRec(id, secret string) FundingRecord {
	return FundingRecord{
		ChannelID:         id,
		ParamsJSON:        `{"channel_id":"` + id + `"}`,
		FundingProofsJSON: `[{"secret":"` + secret + `"}]`,
		ChannelSecretHex:  secret,
		KeysetInfoJSON:    `{}`,
	}
}

// A re-registration with the SAME funding record is idempotent: a retried request
// after an ambiguous network result must not be an error.
func TestSameFundingRecordIsIdempotent(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ledger.Close()) })

	ctx := context.Background()
	registerFor(t, ledger, "chan-same")
	require.NoError(t, ledger.SetFundingRecord(ctx, fundingRec("chan-same", "aa")))
	require.NoError(t, ledger.SetFundingRecord(ctx, fundingRec("chan-same", "aa")),
		"the identical funding record again must be a no-op, not an error")
}

// A DIFFERENT funding record for the same id must be refused, and distinguishably.
func TestConflictingFundingRecordIsRefusedDistinguishably(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ledger.Close()) })

	ctx := context.Background()
	registerFor(t, ledger, "chan-conf")
	require.NoError(t, ledger.SetFundingRecord(ctx, fundingRec("chan-conf", "aa")))

	err = ledger.SetFundingRecord(ctx, fundingRec("chan-conf", "bb"))
	require.Error(t, err, "a different funding record must NOT be accepted")

	require.True(t, errors.Is(err, ErrChannelConflict),
		"the refusal must be ErrChannelConflict; got %v", err)
	// The wrapping matters: callers that only care "is it a duplicate?" keep
	// working, while a caller that must react to a CONFLICT can tell them apart.
	require.True(t, errors.Is(err, ErrChannelExists),
		"ErrChannelConflict must wrap ErrChannelExists so existing callers "+
			"are unaffected; got %v", err)

	// And the ORIGINAL record must survive: a refused registration that still
	// overwrote would be the worst of both.
	got, err := ledger.FundingRecord(ctx, "chan-conf")
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, "aa", got.ChannelSecretHex,
		"the refused registration must not have replaced the original record")
}

// RegisterChannel is the same rule on the registration path.
func TestRegisterChannelWithConflictingFundingIsRefused(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ledger.Close()) })

	ctx := context.Background()
	base := ChannelRecord{
		ChannelID: "chan-reg", Unit: UnitSat, State: ChannelStateOpen,
		Capacity: 1000, Expiry: 1_900_000_000, OpenedAt: 1_700_000_000,
		ParamsJSON:        `{"channel_id":"chan-reg"}`,
		FundingProofsJSON: `[{"secret":"aa"}]`,
		ChannelSecretHex:  "aa",
		KeysetInfoJSON:    `{}`,
	}
	require.NoError(t, ledger.RegisterChannel(ctx, base))

	// Same shape, different funding record.
	conflict := base
	conflict.FundingProofsJSON = `[{"secret":"bb"}]`
	conflict.ChannelSecretHex = "bb"

	err = ledger.RegisterChannel(ctx, conflict)
	require.Error(t, err, "a conflicting re-registration must be refused")
	require.True(t, errors.Is(err, ErrChannelConflict),
		"expected ErrChannelConflict; got %v", err)
}
