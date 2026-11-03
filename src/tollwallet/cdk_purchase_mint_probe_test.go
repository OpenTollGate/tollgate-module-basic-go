//go:build cdk_wallet

package tollwallet

import (
	"fmt"
	"os"
	"testing"
	"time"

	cdk_ffi "github.com/cashubtc/cdk-go/bindings/cdkffi"
)

// TestProbeCdkMintMbTokenForPurchase mints custom-unit ecash with the wallet's
// own machinery and prints the serialized token, so it can be presented to a
// TollGate as payment. This is the operator side of the issuance flow.
//
// Run with:
//
//	TOLLGATE_CDK_MINT_FOR_PURCHASE=http://192.168.1.159:3358 \
//	TOLLGATE_CDK_PURCHASE_METHOD=paypal \
//	TOLLGATE_CDK_PURCHASE_UNIT=mb \
//	TOLLGATE_CDK_PURCHASE_AMOUNT=100 \
//	go test -tags cdk_wallet ./tollwallet -run TestProbeCdkMintMbTokenForPurchase -v
func TestProbeCdkMintMbTokenForPurchase(t *testing.T) {
	mintURL := os.Getenv("TOLLGATE_CDK_MINT_FOR_PURCHASE")
	if mintURL == "" {
		t.Skip("set TOLLGATE_CDK_MINT_FOR_PURCHASE to run")
	}
	method := os.Getenv("TOLLGATE_CDK_PURCHASE_METHOD")
	if method == "" {
		method = "paypal"
	}
	unit := os.Getenv("TOLLGATE_CDK_PURCHASE_UNIT")
	if unit == "" {
		unit = "mb"
	}
	amount := uint64(100)
	fmt.Sscanf(os.Getenv("TOLLGATE_CDK_PURCHASE_AMOUNT"), "%d", &amount)

	dir := t.TempDir()
	w, err := NewWalletPort(dir, []string{mintURL}, false, map[string]string{mintURL: unit})
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}
	cw := w.(*CdkWallet)
	wallet, err := cw.getOrCreateWallet(mintURL)
	if err != nil {
		t.Fatalf("getOrCreateWallet: %v", err)
	}

	amt := cdk_ffi.Amount{Value: amount}
	quote, err := wallet.MintQuote(cdk_ffi.PaymentMethodCustom{Method: method}, &amt, nil, nil)
	if err != nil {
		t.Fatalf("MintQuote(%s): %v", method, err)
	}
	t.Logf("quote id=%s request=%s", quote.Id, quote.Request)

	// Poll for settlement: the bench fakewallet settles when the quote is read.
	deadline := time.Now().Add(60 * time.Second)
	for {
		q, err := wallet.CheckMintQuoteStatus(quote.Id)
		if err == nil {
			t.Logf("quote state=%v", q.State)
			if fmt.Sprintf("%v", q.State) == "2" {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("quote never settled")
		}
		time.Sleep(2 * time.Second)
	}

	proofs, err := wallet.Mint(quote.Id, cdk_ffi.SplitTargetNone{}, nil)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	var total uint64
	for _, p := range proofs {
		total += p.Amount.Value
	}
	t.Logf("minted %d proof(s) totalling %d %s", len(proofs), total, unit)

	tok, err := w.Send(total, mintURL, false)
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	ser, err := tok.Serialize()
	if err != nil {
		t.Fatalf("Serialize: %v", err)
	}
	// The purchase harness needs the token verbatim; print it on its own line.
	fmt.Printf("PURCHASE_TOKEN=%s\n", ser)
	fmt.Printf("PURCHASE_TOKEN_UNIT=%s\n", tok.Unit())
	fmt.Printf("PURCHASE_TOKEN_AMOUNT=%d\n", tok.Amount())
	if f := os.Getenv("TOLLGATE_CDK_TOKEN_OUT"); f != "" {
		if err := os.WriteFile(f, []byte(ser), 0o600); err != nil {
			t.Fatalf("write token: %v", err)
		}
		t.Logf("token written to %s", f)
	}
}
