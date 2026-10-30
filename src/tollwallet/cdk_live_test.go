//go:build cdk_wallet && testenv

package tollwallet

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCdkInProcessLiveRoundTrip drives the full money cycle through the
// WalletPort interface IN-PROCESS (fork staticlib, no sidecar): quote →
// settle (fakewallet) → mint → balance → send → receive on a second
// wallet instance → check state. Skipped unless TOLLWALLET_LIVE_MINT
// points at a zero-fee fakewallet-class mint (the bench's omarchy dev
// mint). This is the in-process leg of the three-backend parity claim:
// the same assertions the sidecar parity lane (tests/cloud-lab/parity)
// makes over the socket, and the gonuts tests make in-process.
func TestCdkInProcessLiveRoundTrip(t *testing.T) {
	mint := os.Getenv("TOLLWALLET_LIVE_MINT")
	if mint == "" {
		t.Skip("TOLLWALLET_LIVE_MINT not set — needs a zero-fee fakewallet-class mint")
	}

	dir := t.TempDir()
	wp, err := NewWalletPort(filepath.Join(dir, "w"), []string{mint}, false)
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}
	defer wp.Shutdown()

	q, err := wp.RequestMintQuote(1000, mint)
	if err != nil {
		t.Fatalf("RequestMintQuote: %v", err)
	}
	if q.QuoteID == "" || q.Request == "" {
		t.Fatalf("quote incomplete: %+v", q)
	}

	// Fakewallet settles instantly; poll to PAID with a bounded wait.
	deadline := time.Now().Add(30 * time.Second)
	paid := false
	for time.Now().Before(deadline) {
		st, err := wp.GetMintQuoteState(q.QuoteID)
		if err != nil {
			t.Fatalf("GetMintQuoteState: %v", err)
		}
		if st == StatePaid || st == StateIssued {
			paid = true
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !paid {
		t.Fatal("quote never reached PAID")
	}

	if _, err := wp.MintTokens(q.QuoteID); err != nil {
		t.Fatalf("MintTokens: %v", err)
	}
	if got := wp.GetBalanceByMint(mint); got != 1000 {
		t.Fatalf("balance after mint: %d, want 1000", got)
	}

	tok, err := wp.Send(400, mint, false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	defer tok.Close()
	if tok.Amount() != 400 || tok.Mint() == "" {
		t.Fatalf("token fields: amount=%d mint=%q", tok.Amount(), tok.Mint())
	}

	// NUT-07 through WalletPort: the token's proofs are unspent until
	// the payee receives (the receive-swap consumes them).
	if spent, err := wp.CheckTokenSpent(tok); err != nil || spent {
		t.Fatalf("CheckTokenSpent before receive: spent=%v err=%v (want false,nil)", spent, err)
	}

	// Receive on an independent wallet instance (fresh store, same
	// seed semantics are NOT wanted here — a second wallet with its own
	// seed is the realistic payee).
	wp2, err := NewWalletPort(filepath.Join(dir, "w2"), []string{mint}, false)
	if err != nil {
		t.Fatalf("NewWalletPort (payee): %v", err)
	}
	defer wp2.Shutdown()
	got, err := wp2.Receive(tok)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if got != 400 {
		t.Fatalf("received %d, want 400", got)
	}
	if b := wp2.GetBalanceByMint(mint); b != 400 {
		t.Fatalf("payee balance: %d, want 400", b)
	}
	if b := wp.GetBalanceByMint(mint); b != 600 {
		t.Fatalf("payer balance after send: %d, want 600", b)
	}

	// After the payee's receive-swap the token is provably spent.
	if spent, err := wp.CheckTokenSpent(tok); err != nil || !spent {
		t.Fatalf("CheckTokenSpent after receive: spent=%v err=%v (want true,nil)", spent, err)
	}
}
