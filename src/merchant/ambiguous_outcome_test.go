package merchant

import (
	"errors"
	"fmt"
	"testing"

	gonutsclient "github.com/OpenTollGate/gonuts-tollgate/wallet/client"
	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// The wallet client's explicit no-answer verdict (gonuts-tollgate v0.13.0)
// must classify as an ambiguous mint outcome — the outcome-unknown notice and
// the late-receive recorder both label their records through this predicate,
// and a no-answer swap is precisely the "the mint may have taken the note"
// case those records exist to distinguish from a definitive refusal.
func TestAmbiguousOutcomeErrorIsAmbiguous(t *testing.T) {
	wrapped := fmt.Errorf("receive failed: %w",
		&gonutsclient.AmbiguousOutcomeError{
			MintURL: "https://mint.example.com",
			Err:     errors.New("connection reset by peer"),
		})
	if !isAmbiguousMintOutcomeError(wrapped) {
		t.Fatal("a wrapped AmbiguousOutcomeError must classify as ambiguous through the error chain")
	}
	if isAmbiguousMintOutcomeError(nil) {
		t.Fatal("nil is never ambiguous")
	}
	// The definitive refusals stay definitive beside it.
	if isAmbiguousMintOutcomeError(fmt.Errorf("wrap: %w", errTokenSpentStub)) {
		t.Fatal("an already-spent refusal must stay definitive, not ambiguous")
	}
}

var errTokenSpentStub = fmt.Errorf("stub: %w", tollwallet.ErrTokenAlreadySpent)
