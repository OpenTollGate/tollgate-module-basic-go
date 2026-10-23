// Package hedgehog implements the Hedgehog two-party payment channel protocol
// for TollGate.
//
// WHAT THIS IS
//
// Hedgehog (github.com/supertestnet/hedgehog) is a two-party channel protocol in
// which a payment can be CREATED unilaterally by the payer and CONSUMED later by
// the payee, with no interaction between them. For TollGate that is the shape
// that matters: the customer may have no WAN, but the customer is the payer, and
// constructing a payment is local work (sign a transaction, hand over text) that
// needs no network. The router is a 24/7 service and completes the spend.
//
// The programme's research (PR #944, docs/analysis/PROGRAMME-RESULT.md) recorded
// the protocol as the closest thing found to a non-custodial AND payer-offline
// instrument, and retracted an earlier claim that it only worked the other way
// round. What remains is cost, not interactivity — which is why this package
// exists: the operator has accepted the on-chain overhead for large or long-lived
// purchases.
//
// DESIGN CONSTRAINTS CARRIED FROM THE RESEARCH
//
//  1. Tapscript only. The 2-of-2 leaf uses OP_CHECKSIG + OP_CHECKSIGADD, never
//     OP_CHECKMULTISIG — BIP-342 disables the latter in tapscript, and Bitcoin
//     Core was MEASURED saying so: "OP_CHECKMULTISIG(VERIFY) is not available in
//     tapscript" (see the sibling research).
//  2. The internal key is the BIP-341 nothing-up-my-sleeve point, so there is no
//     key path and the output is spendable ONLY via a script path.
//  3. A single-leaf tree has NO merkle sibling, so its control block is exactly
//     33 bytes. Appending the leaf's own hash (making 65) reconstructs a
//     different output key and Core reports the thoroughly misleading
//     "Witness program hash mismatch". This cost hours in the research phase.
//  4. The BIP-341 sighash includes sha_prevouts AND sha_sequences, and the
//     per-input section for the non-ANYONECANPAY case is input_index (4 bytes)
//     ONLY. Omitting sha_sequences makes every other variant wrong too, which is
//     why it took a specification read rather than a brute force to find.
//
// Every one of those four constraints is enforced by a test that validates
// against Bitcoin Core rather than against this package's own arithmetic.
package hedgehog

import (
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
)

// NUMSInternalKey is the BIP-341 nothing-up-my-sleeve internal key.
//
// Using it means no party holds a key path, so the funding output can ONLY be
// spent through a script path. That is deliberate: it removes the possibility of
// one party unilaterally key-path-spending the channel, which is what makes the
// revocation scripts meaningful.
var NUMSInternalKey = mustDecodeHex(
	"50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0")

// Errors reported by this package.
var (
	// ErrBadLeafVersion means a control block's leaf version is not 0xc0.
	ErrBadLeafVersion = errors.New("hedgehog: only tapscript leaf version 0xc0 is supported")
	// ErrControlBlockTooShort means the control block cannot even encode an
	// internal key.
	ErrControlBlockTooShort = errors.New("hedgehog: control block shorter than 33 bytes")
	// ErrOddPathLength means the merkle path is not a whole number of siblings.
	ErrOddPathLength = errors.New("hedgehog: control block path is not a multiple of 32 bytes")
	// ErrCommitmentMismatch means the leaf does not commit to the output key.
	ErrCommitmentMismatch = errors.New("hedgehog: leaf does not commit to the output key")
	// ErrBadKey means an x-only key is not a valid curve point.
	ErrBadKey = errors.New("hedgehog: invalid x-only public key")
)

// TapscriptLeafVersion is the only leaf version this package emits or accepts.
const TapscriptLeafVersion byte = byte(txscript.BaseLeafVersion)

// Script opcodes used by Hedgehog's leaves. Declared here rather than using
// txscript's named constants only, because the exact byte values are part of the
// protocol's wire format and a silent change would be a consensus break.
const (
	// OP_RIPEMD160 is 0xa6. It is NOT 0xaa (that is OP_HASH256) and not 0xa9
	// (OP_HASH160). Getting this wrong produces a leaf that is still valid
	// tapscript and still spends, but commits to the wrong hash function --
	// i.e. a silent protocol break, not a build error.
	opRIPEMD160   = 0xa6
	opSHA256      = 0xa8
	opHash160     = 0xa9
	opCheckSig    = 0xac
	opCheckSigAdd = 0xba
	opCheckSeqVer = 0xb2
	opDrop        = 0x75
	opEqual       = 0x87
	opEqualVerify = 0x88
	// OP_NUMEQUAL is 0x9c. Core's multi_a() ends with OP_NUMEQUAL, NOT
	// OP_EQUAL -- confirmed by reading Bitcoin Core's own script builder
	// (src/script/miniscript.h:857-861). Using OP_EQUAL produces a leaf that is
	// still valid tapscript and still spends, but commits to a DIFFERENT output
	// key, so the funding output would never match. That is a silent break, not
	// a build error, and it is exactly why this package validates against Core.
	opNumEqual = 0x9c
	op2        = 0x52
	opPush32      = 0x20
)

// ---------------------------------------------------------------------------
// Leaf scripts
// ---------------------------------------------------------------------------

// MultisigLeaf builds Hedgehog's 2-of-2 leaf:
//
//	<aliceXOnly> OP_CHECKSIG <bobXOnly> OP_CHECKSIGADD OP_2 OP_EQUAL
//
// This is the BIP-342-correct replacement for OP_CHECKMULTISIG, which tapscript
// disables. The ORDER MATTERS at spend time: OP_CHECKSIG takes its key from the
// TOP of the stack, so the witness must be [sig_bob, sig_alice] — not
// [sig_alice, sig_bob]. See WitnessForMultisigLeaf.
func MultisigLeaf(aliceXOnly, bobXOnly []byte) ([]byte, error) {
	if len(aliceXOnly) != 32 || len(bobXOnly) != 32 {
		return nil, fmt.Errorf("%w: keys must be 32 bytes", ErrBadKey)
	}
	// Validate they are real curve points, so a malformed key fails here rather
	// than producing a valid-looking but unspendable output.
	if _, err := schnorr.ParsePubKey(aliceXOnly); err != nil {
		return nil, fmt.Errorf("%w: alice: %v", ErrBadKey, err)
	}
	if _, err := schnorr.ParsePubKey(bobXOnly); err != nil {
		return nil, fmt.Errorf("%w: bob: %v", ErrBadKey, err)
	}
	leaf := make([]byte, 0, 70)
	leaf = append(leaf, opPush32)
	leaf = append(leaf, aliceXOnly...)
	leaf = append(leaf, opCheckSig)
	leaf = append(leaf, opPush32)
	leaf = append(leaf, bobXOnly...)
	leaf = append(leaf, opCheckSigAdd, op2, opNumEqual)
	return leaf, nil
}

// ChecksigLeaf builds the plain `<key> OP_CHECKSIG` leaf.
//
// This is byte-identical to what Bitcoin Core's `pk()` descriptor produces in a
// tapscript context, which makes it the anchor for validating our tree math
// against Core: Core cannot express arbitrary tapscript, but it CAN express
// pk(), so a tree of pk() leaves is comparable leaf-for-leaf.
func ChecksigLeaf(keyXOnly []byte) ([]byte, error) {
	if len(keyXOnly) != 32 {
		return nil, fmt.Errorf("%w: key must be 32 bytes", ErrBadKey)
	}
	if _, err := schnorr.ParsePubKey(keyXOnly); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadKey, err)
	}
	leaf := []byte{opPush32}
	leaf = append(leaf, keyXOnly...)
	leaf = append(leaf, opCheckSig)
	return leaf, nil
}

// TimeoutLeaf builds Hedgehog's CSV timeout leaf:
//
//	<n> OP_CHECKSEQUENCEVERIFY OP_DROP <keyXOnly> OP_CHECKSIG
//
// The relative lock is BLOCK-BASED, so the BIP-68 type flag must be CLEAR. A
// block-based relative lock with the type flag SET is a time-based lock, and
// Bitcoin Core rejects the transaction with an unhelpful error — a real bug
// caught by consensus validation in the research phase.
func TimeoutLeaf(csvBlocks uint16, keyXOnly []byte) ([]byte, error) {
	if len(keyXOnly) != 32 {
		return nil, fmt.Errorf("%w: key must be 32 bytes", ErrBadKey)
	}
	if _, err := schnorr.ParsePubKey(keyXOnly); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadKey, err)
	}
	leaf := append([]byte{}, pushScriptNum(int64(csvBlocks))...)
	leaf = append(leaf, opCheckSeqVer, opDrop, opPush32)
	leaf = append(leaf, keyXOnly...)
	leaf = append(leaf, opCheckSig)
	return leaf, nil
}

// RevocationLeaf builds Hedgehog's revocation leaf:
//
//	OP_RIPEMD160 <20-byte hash> OP_EQUALVERIFY <keyXOnly> OP_CHECKSIG
//
// Spending it requires revealing the preimage of the hash AND signing. The
// preimage is the "revocation secret": handing it over transfers the ability to
// revoke.
func RevocationLeaf(preimageHash20, keyXOnly []byte) ([]byte, error) {
	if len(preimageHash20) != 20 {
		return nil, fmt.Errorf("hedgehog: preimage hash must be 20 bytes, got %d",
			len(preimageHash20))
	}
	if len(keyXOnly) != 32 {
		return nil, fmt.Errorf("%w: key must be 32 bytes", ErrBadKey)
	}
	if _, err := schnorr.ParsePubKey(keyXOnly); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadKey, err)
	}
	leaf := []byte{opRIPEMD160, 0x14}
	leaf = append(leaf, preimageHash20...)
	leaf = append(leaf, opEqualVerify, opPush32)
	leaf = append(leaf, keyXOnly...)
	leaf = append(leaf, opCheckSig)
	return leaf, nil
}

// ---------------------------------------------------------------------------
// Taproot tree
// ---------------------------------------------------------------------------

// Node is a leaf or internal node of a tapscript tree.
type Node interface{ isNode() }

// LeafNode wraps a script as a tree leaf.
type LeafNode struct{ Script []byte }

// BranchNode pairs two sub-trees. Child order does not matter: BIP-341 sorts
// each pair lexicographically before hashing.
type BranchNode struct{ Left, Right Node }

func (*LeafNode) isNode()   {}
func (*BranchNode) isNode() {}

// Leaf wraps a script as a tree leaf.
func Leaf(script []byte) *LeafNode { return &LeafNode{Script: script} }

// Branch pairs two sub-trees.
func Branch(left, right Node) *BranchNode { return &BranchNode{Left: left, Right: right} }

// tapLeafHash computes the BIP-341 leaf hash: tagged_hash("TapLeaf", 0xc0 ||
// compact_size(len) || script).
func tapLeafHash(script []byte) []byte {
	h := txscript.NewBaseTapLeaf(script).TapHash()
	return h[:]
}

// branchHash computes a BIP-341 TapBranch hash with the spec's lexicographic
// ordering.
func branchHash(a, b []byte) []byte {
	if bytesCompare(a, b) > 0 {
		a, b = b, a
	}
	h := chainhash.TaggedHash(chainhash.TagTapBranch, a, b)
	return h[:]
}

// TreeRoot returns the merkle root of a NESTED tree.
//
// The tree SHAPE matters and a flat list cannot always express it. Bitcoin Core's
// descriptor `tr(K,{a,{b,c}})` is a specific binary tree, and flattening the same
// three leaves into a balanced tree promotes an odd node and yields a different,
// WRONG output key. That bug was found by comparing against Core in the research
// phase, so the shape is explicit here.
func TreeRoot(n Node) ([]byte, error) {
	switch t := n.(type) {
	case *LeafNode:
		return tapLeafHash(t.Script), nil
	case *BranchNode:
		if t.Left == nil || t.Right == nil {
			return nil, errors.New("hedgehog: branch has a nil child")
		}
		l, err := TreeRoot(t.Left)
		if err != nil {
			return nil, err
		}
		r, err := TreeRoot(t.Right)
		if err != nil {
			return nil, err
		}
		return branchHash(l, r), nil
	default:
		return nil, fmt.Errorf("hedgehog: unknown node type %T", n)
	}
}

// OutputKey derives the BIP-341 output key for a tree, with the NUMS internal
// key (no key path).
func OutputKey(root []byte) (*btcec.PublicKey, error) {
	internal, err := schnorr.ParsePubKey(NUMSInternalKey)
	if err != nil {
		return nil, fmt.Errorf("%w: nums: %v", ErrBadKey, err)
	}
	if len(root) == 0 {
		return txscript.ComputeTaprootKeyNoScript(internal), nil
	}
	if len(root) != 32 {
		return nil, fmt.Errorf("hedgehog: merkle root must be 32 bytes, got %d", len(root))
	}
	return txscript.ComputeTaprootOutputKey(internal, root), nil
}

// P2TRScriptPubKey returns the witness-v1 scriptPubKey for an output key.
func P2TRScriptPubKey(outputKey *btcec.PublicKey) []byte {
	spk := append([]byte{txscript.OP_1, 0x20}, schnorr.SerializePubKey(outputKey)...)
	return spk
}

// ControlBlock builds the control block for `target` inside the tree `scripts`.
//
// A SINGLE-LEAF tree yields exactly 33 bytes: (leaf_version|parity) ||
// internal_key. There is no merkle sibling, and appending the leaf's own hash
// produces a control block that reconstructs a DIFFERENT output key — see the
// package doc.
func ControlBlock(target []byte, tree Node) ([]byte, error) {
	path, err := merklePath(target, tree)
	if err != nil {
		return nil, err
	}
	root, err := TreeRoot(tree)
	if err != nil {
		return nil, err
	}
	outputKey, err := OutputKey(root)
	if err != nil {
		return nil, err
	}
	parity := byte(0)
	if outputKey.SerializeCompressed()[0] == 0x03 {
		parity = 1
	}
	cb := []byte{TapscriptLeafVersion | parity}
	cb = append(cb, NUMSInternalKey...)
	for _, sibling := range path {
		cb = append(cb, sibling...)
	}
	return cb, nil
}

// merklePath returns the sibling hashes from `target`'s leaf up to the root.
func merklePath(target []byte, n Node) ([][]byte, error) {
	switch t := n.(type) {
	case *LeafNode:
		if bytesEqual(t.Script, target) {
			return nil, nil
		}
		return nil, fmt.Errorf("hedgehog: target leaf not found in tree")
	case *BranchNode:
		lh, err := TreeRoot(t.Left)
		if err != nil {
			return nil, err
		}
		rh, err := TreeRoot(t.Right)
		if err != nil {
			return nil, err
		}
		if sub, err := merklePath(target, t.Left); err == nil {
			return append(sub, rh), nil
		}
		if sub, err := merklePath(target, t.Right); err == nil {
			return append(sub, lh), nil
		}
		return nil, fmt.Errorf("hedgehog: target leaf not found in tree")
	default:
		return nil, fmt.Errorf("hedgehog: unknown node type %T", n)
	}
}

// VerifyCommitment checks that (leaf, controlBlock) commits to outputKey. This is
// the check a party must pass before treating a script-path spend as valid for a
// given funding output.
func VerifyCommitment(outputKey, leaf, controlBlock []byte) error {
	if len(controlBlock) < 33 {
		return fmt.Errorf("%w: got %d", ErrControlBlockTooShort, len(controlBlock))
	}
	if controlBlock[0]&0xfe != TapscriptLeafVersion {
		return fmt.Errorf("%w: got 0x%02x", ErrBadLeafVersion, controlBlock[0])
	}
	if (len(controlBlock)-33)%32 != 0 {
		return fmt.Errorf("%w: path %d bytes", ErrOddPathLength, len(controlBlock)-33)
	}
	root := tapLeafHash(leaf)
	for i := 33; i < len(controlBlock); i += 32 {
		root = branchHash(root, controlBlock[i:i+32])
	}
	derived, err := OutputKey(root)
	if err != nil {
		return err
	}
	if !bytesEqual(schnorr.SerializePubKey(derived), outputKey) {
		return fmt.Errorf("%w\n  want %x\n  got  %x", ErrCommitmentMismatch,
			outputKey, schnorr.SerializePubKey(derived))
	}
	return nil
}

// ---------------------------------------------------------------------------
// small helpers (kept local so the package has no dependency beyond btcd)
// ---------------------------------------------------------------------------

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func bytesCompare(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			if a[i] < b[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// pushScriptNum encodes a small integer as a minimally-encoded script number.
func pushScriptNum(n int64) []byte {
	if n == 0 {
		return []byte{0x00}
	}
	var neg bool
	if n < 0 {
		neg = true
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append(b, byte(n&0xff))
		n >>= 8
	}
	if b[len(b)-1]&0x80 != 0 {
		if neg {
			b = append(b, 0x80)
		} else {
			b = append(b, 0x00)
		}
	} else if neg {
		b[len(b)-1] |= 0x80
	}
	// minimal push opcode for the length
	if len(b) == 1 && b[0] >= 1 && b[0] <= 16 {
		return []byte{0x50 + b[0]}
	}
	return append([]byte{byte(len(b))}, b...)
}

func mustDecodeHex(s string) []byte {
	b := make([]byte, len(s)/2)
	for i := 0; i < len(b); i++ {
		hi := hexVal(s[i*2])
		lo := hexVal(s[i*2+1])
		b[i] = hi<<4 | lo
	}
	return b
}

func hexVal(c byte) byte {
	switch {
	case c >= '0' && c <= '9':
		return c - '0'
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10
	case c >= 'A' && c <= 'F':
		return c - 'A' + 10
	}
	panic("hedgehog: bad hex")
}
