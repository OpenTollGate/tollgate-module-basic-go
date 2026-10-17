package hedgehog

// CHANNEL STATE MACHINE
//
// Hedgehog's whole point for TollGate is that a payment can be CREATED by the
// payer with no counterparty reachable, and CONSUMED later by the payee alone.
// The state machine below makes that explicit: every operation is annotated with
// WHO performs it and WHETHER THE OTHER PARTY MUST BE REACHABLE.
//
// That annotation is the deliverable. The PR #944 research recorded a direction
// claim that was wrong precisely because "who must act" was conflated with "who
// must be reachable". Encoding it in the type system stops the same confusion
// being reintroduced.
//
// Lifecycle:
//
//	OPEN            both parties fund a shared 2-of-2 output (on-chain, both act)
//	PROPOSE         PAYER ALONE, counterparty NOT reachable  <-- the key step
//	ACCEPT          PAYEE later, alone
//	REVOKE          either party, by revealing a revocation secret
//	CLOSE           cooperative 2-of-2 spend, both sign
//	FORCE-CLOSE     one party after the CSV delay (requires watching the chain)

import (
	"errors"
	"fmt"
	"time"
)

// State is the channel's lifecycle position.
type State string

const (
	// StateOpen means the funding output exists on-chain and no payment has
	// been proposed yet.
	StateOpen State = "open"
	// StateProposed means the payer has produced a half-signed update that the
	// payee has not yet completed. This state is reachable while the payee is
	// offline, and it can persist indefinitely.
	StateProposed State = "proposed"
	// StateAccepted means the payee has completed the update and (optionally)
	// broadcast it.
	StateAccepted State = "accepted"
	// StateClosed means the channel has been settled.
	StateClosed State = "closed"
)

// Errors returned by the state machine.
var (
	ErrWrongState      = errors.New("hedgehog: operation not valid in this state")
	ErrNotYourRole     = errors.New("hedgehog: operation not permitted for this role")
	ErrLimitExceeded   = errors.New("hedgehog: amount exceeds the channel balance")
	ErrUpdateTooOld    = errors.New("hedgehog: update is out of date")
)

// Role identifies which side of the channel a participant is.
type Role string

const (
	// RolePayer is the party paying. For a TollGate purchase this is the
	// CUSTOMER, who may have no WAN.
	RolePayer Role = "payer"
	// RolePayee is the party being paid. For a TollGate purchase this is the
	// ROUTER, which is a 24/7 service.
	RolePayee Role = "payee"
)

// Update is one proposed change to the channel's balance distribution.
type Update struct {
	// Seq is a monotonically increasing sequence number. Later updates
	// supersede earlier ones: the newest state a party holds is the one that
	// can be enforced.
	Seq uint64
	// PayerBalance and PayeeBalance are the split, in satoshis, after this
	// update. They must sum to the channel capacity minus fees.
	PayerBalance, PayeeBalance int64
	// ProposedAt records when the payer produced the update. It is metadata for
	// policy (how long has this been pending?) and is NOT committed on-chain.
	ProposedAt time.Time

	// SignedTxid is the txid of the half-signed settlement transaction, and
	// PayerSignature is the payer's signature over it. Together they are the
	// "cheque": inert data the payer hands over while offline.
	SignedTxid     string
	PayerSignature []byte
	// PayeeSignature is filled in when the payee accepts.
	PayeeSignature []byte
}

// Acceptable reports whether `other` may supersede this update.
func (u *Update) Acceptable(other *Update) bool {
	return other != nil && other.Seq > u.Seq
}

// ChannelState is the machine's full state.
type ChannelState struct {
	Channel *Channel
	Role    Role
	State   State
	// Capacity is the funding output value in satoshis.
	Capacity int64
	// Current is the latest update. Nil before the first proposal.
	Current *Update
	// RevocationSecret is the secret that, when revealed to the counterparty,
	// lets them revoke our unilateral claim. Empty until set.
	RevocationSecret []byte

	// pending is a proposal we produced but the counterparty has not accepted.
	// Unexported on purpose: reading it is a mistake. Until acceptance the
	// ENFORCEABLE state is Current, and code that treats a pending update as
	// settled would enforce a state the counterparty never agreed to.
	pending *Update
}

// NewChannelState initialises the machine for a funded channel.
func NewChannelState(c *Channel, role Role, capacity int64) (*ChannelState, error) {
	if c == nil {
		return nil, errors.New("hedgehog: nil channel")
	}
	if capacity <= 0 {
		return nil, fmt.Errorf("hedgehog: capacity must be positive, got %d", capacity)
	}
	if role != RolePayer && role != RolePayee {
		return nil, fmt.Errorf("hedgehog: unknown role %q", role)
	}
	return &ChannelState{
		Channel:  c,
		Role:     role,
		State:    StateOpen,
		Capacity: capacity,
		// The opening distribution before any payment: the payer holds
		// everything, the payee nothing.
		Current: &Update{Seq: 0, PayerBalance: capacity, PayeeBalance: 0},
	}, nil
}

// Propose produces a new update for `amount` satoshis flowing payer -> payee.
//
// COUNTERPARTY REACHABILITY: NONE REQUIRED. This is the operation that makes
// Hedgehog fit TollGate. The payer signs locally and the result is inert until
// the payee acts. The PR #944 research initially claimed this direction was
// impossible; it is possible, and the claim was retracted after executing it.
//
// The caller must sign the settlement transaction and pass the signature in.
func (s *ChannelState) Propose(amount int64,
	signTxid string, payerSig []byte) (*Update, error) {

	if s.Role != RolePayer {
		return nil, fmt.Errorf("%w: only the payer proposes; we are %s",
			ErrNotYourRole, s.Role)
	}
	if s.State == StateClosed {
		return nil, fmt.Errorf("%w: channel is closed", ErrWrongState)
	}
	if amount <= 0 {
		return nil, fmt.Errorf("hedgehog: amount must be positive, got %d", amount)
	}
	if s.Current.PayerBalance-amount < 0 {
		return nil, fmt.Errorf("%w: %d sat requested, payer holds %d",
			ErrLimitExceeded, amount, s.Current.PayerBalance)
	}
	if len(payerSig) == 0 {
		return nil, errors.New("hedgehog: a proposal without a signature is not a cheque")
	}

	u := &Update{
		Seq:            s.Current.Seq + 1,
		PayerBalance:   s.Current.PayerBalance - amount,
		PayeeBalance:   s.Current.PayeeBalance + amount,
		ProposedAt:     time.Now(),
		SignedTxid:     signTxid,
		PayerSignature: payerSig,
	}
	// The proposal is now the newest state WE hold, but it is not yet accepted.
	// Keeping it staged (rather than as Current) matters: until the payee
	// completes it, the enforceable state is still the previous one.
	s.pending = u
	s.State = StateProposed
	return u, nil
}

// Accept completes a proposal received from the counterparty.
//
// COUNTERPARTY REACHABILITY: NONE REQUIRED. The payee needs only the cheque
// bytes, which can arrive by any store-and-forward channel (including email).
func (s *ChannelState) Accept(u *Update, payeeSig []byte) error {
	if s.Role != RolePayee {
		return fmt.Errorf("%w: only the payee accepts; we are %s",
			ErrNotYourRole, s.Role)
	}
	if u == nil {
		return errors.New("hedgehog: nil update")
	}
	if !s.Current.Acceptable(u) {
		return fmt.Errorf("%w: got seq %d, current is %d",
			ErrUpdateTooOld, u.Seq, s.Current.Seq)
	}
	if len(u.PayerSignature) == 0 {
		return errors.New("hedgehog: refusing to accept an unsigned update")
	}
	if u.PayerBalance+u.PayeeBalance > s.Capacity {
		return fmt.Errorf("%w: %d + %d exceeds capacity %d",
			ErrLimitExceeded, u.PayerBalance, u.PayeeBalance, s.Capacity)
	}
	if len(payeeSig) == 0 {
		return errors.New("hedgehog: acceptance requires our signature")
	}
	u.PayeeSignature = payeeSig
	s.Current = u
	s.State = StateAccepted
	return nil
}

// Close performs a cooperative settlement: both parties sign the final state.
//
// COUNTERPARTY REACHABILITY: REQUIRED. This is the cheap exit, and it is the
// only operation here that needs both parties live at once.
func (s *ChannelState) Close() error {
	if s.State == StateClosed {
		return fmt.Errorf("%w: already closed", ErrWrongState)
	}
	if s.State != StateAccepted {
		return fmt.Errorf("%w: can only cooperatively close an accepted state "+
			"(currently %s); use force-close after the CSV delay otherwise",
			ErrWrongState, s.State)
	}
	s.State = StateClosed
	return nil
}

// RevocationSecretFor returns the secret the counterparty must have in order to
// revoke a unilateral claim by this party.
//
// This is where Hedgehog's time obligation lives, and the research phase's D5
// finding is precisely about it: revealing the secret is what makes an old state
// recoverable, and the RECIPIENT of the secret must then watch the chain inside
// the CSV window to use it. Hedgehog relocates the reconciler; it does not
// remove it. For TollGate the relocated duty lands on the ROUTER, which is a
// 24/7 service already running software -- a good fit.
func (s *ChannelState) RevocationSecretFor() []byte {
	return s.RevocationSecret
}

// SetRevocationSecret records the secret we will reveal when we revoke.
func (s *ChannelState) SetRevocationSecret(secret []byte) error {
	if len(secret) == 0 {
		return errors.New("hedgehog: revocation secret must not be empty")
	}
	s.RevocationSecret = secret
	return nil
}

// CSVBlocks reports the relative lock the counterparty must wait before it can
// unilaterally reclaim, and therefore the window in which it must watch.
func (s *ChannelState) CSVBlocks() uint16 { return s.Channel.CSVBlocks }

// WatchWindow is the CSV delay as a duration, at 10-minute blocks. This is the
// period during which SOME party must observe the chain.
func (s *ChannelState) WatchWindow() time.Duration {
	return time.Duration(s.Channel.CSVBlocks) * 10 * time.Minute
}

// Describe renders the machine's state for logs and tests.
func (s *ChannelState) Describe() string {
	cur := "none"
	if s.Current != nil {
		cur = fmt.Sprintf("seq=%d payer=%d payee=%d",
			s.Current.Seq, s.Current.PayerBalance, s.Current.PayeeBalance)
	}
	return fmt.Sprintf("role=%s state=%s capacity=%d current=[%s] csv=%d blocks (%s)",
		s.Role, s.State, s.Capacity, cur, s.Channel.CSVBlocks, s.WatchWindow())
}

// Pending returns the proposal awaiting acceptance, if any. It is exposed for
// INSPECTION only -- the enforceable state is always Current.
func (s *ChannelState) Pending() *Update { return s.pending }
