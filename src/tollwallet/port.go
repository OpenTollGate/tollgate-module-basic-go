package tollwallet

// This file defines WalletPort — the library-agnostic Cashu wallet interface.
// It is the seam between merchant/lightning code and the underlying Cashu
// library (gonuts-tollgate by default, cdk-go behind the cdk_wallet build tag).
//
// Design decisions documented in WIREFORMAT.md:
//   - Token uses an explicit Close() method for CGO lifecycle management
//     (GonutsToken.Close() is a no-op; CdkToken.Close() calls Destroy()).
//   - MintQuoteState uses int underlying type with dual-format JSON:
//     MarshalJSON emits uppercase strings per Cashu NUT-04 spec;
//     UnmarshalJSON accepts both integers (legacy gonuts) and strings.
//   - Existing sentinels ErrTokenAlreadySpent and ErrWalletNotInitialized
//     remain in tollwallet.go unchanged.

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Token is the library-agnostic Cashu token abstraction.
// Callers MUST call Close() when done to release CGO resources
// (cdk-go adapter). The gonuts adapter's Close() is a no-op because
// Go's garbage collector handles gonuts token cleanup.
type Token interface {
	// Mint returns the mint URL embedded in the token.
	Mint() string
	// Amount returns the total token value in the token's unit
	// (typically satoshis).
	Amount() uint64
	// Close releases any CGO-backed resources. Safe to call multiple
	// times (implementations must be idempotent).
	Close()
}

// MintQuoteState represents the state of a Lightning mint quote per NUT-04.
// Underlying type is int for sentinel-value parity with gonuts nut04.State.
//
// JSON wire format:
//   - MarshalJSON emits uppercase strings ("UNPAID", "PAID", "ISSUED",
//     "PENDING", "UNKNOWN") per Cashu NUT-04/NUT-20 specification.
//   - UnmarshalJSON accepts BOTH integers (0-4, legacy gonuts value
//     marshaling) and strings (canonical, case-insensitive) for backward
//     compatibility with existing production data.
//
// See WIREFORMAT.md for the full marshaling analysis and migration rationale.
type MintQuoteState int

const (
	StateUnpaid  MintQuoteState = iota // 0
	StatePaid                          // 1
	StateIssued                        // 2
	StatePending                       // 3
	StateUnknown                       // 4
)

// String returns the canonical uppercase string representation per Cashu
// NUT-04 spec. Note: gonuts returned lowercase "unknown" for Unknown(4);
// this implementation returns uppercase "UNKNOWN" as a spec-compliance
// cleanup. Task 1.2 characterization test pins gonuts's behavior separately.
func (s MintQuoteState) String() string {
	switch s {
	case StateUnpaid:
		return "UNPAID"
	case StatePaid:
		return "PAID"
	case StateIssued:
		return "ISSUED"
	case StatePending:
		return "PENDING"
	default:
		return "UNKNOWN"
	}
}

// MarshalJSON implements json.Marshaler. Emits uppercase strings per
// Cashu NUT-04 specification. Value receiver so it works on both
// MintQuoteState and *MintQuoteState.
func (s MintQuoteState) MarshalJSON() ([]byte, error) {
	return json.Marshal(s.String())
}

// UnmarshalJSON implements json.Unmarshaler. Accepts both formats:
//   - Integer (0-4): legacy gonuts value-marshaling format
//   - String ("UNPAID", "PAID", etc.): canonical Cashu spec format
//
// Detection: if the first byte is a double-quote, parse as string;
// otherwise parse as integer. This mirrors the protoc-gen-go-json pattern
// (https://github.com/protoc-contrib/protoc-gen-go-json) and the
// transitland-lib migration precedent (PR #640).
func (s *MintQuoteState) UnmarshalJSON(data []byte) error {
	if len(data) > 0 && data[0] == '"' {
		// Canonical string format (case-insensitive)
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return fmt.Errorf("MintQuoteState: failed to parse string: %w", err)
		}
		parsed, err := ParseMintQuoteState(str)
		if err != nil {
			return err
		}
		*s = parsed
		return nil
	}
	// Legacy integer format from gonuts value-marshaling
	var n int
	if err := json.Unmarshal(data, &n); err != nil {
		return fmt.Errorf("MintQuoteState: failed to parse integer: %w", err)
	}
	if n < 0 || n > int(StateUnknown) {
		return fmt.Errorf("MintQuoteState: integer %d out of range [0, %d]", n, StateUnknown)
	}
	*s = MintQuoteState(n)
	return nil
}

// ParseMintQuoteState converts a string to MintQuoteState (case-insensitive).
// Returns an error for unrecognized strings. Used by UnmarshalJSON and
// available for direct programmatic use.
func ParseMintQuoteState(s string) (MintQuoteState, error) {
	switch strings.ToUpper(s) {
	case "UNPAID":
		return StateUnpaid, nil
	case "PAID":
		return StatePaid, nil
	case "ISSUED":
		return StateIssued, nil
	case "PENDING":
		return StatePending, nil
	case "UNKNOWN":
		return StateUnknown, nil
	default:
		return StateUnknown, fmt.Errorf("MintQuoteState: unrecognized value %q", s)
	}
}

// MintQuote is the library-agnostic Lightning mint quote response (NUT-04).
// It replaces gonuts's nut04.PostMintQuoteBolt11Response in merchant code.
type MintQuote struct {
	QuoteID string
	Request string // bolt11 payment request
	State   MintQuoteState
	Amount  uint64
	Expiry  uint64
}

// MeltQuote is the library-agnostic Lightning melt quote response (NUT-05).
// It replaces gonuts's wallet.MeltQuote in merchant code.
type MeltQuote struct {
	QuoteID    string
	Amount     uint64
	FeeReserve uint64
	State      MintQuoteState
	Expiry     uint64
}

// MeltResult is the library-agnostic Lightning melt operation result.
// It replaces gonuts's wallet.MeltResult in merchant code.
type MeltResult struct {
	QuoteID  string
	Paid     bool
	Preimage string
}

// WalletPort is the library-agnostic Cashu wallet interface. It abstracts
// away gonuts-tollgate (default) and cdk-go (behind the cdk_wallet build
// tag) so that merchant/lightning code depends only on primitive Go types.
//
// Implementations:
//   - GonutsWallet (file gonuts_wallet.go, build tag !cdk_wallet) — wraps
//     *TollWallet, behavior-identical to current code
//   - CdkWallet (file cdk_wallet.go, build tag cdk_wallet) — wraps
//     cdk_ffi.Wallet via CGO FFI bindings
//
// The interface is consumed by merchant.Merchant via a WalletPort-typed
// field, replacing the current concrete tollwallet.TollWallet field.
// This enables wallet injection for testing (unblocking the happy-path
// and already-spent characterization tests that were SKIP'd in Task 1.1).
type WalletPort interface {
	// DecodeToken parses a Cashu token string (V3 "cashuA..." or V4
	// "cashuB...") into a Token. The caller MUST call Token.Close()
	// when done.
	DecodeToken(tokenStr string) (Token, error)

	// Receive accepts a Cashu token, validates it, optionally swaps to
	// a trusted mint, and credits the wallet. Returns the amount
	// received in the token's unit (typically satoshis).
	Receive(token Token) (uint64, error)

	// GetBalance returns the total wallet balance across all mints.
	GetBalance() uint64

	// GetBalanceByMint returns the balance for a specific mint URL.
	GetBalanceByMint(mintUrl string) uint64

	// GetAllMintBalances returns a map of mint URL to balance.
	GetAllMintBalances() map[string]uint64

	// SendWithOverpayment sends tokens from the wallet with overpayment
	// tolerance. Returns the serialized token string.
	SendWithOverpayment(amount uint64, mintUrl string, maxOverpaymentPercent uint64, maxOverpaymentAbsolute uint64) (string, error)

	// RequestMintQuote requests a Lightning mint quote from the mint.
	// The quote contains a bolt11 invoice that the user pays to mint
	// tokens.
	RequestMintQuote(amount uint64, mintUrl string) (*MintQuote, error)

	// GetMintQuoteState checks the state of a previously requested mint
	// quote. Used by the Lightning quote monitor goroutine.
	GetMintQuoteState(quoteID string) (MintQuoteState, error)

	// MintTokens mints tokens for a paid mint quote.
	MintTokens(quoteID string) (uint64, error)

	// RequestMeltQuote requests a Lightning melt quote for paying a
	// bolt11 invoice from wallet funds.
	RequestMeltQuote(invoice string, mintUrl string) (*MeltQuote, error)

	// Melt executes a melt quote, paying the invoice and consuming
	// wallet proofs.
	Melt(quoteID string) (*MeltResult, error)

	// Shutdown releases wallet resources (database handles, CGO
	// objects). Must be idempotent.
	Shutdown() error
}

// Compile-time assertion that the sentinel error variables defined in
// tollwallet.go (ErrTokenAlreadySpent, ErrWalletNotInitialized) satisfy
// the error interface — they do by construction (errors.New returns *errorString).
// This comment exists to document that port.go intentionally does NOT
// redeclare them.
