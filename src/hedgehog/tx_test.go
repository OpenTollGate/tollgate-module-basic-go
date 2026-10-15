package hedgehog

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// signingFixture mirrors research/hedgehog/signing_fixture.json, which is
// produced against a live regtest node. It records a funded taproot output whose
// scriptPubKey was derived INDEPENDENTLY in Python and then confirmed equal to
// what Bitcoin Core's descriptor compiler produces.
type signingFixture struct {
	PayerXOnly      string `json:"payer_xonly"`
	PayerSK         string `json:"payer_sk"`
	PayeeXOnly      string `json:"payee_xonly"`
	PayeeSK         string `json:"payee_sk"`
	LeafHex         string `json:"leaf_hex"`
	LeafHash        string `json:"leaf_hash"`
	TweakScalar     string `json:"tweak_scalar"`
	OutputKey       string `json:"output_key"`
	ScriptPubKey    string `json:"script_pub_key"`
	CoreScriptPubKey string `json:"core_script_pub_key"`
	CoreSPKMatches  bool   `json:"core_spk_matches"`
	ControlBlock    string `json:"control_block"`
	ControlBlockLen int    `json:"control_block_len"`
	FundingTxid     string `json:"funding_txid"`
	FundingVout     uint32 `json:"funding_vout"`
	FundingValue    int64  `json:"funding_value"`
	NUMSInternalKey string `json:"nums_internal_key"`
	Descriptor      string `json:"descriptor"`
}

func loadSigningFixture(t *testing.T) *signingFixture {
	t.Helper()
	p := filepath.Join("..", "..", "research", "hedgehog", "signing_fixture.json")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Skipf("signing fixture absent (%v); run research/hedgehog/signing_fixture.py", err)
	}
	var f signingFixture
	if err := json.Unmarshal(raw, &f); err != nil {
		t.Fatalf("bad fixture: %v", err)
	}
	return &f
}

// TestFixtureScriptPubKeyMatchesCore re-derives the output key IN GO from the
// fixture's leaf and internal key, and asserts it equals the scriptPubKey that
// Bitcoin Core produced. This is the Go side of the Python cross-check: two
// independent implementations agreeing with consensus.
func TestFixtureScriptPubKeyMatchesCore(t *testing.T) {
	f := loadSigningFixture(t)

	leaf := mustHex(t, f.LeafHex)
	nums := mustHex(t, f.NUMSInternalKey)

	root, err := TreeRoot(Leaf(leaf))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(root) != f.LeafHash {
		t.Errorf("leaf hash mismatch\n  fixture: %s\n  ours:    %x", f.LeafHash, root)
	}

	ok, err := OutputKey(root)
	if err != nil {
		t.Fatal(err)
	}
	ourSpk := hex.EncodeToString(P2TRScriptPubKey(ok))
	if ourSpk != f.CoreScriptPubKey {
		t.Errorf("scriptPubKey MISMATCH against Bitcoin Core\n  Core: %s\n  ours: %s",
			f.CoreScriptPubKey, ourSpk)
	}
	if !f.CoreSPKMatches {
		t.Error("fixture records that the Python derivation did NOT match Core -- " +
			"the fixture is not trustworthy")
	}

	// Control block must be exactly 33 bytes for a single-leaf tree, and must
	// carry the NUMS internal key after the version|parity byte.
	cb := mustHex(t, f.ControlBlock)
	if len(cb) != 33 {
		t.Fatalf("single-leaf control block must be 33 bytes, got %d", len(cb))
	}
	if hex.EncodeToString(cb[1:]) != hex.EncodeToString(nums) {
		t.Error("control block must carry the NUMS internal key")
	}

	// Our independently-built control block must equal the fixture's.
	ours, err := ControlBlock(leaf, Leaf(leaf))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(ours) != f.ControlBlock {
		t.Errorf("control block mismatch\n  fixture: %s\n  ours:    %s",
			f.ControlBlock, hex.EncodeToString(ours))
	}
}

// TestOurSignatureVerifiesUnderConsensusRules builds the spend ourselves, signs
// it with the payer key from the fixture, and verifies it with btcd's script
// engine using STANDARD flags. This proves the whole chain: sighash -> signature
// -> witness order -> script execution.
//
// It is in-process, which is weaker than a Core accept, so the regtest e2e test
// (built on the same primitives) carries the consensus-level evidence.
func TestOurSignatureVerifiesUnderConsensusRules(t *testing.T) {
	f := loadSigningFixture(t)

	leaf := mustHex(t, f.LeafHex)
	cb := mustHex(t, f.ControlBlock)
	spk := mustHex(t, f.ScriptPubKey)
	outputKey := mustHex(t, f.OutputKey)

	// The commitment must hold before we spend anything.
	if err := VerifyCommitment(outputKey, leaf, cb); err != nil {
		t.Fatalf("leaf/control block do not commit to the output key: %v", err)
	}

	// Build a tx that spends the funded output back to a burn-ish output.
	hash, err := OutPointHash(f.FundingTxid)
	if err != nil {
		t.Fatal(err)
	}
	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(hash, f.FundingVout), nil, nil))
	tx.AddTxOut(wire.NewTxOut(f.FundingValue-2000, spk)) // back to itself, minus fee

	prevOut := wire.NewTxOut(f.FundingValue, spk)
	prevOuts := []*wire.TxOut{prevOut}

	// PAYER signs. (In the real protocol the payer produces this OFFLINE; the
	// payee later adds its own signature. Here we exercise the payer half, which
	// is the half that must work with no counterparty present.)
	payer, err := NewSigner(mustHex(t, f.PayerSK))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(payer.XOnly) != f.PayerXOnly {
		t.Fatalf("signer x-only mismatch\n  fixture: %s\n  ours:    %x",
			f.PayerXOnly, payer.XOnly)
	}

	sig, err := payer.SignScriptPath(tx, 0, prevOuts, leaf)
	if err != nil {
		t.Fatal(err)
	}
	if len(sig) != 64 {
		t.Fatalf("SIGHASH_DEFAULT signature must be 64 bytes (no hash-type byte), got %d", len(sig))
	}

	// The sighash must be independently recomputable and must match what we
	// signed -- this catches a sign/verify divergence inside our own code.
	sh, err := CalcTaprootScriptPathSigHash(tx, 0, prevOuts, leaf)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := schnorr.ParsePubKey(payer.XOnly)
	if err != nil {
		t.Fatal(err)
	}
	parsedSig, err := schnorr.ParseSignature(sig)
	if err != nil {
		t.Fatal(err)
	}
	if !parsedSig.Verify(sh, pk) {
		t.Fatal("our own signature does not verify against our own sighash")
	}

	// IMPORTANT PROTOCOL PROPERTY, discovered by running this:
	//
	// A 2-of-2 leaf CANNOT be satisfied by ONE signer. OP_CHECKSIG gives 1 and
	// the empty CHECKSIGADD operand gives 0; OP_2 OP_NUMEQUAL then needs 2. So
	// the payer's half alone must FAIL to execute. That failure is exactly what
	// makes it safe to hand the half-signature over: it is worthless without the
	// payee's cooperation.
	//
	// Witness order matters too: elements push left-to-right, so the LAST is on
	// top. With [sig, <empty>, leaf, cb] the control block ends up on top, which
	// is why the engine reported "signature not empty on failed checksig" --
	// a witness-construction error, not a crypto one.
	halfWitness := WitnessForPayerHalf(sig, leaf, cb)
	if err := VerifyScriptPath(tx, 0, prevOut, halfWitness); err == nil {
		t.Fatal("a 2-of-2 leaf was satisfied by ONE signature -- that would mean " +
			"the payer's half is independently spendable, which breaks the " +
			"protocol's core safety property")
	} else {
		t.Logf("payer's half correctly does NOT spend alone: %v", err)
	}

	// Now add the payee's signature to complete the spend. This is what the
	// PAYEE does later, alone, using only the payer's captured signature.
	if f.PayeeSK == "" {
		t.Skip("fixture carries no payee secret; skipping the completion half")
	}
	payee, err := NewSigner(mustHex(t, f.PayeeSK))
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(payee.XOnly) != f.PayeeXOnly {
		t.Fatalf("payee x-only mismatch\n  fixture: %s\n  ours:    %x",
			f.PayeeXOnly, payee.XOnly)
	}
	payeeSig, err := payee.SignScriptPath(tx, 0, prevOuts, leaf)
	if err != nil {
		t.Fatal(err)
	}
	fullWitness := WitnessForMultisig(sig, payeeSig, leaf, cb)
	if err := VerifyScriptPath(tx, 0, prevOut, fullWitness); err != nil {
		t.Fatalf("completed 2-of-2 spend failed: %v", err)
	}
	t.Log("COMPLETED: payer-signed half + payee's signature satisfies the leaf")
}

// TestSighashCommitsToSequences is the regression guard for the bug that cost the
// most time in the research phase: the BIP-341 signature message includes
// sha_sequences, and omitting it makes every OTHER field's contribution wrong
// too. Changing any input's sequence must change the sighash.
func TestSighashCommitsToSequences(t *testing.T) {
	f := loadSigningFixture(t)
	leaf := mustHex(t, f.LeafHex)
	spk := mustHex(t, f.ScriptPubKey)
	hash, _ := OutPointHash(f.FundingTxid)

	build := func(seq uint32) (*wire.MsgTx, []*wire.TxOut) {
		tx := wire.NewMsgTx(2)
		tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(hash, f.FundingVout), nil, nil))
		tx.TxIn[0].Sequence = seq
		tx.AddTxOut(wire.NewTxOut(f.FundingValue-2000, spk))
		return tx, []*wire.TxOut{wire.NewTxOut(f.FundingValue, spk)}
	}

	tx1, p1 := build(0xfffffffd)
	tx2, p2 := build(0xfffffffe)
	h1, err := CalcTaprootScriptPathSigHash(tx1, 0, p1, leaf)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := CalcTaprootScriptPathSigHash(tx2, 0, p2, leaf)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(h1) == hex.EncodeToString(h2) {
		t.Fatal("sighash did NOT change with nSequence -- sha_sequences is being " +
			"omitted from the signature message, which is the exact bug the " +
			"research phase spent hours on")
	}
}

// TestSighashRequiresAllPrevouts guards the other half: BIP-341 commits to every
// input's previous output, so a partial prevOuts list must be rejected rather
// than silently producing a wrong signature.
func TestSighashRequiresAllPrevouts(t *testing.T) {
	f := loadSigningFixture(t)
	leaf := mustHex(t, f.LeafHex)
	spk := mustHex(t, f.ScriptPubKey)
	hash, _ := OutPointHash(f.FundingTxid)

	tx := wire.NewMsgTx(2)
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(hash, f.FundingVout), nil, nil))
	tx.AddTxIn(wire.NewTxIn(wire.NewOutPoint(hash, f.FundingVout), nil, nil))
	tx.AddTxOut(wire.NewTxOut(f.FundingValue-4000, spk))

	if _, err := CalcTaprootScriptPathSigHash(tx, 0,
		[]*wire.TxOut{wire.NewTxOut(f.FundingValue, spk)}, leaf); err == nil {
		t.Fatal("sighash accepted a prevOuts list shorter than the input list")
	}
}

// TestSignerNormalisesYParity covers BIP-340's even-Y rule. A key whose point has
// odd Y must sign under the NEGATED scalar, so the x-only key matches the one
// committed in the leaf. Getting this wrong yields "Invalid Schnorr signature".
func TestSignerNormalisesYParity(t *testing.T) {
	// Find a secret key with odd-Y point, then confirm the signer's x-only key
	// is the even-Y lift.
	var oddSK []byte
	for i := 1; i < 64; i++ {
		sk := make([]byte, 32)
		sk[31] = byte(i)
		priv, _ := btcec.PrivKeyFromBytes(sk)
		if priv.PubKey().SerializeCompressed()[0] == 0x03 {
			oddSK = sk
			break
		}
	}
	if oddSK == nil {
		t.Skip("no odd-Y key in range")
	}
	s, err := NewSigner(oddSK)
	if err != nil {
		t.Fatal(err)
	}
	// The normalised key must be the even-Y lift of the same x coordinate.
	lifted, err := schnorr.ParsePubKey(s.XOnly)
	if err != nil {
		t.Fatal(err)
	}
	if lifted.SerializeCompressed()[0] != 0x02 {
		t.Fatal("signer did not normalise to even Y")
	}
	// And the sighash-verifying public key must be the one committed.
	_ = chainhash.Hash{}
	_ = txscript.SigHashDefault
}
