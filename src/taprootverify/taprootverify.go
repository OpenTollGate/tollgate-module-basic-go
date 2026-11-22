// Package taprootverify provides an in-process taproot commitment verifier.
//
// WHY THIS EXISTS
//
// TollGate's payment path currently accepts exactly one instrument family
// (Cashu). Anything else -- an LNURLcash bearer note, a one-way-channel leaf, a
// Hedgehog-style revocable script -- is ultimately a BIP-341 taproot output key
// plus a script path, and the router has no way to reason about one today.
//
// This package is a deliberately BOUNDED slice: it verifies a taproot
// COMMITMENT (does this internal key, leaf script and control block produce the
// output key I am looking at?) and derives output keys. It does NOT accept
// payments, does NOT write a ledger, and does NOT touch the wallet. Those are
// separate, larger changes with their own fund-safety obligations.
//
// It is INSTRUMENT-AGNOSTIC on purpose. Nothing here knows what Cashu, LNURLcash
// or a channel leaf is; it knows taproot. That is what makes it reusable by the
// CDK migration the repo already wants, rather than a bespoke path for one
// prototype instrument.
//
// NO NEW DEPENDENCY, NO NEW CGO
//
// btcsuite/btcd's txscript and btcec/v2 are already compiled into the shipped
// router binary as INDIRECT dependencies of the gonuts Lightning stack -- the
// binary even emits txscript's error strings (ErrTaprootSigInvalid,
// ErrTaprootMerkleProofInvalid, ErrSchnorrHashValue, ...). This package
// therefore promotes an existing indirect requirement to a direct one; it does
// not add a dependency, and it is pure Go, so the repository's CGO_ENABLED=0
// multi-arch build story is unaffected.
package taprootverify

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
)

// Errors reported by this package. Named rather than anonymous so callers can
// distinguish "this is not taproot at all" from "this commitment is wrong" --
// the distinction matters when deciding whether to reject an instrument or
// merely log a malformed one.
var (
	// ErrNotTaproot means the scriptPubKey is not a witness-v1 taproot program.
	ErrNotTaproot = errors.New("taprootverify: scriptPubKey is not a taproot output")

	// ErrBadProgramLength means a witness-v1 program is not 32 bytes.
	ErrBadProgramLength = errors.New("taprootverify: witness v1 program must be 32 bytes")

	// ErrBadInternalKey means the internal key is not a valid x-only point.
	ErrBadInternalKey = errors.New("taprootverify: internal key is not a valid x-only public key")

	// ErrBadLeafVersion means the control block's leaf version is not 0xc0.
	ErrBadLeafVersion = errors.New("taprootverify: only tapscript leaf version 0xc0 is supported")

	// ErrControlBlockTooShort means the control block is smaller than the
	// 33-byte minimum (version+parity, internal key) -- i.e. it cannot even
	// encode an internal key, so it is malformed rather than merely mismatched.
	ErrControlBlockTooShort = errors.New("taprootverify: control block shorter than 33 bytes")

	// ErrOddPathLength means the merkle path is not a whole number of 32-byte
	// siblings.
	ErrOddPathLength = errors.New("taprootverify: control block path is not a multiple of 32 bytes")

	// ErrCommitmentMismatch is the load-bearing one: the supplied internal key,
	// leaf script and path do NOT reconstruct the output key being spent.
	ErrCommitmentMismatch = errors.New("taprootverify: commitment does not reproduce the output key")
)

// TapLeafVersion is the only tapscript leaf version this package accepts.
//
// Declared as a plain byte rather than txscript's named type so callers can do
// the bit arithmetic a control block requires (version|parity) without casts.
const TapLeafVersion byte = byte(txscript.BaseLeafVersion)

// IsTaproot reports whether spk is a witness-v1 (taproot) output, and returns
// the 32-byte output key if so.
//
// A witness-v1 program is OP_1 <32 bytes>: 0x51 0x20 || key. Anything else is
// not taproot, and the caller should fall through to whatever it did before.
func IsTaproot(spk []byte) ([]byte, bool) {
	if len(spk) != 34 || spk[0] != txscript.OP_1 || spk[1] != 0x20 {
		return nil, false
	}
	return spk[2:], true
}

// OutputKey derives the BIP-341 output key committed to by an internal key and a
// tapscript merkle root. Pass a nil/empty root for a key-path-only output.
//
// This is the derivation side: given the parties' agreement, what address should
// the money be sent to?
func OutputKey(internalKeyXOnly []byte, merkleRoot []byte) (*btcec.PublicKey, error) {
	internal, err := schnorr.ParsePubKey(internalKeyXOnly)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadInternalKey, err)
	}
	if len(merkleRoot) == 0 {
		return txscript.ComputeTaprootKeyNoScript(internal), nil
	}
	if len(merkleRoot) != chainhash.HashSize {
		return nil, fmt.Errorf("taprootverify: merkle root must be 32 bytes, got %d", len(merkleRoot))
	}
	return txscript.ComputeTaprootOutputKey(internal, merkleRoot), nil
}

// VerifyCommitment checks that (internalKey, leafScript, controlBlock) reproduce
// outputKey -- i.e. that this leaf really is spendable from that output.
//
// It re-derives the output key from the control block's own embedded internal
// key and the sibling path, folds in the leaf, and compares. This is the check
// that a service must pass before treating any script-path instrument as valid
// for a given taproot output.
//
// NOTE the deliberate absence of a "spend" or "accept" function. Verifying a
// commitment is necessary but NOT sufficient to accept value: the repository's
// own doctrine (AGENTS.md) forbids granting on a local check alone, because
// consumption state is not established by a commitment. This package stays on
// the safe side of that line.
func VerifyCommitment(outputKey, internalKeyXOnly, leafScript, controlBlock []byte) error {
	if len(controlBlock) < 33 {
		return fmt.Errorf("%w: got %d, need >= 33", ErrControlBlockTooShort, len(controlBlock))
	}
	leafVersion := controlBlock[0] & 0xfe
	if leafVersion != TapLeafVersion {
		return fmt.Errorf("%w: got 0x%02x", ErrBadLeafVersion, leafVersion)
	}
	if (len(controlBlock)-33)%chainhash.HashSize != 0 {
		return fmt.Errorf("%w: path length %d", ErrOddPathLength, len(controlBlock)-33)
	}

	// Rebuild the merkle root from the sibling path, exactly as BIP-341
	// specifies: fold the leaf hash with each sibling, ordering each pair by
	// byte-wise comparison (the lexicographic rule -- a common place to get this
	// wrong, because the "natural" order is to concatenate, not to sort).
	leafHash := txscript.NewBaseTapLeaf(leafScript).TapHash()
	root := leafHash[:]

	internalKey := controlBlock[1:33]
	path := controlBlock[33:]
	for i := 0; i < len(path); i += chainhash.HashSize {
		sibling := path[i : i+chainhash.HashSize]
		root = branchHash(root, sibling)
	}

	derived, err := OutputKey(internalKey, root)
	if err != nil {
		return err
	}

	// The output key in a scriptPubKey is x-only; compare x coordinates.
	if !bytes.Equal(schnorr.SerializePubKey(derived), outputKey) {
		return fmt.Errorf("%w\n  want %x\n  got  %x",
			ErrCommitmentMismatch, outputKey, schnorr.SerializePubKey(derived))
	}
	return nil
}

// branchHash computes a BIP-341 TapBranch hash, ordering the two children by
// lexicographic byte comparison as the spec requires.
func branchHash(a, b []byte) []byte {
	if bytes.Compare(a, b) > 0 {
		a, b = b, a
	}
	h := chainhash.TaggedHash(chainhash.TagTapBranch, a, b)
	return h[:]
}

// TreeRoot computes the BIP-341 merkle root of a NESTED script tree.
//
// The shape matters and a flat list cannot always express it. Bitcoin Core's
// descriptor language writes `tr(K,{a,{b,c}})`, which is a specific BINARY tree:
// hash(a) paired with the sub-tree (hash(b),hash(c)). Flattening the same three
// leaves into a balanced 3-leaf tree promotes an odd node and yields a
// DIFFERENT, wrong output key -- verified against Core, which is how this bug
// was found. So the tree shape is passed in explicitly rather than inferred.
//
// A leaf is `*ScriptLeaf`; an internal node is `*Branch`. Use Leaf(...) and
// Branch(...) to build the shape that matches your descriptor or agreement.
type ScriptLeaf struct{ Script []byte }

// Branch is an internal node in a tapscript tree.
type Branch struct{ Left, Right Node }

// Node is a leaf or an internal node of a tapscript tree.
type Node interface{ isNode() }

func (*ScriptLeaf) isNode() {}
func (*Branch) isNode()     {}

// Leaf wraps a script as a tree leaf.
func Leaf(script []byte) *ScriptLeaf { return &ScriptLeaf{Script: script} }

// BranchOf pairs two sub-trees. Order does not matter: BIP-341 sorts each pair
// lexicographically before hashing, so callers may pass children in any order.
func BranchOf(left, right Node) *Branch { return &Branch{Left: left, Right: right} }

// TreeRoot returns the merkle root for a nested tree. A single leaf returns its
// own leaf hash (equivalent to a key-path-only output with one script).
func TreeRoot(n Node) ([]byte, error) {
	switch t := n.(type) {
	case *ScriptLeaf:
		h := txscript.NewBaseTapLeaf(t.Script).TapHash()
		return h[:], nil
	case *Branch:
		if t.Left == nil || t.Right == nil {
			return nil, errors.New("taprootverify: branch has a nil child")
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
		return nil, fmt.Errorf("taprootverify: unknown node type %T", n)
	}
}

// MerkleRoot computes a BALANCED tree root from a flat list of leaf scripts,
// promoting an odd node by pairing it with itself (the BIP-341 rule).
//
// Prefer TreeRoot when the tree is not perfectly balanced. This helper exists
// because a balanced tree is the common case for uniform script sets, and
// because callers who write it by hand get the odd-node rule wrong -- note that
// balance is what makes it correct for 1, 2 and 4 leaves but NOT for 3, where a
// nested shape is usually intended instead.
func MerkleRoot(leafScripts ...[]byte) ([]byte, error) {
	if len(leafScripts) == 0 {
		return nil, errors.New("taprootverify: need at least one leaf script")
	}
	level := make([][]byte, 0, len(leafScripts))
	for _, s := range leafScripts {
		h := txscript.NewBaseTapLeaf(s).TapHash()
		level = append(level, h[:])
	}
	for len(level) > 1 {
		if len(level)%2 == 1 {
			level = append(level, level[len(level)-1]) // BIP-341 odd-node promotion
		}
		next := make([][]byte, 0, len(level)/2)
		for i := 0; i < len(level); i += 2 {
			next = append(next, branchHash(level[i], level[i+1]))
		}
		level = next
	}
	return level[0], nil
}
