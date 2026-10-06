package valve

import (
	"fmt"
	"testing"
)

// A "client not found" answer is only evidence about the client it NAMES.
//
// ndsctl is asked about exactly one MAC (`ndsctl deauth <mac>`), so an answer
// that says "not found" while naming a DIFFERENT MAC is not an answer about the
// MAC the module is closing. Reading it as one is the fail-open direction: the
// gate is retired (the close is reported COMPLETE) while the client the module
// actually asked about may still be Authenticated with an open gate. The old
// matcher accepted any answer containing the word "client", so it took that
// answer.
//
// The measured shape (bench MT3000, 2026-09-26, real Wi-Fi client) always names
// the MAC:
//
//	Client a8:a0:92:a5:39:7a not found.
func TestAnAnswerAboutADifferentClientIsNotThisClientSAbsence(t *testing.T) {
	const closing, other = "aa:bb:cc:dd:ee:50", "aa:bb:cc:dd:ee:51"

	for _, output := range []string{
		// The word "client" is present, so the broad disjunct accepted these.
		fmt.Sprintf("Client %s not found.\n", other),
		"client not found",
		"Client entry not found in the client table",
	} {
		if ndsctlUnknownClient(closing, output) {
			t.Errorf("ndsctlUnknownClient(%s, %q) = true, want false: the answer names no evidence about %s, so the close is NOT complete and must not be retired on it",
				closing, output, closing)
		}
	}

	// The answer the bench measured, and the case-insensitive form of it, must
	// still be recognised — tightening the matcher must not lose the fix.
	for _, output := range []string{
		fmt.Sprintf("Client %s not found.\n", closing),
		fmt.Sprintf("client %s NOT FOUND.", closing),
	} {
		if !ndsctlUnknownClient(closing, output) {
			t.Errorf("ndsctlUnknownClient(%s, %q) = false, want true: this is the measured terminal answer and the session must be retired on it", closing, output)
		}
	}

	// A failure that says nothing about the client stays a failure.
	for _, output := range []string{
		"Socket is not ready for communication : Bad file descriptor",
		"Could not connect to server",
		"",
	} {
		if ndsctlUnknownClient(closing, output) {
			t.Errorf("ndsctlUnknownClient(%s, %q) = true, want false: this failure carries no evidence about the client", closing, output)
		}
	}
}
