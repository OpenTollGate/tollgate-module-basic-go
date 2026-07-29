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
