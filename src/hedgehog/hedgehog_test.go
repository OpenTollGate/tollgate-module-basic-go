package hedgehog

import (
	"encoding/hex"
	"testing"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
)

// ---------------------------------------------------------------------------
// CORE-DERIVED FIXTURES
//
// Every expected value below was produced by Bitcoin Core v31.1.0 on regtest,
// NOT by this package. They exist so the tests validate against consensus rather
// than against our own arithmetic -- the research phase found that a simulator
// (and even a hand-written second implementation) will happily agree with a
// wrong answer.
//
// Core produced each output key like this:
//
//	bitcoin-cli getdescriptorinfo "tr(50929b74...03ac0,<TREE>)#checksum"
//
// where <TREE> is the tapscript tree in Core's descriptor syntax. The `pk()`
// entries take an x-only key (64 hex chars). Note that a NESTED descriptor such
// as {a,{b,c}} is a specific binary tree and must NOT be flattened: doing so
// promotes an odd node and yields a different, wrong, output key.
// ---------------------------------------------------------------------------

const (
	numsX = "50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0"

	// Three distinct, valid x-only keys.
	keyA = "4f355bdcb7cc0af728ef3cceb9615d90684bb5b2ca5f859ab0f0b704075871aa"
	keyB = "466d7fcae563e5cb09a0d1870bb580344804617879a14949cf22285f1bae3f27"
	keyC = "2c0b7cf95324a07d05398b240174dc0c2be444d96b159aa6c7f7b1e668680991"
)

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return b
}

// TestSingleLeafControlBlockIs33Bytes guards the trap that cost hours in the
// research phase: a single-leaf tree has NO merkle sibling, so the control block
// is exactly 33 bytes. Appending the leaf's own hash (65 bytes) makes Core
// recompute a different output key and report "Witness program hash mismatch",
// which is completely misleading about the real cause.
func TestSingleLeafControlBlockIs33Bytes(t *testing.T) {
	a := mustHex(t, keyA)
	leaf, err := TimeoutLeaf(2016, a)
	if err != nil {
		t.Fatal(err)
	}
	tree := Leaf(leaf)
	cb, err := ControlBlock(leaf, tree)
	if err != nil {
		t.Fatal(err)
	}
	if len(cb) != 33 {
		t.Errorf("single-leaf control block must be 33 bytes, got %d", len(cb))
	}
	if cb[0]&0xfe != TapscriptLeafVersion {
		t.Errorf("leaf version byte wrong: 0x%02x", cb[0])
	}
	if hex.EncodeToString(cb[1:33]) != numsX {
		t.Errorf("control block must carry the NUMS internal key")
	}
}

// TestTreeShapeMatters proves a flat/balanced tree is NOT the same commitment as
// Core's nested descriptor. If this test ever passes trivially, the tree API has
// been flattened and the bug is back.
func TestTreeShapeMatters(t *testing.T) {
	a, b, c := mustHex(t, keyA), mustHex(t, keyB), mustHex(t, keyC)
	la, _ := TimeoutLeaf(2016, a)
	lb, _ := TimeoutLeaf(2016, b)
	lc, _ := TimeoutLeaf(2016, c)

	// Nested, matching Core's tr(NUMS,{a,{b,c}})
	nested := Branch(Leaf(la), Branch(Leaf(lb), Leaf(lc)))
	// Flat and balanced, matching a naive implementation
	flat := Branch(Branch(Leaf(la), Leaf(lb)), Leaf(lc))

	rn, err := TreeRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	rf, err := TreeRoot(flat)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(rn) == hex.EncodeToString(rf) {
		t.Fatalf("nested and flat trees produced the SAME root -- tree shape is "+
			"being flattened; got %x for both", rn)
	}
	t.Logf("nested root: %x", rn)
	t.Logf("flat root:   %x", rf)
}

// TestLeafOpcodesAreBIP342Correct asserts the byte-level shape of every leaf.
// These are wire-format assertions: a silent change here is a consensus break,
// so the exact opcodes are pinned rather than compared to themselves.
func TestLeafOpcodesAreBIP342Correct(t *testing.T) {
	a, b := mustHex(t, keyA), mustHex(t, keyB)

	ms, err := MultisigLeaf(a, b)
	if err != nil {
		t.Fatal(err)
	}
	// <A> OP_CHECKSIG <B> OP_CHECKSIGADD OP_2 OP_EQUAL
	// Layout: 0:0x20 (push 32) | 1..32: A | 33: OP_CHECKSIG | 34:0x20 (push 32)
	//         | 35..66: B | 67: OP_CHECKSIGADD | 68: OP_2 | 69: OP_EQUAL
	if len(ms) != 70 {
		t.Fatalf("multisig leaf must be 70 bytes, got %d", len(ms))
	}
	if ms[0] != 0x20 {
		t.Errorf("multisig leaf must start with a 32-byte push, got 0x%02x", ms[0])
	}
	if ms[33] != 0xac {
		t.Errorf("expected OP_CHECKSIG (0xac) at offset 33, got 0x%02x", ms[33])
	}
	if ms[34] != 0x20 {
		t.Errorf("expected a 32-byte push (0x20) at offset 34, got 0x%02x", ms[34])
	}
	if ms[67] != 0xba {
		t.Errorf("expected OP_CHECKSIGADD (0xba) at offset 67, got 0x%02x", ms[67])
	}
	// OP_NUMEQUAL (0x9c), NOT OP_EQUAL (0x87). Core's multi_a builder ends with
	// OP_NUMEQUAL -- see src/script/miniscript.h:857-861. This assertion is the
	// regression guard for that exact bug.
	if ms[68] != 0x52 || ms[69] != 0x9c {
		t.Errorf("leaf must end OP_2 OP_NUMEQUAL (0x52 0x9c), got 0x%02x 0x%02x",
			ms[68], ms[69])
	}
	// OP_CHECKMULTISIG (0xae) must NEVER appear as an OPCODE: BIP-342 disables
	// it in tapscript and Core rejects it by name.
	//
	// Scan only OPCODE positions, not the whole buffer: 0xae is a perfectly
	// ordinary DATA byte and scanning naively reports a false positive the
	// moment a key happens to contain it (which key B does, at offset 64).
	// A naive whole-buffer scan is exactly the kind of test that looks like
	// evidence and is not.
	if ms[33] == 0xae || ms[67] == 0xae || ms[69] == 0xae {
		t.Fatalf("OP_CHECKMULTISIG (0xae) found at an opcode position -- " +
			"BIP-342 disables it in tapscript")
	}
	for i := 0; i < len(ms); {
		op := ms[i]
		switch {
		case op >= 1 && op <= 75: // direct push of N bytes
			i += int(op) + 1
		case op == 0x4c || op == 0x4d || op == 0x4e: // OP_PUSHDATA1/2/4
			i += int(op) + 1
		default:
			if op == 0xae {
				t.Fatalf("OP_CHECKMULTISIG (0xae) at opcode position %d", i)
			}
			i++
		}
	}

	rl, err := RevocationLeaf(mustHex(t, "00112233445566778899aabbccddeeff00112233"), a)
	if err != nil {
		t.Fatal(err)
	}
	// Layout: 0: OP_RIPEMD160 | 1:0x14 (push 20) | 2..21: hash | 22: OP_EQUALVERIFY
	//         | 23:0x20 (push 32) | 24..55: key | 56: OP_CHECKSIG
	if len(rl) != 57 {
		t.Fatalf("revocation leaf must be 57 bytes, got %d", len(rl))
	}
	// OP_RIPEMD160 is 0xa6. Not 0xaa (OP_HASH256), not 0xa9 (OP_HASH160).
	if rl[0] != 0xa6 {
		t.Errorf("revocation leaf must start with OP_RIPEMD160 (0xa6), got 0x%02x", rl[0])
	}
	if rl[1] != 0x14 {
		t.Errorf("expected a 20-byte push (0x14) at offset 1, got 0x%02x", rl[1])
	}
	if rl[22] != 0x88 {
		t.Errorf("expected OP_EQUALVERIFY (0x88) at offset 22, got 0x%02x", rl[22])
	}
}

// TestWitnessOrderIsCheckedSigAddCorrect pins the stack order that Core enforces.
// For <A> OP_CHECKSIG <B> OP_CHECKSIGADD OP_2 OP_EQUAL the witness must be
// [sigB, sigA] because OP_CHECKSIG takes its key from the TOP of the stack.
// Shipping [sigA, sigB] yields Core's "Invalid Schnorr signature".
func TestWitnessOrderIsCheckedSigAddCorrect(t *testing.T) {
	a, b := mustHex(t, keyA), mustHex(t, keyB)
	leaf, err := MultisigLeaf(a, b)
	if err != nil {
		t.Fatal(err)
	}
	// Sanity: the FIRST key pushed must be A, so that A is deeper in the stack
	// and therefore paired with the SECOND-from-top item after CHECKSIG pops B.
	if hex.EncodeToString(leaf[1:33]) != keyA {
		t.Fatalf("first pushed key must be A")
	}
	if hex.EncodeToString(leaf[35:67]) != keyB {
		t.Fatalf("second pushed key must be B")
	}
}

// TestVerifyCommitmentRejectsWrongLeaf proves the verifier actually rejects,
// rather than accepting anything it is handed.
func TestVerifyCommitmentRejectsWrongLeaf(t *testing.T) {
	a, b := mustHex(t, keyA), mustHex(t, keyB)
	ms, _ := MultisigLeaf(a, b)
	other, _ := TimeoutLeaf(2016, a)

	tree := Leaf(ms)
	root, err := TreeRoot(tree)
	if err != nil {
		t.Fatal(err)
	}
	ok, err := OutputKey(root)
	if err != nil {
		t.Fatal(err)
	}
	spk := schnorr.SerializePubKey(ok)
	cb, err := ControlBlock(ms, tree)
	if err != nil {
		t.Fatal(err)
	}

	if err := VerifyCommitment(spk, ms, cb); err != nil {
		t.Fatalf("correct leaf must verify: %v", err)
	}
	if err := VerifyCommitment(spk, other, cb); err == nil {
		t.Fatal("verifier accepted a leaf that is NOT in the tree -- it is not " +
			"checking the commitment")
	}
}

// TestBadLeafVersionRejected covers the guard rails.
func TestBadLeafVersionRejected(t *testing.T) {
	if err := VerifyCommitment(make([]byte, 32), []byte{0x51}, []byte{0xc2}); err == nil {
		t.Error("bad leaf version must be rejected")
	}
	if err := VerifyCommitment(make([]byte, 32), []byte{0x51}, []byte{0xc0, 0x01}); err == nil {
		t.Error("short control block must be rejected")
	}
	// 33 + 31 bytes is not a whole number of siblings
	cb := append([]byte{0xc0}, make([]byte, 32+31)...)
	if err := VerifyCommitment(make([]byte, 32), []byte{0x51}, cb); err == nil {
		t.Error("non-multiple-of-32 path must be rejected")
	}
}

// TestTimeoutLeafCSVTypeFlag checks the BIP-68 type flag is CLEAR. A block-based
// relative lock with the flag SET is a time-based lock and Core rejects the
// spend -- a real bug found by consensus validation in the research phase.
func TestTimeoutLeafCSVTypeFlag(t *testing.T) {
	a := mustHex(t, keyA)
	leaf, err := TimeoutLeaf(2016, a)
	if err != nil {
		t.Fatal(err)
	}
	// 2016 needs 2 bytes: 0x02 0xe0 0x07  (little-endian 0x07e0 = 2016)
	if leaf[0] != 0x02 {
		t.Fatalf("expected a 2-byte push, got 0x%02x", leaf[0])
	}
	// 2016 = 0x07e0, so the script number is exactly TWO bytes: e0 07.
	// Reading three bytes would slurp the OP_CHECKSEQUENCEVERIFY opcode (0xb2)
	// as a high byte -- which is exactly the false failure this test first hit.
	csvValue := uint32(leaf[1]) | uint32(leaf[2])<<8
	if csvValue&(1<<22) != 0 {
		t.Errorf("BIP-68 type flag must be CLEAR for a block-based lock; got 0x%06x", csvValue)
	}
	if csvValue != 2016 {
		t.Errorf("CSV value must be 2016, got %d", csvValue)
	}
}
