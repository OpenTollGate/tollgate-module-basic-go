package merchant

import (
	"errors"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/tollwallet"
)

// Boot-time swap resume (#497/#719): a full merchant, once its wallet is
// up, replays the journaled swap intents exactly once — and every result
// shape (nothing pending, partial recovery, unrecoverable-this-pass) is
// non-fatal, because an intent the mint cannot answer yet stays journaled
// and the next boot retries it.

type resumeStubWallet struct {
	tollwallet.WalletPort
	calls    int
	recovery uint64
	failed   int
	err      error
}

func (w *resumeStubWallet) ResumePendingSwaps() (uint64, int, error) {
	w.calls++
	return w.recovery, w.failed, w.err
}

func TestBootResumeReplaysPendingSwapsExactlyOnce(t *testing.T) {
	for _, tc := range []struct {
		name      string
		recovery  uint64
		failed    int
		err       error
		wantCalls int
	}{
		{name: "nothing pending: quiet, no retry storm", wantCalls: 1},
		{name: "value recovered", recovery: 50, wantCalls: 1},
		{name: "some intents unrecoverable this pass", recovery: 20, failed: 1, wantCalls: 1},
		{name: "all intents unrecoverable: error surfaces, boot continues", failed: 2, err: errors.New("2 pending swap(s) unrecoverable this pass"), wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := &resumeStubWallet{recovery: tc.recovery, failed: tc.failed, err: tc.err}
			resumePendingSwapsAtBoot(stub)
			if stub.calls != tc.wantCalls {
				t.Fatalf("ResumePendingSwaps called %d time(s), want %d", stub.calls, tc.wantCalls)
			}
		})
	}
}
