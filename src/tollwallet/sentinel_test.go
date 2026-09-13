package tollwallet

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrTokenAlreadySpent_IsSentinel(t *testing.T) {
	assert.EqualError(t, ErrTokenAlreadySpent, "Token already spent")
}

func TestErrTokenAlreadySpent_WrappedErrorMatches(t *testing.T) {
	upstreamErr := fmt.Errorf("Token already spent in some context")
	wrapped := fmt.Errorf("%w: %v", ErrTokenAlreadySpent, upstreamErr)

	assert.True(t, errors.Is(wrapped, ErrTokenAlreadySpent),
		"wrapped error must match ErrTokenAlreadySpent via errors.Is")
}

func TestErrTokenAlreadySpent_UnrelatedErrorDoesNotMatch(t *testing.T) {
	unrelated := fmt.Errorf("something else went wrong")
	assert.False(t, errors.Is(unrelated, ErrTokenAlreadySpent),
		"unrelated error must not match ErrTokenAlreadySpent")
}

func TestErrTokenAlreadySpent_DoubleWrapStillMatches(t *testing.T) {
	inner := fmt.Errorf("%w: original", ErrTokenAlreadySpent)
	outer := fmt.Errorf("payment failed: %w", inner)

	assert.True(t, errors.Is(outer, ErrTokenAlreadySpent),
		"double-wrapped error must still match ErrTokenAlreadySpent via errors.Is")
}

func TestShutdown_NilWallet_NoPanic(t *testing.T) {
	w := &TollWallet{wallet: nil}
	err := w.Shutdown()
	assert.NoError(t, err)
}

func TestGetMintQuoteState_NilWallet_ReturnsError(t *testing.T) {
	w := &TollWallet{wallet: nil}
	resp, err := w.GetMintQuoteState("quote-id")
	assert.Nil(t, resp)
	assert.ErrorIs(t, err, ErrWalletNotInitialized,
		"GetMintQuoteState on an uninitialized wallet must return ErrWalletNotInitialized, not panic")
}

func TestIsAlreadySpentError_MintPhrasings(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		// Nutshell-family phrasing (the one the old code matched exactly).
		{"nutshell", fmt.Errorf("Token already spent."), true},
		// CDK-family phrasing, which the gonuts bump now surfaces verbatim.
		{"cdk inputs", fmt.Errorf("swap rejected by mint (HTTP 400, code 3): inputs have already been spent"), true},
		{"secrets", fmt.Errorf("secret already spent"), true},
		{"case-insensitive", fmt.Errorf("INPUTS HAVE ALREADY BEEN SPENT"), true},
		// The pre-bump swallowed rejection: empty message must NOT match,
		// or every failed swap would become "already spent".
		{"empty swallowed error", fmt.Errorf("could not swap proofs: "), false},
		{"unrelated", fmt.Errorf("mint rate limited"), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isAlreadySpentError(tc.err))
		})
	}
}
