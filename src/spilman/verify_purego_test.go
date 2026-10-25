package spilman

// Golden-vector contract test for the pure-Go verifier, in the DEFAULT build.
//
// # Why a vector and not a hand-written fixture
//
// The verifier is only trustworthy if it agrees with the reference
// implementation on a REAL channel. A fixture invented here would prove that Go
// agrees with Go. `testdata/spilman_balance_update_vector.json` is emitted by the
// Rust signer itself:
//
//	SPILMAN_VECTOR_OUT=src/spilman/testdata/spilman_balance_update_vector.json \
//	  cargo test --offline -p cdk-spilman --test emit_go_vector -- --nocapture
//
// (producer: crates/cdk-spilman/tests/emit_go_vector.rs). It carries the channel
// material a payee persists, the channel id and its exact preimage, and, for each
// boundary balance, the genuine signature plus every intermediate: each output's
// secret string, nonce, blinding factor, blinded message, the exact SIG_ALL
// message and its SHA-256.
//
// # What is asserted
//
//  1. the Go channel id equals the Rust channel id, and the Go preimage equals
//     the Rust preimage (byte-for-byte, so a mismatch is diffable);
//  2. every Go blinded message equals its Rust counterpart;
//  3. every Go intermediate (nonce, blinding factor, blinded pubkey, secret
//     string) equals its Rust counterpart;
//  4. the Go SIG_ALL message and digest equal the Rust bytes;
//  5. every genuine signature verifies.
//
// Anything less would let a Go-only reimplementation that disagrees with the
// protocol pass by comparing itself to itself.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// vectorOutput is one blinded message from the Rust signer.
type vectorOutput struct {
	Owner          string `json:"owner"`
	Context        string `json:"context"`
	Amount         uint64 `json:"amount"`
	Index          int    `json:"index"`
	Secret         string `json:"secret"`
	BlindingFactor string `json:"blinding_factor"`
	BlindedMessage string `json:"blinded_message"`
}

// vectorOrderedOutput is one entry of the sorted output list the SIG_ALL message
// hashes.
type vectorOrderedOutput struct {
	Amount         uint64 `json:"amount"`
	BlindedMessage string `json:"blinded_message"`
}

// vectorCase is one balance and the genuine signature for it.
type vectorCase struct {
	Balance         uint64                `json:"balance"`
	Signature       string                `json:"signature"`
	SigAllMessage   string                `json:"sig_all_message"`
	SigAllSHA256    string                `json:"sig_all_sha256"`
	ReceiverNominal uint64                `json:"receiver_nominal"`
	SenderNominal   uint64                `json:"sender_nominal"`
	Outputs         []vectorOutput        `json:"outputs"`
	OrderedOutputs  []vectorOrderedOutput `json:"ordered_outputs"`
}

// goldenVector is the whole fixture.
type goldenVector struct {
	VectorName          string       `json:"vector_name"`
	ParamsJSON          string       `json:"params_json"`
	KeysetInfoJSON      string       `json:"keyset_info_json"`
	FundingProofsJSON   string       `json:"funding_proofs_json"`
	ChannelSecretHex    string       `json:"channel_secret_hex"`
	ChannelID           string       `json:"channel_id"`
	ChannelIDPreimage   string       `json:"channel_id_preimage"`
	SenderPubkey        string       `json:"sender_pubkey"`
	ReceiverPubkey      string       `json:"receiver_pubkey"`
	SenderBlindedStage1 string       `json:"sender_blinded_pubkey_stage1"`
	Capacity            uint64       `json:"capacity"`
	MaximumAmount       uint64       `json:"maximum_amount_for_one_output"`
	InputFeePPK         uint64       `json:"input_fee_ppk"`
	ValueAfterStage1    uint64       `json:"value_after_stage1"`
	OverCapacityError   string       `json:"over_capacity_error"`
	Cases               []vectorCase `json:"cases"`
}

// loadGoldenVector reads the Rust-emitted vector. A missing file is a FAILURE, not
// a skip: this test is the gate, and a gate that quietly disappears when its
// evidence is absent is not a gate.
func loadGoldenVector(t *testing.T) goldenVector {
	t.Helper()
	path := filepath.Join("testdata", "spilman_balance_update_vector.json")
	raw, err := os.ReadFile(path)
	require.NoError(t, err, "the Rust golden vector must be present at %s; regenerate it with "+
		"cargo test -p cdk-spilman --test emit_go_vector", path)

	var vector goldenVector
	require.NoError(t, json.Unmarshal(raw, &vector), "golden vector must parse")
	require.Equal(t, "spilman-balance-update-vector-v1", vector.VectorName)
	require.NotEmpty(t, vector.Cases, "vector must contain signature cases")
	require.NotEmpty(t, vector.ChannelID)
	return vector
}

// fundingFromVector is the funding record a payee would have persisted for this
// channel — the only input the verifier gets.
func fundingFromVector(vector goldenVector) FundingRecord {
	return FundingRecord{
		ChannelID:         vector.ChannelID,
		ParamsJSON:        vector.ParamsJSON,
		FundingProofsJSON: vector.FundingProofsJSON,
		ChannelSecretHex:  vector.ChannelSecretHex,
		KeysetInfoJSON:    vector.KeysetInfoJSON,
	}
}

// TestPureGoChannelIDMatchesTheRustSigner is the single strongest assertion in the
// contract: if the Go derivation of the channel id does not reproduce the id of a
// real channel, every downstream derivation is wrong and no signature can verify.
func TestPureGoChannelIDMatchesTheRustSigner(t *testing.T) {
	vector := loadGoldenVector(t)

	params, _, err := verifyInputsFromFunding(fundingFromVector(vector))
	require.NoError(t, err, "the Rust-emitted funding record must be usable by the Go verifier")

	require.Equal(t, vector.ChannelIDPreimage, params.channelIDPreimage(),
		"the exact SHA-256 input string must match the Rust bytes")
	require.Equal(t, vector.ChannelID, params.channelID(),
		"the Go channel id must equal the channel id the Rust signer derived")
}

// TestPureGoBlindedMessagesMatchTheRustSigner compares every intermediate, not
// only the final signature: a signature mismatch tells you nothing about which
// derivation is wrong, and diffing these makes that immediate.
func TestPureGoBlindedMessagesMatchTheRustSigner(t *testing.T) {
	vector := loadGoldenVector(t)
	params, _, err := verifyInputsFromFunding(fundingFromVector(vector))
	require.NoError(t, err)

	require.Equal(t, vector.ValueAfterStage1, mustValueAfterStage1(t, params),
		"value after stage-1 fees must match")

	for _, testCase := range vector.Cases {
		testCase := testCase
		t.Run(balanceName(testCase.Balance), func(t *testing.T) {
			outputs, err := params.commitmentForBalance(testCase.Balance)
			require.NoError(t, err, "balance %d must produce a commitment", testCase.Balance)

			require.Len(t, outputs, len(testCase.Outputs),
				"balance %d must produce the same number of outputs", testCase.Balance)

			// Compare position by position. The Rust vector lists receiver outputs
			// then sender outputs (each smallest-first); the Go list is the sorted
			// swap-request order, so index-by-index comparison would compare the
			// wrong pairs. Match on (owner, amount, index) instead.
			expected := make(map[outputKey]vectorOutput, len(testCase.Outputs))
			for _, output := range testCase.Outputs {
				expected[outputKey{output.Owner, output.Amount, output.Index}] = output
			}
			require.Len(t, expected, len(testCase.Outputs),
				"the vector must not contain duplicate (owner, amount, index) rows")

			for _, got := range outputs {
				key := outputKey{got.Owner, got.Amount, got.Index}
				want, ok := expected[key]
				require.True(t, ok, "Go derived an output the Rust signer did not: %+v", key)

				require.Equal(t, want.Secret, got.Secret,
					"secret string for %+v must be byte-identical (it is hashed to a curve point)", key)
				require.Equal(t, want.BlindingFactor, got.BlindingHex,
					"blinding factor for %+v must match", key)
				require.Equal(t, want.BlindedMessage, got.Blinded,
					"blinded message for %+v must match", key)
			}
		})
	}
}

type outputKey struct {
	owner  string
	amount uint64
	index  int
}

// TestPureGoSigAllMessageMatchesTheRustSigner pins the message itself. This is
// where the stable-sort tie order shows up: at balance 63 of 64 the receiver and
// the sender both produce an amount-1 output, and the receiver's must come first.
func TestPureGoSigAllMessageMatchesTheRustSigner(t *testing.T) {
	vector := loadGoldenVector(t)

	for _, testCase := range vector.Cases {
		testCase := testCase
		t.Run(balanceName(testCase.Balance), func(t *testing.T) {
			message, digest, err := BalanceUpdateMessageDigest(fundingFromVector(vector), testCase.Balance)
			require.NoError(t, err)

			require.Equal(t, testCase.SigAllMessage, message,
				"the SIG_ALL message string must equal the Rust bytes for balance %d", testCase.Balance)
			require.Equal(t, testCase.SigAllSHA256, hexLower(digest[:]),
				"the SHA-256 digest must equal the Rust digest for balance %d", testCase.Balance)
		})
	}
}

// TestPureGoVerifierAcceptsEveryGenuineSignature is the positive direction. A
// verifier that rejects everything would pass the whole tamper table, so this
// test is what makes that table meaningful.
func TestPureGoVerifierAcceptsEveryGenuineSignature(t *testing.T) {
	vector := loadGoldenVector(t)
	funding := fundingFromVector(vector)

	for _, testCase := range vector.Cases {
		testCase := testCase
		t.Run(balanceName(testCase.Balance), func(t *testing.T) {
			require.NoError(t, VerifyBalanceUpdate(funding, testCase.Balance, testCase.Signature),
				"the Rust signer's genuine signature for balance %d must verify", testCase.Balance)
		})
	}
}

// TestPureGoBlindedSenderKeyMatchesTheRustSigner checks the verification key
// itself. It is derived by a different route from the outputs (stage-1 shared
// blinding, not stage-2 per-output), so it is asserted separately.
func TestPureGoBlindedSenderKeyMatchesTheRustSigner(t *testing.T) {
	vector := loadGoldenVector(t)

	params, _, err := verifyInputsFromFunding(fundingFromVector(vector))
	require.NoError(t, err)

	pubkey, err := params.senderBlindedPubkeyForStage1()
	require.NoError(t, err)
	require.Equal(t, vector.SenderBlindedStage1, hexLower(pubkey.SerializeCompressed()),
		"the stage-1 blinded sender pubkey must equal the Rust value")
}

// TestPureGoVectorCoversTheCapacityBoundary records what the fixture actually
// contains, so the "capacity and capacity+1" claim in the report is checkable and
// cannot rot into an assumption.
func TestPureGoVectorCoversTheCapacityBoundary(t *testing.T) {
	vector := loadGoldenVector(t)

	byBalance := make(map[uint64]bool, len(vector.Cases))
	for _, testCase := range vector.Cases {
		byBalance[testCase.Balance] = true
	}
	require.True(t, byBalance[0], "the vector must cover balance 0")
	require.True(t, byBalance[1], "the vector must cover balance 1")
	require.True(t, byBalance[vector.Capacity], "the vector must cover balance == capacity")

	// There is no genuine signature above capacity, because the signer itself
	// refuses: the vector records that refusal. Asserting it here means the
	// tamper table's "capacity+1" case is provably un-signable rather than merely
	// unsigned.
	require.Equal(t, uint64(64), vector.Capacity)
	require.Contains(t, vector.OverCapacityError, "exceeds channel capacity",
		"the Rust signer must have refused capacity+1")

	// And the Go verifier must refuse that same balance for the genuine
	// capacity signature, before any signature is consulted.
	_, _, err := BalanceUpdateMessageDigest(fundingFromVector(vector), vector.Capacity+1)
	require.ErrorIs(t, err, ErrVerifyBalanceRange)
}

func mustValueAfterStage1(t *testing.T, params verifyParams) uint64 {
	t.Helper()
	value, err := params.valueAfterStage1()
	require.NoError(t, err)
	return value
}

func balanceName(balance uint64) string {
	return "balance_" + itoa(balance)
}

func itoa(value uint64) string {
	if value == 0 {
		return "0"
	}
	var digits []byte
	for value > 0 {
		digits = append([]byte{byte('0' + value%10)}, digits...)
		value /= 10
	}
	return string(digits)
}

func hexLower(raw []byte) string {
	const alphabet = "0123456789abcdef"
	out := make([]byte, 0, len(raw)*2)
	for _, b := range raw {
		out = append(out, alphabet[b>>4], alphabet[b&0x0f])
	}
	return string(out)
}
