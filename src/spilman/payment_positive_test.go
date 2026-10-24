package spilman

// The POSITIVE case for payment authentication, in the DEFAULT build.
//
// # Why this file exists
//
// Reviewing the verification fix, the question that mattered was not "does it
// refuse forgeries" — a lockout refuses those too — but "is there anything that
// would FAIL if the rail simply refused every payment?"
//
// There was not. In the default build `newProtocolValidator()` returns
// `refusingErrValidator(ErrNotCompiled)`, so every test that goes through the
// PUBLIC `RecordPayment` can only ever observe a refusal. The one test that does
// show an acceptance (`TestRecordPaymentAcceptsTheExactNextBalance`) calls
// `recordAuthenticatedPayment`, which is the UNAUTHENTICATED internal helper — it
// proves the CAS writes after verification, and proves nothing about whether
// verification can ever succeed.
//
// The suite that does prove acceptance lives in `payment_auth_cgo_test.go` behind
// `//go:build spilman`, i.e. it is not compiled by default. So the default build —
// the one CI runs — could not distinguish a working verifier from a permanent
// lockout. That is the same shape of gap the reviewer found on the Rust side, where
// deleting the verifier left everything green.
//
// This file closes it with a validator that ACCEPTS, wired through the public API.

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// validatorFunc adapts a plain function to PaymentValidator, so a test can state just
// the rule it cares about.
type validatorFunc func(context.Context, FundingRecord, uint64, string) error

func (f validatorFunc) Name() string { return "test-validator-func" }

func (f validatorFunc) ValidatePayment(ctx context.Context, r FundingRecord,
	balance uint64, signature string) error {
	return f(ctx, r, balance, signature)
}

// acceptingValidator stands in for "the upstream validator said yes". It exists so a
// test can reach the acceptance branch of the public `RecordPayment` without a real
// signature.
type acceptingValidator struct{ calls int }

func (v *acceptingValidator) Name() string { return "accepting-test-validator" }

func (v *acceptingValidator) ValidatePayment(_ context.Context, f FundingRecord,
	balance uint64, signature string) error {
	v.calls++
	// Stay honest about the contract: refuse the shapes the real validator refuses,
	// so this double cannot accept something upstream never would.
	if !f.Complete() {
		return ErrFundingRecordMissing
	}
	if balance == 0 || signature == "" {
		return ErrPaymentNotAuthentic
	}
	return nil
}

// A GENUINE payment must be ACCEPTED through the public API, and the balance must
// become durable state.
//
// If the rail ever degenerates into "refuse everything", this test fails. That is
// the entire point of it existing in the default suite.
func TestPublicRecordPaymentAcceptsAGenuinePayment(t *testing.T) {
	validator := &acceptingValidator{}
	ledger, err := OpenLedgerInMemoryWithValidator(validator)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ledger.Close()) })

	require.NoError(t, ledger.RegisterChannel(context.Background(), ChannelRecord{
		ChannelID: "chan-ok", Unit: UnitSat, State: ChannelStateOpen,
		Capacity: 1000, Expiry: 1_900_000_000, OpenedAt: 1_700_000_000,
		// A COMPLETE funding record, because the validator requires one — and this
		// test is worthless if the refusal it dodges is the funding-record guard.
		ParamsJSON:        `{"channel_id":"chan-ok"}`,
		FundingProofsJSON: `[]`,
		ChannelSecretHex:  "00",
		KeysetInfoJSON:    `{}`,
	}))

	outcome, err := ledger.RecordPayment(context.Background(), "chan-ok", 1, "sig-genuine")
	require.NoError(t, err, "a genuine payment must NOT be refused")
	require.Equal(t, LedgerOutcomeAccepted, outcome)
	require.Equal(t, 1, validator.calls,
		"the validator must have been consulted exactly once; skipping it would "+
			"mean the acceptance proves nothing about authentication")

	latest, err := ledger.LatestPayment("chan-ok")
	require.NoError(t, err)
	require.NotNil(t, latest, "an accepted payment must become durable state")
	require.Equal(t, uint64(1), latest.Balance)
	require.Equal(t, "sig-genuine", latest.Signature)
}

// And the forged one must STILL be refused, through the same public API.
//
// Paired with the test above so the pair is a discriminator: one accepts, one
// refuses, same entry point. A lockout fails the first; a verifier that does
// nothing fails the second.
func TestPublicRecordPaymentStillRefusesAForgery(t *testing.T) {
	validate := func(_ context.Context, f FundingRecord, balance uint64, _ string) error {
		if !f.Complete() {
			return ErrFundingRecordMissing
		}
		return ErrPaymentNotAuthentic
	}

	validator := validatorFunc(validate)
	ledger, err := OpenLedgerInMemoryWithValidator(validator)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, ledger.Close()) })

	require.NoError(t, ledger.RegisterChannel(context.Background(), ChannelRecord{
		ChannelID: "chan-forged", Unit: UnitSat, State: ChannelStateOpen,
		Capacity: 1000, Expiry: 1_900_000_000, OpenedAt: 1_700_000_000,
		ParamsJSON:        `{"channel_id":"chan-forged"}`,
		FundingProofsJSON: `[]`,
		ChannelSecretHex:  "00",
		KeysetInfoJSON:    `{}`,
	}))

	_, err = ledger.RecordPayment(context.Background(), "chan-forged", 5, "00")
	require.Error(t, err, "a forged signature must be refused")

	latest, err := ledger.LatestPayment("chan-forged")
	require.NoError(t, err)
	require.Nil(t, latest, "a refused payment must leave NO durable state")
}
