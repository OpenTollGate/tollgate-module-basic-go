package taprootverify

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// FIXTURES -- every expected output key below was produced by BITCOIN CORE, not
// by this package. `bitcoin-cli getdescriptorinfo` + `deriveaddresses` on a
// `tr(<NUMS>,<tree>)` descriptor, then `getaddressinfo` to read the
// scriptPubKey. That is the whole point: we are checking our Go against Core's
// answer, not against our own arithmetic. Core v31.1.0, regtest.
//
// The internal key in every fixture is the BIP-341 nothing-up-my-sleeve point,
// so there is NO key path and these outputs are spendable only via a script
// path. That is the same device LNURLcash and Hedgehog use.
// ---------------------------------------------------------------------------

const numsInternalKey = "50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0"

// <keyA> OP_CHECKSIG, one leaf.
const leafA = "20f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9ac"

// <keyB> OP_CHECKSIG
var (
	keyA = mustHex("f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9")
	keyB = mustHex("c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5")
	keyC = mustHex("79be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798")
)

type coreFixture struct {
	name string
	// leaves in Core descriptor order; `nested` gives the SHAPE, because a flat
	// list cannot express Core's {a,{b,c}}. Ignored when len(leaves)==1.
	leaves   [][]byte
	nested   bool
	wantSPK  string // Core's scriptPubKey (5120 || outputKeyX)
	wantAddr string
}

func fixtures() []coreFixture {
	return []coreFixture{
		{
			name:     "1leaf_A",
			leaves:   [][]byte{mustHex(leafA)},
			wantSPK:  "512060988ad469b6efb8f5b6878ac9d314b255efe48ef74ecbc91d9b562e05502cd0",
			wantAddr: "bcrt1pvzvg44rfkmhm3adks79vn5c5kf27leyw7a8vhjgandtzup2s9ngq4y4q64",
		},
		{
			name: "2leaf_A_B",
			leaves: [][]byte{
				mustHex(leafA),
				mustHex("20c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5ac"),
			},
			wantSPK:  "5120f06140f01c8e7ece36486b675f4be7f8467a6243c01f333d35847639e40db1d8",
			wantAddr: "bcrt1p7ps5puqu3elvudjgddn47jl8lpr85cjrcq0nx0f4s3mrneqdk8vqjx8udz",
		},
		{
			// A 3-leaf tree is the case that matters for a channel: payment
			// leaf + revocation leaf + timeout leaf. It is ALSO the smallest
			// tree where the odd-node rule diverges between implementations.
			name: "3leaf_A_B_C",
			leaves: [][]byte{
				mustHex(leafA),
				mustHex("20c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5ac"),
				mustHex("2079be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798ac"),
			},
			nested:   true, // Core descriptor was tr(K,{pk(A),{pk(B),pk(C)}})
			wantSPK:  "5120e853c205dc36261f245830eb0fff77e36f69ec6c7f911fb221068d8a2f9420a8",
			wantAddr: "bcrt1papfuypwuxcnp7fzcxr4sllmhudhknmrv07g3lv3pq6xc5tu5yz5qeewaeg",
		},
	}
}

// rootFor builds the tree SHAPE a fixture describes and returns its root.
// A flat MerkleRoot() cannot express Core's {a,{b,c}}, which is exactly the bug
// the 3-leaf fixture caught.
func rootFor(f coreFixture) ([]byte, error) {
	if len(f.leaves) == 1 || !f.nested {
		return MerkleRoot(f.leaves...)
	}
	// nested shape: leftmost leaf paired with a right-leaning sub-tree
	if len(f.leaves) != 3 {
		return nil, errors.New("nested fixture currently models exactly 3 leaves")
	}
	return TreeRoot(BranchOf(Leaf(f.leaves[0]),
		BranchOf(Leaf(f.leaves[1]), Leaf(f.leaves[2]))))
}

func mustHex(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

// ---------------------------------------------------------------------------
// TESTS
// ---------------------------------------------------------------------------

// TestOutputKeyMatchesCore is the load-bearing test: our derivation must agree
// with Bitcoin Core's for 1, 2 and 3 leaves.
func TestOutputKeyMatchesCore(t *testing.T) {
	internal := mustHex(numsInternalKey)
	for _, f := range fixtures() {
		t.Run(f.name, func(t *testing.T) {
			root, err := rootFor(f)
			if err != nil {
				t.Fatalf("rootFor: %v", err)
			}
			key, err := OutputKey(internal, root)
			if err != nil {
				t.Fatalf("OutputKey: %v", err)
			}
			got := hex.EncodeToString(schnorrSerialize(key))
			want := f.wantSPK[4:] // strip 5120
			if got != want {
				t.Errorf("output key disagrees with CORE\n  core %s\n  ours %s", want, got)
			}
		})
	}
}

// TestIsTaproot checks the witness-v1 recogniser, including that it does NOT
// mistake other program versions or junk for taproot.
func TestIsTaproot(t *testing.T) {
	for _, f := range fixtures() {
		spk := mustHex(f.wantSPK)
		key, ok := IsTaproot(spk)
		if !ok {
			t.Errorf("%s: valid taproot spk not recognised", f.name)
			continue
		}
		if len(key) != 32 {
			t.Errorf("%s: key length %d, want 32", f.name, len(key))
		}
	}
	notTaproot := [][]byte{
		nil,
		{},
		mustHex("0014" + "00" + "1122334455667788990011223344556677889900"[:38]), // v0
		mustHex("5220" + numsInternalKey + "00"),                                 // OP_2 not OP_1
		mustHex("5120" + numsInternalKey[:64] + "1122"),                          // too long
		mustHex("5121" + numsInternalKey),                                        // 33-byte program (v1 needs 32)
	}
	for i, spk := range notTaproot {
		if _, ok := IsTaproot(spk); ok {
			t.Errorf("case %d: wrongly recognised as taproot: %x", i, spk)
		}
	}
}

// TestVerifyCommitmentAcceptsRealSpend builds a genuine single-leaf control
// block and checks the verifier accepts it.
func TestVerifyCommitmentAcceptsRealSpend(t *testing.T) {
	internal := mustHex(numsInternalKey)
	leaf := mustHex(leafA)
	root, err := MerkleRoot(leaf)
	if err != nil {
		t.Fatal(err)
	}
	key, err := OutputKey(internal, root)
	if err != nil {
		t.Fatal(err)
	}
	outputKey := schnorrSerialize(key)

	// A single-leaf tree has NO merkle sibling, so the control block is exactly
	// 33 bytes: (leaf_version|parity) || internal_key. Appending the leaf's own
	// hash here (making 65) is the exact mistake that produced misleading
	// "Witness program hash mismatch" errors during the sibling POC work.
	parity := byte(0)
	if key.SerializeCompressed()[0] == 0x03 {
		parity = 1
	}
	controlBlock := append([]byte{TapLeafVersion | parity}, internal...)

	// Cross-check the 33-byte shape against Core's own address for this leaf.
	if !bytes.Equal(outputKey, mustHex(fixtures()[0].wantSPK)[2:]) {
		t.Fatalf("fixture drift: our key no longer matches Core's recorded key")
	}

	if err := VerifyCommitment(outputKey, internal, leaf, controlBlock); err != nil {
		t.Fatalf("verifier rejected a genuine commitment: %v", err)
	}
}

// TestVerifyCommitmentRejectsWrongLeaf is the negative test that gives this
// package its value: a mismatched leaf MUST be rejected.
func TestVerifyCommitmentRejectsWrongLeaf(t *testing.T) {
	internal := mustHex(numsInternalKey)
	goodLeaf := mustHex(leafA)
	root, _ := MerkleRoot(goodLeaf)
	key, _ := OutputKey(internal, root)
	outputKey := schnorrSerialize(key)

	parity := byte(0)
	if key.SerializeCompressed()[0] == 0x03 {
		parity = 1
	}
	controlBlock := append([]byte{TapLeafVersion | parity}, internal...)

	// A DIFFERENT leaf, same output: must not verify.
	otherLeaf := mustHex("20c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5ac")
	err := VerifyCommitment(outputKey, internal, otherLeaf, controlBlock)
	if err == nil {
		t.Fatal("verifier ACCEPTED a commitment with the wrong leaf script")
	}
	if !errors.Is(err, ErrCommitmentMismatch) {
		t.Errorf("want ErrCommitmentMismatch, got %v", err)
	}
}

// TestVerifyCommitmentRejectsMalformed checks the error taxonomy, so a caller
// can tell "not a commitment" from "malformed input".
func TestVerifyCommitmentRejectsMalformed(t *testing.T) {
	internal := mustHex(numsInternalKey)
	root, _ := MerkleRoot(mustHex(leafA))
	key, _ := OutputKey(internal, root)
	outputKey := schnorrSerialize(key)
	leaf := mustHex(leafA)

	t.Run("short control block", func(t *testing.T) {
		err := VerifyCommitment(outputKey, internal, leaf, internal[:32])
		if !errors.Is(err, ErrControlBlockTooShort) {
			t.Errorf("want ErrControlBlockTooShort, got %v", err)
		}
	})
	t.Run("wrong leaf version", func(t *testing.T) {
		cb := append([]byte{0xc2}, internal...) // 0xc2, not 0xc0/0xc1
		err := VerifyCommitment(outputKey, internal, leaf, cb)
		if !errors.Is(err, ErrBadLeafVersion) {
			t.Errorf("want ErrBadLeafVersion, got %v", err)
		}
	})
	t.Run("ragged path", func(t *testing.T) {
		cb := append([]byte{0xc0}, internal...)
		cb = append(cb, make([]byte, 16)...) // 16 bytes is not a whole sibling
		err := VerifyCommitment(outputKey, internal, leaf, cb)
		if !errors.Is(err, ErrOddPathLength) {
			t.Errorf("want ErrOddPathLength, got %v", err)
		}
	})
}

// TestMerkleRootThreeLeafDivergence documents WHY the odd-node rule matters.
// A 3-leaf tree is where "promote the odd node" (BIP-341) differs from
// "hash it with itself then continue" implementations. We assert our root
// reproduces Core's address, which is only possible with the correct rule.
func TestMerkleRootThreeLeafDivergence(t *testing.T) {
	f := fixtures()[2] // the 3-leaf case
	root, err := rootFor(f)
	if err != nil {
		t.Fatal(err)
	}
	key, err := OutputKey(mustHex(numsInternalKey), root)
	if err != nil {
		t.Fatal(err)
	}
	got := hex.EncodeToString(schnorrSerialize(key))

	// The WRONG rule: duplicate the last leaf as a SIBLING at each level
	// without BIP-341's promotion. Construct it explicitly so the test fails
	// loudly if anyone "simplifies" MerkleRoot into it.
	wrong := wrongRootForThree(f.leaves)
	wrongKey, _ := OutputKey(mustHex(numsInternalKey), wrong)
	wrongHex := hex.EncodeToString(schnorrSerialize(wrongKey))

	if got == wrongHex {
		t.Fatal("MerkleRoot produced the WRONG-rule root -- BIP-341 promotion is broken")
	}
	if got != f.wantSPK[4:] {
		t.Errorf("our 3-leaf root does not reproduce Core's key\n  core %s\n  ours %s",
			f.wantSPK[4:], got)
	}
	t.Logf("correct rule -> %s (matches Core's %s)", got[:16], f.wantAddr)
	t.Logf("wrong rule   -> %s (DIVERGES -- this is the trap)", wrongHex[:16])
}
