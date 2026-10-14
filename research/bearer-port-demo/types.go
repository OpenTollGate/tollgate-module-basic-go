package bearerport

import "errors"

// The port's whole error vocabulary. Note that there is no error for "not
// spent": absence of an answer is ErrStateUnavailable, which maps to
// StateUnknown. Nothing in this list lets a caller mistake silence for
// "unspent".
var (
	ErrUnknownKind       = errors.New("unknown bearer-instrument encoding")
	ErrNotIssued         = errors.New("authority does not recognise this instrument as its own")
	ErrAlreadyConsumed   = errors.New("instrument already consumed at its authority")
	ErrStateUnavailable  = errors.New("instrument state unavailable: authority did not answer")
	ErrInsufficientFunds = errors.New("insufficient funds")
	ErrAmountNotPositive = errors.New("amount must be positive")
	ErrAmountMismatch    = errors.New("swap does not balance: replacement differs from the issued amount")
	ErrNoAuthority       = errors.New("no authority registered for that target key")
	ErrUnitMismatch      = errors.New("cannot spend an instrument whose unit differs from the target unit")
)

// State is the answer to the NUT-07 question, backend-neutral.
//
// The zero value is deliberately StateUnknown: a caller that forgets to check
// a returned error still cannot conclude "unspent" by accident.
type State int

const (
	StateUnknown State = iota
	StateUnspent
	StateSpent
)

func (s State) String() string {
	switch s {
	case StateUnspent:
		return "UNSPENT"
	case StateSpent:
		return "SPENT"
	default:
		return "UNKNOWN"
	}
}

// CheckMode is how an authority can answer the NUT-07 question at all.
type CheckMode string

const (
	// CheckModeQuery — the authority has a read-only token-state API
	// (Cashu NUT-07 /checkstate). Checking is free and side-effect-free.
	CheckModeQuery CheckMode = "query"
	// CheckModeProbe — the authority has no token-level liveness API. The
	// only sound way to learn the state is to attempt the consumption,
	// which is a MUTATION and must be declared as such (fedimint: reissue).
	CheckModeProbe CheckMode = "probe"
	// CheckModeNone — no sound way to learn the state. The port reports
	// UNKNOWN, always. A backend here cannot be used for irreversible
	// pre-flight decisions.
	CheckModeNone CheckMode = "none"
)

// RecoverMode is which memory the NUT-09 reconstruction replays against.
type RecoverMode string

const (
	// RecoverModeIssuanceLog — the authority keeps a signed-outputs log
	// (Cashu /restore): the port asks which of its derived children were
	// actually signed. This is why losing the log server-side breaks
	// recovery even when the user's seed is intact.
	RecoverModeIssuanceLog RecoverMode = "issuance-log"
	// RecoverModeAuthorityBackup — the authority holds an encrypted backup
	// of the holder's client state (fedimint).
	RecoverModeAuthorityBackup RecoverMode = "authority-backup"
	// RecoverModeAccountLedger — the authority keeps an account ledger
	// (a miner's payout account): recovery is a statement, not a scan.
	RecoverModeAccountLedger RecoverMode = "account-ledger"
	// RecoverModeNone — no reconstruction is possible.
	RecoverModeNone RecoverMode = "none"
)

// Capability is the honest, declared contract of one authority. It is the
// backend-neutral form of the nut_07 / nut_09 flags already present in
// src/tollwallet/manifests/*.json.
type Capability struct {
	Authority  string      `json:"authority"`
	Kind       string      `json:"kind"`
	Unit       string      `json:"unit"`
	StateCheck CheckMode   `json:"state_check"`
	Recover    RecoverMode `json:"recover"`
	Notes      string      `json:"notes"`
}
