//go:build regtest

package hedgehog

// REGTEST LIFECYCLE — END-TO-END AGAINST REAL BITCOIN CONSENSUS
//
// This test is the consensus-level evidence for the whole package. Everything
// else validates against fixtures or btcd's own engine; this one funds a real
// taproot output on regtest, builds the spend with OUR code, and asks bitcoind
// whether it accepts it.
//
// It is behind the `regtest` build tag because it needs a live node. Run it with:
//
//	BTC_RPC=http://127.0.0.1:18443 BTC_USER=... BTC_PASS=... \
//	  go test -tags regtest -run TestRegtestLifecycle -v ./hedgehog/
//
// The lifecycle it exercises is the one TollGate needs:
//
//	OPEN     fund the shared 2-of-2 output (on-chain, both parties act)
//	PROPOSE  PAYER ALONE, payee absent and never contacted  <-- the key step
//	ACCEPT   PAYEE later, alone, using only the cheque
//	CLOSE    cooperative 2-of-2 spend, both signatures, ACCEPTED BY CORE

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/wire"
)

func coreView(t *testing.T) *BitcoinCoreChainView {
	t.Helper()
	url := os.Getenv("BTC_RPC")
	if url == "" {
		url = "http://127.0.0.1:18443"
	}
	v, err := NewBitcoinCoreChainView(CoreConfig{
		URL:    url,
		User:   os.Getenv("BTC_USER"),
		Pass:   os.Getenv("BTC_PASS"),
		Wallet: os.Getenv("BTC_WALLET"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Probe, so a missing node produces a skip rather than a confusing failure.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := v.TipHeight(ctx); err != nil {
		t.Skipf("no regtest node at %s: %v", url, err)
	}
	return v
}

func callStr(t *testing.T, v *BitcoinCoreChainView, wallet bool, method string, params ...any) string {
	t.Helper()
	raw, err := v.CallRaw(context.Background(), wallet, method, params...)
	if err != nil {
		t.Fatalf("%s: %v", method, err)
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("%s: expected a string result, got %s", method, raw)
	}
	return s
}

// mine generates n blocks to a fresh wallet address. generatetoaddress returns an
// ARRAY of block hashes, not a string, so it cannot go through callStr.
func mine(t *testing.T, v *BitcoinCoreChainView, n int) {
	t.Helper()
	addr := callStr(t, v, true, "getnewaddress")
	if _, err := v.CallRaw(context.Background(), true, "generatetoaddress", n, addr); err != nil {
		t.Fatalf("generatetoaddress %d: %v", n, err)
	}
}

// TestRegtestLifecycle drives the full channel against a live node.
func TestRegtestLifecycle(t *testing.T) {
	v := coreView(t)
	ctx := context.Background()

	// ---------------------------------------------------------------------
	// Build the channel from two deterministic keys.
	// ---------------------------------------------------------------------
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
	revHashPayer := ripemd160ForTest(t, []byte("payer-revocation-secret"))
	revHashPayee := ripemd160ForTest(t, []byte("payee-revocation-secret"))

	const csvBlocks = 6 // small so the test can actually expire it
	ch, err := NewChannel(payerX, payeeX, csvBlocks, revHashPayer, revHashPayee)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("funding scriptPubKey: %x", ch.ScriptPubKey)

	// ---------------------------------------------------------------------
	// OPEN: fund the channel output. Both parties act; this is the on-chain
	// step whose cost the operator has accepted for large purchases.
	// ---------------------------------------------------------------------
	spkHex := hex.EncodeToString(ch.ScriptPubKey)
	funded, err := v.CallRaw(ctx, true, "sendtoaddress",
		p2trAddressFromSPK(t, v, spkHex), 1.0)
	if err != nil {
		t.Fatalf("funding the channel: %v", err)
	}
	var fundingTxid string
	if err := json.Unmarshal(funded, &fundingTxid); err != nil {
		t.Fatal(err)
	}
	// Mine it so the CSV window starts counting.
	mine(t, v, 1)
	t.Logf("OPEN: funded %s", fundingTxid)

	st, err := v.GetTxStatus(ctx, fundingTxid)
	if err != nil {
		t.Fatal(err)
	}
	if !st.Confirmed {
		t.Fatalf("funding tx should be confirmed after mining: %+v", st)
	}
	t.Logf("OPEN confirmed at height %d", st.Height)

	// Locate the funding output.
	var fundingVout uint32 = 0xffffffff
	var fundingValue int64
	for i := uint32(0); i < 4; i++ {
		o, err := v.GetTxOutput(ctx, fundingTxid, i)
		if err != nil {
			continue
		}
		if hex.EncodeToString(o.ScriptPubKey) == spkHex {
			fundingVout, fundingValue = i, o.Value
			break
		}
	}
	if fundingVout == 0xffffffff {
		t.Fatal("could not locate the channel output")
	}
	t.Logf("OPEN: vout=%d value=%d", fundingVout, fundingValue)

	// ---------------------------------------------------------------------
	// PROPOSE: the PAYER produces a cheque with the payee ABSENT.
	//
	// The payee object is not constructed and not contacted anywhere in this
	// block. That is the property TollGate needs: the customer may have no WAN.
	// ---------------------------------------------------------------------
	hash, err := chainhash.NewHashFromStr(fundingTxid)
	if err != nil {
		t.Fatal(err)
	}
	settle := wire.NewMsgTx(2)
	settle.AddTxIn(wire.NewTxIn(wire.NewOutPoint(hash, fundingVout), nil, nil))
	// Send the whole value back to the same output, minus a fee. A real
	// implementation would split payer/payee here; for the lifecycle test the
	// destination is immaterial, only that the SCRIPT PATH executes.
	settle.AddTxOut(wire.NewTxOut(fundingValue-3000, ch.ScriptPubKey))

	prevOut := wire.NewTxOut(fundingValue, ch.ScriptPubKey)
	prevOuts := []*wire.TxOut{prevOut}

	payer, err := NewSigner(payerSK)
	if err != nil {
		t.Fatal(err)
	}
	msLeaf := ch.Scripts.Multisig
	payerSig, err := payer.SignScriptPath(settle, 0, prevOuts, msLeaf)
	if err != nil {
		t.Fatalf("payer signing offline: %v", err)
	}

	payerState, err := NewChannelState(ch, RolePayer, fundingValue)
	if err != nil {
		t.Fatal(err)
	}
	cheque, err := payerState.Propose(250_000, settle.TxHash().String(), payerSig)
	if err != nil {
		t.Fatalf("payer must be able to propose alone: %v", err)
	}
	t.Logf("PROPOSE: payer produced a cheque offline (seq=%d, %d B signature)",
		cheque.Seq, len(cheque.PayerSignature))

	// The payer's half must NOT be spendable alone -- the safety property that
	// makes it safe to transmit.
	cb, err := ch.ControlBlockFor(msLeaf)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyScriptPath(settle, 0, prevOut,
		WitnessForPayerHalf(payerSig, msLeaf, cb)); err == nil {
		t.Fatal("payer's half spent the channel alone -- the protocol's core " +
			"safety property is broken")
	} else {
		t.Logf("PROPOSE: payer's half correctly cannot spend alone (%v)", err)
	}

	// ---------------------------------------------------------------------
	// ACCEPT + CLOSE: the PAYEE completes it later, alone, and we ask CORE.
	// ---------------------------------------------------------------------
	payee, err := NewSigner(payeeSK)
	if err != nil {
		t.Fatal(err)
	}
	payeeSig, err := payee.SignScriptPath(settle, 0, prevOuts, msLeaf)
	if err != nil {
		t.Fatalf("payee signing: %v", err)
	}

	payeeState, err := NewChannelState(ch, RolePayee, fundingValue)
	if err != nil {
		t.Fatal(err)
	}
	if err := payeeState.Accept(cheque, payeeSig); err != nil {
		t.Fatalf("payee must accept alone: %v", err)
	}
	t.Logf("ACCEPT: payee completed alone: %s", payeeState.Describe())

	settle.TxIn[0].Witness = WitnessForMultisig(payerSig, payeeSig, msLeaf, cb)
	rawHex := hex.EncodeToString(serializeTxForTest(t, settle))

	// THE DECISIVE CALL: does real consensus accept our spend?
	// testmempoolaccept takes an ARRAY of raw tx hex strings. Pass a Go slice so
	// the JSON encoder produces a real array, not a pre-baked fragment.
	res, err := v.CallRaw(ctx, false, "testmempoolaccept", []string{rawHex})
	if err != nil {
		t.Fatalf("testmempoolaccept: %v", err)
	}
	var accepted []struct {
		Allowed      bool   `json:"allowed"`
		RejectReason string `json:"reject-reason"`
	}
	if err := json.Unmarshal(res, &accepted); err != nil {
		t.Fatal(err)
	}
	if len(accepted) == 0 {
		t.Fatal("empty testmempoolaccept result")
	}
	if !accepted[0].Allowed {
		t.Fatalf("BITCOIN CORE REJECTED our spend: %q\nraw: %s",
			accepted[0].RejectReason, rawHex)
	}
	t.Logf("CLOSE: *** Bitcoin Core ACCEPTED the spend ***")

	// Broadcast it, so the evidence is a real transaction, not just a verdict.
	bcast, err := v.CallRaw(ctx, false, "sendrawtransaction", rawHex)
	if err != nil {
		t.Fatalf("broadcast: %v", err)
	}
	var bcastTxid string
	if err := json.Unmarshal(bcast, &bcastTxid); err != nil {
		t.Fatal(err)
	}
	t.Logf("CLOSE: broadcast %s", bcastTxid)

	if err := payeeState.Close(); err != nil {
		t.Fatalf("cooperative close: %v", err)
	}
	t.Logf("CLOSE: %s", payeeState.Describe())

	// ---------------------------------------------------------------------
	// The CSV window, measured on a real chain.
	// ---------------------------------------------------------------------
	watch, err := CheckCSV(ctx, v, fundingTxid, csvBlocks)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CSV after mining: %s", watch)
	if watch.Expired() {
		t.Fatalf("a %d-block window should not be expired immediately: %s",
			csvBlocks, watch)
	}
	mine(t, v, int(csvBlocks)+1)
	watch2, err := CheckCSV(ctx, v, fundingTxid, csvBlocks)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("CSV after %d more blocks: %s", csvBlocks+1, watch2)
	if !watch2.Expired() {
		t.Fatalf("window should be expired after %d blocks: %s", csvBlocks+1, watch2)
	}
	t.Logf("*** LIFECYCLE COMPLETE: open -> propose(offline payer) -> accept -> "+
		"close(Core-accepted) -> csv expired on a real chain ***")
}
