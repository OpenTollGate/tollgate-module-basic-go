package spilman

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// registerTestFunding attaches a funding record to a channel opened by
// openTestChannel, so the channel is verifiable. Rail tests use a fake validator
// (the real one needs the Rust core); the funding content only has to be
// non-empty and stable for those.
func registerTestFunding(t *testing.T, ledger *ChannelLedger, channelID string) {
	t.Helper()
	require.NoError(t, ledger.SetFundingRecord(context.Background(), FundingRecord{
		ChannelID:         channelID,
		ParamsJSON:        `{"capacity":100}`,
		FundingProofsJSON: `[{"amount":100}]`,
		ChannelSecretHex:  "1122",
		KeysetInfoJSON:    `{"keysetId":"00aa"}`,
	}))
}

func openTestChannel(t *testing.T, rail *SellerRail) {
	t.Helper()
	status, err := rail.Open(context.Background(), ChannelOpenRequest{
		ChannelID: "chan-1",
		Unit:      UnitSat,
		Capacity:  100,
		Expiry:    1_900_000_000,
		MintURL:   "https://mint.example",
		KeysetID:  "00aa",
	})
	require.NoError(t, err)
	require.Equal(t, ChannelStateOpen, status.State)
	require.Equal(t, "chan-1", status.ChannelID)
	require.Equal(t, uint64(100), status.Capacity)
}

func TestSellerRailOpensAndReportsAChannel(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()
	rail := NewSellerRail(ledger)

	openTestChannel(t, rail)

	status, err := rail.Status(context.Background(), "chan-1")
	require.NoError(t, err)
	require.Equal(t, ChannelStateOpen, status.State)
	require.Equal(t, UnitSat, status.Unit)
	require.Equal(t, uint64(0), status.Balance)
	require.Equal(t, uint64(0), status.PaymentCount)

	_, err = rail.Status(context.Background(), "nope")
	require.ErrorIs(t, err, ErrChannelUnknown)
}

func TestSellerRailRefusesDuplicateAndUnsupportedUnitOpens(t *testing.T) {
	ledger, err := OpenLedgerInMemory()
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()
	rail := NewSellerRail(ledger)

	openTestChannel(t, rail)
	_, err = rail.Open(context.Background(), ChannelOpenRequest{ChannelID: "chan-1", Unit: UnitSat, Capacity: 10})
	require.ErrorIs(t, err, ErrChannelExists)

	_, err = rail.Open(context.Background(), ChannelOpenRequest{ChannelID: "chan-2", Unit: "usd", Capacity: 10})
	require.ErrorIs(t, err, ErrUnsupportedUnit)

	_, err = rail.Open(context.Background(), ChannelOpenRequest{ChannelID: "chan-3", Unit: UnitSat})
	require.ErrorIs(t, err, ErrInvalidChannel)

	_, err = rail.Open(context.Background(), ChannelOpenRequest{ChannelID: "", Unit: UnitSat, Capacity: 1})
	require.ErrorIs(t, err, ErrInvalidChannel)
}

// The payee may only serve bytes for a balance it durably recorded, only in the
// exact-next order, and only once the payment authenticated.
func TestSellerRailAcceptPaymentIsCASOrdered(t *testing.T) {
	validator := genuineLooking("sig-1", "sig-5", "sig-5-other", "sig-4")
	ledger, err := OpenLedgerInMemoryWithValidator(validator)
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()
	rail := NewSellerRail(ledger)
	openTestChannel(t, rail)
	ctx := context.Background()
	registerTestFunding(t, ledger, "chan-1")

	outcome, err := rail.AcceptPayment(ctx, ChannelPayment{ChannelID: "chan-1", Balance: 1, Signature: "sig-1"})
	require.NoError(t, err)
	require.Equal(t, LedgerOutcomeAccepted, outcome)

	outcome, err = rail.AcceptPayment(ctx, ChannelPayment{ChannelID: "chan-1", Balance: 5, Signature: "sig-5"})
	require.NoError(t, err)
	require.Equal(t, LedgerOutcomeAccepted, outcome)

	// Replay of the same balance with a different signature. It AUTHENTICATES
	// (the validator accepts the signature) and is still refused, because it does
	// not advance the stored balance.
	_, err = rail.AcceptPayment(ctx, ChannelPayment{ChannelID: "chan-1", Balance: 5, Signature: "sig-5-other"})
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	// Stale rollback.
	_, err = rail.AcceptPayment(ctx, ChannelPayment{ChannelID: "chan-1", Balance: 4, Signature: "sig-4"})
	require.ErrorIs(t, err, ErrBalanceNotAdvanced)

	status, err := rail.Status(ctx, "chan-1")
	require.NoError(t, err)
	require.Equal(t, uint64(5), status.Balance)
	require.Equal(t, uint64(2), status.PaymentCount)

	// A payment for an unregistered channel is refused outright.
	_, err = rail.AcceptPayment(ctx, ChannelPayment{ChannelID: "chan-2", Balance: 1, Signature: "sig"})
	require.ErrorIs(t, err, ErrChannelUnknown)
}

func TestSellerRailRejectsPaymentsBeyondCapacity(t *testing.T) {
	validator := genuineLooking("sig")
	ledger, err := OpenLedgerInMemoryWithValidator(validator)
	require.NoError(t, err)
	defer func() { require.NoError(t, ledger.Close()) }()
	rail := NewSellerRail(ledger)
	openTestChannel(t, rail)
	registerTestFunding(t, ledger, "chan-1")
	ctx := context.Background()

	_, err = rail.AcceptPayment(ctx, ChannelPayment{ChannelID: "chan-1", Balance: 101, Signature: "sig"})
	require.ErrorIs(t, err, ErrCapacityExceeded)

	status, err := rail.Status(ctx, "chan-1")
	require.NoError(t, err)
	require.Equal(t, uint64(0), status.Balance)
}

// Buyer-side methods must be typed refusals that name the missing exports, not
// a plausible-looking implementation.
func TestBuyerRailIsExplicitlyNotImplemented(t *testing.T) {
	rail := NewBuyerRail()
	ctx := context.Background()

	_, err := rail.OpenChannelFromToken(ctx, "cashuA...", ChannelOpenRequest{ChannelID: "c", Unit: UnitSat})
	require.ErrorIs(t, err, ErrBuyerUnsupported)
	require.Contains(t, err.Error(), "OpenChannelFromToken")
	require.Contains(t, err.Error(), "prepare_open_channel_recovery")

	_, err = rail.RecoverOpenChannel(ctx, "c")
	require.ErrorIs(t, err, ErrBuyerUnsupported)
	require.Contains(t, err.Error(), "recover_open_channel_from_swap")

	_, err = rail.RefundAfterExpiry(ctx, "c")
	require.ErrorIs(t, err, ErrBuyerUnsupported)
	require.Contains(t, err.Error(), "prepare_sender_refund_after_expiry")
}

// The rail's claim that the buyer path is unsupported is only honest if none of
// the "missing" names actually exists in the shipped client binding.
func TestMissingBuyerExportsAreGenuinelyAbsentFromTheBinding(t *testing.T) {
	require.Len(t, ClientBindingExports, 14, "measured client export count")

	for _, missing := range MissingBuyerBindingExports {
		for _, existing := range ClientBindingExports {
			require.NotEqual(t, existing, missing,
				"%q is a real client export; it must not be listed as missing", missing)
		}
		require.False(t,
			strings.Contains(strings.Join(ClientBindingExports, " "), missing),
			"%q must not appear anywhere in the client exports", missing)
	}

	// The seller side *does* have the close path; that is why this rail is
	// seller-side-only.
	require.Contains(t, SellerBindingExports, "spilman_bridge_create_unilateral_close_data")
	require.Contains(t, SellerBindingExports, "spilman_bridge_execute_unilateral_close")
}

func TestBuyerUnsupportedErrorNamesTheBindingGap(t *testing.T) {
	_, err := NewBuyerRail().RefundAfterExpiry(context.Background(), "c")
	var wrapped error = err
	require.True(t, errors.Is(wrapped, ErrBuyerUnsupported))
	require.Contains(t, err.Error(), "libcdk_spilman_go.so")
}
