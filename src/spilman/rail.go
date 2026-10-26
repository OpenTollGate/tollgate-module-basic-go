package spilman

import (
	"context"
	"fmt"
)

// ChannelState is the durable lifecycle state of a channel.
type ChannelState string

const (
	ChannelStateUnknown ChannelState = "unknown"
	ChannelStateOpen    ChannelState = "open"
	ChannelStateClosing ChannelState = "closing"
	ChannelStateClosed  ChannelState = "closed"
)

// ChannelStatus is what Status reports. Balance is the latest *durable* raw
// balance in the channel's own unit, and PaymentCount is how many payments the
// payee has durably accepted — never a value the payee cannot prove.
type ChannelStatus struct {
	ChannelID    string
	State        ChannelState
	Balance      uint64
	Unit         string
	PaymentCount uint64
	UpdatedAt    uint64
	Capacity     uint64
	Expiry       uint64
}

// ChannelOpenRequest registers a channel the payee will accept payments on.
// This is the seller-side direction: the *buyer* funded the channel and the
// payee records what it is willing to serve.
//
// ParamsJSON, FundingProofsJSON, ChannelSecretHex and KeysetInfoJSON are the
// channel's funding record. They are what every later payment signature is
// verified against, so a channel opened without them can never accept a
// payment (RecordPayment refuses it as unverifiable). See Open.
type ChannelOpenRequest struct {
	ChannelID      string
	Unit           string
	Capacity       uint64
	Expiry         uint64
	ReceiverPubkey string
	MintURL        string
	KeysetID       string
	FundingTxID    string

	ParamsJSON        string
	FundingProofsJSON string
	ChannelSecretHex  string
	KeysetInfoJSON    string
}

// ChannelPayment is a signed balance update offered by the payer.
type ChannelPayment struct {
	ChannelID string
	Balance   uint64
	Signature string
}

// ChannelSettleRequest asks the payee to settle a channel.
type ChannelSettleRequest struct {
	ChannelID string
}

// ChannelSettleResult is the settled outcome of a channel.
type ChannelSettleResult struct {
	ChannelID     string
	PaidMillisats uint64
	RawBalance    uint64
	Unit          string
}

// ChannelPaymentRail is the channel-lifecycle seam. It is deliberately a
// sibling of WalletPort (src/tollwallet/port.go) rather than a fourth backend
// of it: WalletPort is token/bearer-shaped (Receive/Send/Melt/Quote) and has no
// vocabulary for channel open / sign / settle / refund at all.
//
// The rail is SELLER-SIDE (payee/upstream role). See BuyerRail for the payer
// side, which is not implemented.
type ChannelPaymentRail interface {
	// Name is a stable identifier for logs and diagnostics.
	Name() string
	// Open durably registers a channel the payee will serve.
	Open(ctx context.Context, req ChannelOpenRequest) (ChannelStatus, error)
	// Status reports the durable state and latest durable balance.
	Status(ctx context.Context, channelID string) (ChannelStatus, error)
	// AcceptPayment durably records a signed balance update via a single-statement
	// strict-advance guard (the write lands only if the offered balance is
	// strictly greater than the stored one — a monotonic guard, not a CAS against
	// an expected previous value), before the caller serves a single byte.
	AcceptPayment(ctx context.Context, payment ChannelPayment) (LedgerOutcome, error)
	// Settle closes the channel for value, paying the payee its latest durable
	// balance. Needs the protocol binding.
	Settle(ctx context.Context, req ChannelSettleRequest) (ChannelSettleResult, error)
	// Close begins closing the channel, capturing the payee's unilateral-exit
	// data when the binding is available.
	Close(ctx context.Context, channelID string) error
}

// ProtocolBridge is the cgo-backed protocol surface the rail needs from the
// Rust core. It is implemented in the `spilman`-tagged build (rail_cgo.go) and
// stubbed otherwise (rail_stub.go), so the default CGO_ENABLED=0 build compiles
// and refuses honestly instead of pretending.
type ProtocolBridge interface {
	// Name identifies the linked binding, or "not-compiled" for the stub.
	Name() string
	// UnilateralCloseData returns the payee's unilateral-exit data for a
	// channel. Seller-side: the binding has this (spilman_bridge_create_unilateral_close_data).
	UnilateralCloseData(ctx context.Context, channelID string) ([]byte, error)
	// ExecuteCooperativeClose settles a channel and reports what was paid.
	ExecuteCooperativeClose(ctx context.Context, req ChannelSettleRequest) (ChannelSettleResult, error)
}

// BuyerRail is the payer side of a channel. It is NOT IMPLEMENTED and every
// method returns ErrBuyerUnsupported, naming the binding exports that would be
// required. Shipping this as a plausible-looking implementation would put
// router funds behind a funding swap that has no recovery path on the Go
// binding: a crash between swap submit and response is unreconcilable and there
// is no refund export.
type BuyerRail interface {
	// OpenChannelFromToken funds a new channel from a Cashu token.
	OpenChannelFromToken(ctx context.Context, token string, req ChannelOpenRequest) (ChannelStatus, error)
	// RecoverOpenChannel reconciles an interrupted channel open.
	RecoverOpenChannel(ctx context.Context, channelID string) (ChannelStatus, error)
	// RefundAfterExpiry reclaims the payer's unspent capacity after expiry.
	RefundAfterExpiry(ctx context.Context, channelID string) (uint64, error)
}

// buyerUnsupported names the method and the missing binding exports, so an
// operator reading the error learns exactly what has to be added upstream.
func buyerUnsupported(method string) error {
	return fmt.Errorf(
		"%w: BuyerRail.%s needs binding exports %v, none of which exist in the %d shipped client exports of libcdk_spilman_go.so",
		ErrBuyerUnsupported, method, MissingBuyerBindingExports, len(ClientBindingExports),
	)
}

// unsupportedBuyerRail is the only BuyerRail this rail ships.
type unsupportedBuyerRail struct{}

// NewBuyerRail returns the payer-side rail. It is explicitly not implemented:
// see MissingBuyerBindingExports. Every method fails with ErrBuyerUnsupported.
func NewBuyerRail() BuyerRail { return unsupportedBuyerRail{} }

func (unsupportedBuyerRail) OpenChannelFromToken(_ context.Context, _ string, _ ChannelOpenRequest) (ChannelStatus, error) {
	return ChannelStatus{}, buyerUnsupported("OpenChannelFromToken")
}

func (unsupportedBuyerRail) RecoverOpenChannel(_ context.Context, _ string) (ChannelStatus, error) {
	return ChannelStatus{}, buyerUnsupported("RecoverOpenChannel")
}

func (unsupportedBuyerRail) RefundAfterExpiry(_ context.Context, _ string) (uint64, error) {
	return 0, buyerUnsupported("RefundAfterExpiry")
}

// SellerRail is the payee-side rail: a durable CAS ledger plus the protocol
// binding for the operations that need it.
type SellerRail struct {
	ledger *ChannelLedger
	bridge ProtocolBridge
}

// NewSellerRail builds the payee-side rail over a durable ledger.
//
// The ledger must have been opened with a validator: payment authentication
// happens inside ChannelLedger.RecordPayment, before the durable write, so there
// is no rail shape that records an unverified balance.
func NewSellerRail(ledger *ChannelLedger) *SellerRail {
	return &SellerRail{ledger: ledger, bridge: newProtocolBridge()}
}

// Validator names the payment validator this build authenticates with. The
// default CGO_ENABLED=0 build reports "not-compiled" and refuses every payment.
func (r *SellerRail) Validator() string {
	if r == nil || r.ledger == nil || r.ledger.validator == nil {
		return "none"
	}
	return r.ledger.validator.Name()
}

func (r *SellerRail) Name() string { return "spilman-seller" }

func (r *SellerRail) Open(ctx context.Context, req ChannelOpenRequest) (ChannelStatus, error) {
	if req.ChannelID == "" {
		return ChannelStatus{}, fmt.Errorf("%w: empty channel id", ErrInvalidChannel)
	}
	if req.Capacity == 0 {
		return ChannelStatus{}, fmt.Errorf("%w: channel %q has zero capacity", ErrInvalidChannel, req.ChannelID)
	}
	// Validating the unit here means an unpriced channel can never be registered.
	if _, err := MillisatsToRawUnits(req.Unit, 0); err != nil {
		return ChannelStatus{}, err
	}

	rec := ChannelRecord{
		ChannelID:         req.ChannelID,
		Unit:              req.Unit,
		State:             ChannelStateOpen,
		Capacity:          req.Capacity,
		Expiry:            req.Expiry,
		OpenedAt:          nowSeconds(),
		ReceiverPubkey:    req.ReceiverPubkey,
		MintURL:           req.MintURL,
		KeysetID:          req.KeysetID,
		FundingTxID:       req.FundingTxID,
		ParamsJSON:        req.ParamsJSON,
		FundingProofsJSON: req.FundingProofsJSON,
		ChannelSecretHex:  req.ChannelSecretHex,
		KeysetInfoJSON:    req.KeysetInfoJSON,
	}
	if err := r.ledger.RegisterChannel(ctx, rec); err != nil {
		return ChannelStatus{}, err
	}
	return r.statusFrom(ctx, &rec)
}

func (r *SellerRail) Status(ctx context.Context, channelID string) (ChannelStatus, error) {
	rec, err := r.ledger.Channel(ctx, channelID)
	if err != nil {
		return ChannelStatus{}, err
	}
	if rec == nil {
		return ChannelStatus{}, fmt.Errorf("%w: %q", ErrChannelUnknown, channelID)
	}
	return r.statusFrom(ctx, rec)
}

// statusFrom merges the durable channel registration with the durable balance.
func (r *SellerRail) statusFrom(ctx context.Context, rec *ChannelRecord) (ChannelStatus, error) {
	status := ChannelStatus{
		ChannelID: rec.ChannelID,
		State:     rec.State,
		Unit:      rec.Unit,
		Capacity:  rec.Capacity,
		Expiry:    rec.Expiry,
	}
	latest, err := r.ledger.LatestPayment(rec.ChannelID)
	if err != nil {
		return ChannelStatus{}, err
	}
	if latest != nil {
		status.Balance = latest.Balance
		status.PaymentCount = latest.PaymentCount
		status.UpdatedAt = latest.UpdatedAt
	}
	return status, nil
}

// AcceptPayment durably records a signed balance update behind the ledger's
// strict-advance guard (not a CAS against an expected previous balance).
// Nothing is served until this returns nil.
func (r *SellerRail) AcceptPayment(ctx context.Context, payment ChannelPayment) (LedgerOutcome, error) {
	rec, err := r.ledger.Channel(ctx, payment.ChannelID)
	if err != nil {
		return "", err
	}
	if rec == nil {
		return "", fmt.Errorf("%w: %q", ErrChannelUnknown, payment.ChannelID)
	}
	if rec.State == ChannelStateClosed {
		return "", fmt.Errorf("%w: channel %q is closed", ErrChannelUnknown, payment.ChannelID)
	}
	if payment.Balance > rec.Capacity {
		return "", fmt.Errorf("%w: channel %q capacity %d, offered %d",
			ErrCapacityExceeded, payment.ChannelID, rec.Capacity, payment.Balance)
	}
	return r.ledger.RecordPayment(ctx, payment.ChannelID, payment.Balance, payment.Signature)
}

// Settle closes the channel for value, paying the payee its latest durable
// balance. Requires the protocol binding: the swap is a real mint round trip and
// there is no honest way to fake it.
func (r *SellerRail) Settle(ctx context.Context, req ChannelSettleRequest) (ChannelSettleResult, error) {
	rec, err := r.ledger.Channel(ctx, req.ChannelID)
	if err != nil {
		return ChannelSettleResult{}, err
	}
	if rec == nil {
		return ChannelSettleResult{}, fmt.Errorf("%w: %q", ErrChannelUnknown, req.ChannelID)
	}

	result, err := r.bridge.ExecuteCooperativeClose(ctx, req)
	if err != nil {
		return ChannelSettleResult{}, err
	}

	raw, _, err := r.ledger.Balance(req.ChannelID)
	if err != nil {
		return ChannelSettleResult{}, err
	}
	paid, err := RawUnitsToMillisats(rec.Unit, raw)
	if err != nil {
		return ChannelSettleResult{}, err
	}
	if err := r.ledger.SetChannelState(ctx, req.ChannelID, ChannelStateClosed); err != nil {
		return ChannelSettleResult{}, err
	}
	result.ChannelID = req.ChannelID
	result.RawBalance = raw
	result.Unit = rec.Unit
	result.PaidMillisats = paid
	return result, nil
}

// Close begins closing the channel. When the binding is available it captures
// the payee's unilateral-exit data and persists it before the state moves: the
// payee's only claim on an uncooperative channel must not be lost to a crash.
func (r *SellerRail) Close(ctx context.Context, channelID string) error {
	rec, err := r.ledger.Channel(ctx, channelID)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("%w: %q", ErrChannelUnknown, channelID)
	}

	data, err := r.bridge.UnilateralCloseData(ctx, channelID)
	if err != nil {
		return err
	}
	if err := r.ledger.SetUnilateralCloseData(ctx, channelID, data); err != nil {
		return err
	}
	return r.ledger.SetChannelState(ctx, channelID, ChannelStateClosing)
}
