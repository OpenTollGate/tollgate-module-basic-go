//go:build spilman

package spilman

// Fixtures for the payment-authentication tests.
//
// These are built from REAL material, not hand-written JSON: the keyset's public
// keys come from the binding, the channel secret is the real ECDH derivation,
// the channel id is the protocol's own hash, and the balance signature is
// produced by upstream's signer over the real commitment for that balance.
//
// A test that asserted "a genuine signature is accepted" against a fabricated
// signature would prove nothing: it would only show that the negative case
// fails. The load-bearing direction needs a signature that upstream actually
// produced, which is why this file signs instead of pasting.

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixtureChannel is one fully-formed channel: everything a payee would persist
// at registration, plus the payer's keys so a test can sign further payments.
type fixtureChannel struct {
	Funding       FundingRecord
	ChannelID     string
	SenderSecret  string
	SenderPubkey  string
	Capacity      uint64
	proofsJSON    string
	paramsJSON    string
	keysetJSON    string
	channelSecret string
}

// sign returns a genuine payer signature for balance on this channel.
func (f fixtureChannel) sign(t *testing.T, balance uint64) string {
	t.Helper()
	raw, err := CreateSignedBalanceUpdate(f.paramsJSON, f.keysetJSON, f.SenderSecret, f.proofsJSON, balance)
	require.NoError(t, err, "upstream signer must produce a balance update")

	var signed struct {
		Amount    uint64 `json:"amount"`
		ChannelID string `json:"channel_id"`
		Signature string `json:"signature"`
	}
	require.NoError(t, json.Unmarshal([]byte(raw), &signed))
	require.Equal(t, f.ChannelID, signed.ChannelID, "fixture channel id must match the signed update")
	require.Equal(t, balance, signed.Amount)
	require.NotEmpty(t, signed.Signature)
	return signed.Signature
}

// newFixtureChannel builds a channel of the given capacity backed by a single
// funding proof. The funding proof's signature is not a valid mint signature;
// `validate_payment` verifies the SENDER's signature over the commitment derived
// from the proofs, which is what this test is about, and deliberately does not
// re-check the mint's DLEQ proof (that is `fund_channel`'s job).
func newFixtureChannel(t *testing.T, capacity uint64) fixtureChannel {
	t.Helper()
	return newFixtureChannelWith(t, capacity, 2)
}

// newFixtureChannelWith builds a channel whose receiver secret is receiverKey, so
// two channels can be given different channel secrets. The channel secret is the
// ECDH of sender and receiver keys, and it is part of the commitment the sender
// signs, which is what makes one channel's signature worthless on another.
func newFixtureChannelWith(t *testing.T, capacity uint64, receiverKey uint64) fixtureChannel {
	t.Helper()

	// secp256k1 key 1 for the payer: fixed, well-known.
	senderSecret := "0000000000000000000000000000000000000000000000000000000000000001"
	senderPubkey, err := SecretKeyToPubkey(senderSecret)
	require.NoError(t, err)
	receiverSecret := fmt.Sprintf("%064x", receiverKey)
	receiverPubkey, err := SecretKeyToPubkey(receiverSecret)
	require.NoError(t, err)

	channelSecret, err := ComputeChannelSecret(senderSecret, receiverPubkey)
	require.NoError(t, err)

	// A binary keyset covering capacity exactly, so a single funding proof of
	// `capacity` is spendable and no fee rounding is involved.
	keys := map[string]string{}
	for amount := uint64(1); amount <= capacity; amount <<= 1 {
		secret := fmt.Sprintf("%064x", amount+1000)
		pubkey, err := SecretKeyToPubkey(secret)
		require.NoError(t, err)
		keys[fmt.Sprint(amount)] = pubkey
	}
	keysJSON, err := json.Marshal(keys)
	require.NoError(t, err)
	keysetJSON := fmt.Sprintf(
		`{"keysetId":"009a1f293253e41e","unit":"sat","inputFeePpk":0,"keys":%s}`, keysJSON)

	paramsJSON := fmt.Sprintf(
		`{"mint":"https://mint.example","unit":"sat","capacity":%d,"funding_token_amount":%d,`+
			`"maximum_amount_for_one_output":%d,"setup_timestamp":1700000000,`+
			`"expiry_timestamp":1900000000,"sender_pubkey":%q,"receiver_pubkey":%q,`+
			`"keyset_id":"009a1f293253e41e","input_fee_ppk":0}`,
		capacity, capacity, capacity, senderPubkey, receiverPubkey)

	channelID, err := ChannelIDFromParams(paramsJSON, channelSecret, keysetJSON)
	require.NoError(t, err)
	require.Len(t, channelID, 64)

	proofsJSON := fmt.Sprintf(
		`[{"amount":%d,"id":"009a1f293253e41e","secret":"fixture-secret","C":%q}]`,
		capacity, senderPubkey)

	return fixtureChannel{
		Funding: FundingRecord{
			ChannelID:         channelID,
			ParamsJSON:        paramsJSON,
			FundingProofsJSON: proofsJSON,
			ChannelSecretHex:  channelSecret,
			KeysetInfoJSON:    keysetJSON,
		},
		ChannelID:     channelID,
		SenderSecret:  senderSecret,
		SenderPubkey:  senderPubkey,
		Capacity:      capacity,
		proofsJSON:    proofsJSON,
		paramsJSON:    paramsJSON,
		keysetJSON:    keysetJSON,
		channelSecret: channelSecret,
	}
}
