//go:build cdk_wallet

package tollwallet

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"

	cdk_ffi "github.com/cashubtc/cdk-go/bindings/cdkffi"
)

// TestProbeCdkSelfLoopMintsWithTheWallet asks the FFI wallet to mint an
// mb-denominated token from the custom-method ("teller") mb mint using its own
// machinery, then feeds that token back through Receive.
//
// This exists to separate two very different failures:
//   - the FFI cannot mint/receive custom units at all, or
//   - the FFI is fine, and only foreign-produced tokens (my bench python) are
//     rejected.
//
// Run with:
//
//	TOLLGATE_CDK_SELFLOOP_MINT=http://127.0.0.1:3348 \
//	TOLLGATE_CDK_SELFLOOP_METHOD=teller \
//	TOLLGATE_CDK_SELFLOOP_UNIT=mb \
//	go test -tags cdk_wallet ./tollwallet -run TestProbeCdkSelfLoop -v
func TestProbeCdkSelfLoopMintsWithTheWallet(t *testing.T) {
	if os.Getenv("TOLLGATE_CDK_SELFLOOP_MINT") == "" {
		t.Skip("set TOLLGATE_CDK_SELFLOOP_MINT to run the self-loop probe")
	}
	mintURL := os.Getenv("TOLLGATE_CDK_SELFLOOP_MINT")
	method := os.Getenv("TOLLGATE_CDK_SELFLOOP_METHOD")
	if method == "" {
		method = "teller"
	}
	unit := os.Getenv("TOLLGATE_CDK_SELFLOOP_UNIT")
	if unit == "" {
		unit = "mb"
	}

	dir := t.TempDir()
	w, err := NewWalletPort(dir, []string{mintURL}, false, map[string]string{mintURL: unit})
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}

	// Step 1: ask for a quote on the custom payment method.
	cw, ok := w.(*CdkWallet)
	if !ok {
		t.Fatalf("wallet is %T, want *CdkWallet", w)
	}
	wallet, err := cw.getOrCreateWallet(mintURL)
	if err != nil {
		t.Fatalf("getOrCreateWallet: %v", err)
	}
	amt := cdk_ffi.Amount{Value: 100}
	quote, err := wallet.MintQuote(cdk_ffi.PaymentMethodCustom{Method: method}, &amt, nil, nil)
	if err != nil {
		t.Fatalf("MintQuote(%s): %v", method, err)
	}
	t.Logf("quote: id=%s request=%s state=%v", quote.Id, quote.Request, quote.State)

	// Step 2: the teller (operator) settles it out of band. The probe does this
	// via the mint's own HTTP surface so the test stays self-contained; a real
	// deployment does it from the operator's console.
	if err := settleTellerQuote(quote.Request); err != nil {
		t.Logf("settle via bench helper failed (%v) — quote may auto-settle", err)
	}

	// Step 3: mint with the wallet's own machinery.
	proofs, err := wallet.Mint(quote.Id, cdk_ffi.SplitTargetNone{}, nil)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	var total uint64
	for _, p := range proofs {
		total += p.Amount.Value
	}
	t.Logf("wallet minted %d proof(s) totalling %d %s", len(proofs), total, unit)
	if total == 0 {
		t.Fatalf("minted nothing")
	}

	// Step 4: the wallet's own token, back through the wallet's own Receive.
	tok, err := w.Send(total, mintURL, false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	ser, err := tok.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	t.Logf("wallet-produced token: %d bytes, unit reported %q", len(ser), tok.Unit())

	// Receive into a SECOND wallet instance (distinct dir + seed is fine; here we
	// use a fresh dir so the proofs are unknown to it and a real swap must run).
	dir2 := t.TempDir()
	w2, err := NewWalletPort(dir2, []string{mintURL}, false, map[string]string{mintURL: unit})
	if err != nil {
		t.Fatalf("NewWalletPort(2): %v", err)
	}
	got, err := w2.Receive(tok)
	if err != nil {
		t.Fatalf("self-loop Receive FAILED: %v", err)
	}
	t.Logf("SELF-LOOP RECEIVED %d %s (wallet-produced token)", got, unit)
	if got != total {
		t.Errorf("received %d, want %d", got, total)
	}
}

// settleTellerQuote marks a teller quote paid through the bench mint's
// processor admin endpoint. Bench-only helper.
func settleTellerQuote(request string) error {
	url := os.Getenv("TOLLGATE_CDK_SELFLOOP_SETTLE")
	if url == "" {
		url = "http://127.0.0.1:8099/settle?ticket="
	}
	resp, err := http.Post(url+request, "application/json", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("settle response: %d %s\n", resp.StatusCode, string(body))
	return nil
}
