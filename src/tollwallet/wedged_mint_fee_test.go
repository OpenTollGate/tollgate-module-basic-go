package tollwallet

import (
	"net"
	"testing"
	"time"
)

// silentMint listens and accepts but never answers — the wedged-mint
// shape (#525): TCP connects, then nothing.
func silentMint(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// hold the connection open, say nothing
			_ = conn
		}
	}()
	return "http://" + ln.Addr().String()
}

// The fee fetch must return within its budget against a wedged mint,
// with an error callers can fall through on — not park the payment lane
// for the lib's full retry ladder.
func TestMintKeysetFeesBoundedAgainstWedgedMint(t *testing.T) {
	old := keysetFeeFetchBudget
	keysetFeeFetchBudget = 300 * time.Millisecond
	t.Cleanup(func() { keysetFeeFetchBudget = old })

	start := time.Now()
	_, err := mintKeysetFeesBounded(silentMint(t))
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error from a wedged mint, got none")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("bounded fetch took %s; the budget (%s) did not apply", elapsed, keysetFeeFetchBudget)
	}
}

// The unbounded original is the regression witness: against the same
// silent mint it stays blocked past any sane payment deadline (the lib's
// own timeout ladder is tens of seconds to minutes). Guarded by the
// package test timeout rather than an in-test deadline so the failure
// mode is the hang itself.
func TestMintKeysetFeesUnboundedBlocksOnWedgedMint(t *testing.T) {
	if testing.Short() {
		t.Skip("demonstrates a multi-second block by design")
	}
	done := make(chan struct{})
	go func() {
		_, _ = mintKeysetFees(silentMint(t))
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("unbounded fetch returned against a wedged mint — the retry ladder changed; revisit the budget")
	case <-time.After(2 * time.Second):
		// still blocked after 2 s: the wedge holds, which is exactly why
		// the budget wrapper exists.
	}
}
