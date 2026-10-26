package spilman

// The protocol validator seam.
//
// This file holds the parts of payment authentication that do NOT depend on the
// Rust core, so the untagged (default, CGO_ENABLED=0) build can still refuse
// correctly and tests can exercise the fail-closed ordering without linking a
// .so. The implementation of PaymentValidator lives in the build-tag pair:
//
//   - rail_cgo.go   (//go:build spilman) -> upstream cdk-spilman's own validator
//   - rail_stub.go  (//go:build !spilman) -> refuses every payment by name
//
// A validator is required, never optional: nether nil nor a stub that says yes
// may ever authenticate a balance.

import (
	"context"
	"encoding/json"
	"fmt"
)

// fundingProof is the subset of a Cashu proof's shape the rail inspects when it
// cross-checks a funding record against what the payee agreed to serve. It is
// deliberately small: parsing proofs here is a sanity check, not a substitute
// for the upstream validator, which is the only thing that authenticates a
// signature.
type fundingProof struct {
	Amount    uint64 `json:"amount"`
	KeysetID  string `json:"id"`
	Secret    string `json:"secret"`
	Signature string `json:"C"`
}

// fundingProofsTotal parses a funding-proofs JSON array and returns the number
// of proofs and their total nominal value. Both must be non-zero for a channel
// to have anything to serve against.
func fundingProofsTotal(fundingProofsJSON string) (count int, total uint64, err error) {
	var proofs []fundingProof
	if err := json.Unmarshal([]byte(fundingProofsJSON), &proofs); err != nil {
		return 0, 0, fmt.Errorf("%w: parse funding proofs: %v", ErrFundingRecordMissing, err)
	}
	for _, proof := range proofs {
		total += proof.Amount
	}
	return len(proofs), total, nil
}

// closingData and channelPolicy are the two shapes upstream's host callbacks
// can ask a validate-only host for. They are unexported because nothing outside
// this package should build them: the validator answers nil / zero for both,
// and the close path is a separate concern (see ErrSellerBridgeUnwired).
type closingData struct {
	ExpiryTimestamp uint64
	Balance         uint64
	Signature       string
}

type channelPolicy struct {
	MinExpiryInSeconds uint64
	MinCapacity        uint64
	MaxAmountPerOutput *uint64
}

// refusingValidator is a PaymentValidator that refuses every payment, naming
// why. It is what the default build uses: without the protocol binding there is
// no way to check a Schnorr signature over the channel commitment, and a payee
// that cannot check it must not credit it.
type refusingValidator struct {
	reason error
}

func (v refusingValidator) Name() string { return "not-compiled" }

func (v refusingValidator) ValidatePayment(_ context.Context, funding FundingRecord, balance uint64, _ string) error {
	return fmt.Errorf("%w: %q balance %d", v.reason, funding.ChannelID, balance)
}

// refusingErrValidator refuses every payment with err. Used by the !spilman
// build so the refusal names the build tag rather than looking like a bad
// signature.
func refusingErrValidator(err error) PaymentValidator { return refusingValidator{reason: err} }
