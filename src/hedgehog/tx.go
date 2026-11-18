package hedgehog

import (
	"errors"
	"fmt"

	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	"github.com/btcsuite/btcd/chaincfg/chainhash"
	"github.com/btcsuite/btcd/txscript"
	"github.com/btcsuite/btcd/wire"
)

// SIGHASH CORRECTNESS — WHY THIS FILE USES btcd RATHER THAN ITS OWN MATH
//
// The research phase (PR #944) implemented BIP-341's signature message by hand
// and got it wrong in four ways from a single misreading:
//
//   - sha_prevouts omitted
//   - sha_sequences omitted            <- the decisive omission
//   - the ANYONECANPAY per-input form emitted for the default form
//   - double SHA256 where the spec uses a single SHA256 for the streams
//
// All four were silently self-consistent, so a hand-written second
// implementation AGREED with the first while both disagreed with Bitcoin Core.
// The omission was eventually found by reading the BIP's field list, not by
// reasoning or brute force.
//
// The lesson is therefore not "be careful" but "do not hand-roll consensus
// hashing". This file delegates to btcd's audited implementation and PROVES the
// delegation with a signature Bitcoin Core itself produced.

// ChannelScripts holds every leaf of a channel's tapscript tree.
type ChannelScripts struct {
	// RevocationByPayer is spendable by the payer after the CSV delay, or by
	// the payee immediately if the payee knows the revocation secret.
	RevocationByPayer []byte
	// RevocationByPayee is the mirror image.
	RevocationByPayee []byte
	// Multisig is the cooperative 2-of-2 path.
	Multisig []byte
}

// Channel describes a Hedgehog channel between two parties.
type Channel struct {
	// PayerXOnly and PayeeXOnly are the two parties' x-only public keys.
	PayerXOnly, PayeeXOnly []byte
	// CSVBlocks is the relative lock for the revocation paths.
	CSVBlocks uint16
	// PayerRevocationHash and PayeeRevocationHash are RIPEMD160 preimage hashes.
	PayerRevocationHash, PayeeRevocationHash []byte

	Scripts ChannelScripts
	Tree    Node
	// OutputKey is the P2TR output key of the funding output.
	OutputKey *btcec.PublicKey
	// ScriptPubKey is the funding output's script.
	ScriptPubKey []byte
}

// NewChannel builds the tapscript tree and funding output for a channel.
//
// The tree is NESTED, matching the shape used throughout the research phase:
//
//	Branch( Leaf(multisig),
//	        Branch( Leaf(payerRevocation), Leaf(payeeRevocation) ) )
func NewChannel(payerX, payeeX []byte, csvBlocks uint16,
	payerRevHash, payeeRevHash []byte) (*Channel, error) {

	ms, err := MultisigLeaf(payerX, payeeX)
	if err != nil {
		return nil, err
	}
	// Revocation-by-payer: the PAYEE can revoke the payer's attempt to
	// unilaterally reclaim, by revealing the preimage. The signing key is the
	// payee's, because it is the payee who exercises this path.
	revPayer, err := RevocationLeaf(payerRevHash, payeeX)
	if err != nil {
		return nil, err
	}
	// Revocation-by-payee: the mirror, signed by the payer.
	revPayee, err := RevocationLeaf(payeeRevHash, payerX)
	if err != nil {
		return nil, err
	}

	tree := Branch(
		Leaf(ms),
		Branch(Leaf(revPayer), Leaf(revPayee)),
	)
	root, err := TreeRoot(tree)
	if err != nil {
		return nil, err
	}
	ok, err := OutputKey(root)
	if err != nil {
		return nil, err
	}

	return &Channel{
		PayerXOnly: payerX, PayeeXOnly: payeeX,
		CSVBlocks:          csvBlocks,
		PayerRevocationHash: payerRevHash, PayeeRevocationHash: payeeRevHash,
		Scripts: ChannelScripts{
			RevocationByPayer: revPayer,
			RevocationByPayee: revPayee,
			Multisig:          ms,
		},
		Tree:         tree,
		OutputKey:    ok,
		ScriptPubKey: P2TRScriptPubKey(ok),
	}, nil
}

// ControlBlockFor returns the control block for one of the channel's leaves.
func (c *Channel) ControlBlockFor(leaf []byte) ([]byte, error) {
	return ControlBlock(leaf, c.Tree)
}

// ---------------------------------------------------------------------------
// Signing
// ---------------------------------------------------------------------------

// Signer signs a taproot script-path input.
type Signer struct {
	// PrivKey is the 32-byte secret key. Its Y parity is normalised so the
	// x-only public key matches the one committed in the leaf.
	PrivKey *btcec.PrivateKey
	// XOnly is the x-only public key of PrivKey.
	XOnly []byte
}

// NewSigner builds a signer from a 32-byte secret key, normalising the Y parity
// so the x-only key is well defined.
//
// BIP-340 signs with the key whose Y is EVEN. If the secret key's point has odd
// Y, the effective secret is (n - d). Getting this wrong produces a signature
// that verifies against the wrong x-only key, which Core reports simply as
// "Invalid Schnorr signature" — a real trap in the research phase.
func NewSigner(seckey []byte) (*Signer, error) {
	if len(seckey) != 32 {
		return nil, fmt.Errorf("hedgehog: secret key must be 32 bytes")
	}
	priv, _ := btcec.PrivKeyFromBytes(seckey)
	if priv.PubKey().SerializeCompressed()[0] == 0x03 {
		// negate the scalar so Y becomes even
		var s btcec.ModNScalar
		s.SetByteSlice(seckey)
		s.Negate()
		b := s.Bytes()
		priv, _ = btcec.PrivKeyFromBytes(b[:])
	}
	return &Signer{
		PrivKey: priv,
		XOnly:   schnorr.SerializePubKey(priv.PubKey()),
	}, nil
}

// XOnlyOf returns the BIP-340 x-only key for a 32-byte secret key.
func XOnlyOf(seckey []byte) ([]byte, error) {
	s, err := NewSigner(seckey)
	if err != nil {
		return nil, err
	}
	return s.XOnly, nil
}

// SignScriptPath signs input `idx` of `tx` for the given leaf.
//
// prevOuts must contain EVERY input's previous output, in order: BIP-341 commits
// to all of them (sha_prevouts, sha_amounts, sha_scriptpubkeys, sha_sequences),
// so a partial list cannot produce a correct signature.
func (s *Signer) SignScriptPath(tx *wire.MsgTx, idx int, prevOuts []*wire.TxOut,
	leaf []byte) ([]byte, error) {

	sigHash, err := CalcTaprootScriptPathSigHash(tx, idx, prevOuts, leaf)
	if err != nil {
		return nil, err
	}
	sig, err := schnorr.Sign(s.PrivKey, sigHash)
	if err != nil {
		return nil, fmt.Errorf("hedgehog: sign: %w", err)
	}
	// SIGHASH_DEFAULT means the signature carries NO appended hash-type byte.
	return sig.Serialize(), nil
}

// CalcTaprootScriptPathSigHash computes the BIP-341 sighash for a script-path
// spend, delegating to btcd's audited implementation.
func CalcTaprootScriptPathSigHash(tx *wire.MsgTx, idx int,
	prevOuts []*wire.TxOut, leaf []byte) ([]byte, error) {

	if idx < 0 || idx >= len(tx.TxIn) {
		return nil, fmt.Errorf("hedgehog: input index %d out of range", idx)
	}
	if len(prevOuts) != len(tx.TxIn) {
		return nil, fmt.Errorf("hedgehog: prevOuts has %d entries for %d inputs; "+
			"BIP-341 commits to ALL inputs, so every previous output is required",
			len(prevOuts), len(tx.TxIn))
	}
	prevFetcher := txscript.NewMultiPrevOutFetcher(nil)
	for i, po := range prevOuts {
		prevFetcher.AddPrevOut(tx.TxIn[i].PreviousOutPoint, po)
	}
	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)

	// CRITICAL: use the TAPSCRIPT variant, not CalcTaprootSignatureHash.
	//
	// CalcTaprootSignatureHash is the KEY-PATH variant: it leaves ext_flag == 0,
	// so the BIP-342 extension (tapleaf hash, key version, code-separator
	// position) is never appended to the message. Signing with it produces
	// perfectly well-formed signatures that the tapscript interpreter rejects
	// with "Invalid Schnorr signature" -- because the interpreter computes the
	// MESSAGE WITH the extension.
	//
	// This was a real failure in this package: both signatures verified against
	// their own keys, yet the spend failed, which localises the fault to the
	// message rather than the signature. CalcTapscriptSignaturehash sets
	// ext_flag = tapscriptSighashExtFlag and appends the tapleaf hash for us.
	tapLeaf := txscript.NewBaseTapLeaf(leaf)
	return txscript.CalcTapscriptSignaturehash(sigHashes, txscript.SigHashDefault,
		tx, idx, prevFetcher, tapLeaf)
}

// ---------------------------------------------------------------------------
// Witness construction
// ---------------------------------------------------------------------------

// WitnessForMultisig builds the witness for the cooperative 2-of-2 path.
//
// ORDER IS LOAD-BEARING. The leaf is
//
//	<payer> OP_CHECKSIG <payee> OP_CHECKSIGADD OP_2 OP_NUMEQUAL
//
// and OP_CHECKSIG takes its key from the TOP of the stack. Witness elements are
// pushed left to right, so the LAST element ends up on top. Therefore:
//
//	OP_CHECKSIG    pairs <payer> with the TOP item    -> top must be payerSig
//	OP_CHECKSIGADD pairs <payee> with the next item   -> next must be payeeSig
//
// which means the witness must be [payeeSig, payerSig, leaf, controlBlock].
// Shipping [payerSig, payeeSig, ...] makes Core report "Invalid Schnorr
// signature" — a real bug from the research phase.
func WitnessForMultisig(payerSig, payeeSig, leaf, controlBlock []byte) [][]byte {
	return [][]byte{payeeSig, payerSig, leaf, controlBlock}
}

// WitnessForChecksig builds the witness for a single-signature leaf, which is
// the shape of the CSV timeout and revocation paths.
func WitnessForChecksig(sig, leaf, controlBlock []byte) [][]byte {
	return [][]byte{sig, leaf, controlBlock}
}

// WitnessForPayerHalf builds the half-signed witness the PAYER hands over.
//
// It is deliberately NOT spendable alone: with one signature the leaf evaluates
// to 1 where OP_2 OP_NUMEQUAL needs 2. That is the protocol's safety property --
// the payer's half is worthless without the payee's cooperation, which is what
// makes it safe to transmit as plaintext.
func WitnessForPayerHalf(payerSig, leaf, controlBlock []byte) [][]byte {
	return [][]byte{{}, payerSig, leaf, controlBlock}
}

// WitnessForRevocation builds the witness for a revocation leaf:
//
//	OP_RIPEMD160 <hash> OP_EQUALVERIFY <key> OP_CHECKSIG
//
// The preimage is consumed by OP_RIPEMD160, and the signature by OP_CHECKSIG, so
// the preimage sits below the signature: [preimage, sig, leaf, controlBlock].
func WitnessForRevocation(preimage, sig, leaf, controlBlock []byte) [][]byte {
	return [][]byte{preimage, sig, leaf, controlBlock}
}

// ---------------------------------------------------------------------------
// Verification (in-process, no chain access)
// ---------------------------------------------------------------------------

// VerifyScriptPath executes a tapscript spend in-process against btcd's script
// engine. This is a CONVENIENCE check for the router side, which has no chain
// access; it is not a substitute for consensus validation, and the test suite
// validates against Bitcoin Core wherever a node is available.
func VerifyScriptPath(tx *wire.MsgTx, idx int, prevOut *wire.TxOut,
	witness [][]byte) error {

	if idx < 0 || idx >= len(tx.TxIn) {
		return fmt.Errorf("hedgehog: input index %d out of range", idx)
	}
	prevFetcher := txscript.NewCannedPrevOutputFetcher(
		prevOut.PkScript, prevOut.Value)
	sigHashes := txscript.NewTxSigHashes(tx, prevFetcher)

	// The engine needs the witness in place; work on a copy so the caller's
	// transaction is not mutated by a verification.
	cp := tx.Copy()
	cp.TxIn[idx].Witness = witness

	vm, err := txscript.NewEngine(prevOut.PkScript, cp, idx,
		txscript.StandardVerifyFlags, nil, sigHashes, prevOut.Value,
		prevFetcher)
	if err != nil {
		return fmt.Errorf("hedgehog: engine: %w", err)
	}
	if err := vm.Execute(); err != nil {
		return fmt.Errorf("hedgehog: script failed: %w", err)
	}
	return nil
}

// ErrNotImplemented marks a path that a later change will fill in.
var ErrNotImplemented = errors.New("hedgehog: not implemented")

// OutPointHash parses a txid in display (big-endian) order into a chainhash.
func OutPointHash(txidHex string) (*chainhash.Hash, error) {
	return chainhash.NewHashFromStr(txidHex)
}
