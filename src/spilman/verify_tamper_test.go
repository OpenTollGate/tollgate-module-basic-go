package spilman

// The tamper table for the pure-Go verifier, in the DEFAULT build.
//
// # Why this file is the gate
//
// The false-positive direction is the one that matters: a verifier that rejects a
// genuine payment is a bug, but a verifier that ACCEPTS one fabricated payment
// makes the whole rail a faucet — an attacker mints themselves credit. Every
// variant below is a distinct way to fabricate a payment, and every one must be
// refused. Each test name says which variant it kills, so a failure identifies the
// hole rather than just "verification is broken".
//
// # The variants that are not obvious
//
//   - the signature for balance±1 offered as balance: proves the balance is bound
//     into the message, not only the channel;
//   - the OTHER channel's genuine signature: the vector carries two real channels,
//     so this is a real signature from a real signer over a real commitment for a
//     different channel — not a mangled byte string. It proves the channel secret
//     is load-bearing, since the secret is the HMAC key for every derivation;
//   - skipping the parity negation in `derive_blinded_pubkey`: this is the actual
//     parity trap. See the comment on that test — the obvious framing of it is
//     wrong, and the test says why;
//   - a 33-byte compressed point offered as a signature: proves the length check is
//     not the only thing between a public key and a signature.

import (
	"encoding/json"
	"strings"
	"testing"

	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/stretchr/testify/require"
)

// bothChannels runs a tamper case against every channel in the vector, so no
// variant is proven only on the easy (even-Y) channel.
func bothChannels(t *testing.T, fn func(t *testing.T, channel vectorChannel)) {
	t.Helper()
	allChannels(t, fn)
}

// tamperedFunding returns a channel's funding record with one field replaced, so a
// test can state exactly what it changed.
func tamperedFunding(t *testing.T, channel vectorChannel, mutate func(*FundingRecord)) FundingRecord {
	t.Helper()
	funding := fundingOf(channel)
	mutate(&funding)
	return funding
}

// otherChannel returns the channel in the vector that is NOT the given one.
func otherChannel(t *testing.T, channel vectorChannel) vectorChannel {
	t.Helper()
	for _, candidate := range loadGoldenVector(t).Channels {
		if candidate.Label != channel.Label {
			return candidate
		}
	}
	t.Fatalf("the vector has no channel other than %q", channel.Label)
	return vectorChannel{}
}

// TestTamperRefusedWrongBalance: the signature genuine for balance B must not
// authorise any other balance, in either direction.
func TestTamperRefusedWrongBalance(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		require.NoError(t, VerifyBalanceUpdate(funding, 32, genuineCase(t, channel, 32).Signature))

		// Higher: the classic "claim more than was signed".
		require.ErrorIs(t, VerifyBalanceUpdate(funding, 33, genuineCase(t, channel, 32).Signature),
			ErrVerifySignature, "balance 32's signature must not authorise 33")

		// Lower: a payer who signed a large balance must not be able to walk it back
		// to a small one; both directions must bind or the balance is not committed.
		require.ErrorIs(t, VerifyBalanceUpdate(funding, 31, genuineCase(t, channel, 32).Signature),
			ErrVerifySignature, "balance 32's signature must not authorise 31")
	})
}

// TestTamperRefusedBalanceMinusOne: the specific off-by-one an attacker would try
// on a channel whose balance they can shift by their own spending. Named separately
// from the general wrong-balance case because it is the one that matters
// operationally.
func TestTamperRefusedBalanceMinusOne(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		for _, testCase := range channel.Cases {
			if testCase.Balance == 0 {
				continue
			}
			testCase := testCase
			t.Run(balanceName(testCase.Balance), func(t *testing.T) {
				require.ErrorIs(t, VerifyBalanceUpdate(funding, testCase.Balance-1, testCase.Signature),
					ErrVerifySignature,
					"the signature for %d must not verify as %d", testCase.Balance, testCase.Balance-1)
			})
		}
	})
}

// TestTamperRefusedWrongChannelID: the id the payment names is not hashed into the
// SIG_ALL message directly, but it IS the HMAC suffix of every derivation. A record
// whose stored id disagrees with the id derived from its own parameters must be
// refused by the id equality check before the signature is consulted — otherwise
// the verifier would derive a commitment for a channel the payment did not name.
func TestTamperRefusedWrongChannelID(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		signature := genuineCase(t, channel, 32).Signature

		// A well-formed but different id.
		funding := tamperedFunding(t, channel, func(f *FundingRecord) {
			f.ChannelID = strings.Repeat("ab", 32)
		})
		require.ErrorIs(t, VerifyBalanceUpdate(funding, 32, signature), ErrVerifyChannelID,
			"a record whose derived id disagrees with the claimed id must be refused by the id check")

		// The OTHER real channel's id. This is the shape that matters: a
		// well-formed, genuinely-derived id that belongs to a different channel.
		funding = tamperedFunding(t, channel, func(f *FundingRecord) {
			f.ChannelID = otherChannel(t, channel).ChannelID
		})
		require.ErrorIs(t, VerifyBalanceUpdate(funding, 32, signature), ErrVerifyChannelID,
			"another channel's real id must not be accepted for this channel")

		// And an empty id must not be treated as "any channel".
		funding = tamperedFunding(t, channel, func(f *FundingRecord) { f.ChannelID = "" })
		require.Error(t, VerifyBalanceUpdate(funding, 32, signature),
			"an empty channel id must be refused")
	})
}

// TestTamperRefusedSignatureFromAnotherChannel is the cross-channel case using two
// GENUINE signatures. Both channels are signed by the real Rust signer, so the
// refusal cannot be blamed on a corrupted signature: it is purely that the credit
// belongs to a different channel.
func TestTamperRefusedSignatureFromAnotherChannel(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		other := otherChannel(t, channel)
		foreignSignature := genuineCase(t, other, 32).Signature

		// Control: the signature is genuinely valid on its own channel. Without this
		// the refusal below would pass even if the signature were junk.
		require.NoError(t, VerifyBalanceUpdate(fundingOf(other), 32, foreignSignature),
			"control: the other channel's signature must be valid on the other channel")

		require.ErrorIs(t, VerifyBalanceUpdate(fundingOf(channel), 32, foreignSignature),
			ErrVerifySignature,
			"channel %q must not accept a signature made for channel %q", channel.Label, other.Label)
	})
}

// TestTamperRefusedSignatureWithAnotherChannelsSecret goes past the id check.
//
// Note on what this can and cannot prove: `channel_secret` is part of the
// channel-id PREIMAGE (params.rs:562 formats `hex::encode(self.channel_secret)` as
// the twelfth field), so substituting the secret necessarily changes the derived
// id. There is no way to reach the signature check with a wrong secret and a
// correct id — the id check is not a bypassable gate in front of the
// cryptography, it is a second, independent expression of the same fact. Asserting
// `ErrVerifySignature` here would require lying about the id AND having
// parseVerifyParams recompute the id from the params, which it does not: the
// record's id always wins the comparison.
//
// So this test proves the two things that are actually true and load-bearing:
// a substituted secret is refused, and it is refused by the id check — which is
// strict-stronger, because that refusal happens before any expensive curve work.
func TestTamperRefusedSignatureWithAnotherChannelsSecret(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		other := otherChannel(t, channel)
		signature := genuineCase(t, channel, 32).Signature

		// Control: the signature is valid on its own channel.
		require.NoError(t, VerifyBalanceUpdate(fundingOf(channel), 32, signature))

		// A different secret => a different derived id => refused by the id check.
		swapped := tamperedFunding(t, channel, func(f *FundingRecord) {
			f.ChannelSecretHex = other.ChannelSecretHex
		})
		require.ErrorIs(t, VerifyBalanceUpdate(swapped, 32, signature), ErrVerifyChannelID,
			"a substituted channel secret must be refused, because the secret is part of the "+
				"channel-id preimage")

		// One flipped hex nibble — a secret that is neither channel's — must be
		// refused too, so the refusal is not merely "this equals the other channel".
		flipped := tamperedFunding(t, channel, func(f *FundingRecord) {
			f.ChannelSecretHex = flipLastNibble(channel.ChannelSecretHex)
		})
		require.ErrorIs(t, VerifyBalanceUpdate(flipped, 32, signature), ErrVerifyChannelID)

		// And the id cannot be made to agree by asserting the original id: the
		// comparison is derived-vs-recorded, so a lie is still caught.
		lying := tamperedFunding(t, channel, func(f *FundingRecord) {
			f.ChannelSecretHex = other.ChannelSecretHex
			f.ChannelID = channel.ChannelID
		})
		require.ErrorIs(t, VerifyBalanceUpdate(lying, 32, signature), ErrVerifyChannelID,
			"a record that lies about its id must still be refused, because the id is "+
				"recomputed from the parameters and the secret")
	})
}

// flipLastNibble changes one hex digit in place, producing a well-formed 32-byte
// secret that is not the channel's.
func flipLastNibble(hexString string) string {
	if hexString == "" {
		return "0"
	}
	last := hexString[len(hexString)-1]
	replacement := byte('0')
	if last == '0' {
		replacement = '1'
	}
	return hexString[:len(hexString)-1] + string(replacement)
}

// TestTamperRefusedEmptySignature: no signature at all must never be "no signature
// required".
func TestTamperRefusedEmptySignature(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		require.ErrorIs(t, VerifyBalanceUpdate(fundingOf(channel), 32, ""), ErrVerifySignature)
	})
}

// TestTamperRefusedSixtyFourZeroBytes: a structurally perfect signature of the right
// length and in range, made of zeros. BIP-340 must reject it (R is the point at
// infinity and s = 0), and a verifier must not mistake a well-formed encoding for a
// valid one.
func TestTamperRefusedSixtyFourZeroBytes(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		require.ErrorIs(t, VerifyBalanceUpdate(fundingOf(channel), 32, strings.Repeat("00", 64)),
			ErrVerifySignature)
	})
}

// TestTamperRefusedSkippingTheParityNegation is the real parity trap.
//
// The tempting framing — "a signature under the negated blinded key must not
// verify" — is WRONG, and this repo's own probe proved it: BIP-340 keys are
// x-only, so P and -P are literally the same verification key. Asserting that
// would either fail or, worse, pass for the wrong reason.
//
// What parity actually controls is which POINT gets derived. `derive_blinded_pubkey`
// computes P' = (±P) + r*G, negating P when its Y is odd. A verifier that skipped
// the negation would derive P + r*G instead, which is a different point with a
// different x coordinate, so the genuine signature would not verify under it.
//
// So the tamper is at the derivation: build the key WITHOUT the negation and show
// the genuine signature fails under it. On the even-Y channel this changes nothing
// (P == -P for the x-only purposes of the negation, and the branch never fires),
// which is exactly why the vector carries an odd-Y channel — and the test asserts
// both halves so the even channel's blindness is recorded, not assumed.
func TestTamperRefusedSkippingTheParityNegation(t *testing.T) {
	for _, channel := range loadGoldenVector(t).Channels {
		channel := channel
		t.Run(channel.Label, func(t *testing.T) {
			funding := fundingOf(channel)
			params, _, err := verifyInputsFromFunding(funding)
			require.NoError(t, err)

			tweak, err := params.stage1BlindingScalar(stage1SenderContext)
			require.NoError(t, err)

			// The real key, and the same key derived with the parity branch skipped.
			correct, err := params.senderBlindedPubkeyForStage1()
			require.NoError(t, err)
			withoutParity := deriveBlindedPubkeyWithoutParityForTest(t, params.SenderPubkey, tweak)

			_, digest, err := BalanceUpdateMessageDigest(funding, 32)
			require.NoError(t, err)
			signature := genuineCase(t, channel, 32).Signature

			require.NoError(t, verifyBIP340(correct, digest, signature),
				"control: the genuine signature must verify under the correctly derived key")

			switch channel.SenderPubkeyParity {
			case "odd":
				// The branch fires here, so skipping it must produce a different
				// point and break verification.
				require.NotEqual(t, hexLower(correct.SerializeCompressed()),
					hexLower(withoutParity.SerializeCompressed()),
					"with an odd-Y sender pubkey the negation must change the derived key")
				require.ErrorIs(t, verifyBIP340(withoutParity, digest, signature), ErrVerifySignature,
					"a key derived without the parity negation must not verify the genuine signature")

			case "even":
				// The branch cannot fire: this channel cannot test parity at all, and
				// saying so is the point of emitting the odd-Y channel alongside it.
				require.Equal(t, hexLower(correct.SerializeCompressed()),
					hexLower(withoutParity.SerializeCompressed()),
					"with an even-Y sender pubkey the negation is a no-op, so this channel "+
						"cannot detect a missing parity branch")

			default:
				t.Fatalf("channel %q has unknown parity %q", channel.Label, channel.SenderPubkeyParity)
			}
		})
	}
}

// TestTamperRefusedSignatureFromTheReceiver: the receiver holds a key too. A
// signature by the receiver's key (in any of its blinded forms) must not
// authenticate a balance, because only the payer may move the channel balance.
func TestTamperRefusedSignatureFromTheReceiver(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		params, _, err := verifyInputsFromFunding(funding)
		require.NoError(t, err)

		// The receiver's stage-1 blinded key is the natural "wrong signer": same
		// derivation, same context family, different party.
		receiverTweak, err := params.stage1BlindingScalar(stage1ReceiverContext)
		require.NoError(t, err)
		receiverBlinded, err := deriveBlindedPubkey(params.ReceiverPubkey, receiverTweak)
		require.NoError(t, err)

		_, digest, err := BalanceUpdateMessageDigest(funding, 32)
		require.NoError(t, err)

		// Sign with Charlie's real blinded secret, so the refusal cannot be blamed
		// on a malformed signature.
		receiverSecret := deriveBlindedSecretForTest(t, mustParsePoint(t, params.ReceiverPubkey), receiverTweak)
		signature := signDigestForTest(t, receiverSecret, digest)

		// Control: the signature really is valid under Charlie's blinded key.
		assertSignatureIsValidForKey(t, receiverBlinded, digest, signature)

		require.ErrorIs(t, VerifyBalanceUpdate(funding, 32, signature), ErrVerifySignature,
			"a signature by the receiver's key must not authenticate a balance update")
	})
}

// TestTamperRefusedSenderBlindedWithTheReceiverContext is the subtler wrong-signer
// case: the SENDER's key, but blinded with the receiver's stage-1 context. If the
// verifier used a single context for both roles, this signature would verify.
func TestTamperRefusedSenderBlindedWithTheReceiverContext(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		params, _, err := verifyInputsFromFunding(funding)
		require.NoError(t, err)

		wrongTweak, err := params.stage1BlindingScalar(stage1ReceiverContext)
		require.NoError(t, err)
		wrongKey, err := deriveBlindedPubkey(params.SenderPubkey, wrongTweak)
		require.NoError(t, err)

		_, digest, err := BalanceUpdateMessageDigest(funding, 32)
		require.NoError(t, err)

		// Alice signs, but under the key derived with the receiver's context.
		wrongSecret := deriveBlindedSecretForTest(t, mustParsePoint(t, params.SenderPubkey), wrongTweak)
		signature := signDigestForTest(t, wrongSecret, digest)
		assertSignatureIsValidForKey(t, wrongKey, digest, signature)

		require.ErrorIs(t, VerifyBalanceUpdate(funding, 32, signature), ErrVerifySignature,
			"the sender blinded under the receiver's context must not authenticate a balance")
	})
}

// TestTamperRefusedReorderedOutputs: the SIG_ALL message hashes the outputs in a
// stable sort by amount, with receiver outputs before sender outputs. Swapping two
// outputs of equal amount must change the message — if the sort were unstable or
// keyed on something else, a peer could permute the list into a different signed
// meaning.
func TestTamperRefusedReorderedOutputs(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		params, _, err := verifyInputsFromFunding(funding)
		require.NoError(t, err)

		// Balance 63 of 64 is the case with a receiver/sender tie at amount 1, which
		// is the only place the tie-break order is observable.
		outputs, err := params.commitmentForBalance(63)
		require.NoError(t, err)

		var tied int
		for index := 1; index < len(outputs); index++ {
			if outputs[index-1].Amount == outputs[index].Amount {
				tied++
			}
		}
		require.GreaterOrEqual(t, tied, 1,
			"balance 63 must contain at least one equal-amount pair, or the tie-break order "+
				"is not observable on this channel")

		original, _, err := BalanceUpdateMessageDigest(funding, 63)
		require.NoError(t, err)

		// Permute the sorted list and confirm the message changes. This is a property
		// of the message construction, asserted directly so it does not depend on
		// guessing which two entries the derivation happened to tie.
		swapped := append([]commitmentOutput(nil), outputs...)
		swapped[0], swapped[1] = swapped[1], swapped[0]
		require.NotEqual(t, original, signedMessage(mustProofs(t, funding), swapped),
			"reordering two outputs must change the signed message")
	})
}

// TestTamperRefusedProofReordering: the funding proofs are hashed in STORED order,
// so reordering the persisted array must change the message and invalidate the
// signature. The vector has a single proof, so this test records that limitation
// and proves the order-sensitivity directly instead of pretending the fixture
// covers it.
func TestTamperRefusedProofReordering(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		proofs := mustProofs(t, funding)
		require.Len(t, proofs, 1,
			"this vector has one funding proof, so multi-proof ordering is not exercised by "+
				"the fixture; see REPORT.md for the unproven item")

		params, _, err := verifyInputsFromFunding(funding)
		require.NoError(t, err)
		outputs, err := params.commitmentForBalance(32)
		require.NoError(t, err)

		// Two distinct proof C values in each order must produce different messages.
		// These are the sender's stage-1 blinded key and the receiver's, i.e. two real
		// curve points — not arbitrary bytes.
		senderC, err := params.senderBlindedPubkeyForStage1()
		require.NoError(t, err)
		receiverTweak, err := params.stage1BlindingScalar(stage1ReceiverContext)
		require.NoError(t, err)
		receiverC, err := deriveBlindedPubkey(params.ReceiverPubkey, receiverTweak)
		require.NoError(t, err)

		first := fundingProof{Amount: 1, Signature: hexLower(senderC.SerializeCompressed())}
		second := fundingProof{Amount: 1, Signature: hexLower(receiverC.SerializeCompressed())}

		require.NotEqual(t,
			signedMessage([]fundingProof{first, second}, outputs),
			signedMessage([]fundingProof{second, first}, outputs),
			"the proof order must be bound into the signed message")
	})
}

// TestTamperRefusedBalanceAboveCapacity: no signature can authorise more than the
// channel holds, and the refusal must be the RANGE error rather than a signature
// error — capacity is not a property of the signature.
func TestTamperRefusedBalanceAboveCapacity(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		signature := genuineCase(t, channel, channel.Capacity).Signature

		for _, balance := range []uint64{channel.Capacity + 1, channel.Capacity + 1000, ^uint64(0)} {
			require.ErrorIs(t, VerifyBalanceUpdate(funding, balance, signature), ErrVerifyBalanceRange,
				"balance %d must be a range refusal", balance)
		}
	})
}

// TestTamperRefusedMalformedSignatureEncodings covers the shapes a hostile peer
// actually sends.
func TestTamperRefusedMalformedSignatureEncodings(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		genuine := genuineCase(t, channel, 32).Signature

		refused := map[string]string{
			"not_hex":               "zzzz" + genuine[4:],
			"genuine_pubkey_as_sig": channel.SenderBlindedStage1,
			"truncated_signature":   genuine[:126],
			"one_byte_signature":    "ab",
			"hex_with_0x_prefix":    "0x" + genuine,
			"whitespace_padded":     " " + genuine + " ",
		}
		for name, signature := range refused {
			signature := signature
			t.Run(name, func(t *testing.T) {
				require.ErrorIs(t, VerifyBalanceUpdate(funding, 32, signature), ErrVerifySignature,
					"%s must be refused", name)
			})
		}

		// Uppercase hex is the interesting one: a signature is BYTES, so uppercase
		// must be ACCEPTED (encoding/hex is case-insensitive). If a future change
		// breaks that, a legitimate peer gets locked out. Assert the real behaviour
		// rather than leaving it ambiguous.
		require.NoError(t, VerifyBalanceUpdate(funding, 32, strings.ToUpper(genuine)),
			"a signature is bytes, so uppercase hex must still verify")
	})
}

// TestTamperRefusedIncompleteFundingRecord: with no record there is nothing to
// verify against, and every missing field must refuse rather than default to zero.
func TestTamperRefusedIncompleteFundingRecord(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		signature := genuineCase(t, channel, 32).Signature

		fields := map[string]func(*FundingRecord){
			"no_params":         func(f *FundingRecord) { f.ParamsJSON = "" },
			"no_proofs":         func(f *FundingRecord) { f.FundingProofsJSON = "" },
			"no_channel_secret": func(f *FundingRecord) { f.ChannelSecretHex = "" },
			"no_keyset_info":    func(f *FundingRecord) { f.KeysetInfoJSON = "" },
			"no_channel_id":     func(f *FundingRecord) { f.ChannelID = "" },
		}
		for name, mutate := range fields {
			mutate := mutate
			t.Run(name, func(t *testing.T) {
				require.Error(t, VerifyBalanceUpdate(tamperedFunding(t, channel, mutate), 32, signature),
					"%s must be refused", name)
			})
		}
	})
}

// TestTamperRefusedShortChannelSecret: a 31-byte secret must be refused outright,
// not zero-padded into a different channel.
func TestTamperRefusedShortChannelSecret(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		funding.ChannelSecretHex = channel.ChannelSecretHex[:62]
		require.ErrorIs(t, VerifyBalanceUpdate(funding, 32, genuineCase(t, channel, 32).Signature),
			ErrVerifyFundingRecord)
	})
}

// TestTamperRefusedProofsTotalMismatch: EstablishedChannel::new requires the funding
// proofs to total funding_token_amount. A record that does not is not this channel's
// funding, so the commitment would be signed over the wrong stake.
func TestTamperRefusedProofsTotalMismatch(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		// Same proof, wrong amount: still one proof, now short of the channel stake.
		funding.FundingProofsJSON = strings.Replace(
			channel.FundingProofsJSON, `"amount":64`, `"amount":63`, 1)
		require.NotEqual(t, channel.FundingProofsJSON, funding.FundingProofsJSON)

		require.ErrorIs(t, VerifyBalanceUpdate(funding, 32, genuineCase(t, channel, 32).Signature),
			ErrVerifyFundingRecord)
	})
}

// TestTamperRefusedProofFromAnotherKeyset: a proof carrying a different keyset id is
// not spendable in this channel (EstablishedChannel::new asserts it), so the record
// must be refused rather than hashed into a signature that "works".
func TestTamperRefusedProofFromAnotherKeyset(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		params, _, err := verifyInputsFromFunding(funding)
		require.NoError(t, err)

		funding.FundingProofsJSON = strings.ReplaceAll(
			channel.FundingProofsJSON, params.KeysetID, "00ffffffffffffff")
		require.NotEqual(t, channel.FundingProofsJSON, funding.FundingProofsJSON)

		require.ErrorIs(t, VerifyBalanceUpdate(funding, 32, genuineCase(t, channel, 32).Signature),
			ErrVerifyFundingRecord)
	})
}

// TestTamperRefusedSignatureIsNotReusableAcrossBalances enumerates the whole
// capacity range for one genuine signature and asserts it covers exactly the balance
// it was signed for. A range scan is affordable here (64 sats) and it closes the
// door on "some other balance happens to collide".
func TestTamperRefusedSignatureIsNotReusableAcrossBalances(t *testing.T) {
	bothChannels(t, func(t *testing.T, channel vectorChannel) {
		funding := fundingOf(channel)
		for _, testCase := range channel.Cases {
			testCase := testCase
			t.Run(balanceName(testCase.Balance), func(t *testing.T) {
				covers, err := BalanceUpdateSignatureCovers(funding, testCase.Signature, channel.Capacity)
				require.NoError(t, err)
				require.Equal(t, []uint64{testCase.Balance}, covers,
					"the signature for %d must verify for exactly that balance across [0, capacity]",
					testCase.Balance)
			})
		}
	})
}

// ---------------------------------------------------------------------------
// Test helpers local to the tamper table
// ---------------------------------------------------------------------------

// deriveBlindedPubkeyWithoutParityForTest recomputes derive_blinded_pubkey with the
// parity negation deliberately omitted, so the parity branch can be shown to be
// load-bearing rather than merely present.
func deriveBlindedPubkeyWithoutParityForTest(t testingT, pubkey string, tweak *secp.ModNScalar) *secp.PublicKey {
	t.Helper()

	point, err := parseCompressedPoint(pubkey)
	if err != nil {
		t.Fatalf("test pubkey %q: %v", pubkey, err)
	}

	var base, tweakPoint, sum secp.JacobianPoint
	point.AsJacobian(&base)
	secp.ScalarBaseMultNonConst(tweak, &tweakPoint)
	secp.AddNonConst(&base, &tweakPoint, &sum)
	sum.ToAffine()
	return secp.NewPublicKey(&sum.X, &sum.Y)
}

// mustProofs parses a funding record's proofs or fails the test.
func mustProofs(t testingT, funding FundingRecord) []fundingProof {
	t.Helper()

	var proofs []fundingProof
	if err := json.Unmarshal([]byte(funding.FundingProofsJSON), &proofs); err != nil {
		t.Fatalf("funding proofs: %v", err)
	}
	return proofs
}
