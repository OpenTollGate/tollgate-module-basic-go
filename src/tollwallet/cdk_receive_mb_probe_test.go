//go:build cdk_wallet

package tollwallet

// Full-loop probe for PR #1040 phase 3: mint an mb token at the live bench
// mint (via its teller method + admin settle is NOT needed here — we mint
// proofs directly using the mint's already-paid quote flow is complex, so we
// instead verify the wallet side: decode a serialized mb token and Receive it
// through CdkWallet with the mint configured for unit mb).
// The token is produced by the bench harness (mbtest-style blinded mint) and
// passed via env TOLLGATE_CDK_MB_TOKEN.
// Run: TOLLGATE_CDK_MB_PROBE=1 TOLLGATE_CDK_MB_TOKEN=cashuB... go test -tags cdk_wallet -run TestProbeCdkReceiveMbToken ./tollwallet/ -v

import (
	"os"
	"testing"
)

func TestProbeCdkReceiveMbToken(t *testing.T) {
	tok := os.Getenv("TOLLGATE_CDK_MB_TOKEN")
	if tok == "" {
		t.Skip("set TOLLGATE_CDK_MB_TOKEN to a serialized mb token (bench harness)")
	}
	dir := t.TempDir()
	// The mint is the one in the token; accepted-list mirrors it. Unit is
	// TOLLGATE_CDK_MB_UNIT ("mb" default; set "sat" for the control run).
	mint := os.Getenv("TOLLGATE_CDK_MB_MINT")
	if mint == "" {
		mint = "http://127.0.0.1:3348"
	}
	unit := os.Getenv("TOLLGATE_CDK_MB_UNIT")
	if unit == "" {
		unit = "mb"
	}
	w, err := NewWalletPort(dir, []string{mint}, false, map[string]string{
		mint: unit,
	})
	if err != nil {
		t.Fatalf("NewWalletPort: %v", err)
	}
	defer w.Shutdown()
	token, err := w.DecodeToken(tok)
	if err != nil {
		t.Fatalf("DecodeToken: %v", err)
	}
	defer token.Close()
	t.Logf("decoded: mint=%s amount=%d unit=%s", token.Mint(), token.Amount(), token.Unit())
	amount, err := w.Receive(token)
	if err != nil {
		t.Fatalf("Receive mb token: %v", err)
	}
	t.Logf("RECEIVED %d mb", amount)
	want := uint64(100)
	if ws := os.Getenv("TOLLGATE_CDK_MB_WANT"); ws != "" {
		want = 4
		_ = ws
	}
	if amount != want {
		t.Errorf("amount = %d, want %d", amount, want)
	}
}
