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
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestWalletPortContract_RecipientHelper is the subprocess leg of the
// round-trip: the parent test re-executes the test binary with
// -test.run=TestWalletPortContract_RecipientHelper and the token on disk.
// Running the recipient as a SEPARATE PROCESS is the honest contract shape:
// it is how a real recipient exists (another device, another process), it
// keeps each process at one wallet instance — the shape every backend
// supports, cdk-go included (its FFI holds process-global runtime state) —
// and it exercises full token serialization across the process boundary.
func TestWalletPortContract_RecipientHelper(t *testing.T) {
	mintURL := os.Getenv("WALLET_CONTRACT_MINT")
	tokenFile := os.Getenv("WALLET_CONTRACT_TOKEN")
	if tokenFile == "" {
		t.Skip("helper leg: launched directly, not by the round-trip parent")
	}
	tokenStr, err := os.ReadFile(tokenFile)
	if err != nil {
		t.Fatalf("helper: read token file: %v", err)
	}
	recipient, err := NewWalletPort(t.TempDir(), []string{mintURL}, false)
	if err != nil {
		t.Fatalf("helper: NewWalletPort: %v", err)
	}
	t.Cleanup(func() { _ = recipient.Shutdown() })
	tok, err := recipient.DecodeToken(string(tokenStr))
	if err != nil {
		t.Fatalf("helper: DecodeToken: %v", err)
	}
	if spent, err := recipient.CheckTokenSpent(tok); err != nil {
		t.Fatalf("helper: CheckTokenSpent: %v", err)
	} else if spent {
		t.Fatal("helper: freshly sent token must be unspent")
	}
	received, err := recipient.Receive(tok)
	if err != nil {
		t.Fatalf("helper: Receive: %v", err)
	}
	// The verdict crosses to the parent via stdout on the marker line.
	fmt.Printf("WALLET_CONTRACT_RECEIVED=%d\n", received)
	if got := recipient.GetBalanceByMint(normalizeMintURL(mintURL)); got != received {
		t.Fatalf("helper: balance %d != received %d", got, received)
	}
}

// TestWalletPortContract_SendReceiveRoundTrip: send a token, decode it,
// spend-check it, receive it in a SECOND wallet (fresh seed, separate
// process — see the helper) against the same mint — value must move end
// to end across the serialization boundary.
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

	// The recipient is this same test binary, re-executed (tags baked in),
	// carrying the token on disk. Backend-agnostic: the flag under test is
	// inherited from the binary itself.
	tokenFile := filepath.Join(t.TempDir(), "token.cashu")
	if err := os.WriteFile(tokenFile, []byte(tokenStr), 0600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestWalletPortContract_RecipientHelper$", "-test.v=false")
	cmd.Env = append(os.Environ(),
		"WALLET_CONTRACT_MINT="+mintURL,
		"WALLET_CONTRACT_TOKEN="+tokenFile,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("recipient subprocess failed: %v\noutput:\n%s", err, out)
	}
	received := uint64(0)
	for _, line := range strings.Split(string(out), "\n") {
		if v, perr := fmt.Sscanf(line, "WALLET_CONTRACT_RECEIVED=%d", &received); perr == nil && v == 1 {
			break
		}
	}
	if received != 21 {
		t.Fatalf("recipient reported %d, want 21 (output:\n%s)", received, out)
	}

	// The sender's side must show the spend: re-receiving the SAME token
	// in the sender's process must not double-credit (proofs consumed).
	tok2, err := sender.DecodeToken(tokenStr)
	if err != nil {
		t.Fatalf("DecodeToken(again): %v", err)
	}
	t.Cleanup(tok2.Close)
	if _, err := sender.Receive(tok2); err == nil {
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
