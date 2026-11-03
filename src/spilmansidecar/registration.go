package spilmansidecar

// Registration validation over the sidecar socket — STUB (RED baseline).
//
// The types below are the real wire contract (they mirror the sidecar's
// `ValidateRegistrationRequest` field for field, because the sidecar parses
// with `deny_unknown_fields`: an extra field is a refused request, not a
// tolerated one). Only the method body is a stub: it fabricates a result so the
// contract tests are observed failing on SEMANTICS rather than on a compile
// error.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// MethodValidateRegistration is the contract method name for validation.
const MethodValidateRegistration = "validate_registration"

// CapabilityValidateRegistration is the capability a caller must see before
// relying on the method. A transport-only sidecar build does not advertise it.
const CapabilityValidateRegistration = "validate_registration.v1"

// TrustSignalRegistrationValidated is the only trust signal this contract
// defines. It names exactly what was proved: the registration's outer fields,
// its nested params, the trusted mint and the trusted keyset agree and a
// channel id follows. It is NOT a statement about mint funds.
const TrustSignalRegistrationValidated = "registration_validated"

// Refusal codes the sidecar can return for this method. They are declared as
// constants so a caller can branch on them without retyping strings, but the
// client always carries the peer's own code through: a code this build does not
// know is still reported, never rewritten.
const (
	RefusalReceiverMismatch     = "receiver_pubkey_mismatch"
	RefusalNestedMismatch       = "nested_params_mismatch"
	RefusalMintNotTrusted       = "mint_not_trusted"
	RefusalKeysetMismatch       = "keyset_mismatch"
	RefusalUnsupportedUnit      = "unsupported_unit"
	RefusalZeroCapacity         = "zero_capacity"
	RefusalRegistrationExpired  = "registration_expired"
	RefusalInvalidLifetime      = "invalid_lifetime"
	RefusalChannelNotDerivable  = "channel_id_not_derivable"
	RefusalSenderEqualsReceiver = "sender_equals_receiver"
)

// RefusalError carries the sidecar's own refusal code unchanged, and matches
// [ErrRegistrationRefused] through Unwrap so a caller can test the class with
// errors.Is and still read the specific code. Rewriting the code would hide
// which operator action a refusal needs.
type RefusalError struct {
	Code string
}

func (e *RefusalError) Error() string {
	return "spilmansidecar: registration refused: " + e.Code
}

// Unwrap returns the refusal CLASS, and also ErrUnsupportedCapability when the
// peer's code was that one. A transport-only deploy must stay recognisable as a
// capability mismatch: a caller that gates on errors.Is(err,
// ErrUnsupportedCapability) has to see it, or it will treat a wrong deploy as a
// bad registration.
func (e *RefusalError) Unwrap() []error {
	if e.Code == string(ErrCodeUnsupportedCapability) {
		return []error{ErrRegistrationRefused, ErrUnsupportedCapability}
	}
	return []error{ErrRegistrationRefused}
}

// ErrRegistrationRefused is the refusal class: the sidecar read the request and
// declined it. Distinct from a transport failure, which says nothing about the
// registration and must not be reported as a refusal.
var ErrRegistrationRefused = errors.New("spilmansidecar: registration refused")

// Registration is the outer record the daemon would persist. The field set is
// closed on the sidecar side, so this struct must stay in lockstep with
// sidecar/spilman/src/registration.rs::Registration.
type Registration struct {
	SenderPubkey              string `json:"sender_pubkey"`
	ReceiverPubkey            string `json:"receiver_pubkey"`
	Mint                      string `json:"mint"`
	Unit                      string `json:"unit"`
	Capacity                  uint64 `json:"capacity"`
	FundingTokenAmount        uint64 `json:"funding_token_amount"`
	ExpiryTimestamp           uint64 `json:"expiry_timestamp"`
	SetupTimestamp            uint64 `json:"setup_timestamp"`
	KeysetID                  string `json:"keyset_id"`
	InputFeePPK               uint64 `json:"input_fee_ppk"`
	MaximumAmountForOneOutput uint64 `json:"maximum_amount_for_one_output"`
}

// RegistrationRequest is the bounded, versioned request payload.
//
// `trusted_keyset_info` is raw JSON on purpose: the keyset shape belongs to the
// protocol (NUT-02 keys, fee, unit, expiry), and re-modelling it in Go would be
// a second declaration of a shape the sidecar already owns.
type RegistrationRequest struct {
	// ReceiverSecretHex is the payee's receiver scalar, derived by the daemon
	// from its own merchant identity. It is sent over the authenticated local
	// socket and must never be logged.
	ReceiverSecretHex string          `json:"receiver_secret_hex"`
	TrustedMintURL    string          `json:"trusted_mint_url"`
	TrustedKeysetInfo json.RawMessage `json:"trusted_keyset_info"`
	Registration      Registration    `json:"registration"`
	ParamsJSON        string          `json:"params_json"`
	// NowUnix is the instant to judge expiry against. Supplied rather than read
	// from a clock so a validation decision is replayable and attributable.
	NowUnix uint64 `json:"now_unix"`
}

// ValidatedRegistration holds only values the sidecar DERIVED. Nothing in it is
// an echo of the request, which is what makes it safe to persist.
type ValidatedRegistration struct {
	ChannelID                 string `json:"channel_id"`
	CanonicalParamsJSON       string `json:"canonical_params_json"`
	ReceiverPubkey            string `json:"receiver_pubkey"`
	SenderPubkey              string `json:"sender_pubkey"`
	Mint                      string `json:"mint"`
	Unit                      string `json:"unit"`
	Capacity                  uint64 `json:"capacity"`
	FundingTokenAmount        uint64 `json:"funding_token_amount"`
	ExpiryTimestamp           uint64 `json:"expiry_timestamp"`
	SetupTimestamp            uint64 `json:"setup_timestamp"`
	KeysetID                  string `json:"keyset_id"`
	InputFeePPK               uint64 `json:"input_fee_ppk"`
	MaximumAmountForOneOutput uint64 `json:"maximum_amount_for_one_output"`
	TrustSignal               string `json:"trust_signal"`
	// MintFundsAsserted is always false. It is present so a caller cannot read
	// validation success as a claim about the mint's funds, and the client
	// refuses a success that omits it.
	MintFundsAsserted bool `json:"mint_funds_asserted"`
}

// ValidateRegistration asks the sidecar to validate one registration.
//
// It refuses locally, before anything is written, when the operation id would
// not survive the wire contract or when the encoded request would exceed the
// frame bound. A refusal from the sidecar comes back as [*RefusalError] with the
// peer's own code.
//
// The returned values are what the SIDECAR derived. A nil error is a statement
// about parameter agreement, never about whether the mint holds funds, whether
// a swap would succeed, or whether settlement works — none of which this
// milestone implements.
func (c *Client) ValidateRegistration(ctx context.Context, operationID string, request RegistrationRequest) (*ValidatedRegistration, error) {
	if err := ValidateID(operationID); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidOperationID, err)
	}

	params, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("spilmansidecar: encode registration request: %w", err)
	}
	// The frame bound is NOT re-guessed here. `exchange` measures the assembled
	// frame — version, ids, method and params — and refuses it before writing,
	// so the bound is declared once (AGENTS.md rule 1). A local approximation
	// would be a second, quietly divergent limit.

	requestID, err := c.nextRequestID(operationID)
	if err != nil {
		return nil, err
	}
	response, err := c.exchange(ctx, MethodValidateRegistration, requestID, operationID, params)
	if err != nil {
		return nil, err
	}

	if response.Error != "" {
		return nil, &RefusalError{Code: response.Error}
	}
	if len(response.Validated) == 0 {
		return nil, fmt.Errorf("%w: success without validated values", ErrResponseShape)
	}

	var validated ValidatedRegistration
	decoder := json.NewDecoder(strings.NewReader(string(response.Validated)))
	// An unknown field is protocol drift. Silently dropping it would hide a peer
	// that believes it is telling us something important.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&validated); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrResponseShape, err)
	}
	if decoder.More() {
		return nil, fmt.Errorf("%w: trailing content after validated values", ErrResponseShape)
	}

	// The honesty marker must be PRESENT and false. A `bool` cannot tell absent
	// from false after decoding, so presence is checked against the raw object:
	// an omitted marker would otherwise read as "no claim made", which in a log
	// is indistinguishable from a claim. A peer asserting mint funds is claiming
	// something this capability cannot establish, so that is refused too.
	var markerPresence map[string]json.RawMessage
	if err := json.Unmarshal(response.Validated, &markerPresence); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrResponseShape, err)
	}
	if _, ok := markerPresence["mint_funds_asserted"]; !ok {
		return nil, fmt.Errorf("%w: success without the mint_funds_asserted marker", ErrResponseShape)
	}
	if validated.MintFundsAsserted {
		return nil, fmt.Errorf("%w: peer asserted mint funds, which this capability cannot do", ErrResponseShape)
	}
	// A success whose derived values are empty is a peer that accepted what it
	// did not derive. The channel id is the entire product of the derivation.
	for _, required := range []struct {
		name  string
		value string
	}{
		{"channel_id", validated.ChannelID},
		{"canonical_params_json", validated.CanonicalParamsJSON},
		{"receiver_pubkey", validated.ReceiverPubkey},
		{"sender_pubkey", validated.SenderPubkey},
		{"keyset_id", validated.KeysetID},
		{"trust_signal", validated.TrustSignal},
	} {
		if strings.TrimSpace(required.value) == "" {
			return nil, fmt.Errorf("%w: success with empty %s", ErrResponseShape, required.name)
		}
	}
	// A channel id is a SHA-256 digest in hex. Anything else would be persisted
	// as the channel's identity and every later close lookup would miss it.
	if len(validated.ChannelID) != 64 || !isLowerHex(validated.ChannelID) {
		return nil, fmt.Errorf("%w: channel_id is not a 32-byte hex digest", ErrResponseShape)
	}
	if validated.TrustSignal != TrustSignalRegistrationValidated {
		return nil, fmt.Errorf("%w: unrecognised trust signal %q", ErrResponseShape, validated.TrustSignal)
	}
	return &validated, nil
}

// isLowerHex reports whether s is entirely lowercase hexadecimal.
//
// Lowercase specifically, not merely "hex": the protocol emits lowercase, and a
// channel id accepted in two spellings would put two spellings of one digest in
// a store keyed by it. Accepting both hides that until the worst moment — a
// channel close whose lookup misses.
func isLowerHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
		case c >= 'a' && c <= 'f':
		default:
			return false
		}
	}
	return len(s) > 0
}
