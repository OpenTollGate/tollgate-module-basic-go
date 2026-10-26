package spilman

import (
	"errors"
	"fmt"
)

// Sentinel errors of the Spilman rail. Every one is a *refusal*: the rail fails
// closed, because the alternatives are serving bytes for money it cannot prove
// or double-crediting a replayed payment.
var (
	// ErrZeroRate means a billing rate was zero. Never divide by it.
	ErrZeroRate = errors.New("billing rates must be positive")

	// ErrUnsupportedUnit means the channel's raw unit is not one this rail knows
	// how to convert (only "sat" and "msat" are supported).
	ErrUnsupportedUnit = errors.New("unsupported channel unit")

	// ErrOverflow means exact arithmetic produced a value outside uint64. The
	// rail refuses rather than truncating a money amount.
	ErrOverflow = errors.New("arithmetic overflow")

	// ErrBalanceNotAdvanced means the offered balance did not strictly advance
	// the stored one: replay (equal), rollback (lower) or a same-balance
	// different-signature retry. Covers the double-credit that a monotonic-max
	// update accepts.
	ErrBalanceNotAdvanced = errors.New("balance does not advance the stored balance")

	// ErrChannelUnknown means no channel with that ID is registered.
	ErrChannelUnknown = errors.New("unknown channel")

	// ErrChannelExists means a channel with that ID is already registered.
	ErrChannelExists = errors.New("channel already registered")

	// ErrChannelConflict means a channel with that ID is registered with a
	// DIFFERENT funding record. This is the case that must not be treated as a
	// harmless re-registration: the funding record anchors every later payment
	// signature, so accepting a second, different one would re-point the channel
	// at funding the payee never agreed to serve against. It wraps
	// ErrChannelExists, because it is still a refusal to register.
	ErrChannelConflict = fmt.Errorf("%w: with a different funding record", ErrChannelExists)

	// ErrInvalidChannel means a channel request is malformed (missing ID, zero
	// capacity, bad expiry). Never register a channel you cannot price.
	ErrInvalidChannel = errors.New("invalid channel request")

	// ErrCapacityExceeded means an offered balance exceeds the capacity the
	// payee agreed to serve. Refusing is what stops a payer from "paying" more
	// than the funding proofs can ever cover.
	ErrCapacityExceeded = errors.New("offered balance exceeds channel capacity")

	// ErrPaymentNotAuthentic means the payment's signature did not verify
	// against the channel's persisted funding record, so the balance is a
	// forgery until proven otherwise. Nothing was written. This is the guard
	// that monotonicity alone does not provide: a higher balance is not
	// evidence that the payer authorised it.
	ErrPaymentNotAuthentic = errors.New("payment signature does not authenticate against the channel's funding record")

	// ErrFundingRecordMissing means the channel has no persisted funding record
	// (params, funding proofs, channel secret, keyset info) to verify a payment
	// against. Refusing is the only safe answer: an unverifiable balance must
	// never become credit.
	ErrFundingRecordMissing = errors.New("channel has no persisted funding record")

	// ErrNotCompiled means the operation needs the cgo protocol binding and this
	// binary was built without the `spilman` build tag (the default
	// CGO_ENABLED=0 build). The rail refuses to pretend it performed the work.
	ErrNotCompiled = errors.New("spilman protocol binding not compiled in (build with -tags spilman)")

	// ErrSellerBridgeUnwired means the cgo build is linked, but the seller-side
	// *close* path still needs the callback host adapter (spilman_bridge_new plus
	// the 22-callback SpilmanHostCallbacks struct). The primitives that need no
	// host are wired; the ones that do are named here rather than faked.
	ErrSellerBridgeUnwired = errors.New("spilman seller bridge close path is not wired")

	// ErrBuyerUnsupported means the *payer* side of the rail is not implemented,
	// because the Go client binding (crates/cdk-spilman-go, client_bridge
	// exports) has no recovery, refund, restore or unilateral-exit export. See
	// MissingBuyerBindingExports.
	ErrBuyerUnsupported = errors.New("spilman buyer side is not implemented in this rail")

	// ErrLedger means the durable ledger failed. Always fail closed: an
	// unreadable ledger must never be read as "no balance".
	ErrLedger = errors.New("spilman ledger error")
)

// ZeroRateError carries both offending rates.
type ZeroRateError struct {
	InBytesPerMillisat  uint64
	OutBytesPerMillisat uint64
}

func (e *ZeroRateError) Error() string {
	return fmt.Sprintf("billing rates must be positive: in=%d, out=%d", e.InBytesPerMillisat, e.OutBytesPerMillisat)
}

func (e *ZeroRateError) Is(target error) bool { return target == ErrZeroRate }

// BalanceNotAdvancedError is the advance-guard refusal, with the exact values that
// disagreed so the caller can reconcile instead of blind-retrying.
type BalanceNotAdvancedError struct {
	ChannelID string
	Stored    uint64
	Offered   uint64
}

func (e *BalanceNotAdvancedError) Error() string {
	return fmt.Sprintf(
		"balance %d does not advance channel %q (stored balance %d); refusing to credit it",
		e.Offered, e.ChannelID, e.Stored,
	)
}

func (e *BalanceNotAdvancedError) Is(target error) bool { return target == ErrBalanceNotAdvanced }

// MissingBuyerBindingExports are the client-binding capabilities the rail would
// need to ship a payer (buyer) path. None of them exists in the 14 client
// exports of the shipped libcdk_spilman_go.so, which is why the buyer side of
// this rail is an interface returning ErrBuyerUnsupported rather than a
// plausible-looking implementation.
var MissingBuyerBindingExports = []string{
	"prepare_open_channel_recovery",
	"recover_open_channel_from_swap",
	"restore_funding_proofs",
	"prepare_sender_refund_after_expiry",
}

// ClientBindingExports is the measured, complete set of client-side exports of
// the shipped binding (nm -D on libcdk_spilman_go.so). It exists so a test can
// assert that every entry of MissingBuyerBindingExports is genuinely absent
// from the binding instead of drifting into a name that happens to exist.
var ClientBindingExports = []string{
	"spilman_client_bridge_close_channel",
	"spilman_client_bridge_delete_channel",
	"spilman_client_bridge_free",
	"spilman_client_bridge_get_channel_info",
	"spilman_client_bridge_list_channels",
	"spilman_client_bridge_mark_channel_unusable",
	"spilman_client_bridge_new",
	"spilman_client_bridge_open_channel_from_token",
	"spilman_client_bridge_process_cooperative_close_response",
	"spilman_client_bridge_sign_and_record_payment",
	"spilman_client_bridge_sign_channel_registration",
	"spilman_client_bridge_sign_cooperative_close_request",
	"spilman_client_bridge_sign_payment",
	"spilman_client_bridge_sign_payment_header",
}

// SellerBindingExports are the seller-side close exports the binding *does*
// have — the reason this rail is seller-side-only.
var SellerBindingExports = []string{
	"spilman_bridge_create_unilateral_close_data",
	"spilman_bridge_execute_unilateral_close",
}
