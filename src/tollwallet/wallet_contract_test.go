//go:build testenv

package tollwallet

// The WalletPort parity contract (docs/architecture/wallet-parity, the
// "just a flag" program): ONE suite, run against BOTH backends by tags —
//   gonuts:  go test -tags testenv -run TestWalletPortContract
//   cdk-go:  go test -tags testenv,cdk_wallet -run TestWalletPortContract
// NewWalletPort IS the flag under test: nothing in this file may branch
// on the backend. Every case runs against a real local cdk-mintd
// (CDK_MINT_URL; fake-wallet settles quotes) so the verdicts exercise
// wire behavior, not stubs.
//
// Parity = both runs green on the same contract with the same
// observable outcomes (balances, token round-trips, spent verdicts).

import (
	"encoding/base64"
	"os"
	"testing"
	"time"
)

func contractMintURL(t *testing.T) string {
	t.Helper()
	u := os.Getenv("CDK_MINT_URL")
	if u == "" {
		t.Skip("CDK_MINT_URL not set — parity contract runs against a real local mint (cdk-mintd --fakewallet)")
	}
	return normalizeMintURL(u)
}

// fundContractWallet drives the mint-quote lifecycle (the port's
// RequestMintQuote → GetMintQuoteState → MintTokens) to give w exactly
// amount sats, returning once the balance reflects it. The fake wallet
// settles quotes within seconds, so the state poll has a deadline.
func fundContractWallet(t *testing.T, w WalletPort, mintURL string, amount uint64) {
	t.Helper()
	q, err := w.RequestMintQuote(amount, mintURL)
	if err != nil {
		t.Fatalf("RequestMintQuote: %v", err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		state, err := w.GetMintQuoteState(q.QuoteID)
		if err != nil {
			t.Fatalf("GetMintQuoteState: %v", err)
		}
		if state == StatePaid {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("mint quote %s never PAID (fake wallet should settle in seconds): state=%d", q.QuoteID, state)
		}
		time.Sleep(300 * time.Millisecond)
	}
	minted, err := w.MintTokens(q.QuoteID)
	if err != nil {
		t.Fatalf("MintTokens: %v", err)
	}
	if minted != amount {
		t.Fatalf("MintTokens = %d, want %d", minted, amount)
	}
	if got := w.GetBalance(); got < amount {
		t.Fatalf("balance after mint = %d, want >= %d", got, amount)
	}
}

// TestWalletPortContract_MintLifecycle pins the funding leg every other
// case builds on, including quote-state observability.
func TestWalletPortContract_MintLifecycle(t *testing.T) {
	mintURL := contractMintURL(t)
	w, err := NewWalletPort(t.TempDir(), []string{mintURL}, false)
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}
	t.Cleanup(func() { _ = w.Shutdown() })
	fundContractWallet(t, w, mintURL, 64)
}

// TestWalletPortContract_SendReceiveRoundTrip: send a token, decode it,
// spend-check it, receive it in a SECOND wallet instance (fresh seed)
// against the same mint — value must move end to end.
func TestWalletPortContract_SendReceiveRoundTrip(t *testing.T) {
	mintURL := contractMintURL(t)
	sender, err := NewWalletPort(t.TempDir(), []string{mintURL}, false)
	if err != nil {
		t.Fatalf("sender NewWalletPort: %v", err)
	}
	t.Cleanup(func() { _ = sender.Shutdown() })
	fundContractWallet(t, sender, mintURL, 64)

	tokenStr, err := sender.SendWithOverpayment(21, mintURL, 0, 0)
	if err != nil {
		t.Fatalf("SendWithOverpayment: %v", err)
	}
	if tokenStr == "" {
		t.Fatal("empty token string")
	}
	if got := sender.GetBalanceByMint(mintURL); got != 64-21 && got != 43 && got > 43 {
		// exact change shape may differ by fee/overpayment policy; it must
		// not be more than the pre-send balance minus the sent amount
		t.Fatalf("sender balance after send = %d, want <= 43", got)
	}

	recipient, err := NewWalletPort(t.TempDir(), []string{mintURL}, false)
	if err != nil {
		t.Fatalf("recipient NewWalletPort: %v", err)
	}
	t.Cleanup(func() { _ = recipient.Shutdown() })

	tok, err := recipient.DecodeToken(tokenStr)
	if err != nil {
		t.Fatalf("DecodeToken: %v", err)
	}
	t.Cleanup(tok.Close)
	if tok.Mint() != normalizeMintURL(mintURL) && tok.Mint() != mintURL {
		t.Fatalf("token mint %q does not name the contract mint %s", tok.Mint(), mintURL)
	}

	spent, err := recipient.CheckTokenSpent(tok)
	if err != nil {
		t.Fatalf("CheckTokenSpent before receive: %v", err)
	}
	if spent {
		t.Fatal("a freshly sent token must be unspent before receive")
	}

	received, err := recipient.Receive(tok)
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if received != 21 {
		t.Fatalf("Receive = %d, want 21", received)
	}
	if got := recipient.GetBalanceByMint(mintURL); got != 21 {
		t.Fatalf("recipient balance = %d, want 21", got)
	}

	// The received proofs' secrets must now be spendable onward: a
	// second receive of the SAME token must not double-credit (spent).
	tok2, err := recipient.DecodeToken(tokenStr)
	if err != nil {
		t.Fatalf("DecodeToken(again): %v", err)
	}
	t.Cleanup(tok2.Close)
	if _, err := recipient.Receive(tok2); err == nil {
		t.Fatal("re-receiving the same token must fail (proofs consumed by the first receive's swap)")
	}
}

// TestWalletPortContract_MeltLifecycle: fund, request a melt quote for a
// small fake-wallet invoice, melt, observe the balance drop by the quote
// cost. The invoice comes from the mint itself (request a mint quote and
// melt against ITS payment request) so no external LN is needed.
func TestWalletPortContract_MeltLifecycle(t *testing.T) {
	mintURL := contractMintURL(t)
	w, err := NewWalletPort(t.TempDir(), []string{mintURL}, false)
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}
	t.Cleanup(func() { _ = w.Shutdown() })
	fundContractWallet(t, w, mintURL, 64)

	// Ask the mint for a second invoice (a mint quote's payment_request
	// is a payable bolt11 under the fake wallet) and melt into it.
	q, err := w.RequestMintQuote(10, mintURL)
	if err != nil {
		t.Fatalf("RequestMintQuote(10) for the melt invoice: %v", err)
	}
	if q.Request == "" {
		t.Fatal("mint quote carries no invoice — cannot drive the melt leg")
	}
	mq, err := w.RequestMeltQuote(q.Request, mintURL)
	if err != nil {
		t.Fatalf("RequestMeltQuote: %v", err)
	}
	res, err := w.Melt(mq.QuoteID)
	if err != nil {
		t.Fatalf("Melt: %v", err)
	}
	if !res.Paid {
		t.Fatal("melt reported not paid")
	}
	if got := w.GetBalance(); got > 64-10 {
		t.Fatalf("balance after melt = %d, want <= 54 (melt + fees)", got)
	}
}

// TestWalletPortContract_Drain empties a funded mint balance into one
// token and the returned amount must equal the balance it emptied.
func TestWalletPortContract_Drain(t *testing.T) {
	mintURL := contractMintURL(t)
	w, err := NewWalletPort(t.TempDir(), []string{mintURL}, false)
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}
	t.Cleanup(func() { _ = w.Shutdown() })
	fundContractWallet(t, w, mintURL, 64)

	tok, amount, err := w.Drain(mintURL)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	t.Cleanup(tok.Close)
	if amount != 64 {
		t.Fatalf("drained %d, want 64", amount)
	}
	if got := w.GetBalanceByMint(mintURL); got != 0 {
		t.Fatalf("balance after drain = %d, want 0", got)
	}
	// The drained token must decode with the full amount.
	tokStr, err := tok.Serialize()
	if err != nil {
		t.Fatalf("Serialize drained token: %v", err)
	}
	t2, err := w.DecodeToken(tokStr)
	if err != nil {
		t.Fatalf("DecodeToken drained: %v", err)
	}
	t.Cleanup(t2.Close)
	if t2.Amount() != 64 {
		t.Fatalf("drained token amount = %d, want 64", t2.Amount())
	}
}

// TestWalletPortContract_DecodeRejectsGarbage: the decode contract must
// fail cleanly (never panic) on hostile input — the corpus lane covers
// deep vectors; this is the smoke that a bogus string is an error.
func TestWalletPortContract_DecodeRejectsGarbage(t *testing.T) {
	_ = contractMintURL(t) // decode is offline, but keep the lane gate uniform
	w, err := NewWalletPort(t.TempDir(), []string{"http://decode.invalid"}, false)
	if err != nil {
		t.Skipf("backend requires a live mint for construction: %v", err)
	}
	t.Cleanup(func() { _ = w.Shutdown() })
	for _, bad := range []string{"", "cashuA", "cashuA!!!!", "cashuB" + base64.RawURLEncoding.EncodeToString([]byte("{}")), string([]byte{0xff, 0xfe})} {
		if _, err := w.DecodeToken(bad); err == nil {
			t.Errorf("DecodeToken(%q) accepted garbage", bad)
		}
	}
}
