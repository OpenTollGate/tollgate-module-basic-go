package hedgehog

// Tests for the cmd/hedgehog-verify CLI.
//
// The CLI is the ON-DEVICE artefact: it is cross-compiled for aarch64 and runs on
// the router, where there is no Bitcoin node. So it must be tested as a BINARY,
// not only as a library -- the JSON contract and the exit codes are what the lane
// and the router actually depend on.
//
// The witness is built IN-PROCESS (the Go tests hold the keys) and handed to the
// binary as JSON, which is exactly how the router will invoke it.

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
)

// buildVerifyCLI compiles the CLI once per test run.
func buildVerifyCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "hedgehog-verify")
	cmd := exec.Command("go", "build", "-o", bin,
		"github.com/OpenTollGate/tollgate-module-basic-go/cmd/hedgehog-verify")
	cmd.Env = append(os.Environ(), "GOTOOLCHAIN=local", "GOFLAGS=-mod=mod")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build the CLI here: %v\n%s", err, out)
	}
	return bin
}

// invokeVerify runs the binary with a JSON request on stdin.
func invokeVerify(t *testing.T, bin string, req map[string]any) (int, map[string]any) {
	t.Helper()
	payload, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-")
	cmd.Stdin = bytes.NewReader(payload)
	out, err := cmd.Output()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("running the CLI: %v", err)
	}
	var resp map[string]any
	if len(out) > 0 {
		if uerr := json.Unmarshal(out, &resp); uerr != nil {
			t.Fatalf("CLI did not emit JSON: %q", string(out))
		}
	}
	return code, resp
}

// TestVerifyCLIRejectsMalformedInput pins the exit-code contract: 2 means the
// input could not be parsed, which is distinct from 1 (a spend that did not
// verify). A lane needs to tell those apart.
func TestVerifyCLIRejectsMalformedInput(t *testing.T) {
	bin := buildVerifyCLI(t)

	code, resp := invokeVerify(t, bin, map[string]any{
		"tx_hex": "zzzz", "prev_script": "00"})
	if code != 2 {
		t.Errorf("bad hex should exit 2, got %d (%v)", code, resp)
	}

	code, _ = invokeVerify(t, bin, map[string]any{
		"tx_hex": "0200000001", "prev_script": "00", "vout": 99})
	if code != 2 {
		t.Errorf("out-of-range vout should exit 2, got %d", code)
	}

	code, _ = invokeVerify(t, bin, map[string]any{
		"tx_hex": "00", "prev_script": "zz"})
	if code != 2 {
		t.Errorf("bad prev_script hex should exit 2, got %d", code)
	}
}

// TestVerifyCLIOnRealSpend is the substantive test: it builds the exact taproot
// spend the regtest lifecycle produces, and checks BOTH directions.
//
// Either direction alone is weak. A verifier that accepted everything passes the
// positive case; one that rejected everything passes the negative. Only both
// together pin the behaviour.
func TestVerifyCLIOnRealSpend(t *testing.T) {
	bin := buildVerifyCLI(t)

	payerSK := make([]byte, 32)
	payeeSK := make([]byte, 32)
	payerSK[31], payeeSK[31] = 0x11, 0x22

	payerX, err := XOnlyOf(payerSK)
	if err != nil {
		t.Fatal(err)
	}
	payeeX, err := XOnlyOf(payeeSK)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := NewChannel(payerX, payeeX, 2016, make([]byte, 20), make([]byte, 20))
	if err != nil {
		t.Fatal(err)
	}

	const fundingValue = 100_000_000
	// A synthetic funding txid; the verifier does not consult the chain, which is
	// the whole point of it running on a node-less router.
	var h chainhash.Hash
	for i := range h {
		h[i] = byte(i + 1)
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(&h, 0), nil, nil))
	tx.AddTxOut(wire.NewTxOut(fundingValue-3000, ch.ScriptPubKey))

	prevOuts := []*wire.TxOut{wire.NewTxOut(fundingValue, ch.ScriptPubKey)}
	leaf := ch.Scripts.Multisig

	payer, err := NewSigner(payerSK)
	if err != nil {
		t.Fatal(err)
	}
	payee, err := NewSigner(payeeSK)
	if err != nil {
		t.Fatal(err)
	}
	payerSig, err := payer.SignScriptPath(tx, 0, prevOuts, leaf)
	if err != nil {
		t.Fatal(err)
	}
	payeeSig, err := payee.SignScriptPath(tx, 0, prevOuts, leaf)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := ch.ControlBlockFor(leaf)
	if err != nil {
		t.Fatal(err)
	}

	// Serialise the tx WITHOUT witness; the CLI takes the witness separately.
	bare := tx.Copy()
	for _, in := range bare.TxIn {
		in.Witness = nil
	}
	var buf writerBufferForTest
	if err := bare.Serialize(&buf); err != nil {
		t.Fatal(err)
	}

	hexOf := func(bs [][]byte) []string {
		out := make([]string, 0, len(bs))
		for _, b := range bs {
			out = append(out, hex.EncodeToString(b))
		}
		return out
	}

	base := map[string]any{
		"tx_hex":      hex.EncodeToString(buf.Bytes()),
		"vout":        0,
		"prev_value":  fundingValue,
		"prev_script": hex.EncodeToString(ch.ScriptPubKey),
	}

	// Positive: the completed 2-of-2 spend must verify.
	full := map[string]any{}
	for k, v := range base {
		full[k] = v
	}
	full["witness"] = hexOf(WitnessForMultisig(payerSig, payeeSig, leaf, cb))
	code, resp := invokeVerify(t, bin, full)
	if code != 0 || resp["valid"] != true {
		t.Errorf("completed 2-of-2 spend should verify (code=%d, resp=%v)",
			code, resp)
	}

	// Negative: the payer's half alone must NOT verify. This is the safety
	// property that makes a cheque safe to hand over.
	half := map[string]any{}
	for k, v := range base {
		half[k] = v
	}
	half["witness"] = hexOf(WitnessForPayerHalf(payerSig, leaf, cb))
	half["expect_failure"] = true
	code, resp = invokeVerify(t, bin, half)
	if code != 0 {
		t.Errorf("payer's half must NOT verify, and expect_failure should make "+
			"that exit 0 (code=%d, resp=%v)", code, resp)
	}

	// And without expect_failure, the same witness must exit 1 -- proving the
	// verifier genuinely rejects it rather than the flag doing the work.
	half2 := map[string]any{}
	for k, v := range base {
		half2[k] = v
	}
	half2["witness"] = hexOf(WitnessForPayerHalf(payerSig, leaf, cb))
	code, resp = invokeVerify(t, bin, half2)
	if code != 1 {
		t.Errorf("payer's half should exit 1 without expect_failure, got %d", code)
	}
	if resp["valid"] == true {
		t.Error("verifier reported the payer's half as valid")
	}
	t.Logf("negative-case reason: %v", resp["reason"])
}

type writerBufferForTest struct{ b []byte }

func (w *writerBufferForTest) Write(p []byte) (int, error) {
	w.b = append(w.b, p...)
	return len(p), nil
}
func (w *writerBufferForTest) Bytes() []byte { return w.b }
