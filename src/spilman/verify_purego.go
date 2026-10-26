package spilman

// Pure-Go Spilman balance-update verification.
//
// # What this is
//
// The default (CGO_ENABLED=0) build has no Rust core, so it used to be able to
// REFUSE a payment but never ACCEPT one. This file reproduces the verifier in
// pure Go, so a Go payee can authenticate a real payment with nothing but the
// funding record it already persists.
//
// # Fidelity
//
// Every derivation here is transcribed from the reference implementation
// (crates/cdk-spilman). The line references are to the Rust source, not to this
// file:
//
//	channel id            params.rs:562  get_channel_id_bytes
//	stage-1 blinding      params.rs:624  derive_blinding_scalar
//	stage-2 ephemeral     params.rs:661  derive_stage2_p2bk_ephemeral_secret_for_output
//	NUT-28 shared x       params.rs:688  derive_nut28_shared_secret_x
//	NUT-28 P2BK scalar    params.rs:700  derive__nut28_P2KB_shared_secret_scalar
//	blinded pubkey        params.rs:247  derive_blinded_pubkey
//	scalar retry          params.rs:37   hash_to_secp_scalar (incl. the 0xff retry)
//	deterministic output  params.rs:920  create_deterministic_output_with_blinding
//	amount decomposition  keysets_and_amounts.rs:45   from_target (greedy, largest first)
//	after-fee value       keysets_and_amounts.rs:126  value_after_fees
//	after-fee inverse     keysets_and_amounts.rs:196  inverse_deterministic_value_after_fees
//	commitment outputs    deterministic.rs:286        for_balance
//	output order          deterministic.rs:343        create_swap_request (STABLE sort)
//	SIG_ALL message       nut03.rs:99                sig_all_msg_to_sign
//	signature check       nut01/public_key.rs:95      PublicKey::verify  (SHA-256 then BIP-340)
//	stage-1 sender key    params.rs:715                get_sender_blinded_pubkey_for_stage1
//
// Two details are easy to get wrong and are called out where they happen:
//
//  1. `sig_all_msg_to_sign` returns the message, and `PublicKey::verify`
//     SHA-256s it before BIP-340. Hashing twice (or not at all) verifies
//     nothing, so the digest is computed in exactly one place: messageDigest.
//  2. Outputs are ordered by a STABLE sort by amount. Receiver outputs come
//     first, so on an amount tie the receiver's output precedes the sender's.
//     sort.SliceStable is required; sort.Slice would reorder ties and change the
//     signed bytes for any non-trivial balance (balance 63 of 64 has such a tie).
//
// # Cryptographic primitives
//
// Curve arithmetic is github.com/decred/dcrd/dcrec/secp256k1/v4 and BIP-340
// verification is github.com/btcsuite/btcd/btcec/v2/schnorr. Note that
// dcrd's own `schnorr` subpackage is EC-Schnorr-DCRv0 (BLAKE-256, different
// challenge), NOT BIP-340, and would verify nothing here.
//
// SECURITY: this is a verifier, not a signer. It never takes a secret key and
// returns no value that authorises anything by itself; the only success shape is
// a nil error from VerifyBalanceUpdate.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/btcsuite/btcd/btcec/v2/schnorr"
	secp "github.com/decred/dcrd/dcrec/secp256k1/v4"
)

// Verify errors. Each one is a distinct, testable refusal reason: a verifier
// whose failures are all "invalid" cannot be audited.
var (
	// ErrVerifyFundingRecord means the channel's persisted funding record is
	// incomplete or unparsable, so there is nothing to verify against.
	ErrVerifyFundingRecord = errors.New("spilman: funding record unusable for verification")
	// ErrVerifyBalanceRange means the claimed balance exceeds the channel
	// capacity, or no commitment can be built for it.
	ErrVerifyBalanceRange = errors.New("spilman: balance outside channel capacity")
	// ErrVerifySignature means the signature is malformed or does not verify.
	ErrVerifySignature = errors.New("spilman: balance-update signature is not authentic")
	// ErrVerifyChannelID means the channel id re-derived from the funding record
	// does not match the id the payment names. The record and the claim disagree,
	// so neither can be trusted.
	ErrVerifyChannelID = errors.New("spilman: channel id does not match the funding record")
)

// secp256k1 group order, big-endian. A candidate scalar is valid iff it is
// non-zero and strictly below this.
var secpOrder = func() [32]byte {
	// n = FFFFFFFF FFFFFFFF FFFFFFFF FFFFFFFE BAAEDCE6 AF48A03B BFD25E8C D0364141
	raw, err := hex.DecodeString("fffffffffffffffffffffffffffffffebaaedce6af48a03bbfd25e8cd0364141")
	if err != nil || len(raw) != 32 {
		panic("spilman: secp256k1 order constant is malformed")
	}
	var out [32]byte
	copy(out[:], raw)
	return out
}()

// scalarFromBytes is Rust's `Scalar::from_be_bytes(...)`: it succeeds only for a
// canonical, non-zero scalar below the group order. The Rust code additionally
// tests `!= Scalar::ZERO` even though `from_be_bytes` already rejects overflow;
// zero is handled the same way here.
func scalarFromBytes(b [32]byte) (*secp.ModNScalar, bool) {
	var s secp.ModNScalar
	if overflow := s.SetBytes(&b); overflow != 0 {
		return nil, false
	}
	if s.IsZero() {
		return nil, false
	}
	return &s, true
}

// hashToSecpScalar is params.rs:37 hash_to_secp_scalar.
//
// The 0xff retry is part of the specification, not defensive padding: for
// HMAC-SHA256 over a fixed key the first candidate is out of range with
// probability ~2^-128 and zero with probability ~2^-256, but a verifier that
// skipped the retry would silently disagree with the signer on exactly those
// inputs — and a disagreement on any input is a lockout on a genuine payment.
func hashToSecpScalar(input []byte, hash func([]byte) [32]byte) (*secp.ModNScalar, error) {
	if scalar, ok := scalarFromBytes(hash(input)); ok {
		return scalar, nil
	}

	retry := make([]byte, len(input)+1)
	copy(retry, input)
	retry[len(input)] = 0xff
	if scalar, ok := scalarFromBytes(hash(retry)); ok {
		return scalar, nil
	}

	return nil, fmt.Errorf("%w: no valid secp256k1 scalar after 0xff retry", ErrVerifyFundingRecord)
}

func sha256Sum(b []byte) [32]byte { return sha256.Sum256(b) }

func hmacSHA256(key, message []byte) [32]byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(message)
	var out [32]byte
	copy(out[:], mac.Sum(nil))
	return out
}

func hmacScalar(key, message []byte) (*secp.ModNScalar, error) {
	return hashToSecpScalar(message, func(input []byte) [32]byte { return hmacSHA256(key, input) })
}

// ---------------------------------------------------------------------------
// Keyset info
// ---------------------------------------------------------------------------

// verifyKeyset is the subset of KeysetInfo (keysets_and_amounts.rs:158) the
// commitment derivation needs: the denominations, the per-input fee, and the
// keyset id that the outputs carry.
type verifyKeyset struct {
	ID           string
	InputFeePPK  uint64
	AmountsDesc  []uint64 // keysets_and_amounts.rs:189, sorted LARGEST first
	maximumLimit uint64
}

// parseVerifyKeyset accepts the JSON the rail persists (bindings.rs:33
// parse_keyset_info_from_json): `keysetId` or `keyset_id`, `inputFeePpk` or
// `input_fee_ppk`, and a `keys` object keyed by decimal amount. Denominations
// are derived from the key set exactly as KeysetInfo::new does, so a caller
// cannot smuggle in an `amounts` array that disagrees with the keys.
func parseVerifyKeyset(raw string) (verifyKeyset, error) {
	var envelope struct {
		KeysetID     string            `json:"keysetId"`
		KeysetIDSnk  string            `json:"keyset_id"`
		InputFeePPK  *uint64           `json:"inputFeePpk"`
		InputFeeSnk  *uint64           `json:"input_fee_ppk"`
		Unit         string            `json:"unit"`
		Keys         map[string]string `json:"keys"`
		FinalExpiry  *uint64           `json:"finalExpiry"`
		FinalExpiryS *uint64           `json:"final_expiry"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return verifyKeyset{}, fmt.Errorf("%w: parse keyset info: %v", ErrVerifyFundingRecord, err)
	}

	id := envelope.KeysetID
	if id == "" {
		id = envelope.KeysetIDSnk
	}
	if id == "" {
		return verifyKeyset{}, fmt.Errorf("%w: keyset info has no keyset id", ErrVerifyFundingRecord)
	}

	fee := envelope.InputFeePPK
	if fee == nil {
		fee = envelope.InputFeeSnk
	}
	if fee == nil {
		return verifyKeyset{}, fmt.Errorf("%w: keyset info has no input fee", ErrVerifyFundingRecord)
	}

	if len(envelope.Keys) == 0 {
		return verifyKeyset{}, fmt.Errorf("%w: keyset info has no keys", ErrVerifyFundingRecord)
	}
	amounts := make([]uint64, 0, len(envelope.Keys))
	for text := range envelope.Keys {
		var amount uint64
		if _, err := fmt.Sscanf(text, "%d", &amount); err != nil {
			return verifyKeyset{}, fmt.Errorf("%w: keyset amount %q: %v", ErrVerifyFundingRecord, text, err)
		}
		// The denomination's public key must be a real curve point: downstream
		// derivation uses these values, and a malformed one must not survive to
		// produce a subtly different commitment.
		if _, err := parseCompressedPoint(envelope.Keys[text]); err != nil {
			return verifyKeyset{}, fmt.Errorf("%w: keyset key for amount %d: %v", ErrVerifyFundingRecord, amount, err)
		}
		amounts = append(amounts, amount)
	}
	sort.Slice(amounts, func(i, j int) bool { return amounts[i] > amounts[j] })
	if len(amounts) == 0 || amounts[len(amounts)-1] == 0 {
		return verifyKeyset{}, fmt.Errorf("%w: keyset has no usable denomination", ErrVerifyFundingRecord)
	}

	return verifyKeyset{ID: id, InputFeePPK: *fee, AmountsDesc: amounts}, nil
}

// fromTarget is keysets_and_amounts.rs:45 OrderedListOfAmounts::from_target: a
// greedy largest-first decomposition that skips denominations above
// maximumAmount (zero means no limit) and fails when a remainder cannot be
// represented. The count map is what the caller iterates.
func (k verifyKeyset) fromTarget(target, maximumAmount uint64) (map[uint64]int, error) {
	counts := make(map[uint64]int)
	if target == 0 {
		return counts, nil
	}

	remaining := target
	for _, amount := range k.AmountsDesc {
		if maximumAmount > 0 && amount > maximumAmount {
			continue
		}
		if amount == 0 {
			continue
		}
		count := 0
		for remaining >= amount {
			remaining -= amount
			count++
		}
		if count > 0 {
			counts[amount] = count
		}
	}
	if remaining != 0 {
		return nil, fmt.Errorf("%w: cannot represent %d from the keyset (max %d)",
			ErrVerifyBalanceRange, target, maximumAmount)
	}
	return counts, nil
}

// outputCount is OrderedListOfAmounts::len -- the number of outputs, which sets
// the input fee.
func outputCount(counts map[uint64]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

// valueAfterFees is keysets_and_amounts.rs:126: nominal total, minus
// ceil(input_fee_ppk * numOutputs / 1000), saturating at zero.
func (k verifyKeyset) valueAfterFees(counts map[uint64]int) uint64 {
	var nominal uint64
	for amount, count := range counts {
		nominal += amount * uint64(count)
	}
	if k.InputFeePPK == 0 {
		return nominal
	}
	numOutputs := uint64(outputCount(counts))
	fee := (k.InputFeePPK*numOutputs + 999) / 1000
	if fee >= nominal {
		return 0
	}
	return nominal - fee
}

// deterministicValueAfterFees is keysets_and_amounts.rs:184.
func (k verifyKeyset) deterministicValueAfterFees(nominal, maximumAmount uint64) (uint64, error) {
	counts, err := k.fromTarget(nominal, maximumAmount)
	if err != nil {
		return 0, err
	}
	return k.valueAfterFees(counts), nil
}

// inverseDeterministicValueAfterFees is keysets_and_amounts.rs:196. It searches
// upward from the target for the smallest nominal value that yields at least the
// target after fees, which is what makes a zero-fee channel's denominations
// exactly the balance (and a fee-bearing channel's off by a sat or two).
//
// The Rust loop has no upper bound other than convergence. It is bounded here
// only to keep a malformed keyset from spinning forever, and the bound is far
// above any reachable answer because each extra output costs < 1 unit of value.
func (k verifyKeyset) inverseDeterministicValueAfterFees(target, maximumAmount uint64) (uint64, error) {
	if target == 0 {
		return 0, nil
	}

	var nominal uint64
	if k.InputFeePPK == 0 {
		nominal = target
	} else {
		nominal = target
		limit := target + uint64(outputCountMustExist(target))*k.InputFeePPK + 1024
		for {
			actual, err := k.deterministicValueAfterFees(nominal, maximumAmount)
			if err != nil {
				return 0, err
			}
			if actual >= target {
				break
			}
			nominal++
			if nominal > limit {
				return 0, fmt.Errorf("%w: inverse fee for %d did not converge below %d",
					ErrVerifyBalanceRange, target, limit)
			}
		}
	}

	// Even with zero fees the nominal must land on a representable value.
	if _, err := k.fromTarget(nominal, maximumAmount); err != nil {
		return 0, err
	}
	return nominal, nil
}

// outputCountMustExist bounds the inverse search generously: a decomposition of
// `target` uses at most one output per unit when the smallest denomination is 1.
func outputCountMustExist(target uint64) uint64 {
	if target > 1<<20 {
		return 1 << 20
	}
	return target
}

// ---------------------------------------------------------------------------
// Channel parameters and channel id
// ---------------------------------------------------------------------------

// verifyParams is the persisted ChannelParameters JSON (params.rs:124), in the
// production shape that get_channel_id_params_json emits:
//
//	{"mint","unit","capacity","funding_token_amount","keyset_id","input_fee_ppk",
//	 "maximum_amount_for_one_output","setup_timestamp","sender_pubkey",
//	 "receiver_pubkey","expiry_timestamp"}
type verifyParams struct {
	Mint                      string
	Unit                      string
	Capacity                  uint64
	FundingTokenAmount        uint64
	KeysetID                  string
	InputFeePPK               uint64
	MaximumAmountForOneOutput uint64
	SetupTimestamp            uint64
	SenderPubkey              string
	ReceiverPubkey            string
	ExpiryTimestamp           uint64
	ChannelSecret             [32]byte
	keyset                    verifyKeyset
}

// parseVerifyParams mirrors params.rs:449 from_json_with_channel_secret,
// including its consistency checks: the JSON keyset id and input fee must equal
// the ones in the keyset info the caller passed. Those are not cosmetic — the
// channel id hashes the JSON values while the fee maths uses the keyset values,
// so a mismatch would make the id and the commitment describe different channels.
func parseVerifyParams(rawParams, rawKeyset, channelSecretHex string) (verifyParams, error) {
	keyset, err := parseVerifyKeyset(rawKeyset)
	if err != nil {
		return verifyParams{}, err
	}

	secretBytes, err := hex.DecodeString(channelSecretHex)
	if err != nil || len(secretBytes) != 32 {
		return verifyParams{}, fmt.Errorf("%w: channel secret must be 32 bytes of hex", ErrVerifyFundingRecord)
	}
	var secret [32]byte
	copy(secret[:], secretBytes)

	var envelope struct {
		Mint                      string  `json:"mint"`
		Unit                      string  `json:"unit"`
		Capacity                  uint64  `json:"capacity"`
		FundingTokenAmount        uint64  `json:"funding_token_amount"`
		KeysetID                  string  `json:"keyset_id"`
		KeysetIDCamel             string  `json:"keysetId"`
		InputFeePPK               *uint64 `json:"input_fee_ppk"`
		InputFeePPKCamel          *uint64 `json:"inputFeePpk"`
		MaximumAmountForOneOutput *uint64 `json:"maximum_amount_for_one_output"`
		SetupTimestamp            *uint64 `json:"setup_timestamp"`
		SenderPubkey              string  `json:"sender_pubkey"`
		ReceiverPubkey            string  `json:"receiver_pubkey"`
		ExpiryTimestamp           *uint64 `json:"expiry_timestamp"`
	}
	if err := json.Unmarshal([]byte(rawParams), &envelope); err != nil {
		return verifyParams{}, fmt.Errorf("%w: parse params: %v", ErrVerifyFundingRecord, err)
	}

	keysetID := envelope.KeysetID
	if keysetID == "" {
		keysetID = envelope.KeysetIDCamel
	}
	if keysetID == "" {
		return verifyParams{}, fmt.Errorf("%w: params have no keyset id", ErrVerifyFundingRecord)
	}
	fee := envelope.InputFeePPK
	if fee == nil {
		fee = envelope.InputFeePPKCamel
	}
	if fee == nil {
		return verifyParams{}, fmt.Errorf("%w: params have no input fee", ErrVerifyFundingRecord)
	}
	if envelope.MaximumAmountForOneOutput == nil {
		return verifyParams{}, fmt.Errorf("%w: params have no maximum_amount_for_one_output", ErrVerifyFundingRecord)
	}
	if envelope.SetupTimestamp == nil {
		return verifyParams{}, fmt.Errorf("%w: params have no setup_timestamp", ErrVerifyFundingRecord)
	}
	if envelope.ExpiryTimestamp == nil {
		return verifyParams{}, fmt.Errorf("%w: params have no expiry_timestamp", ErrVerifyFundingRecord)
	}
	if envelope.Mint == "" {
		return verifyParams{}, fmt.Errorf("%w: params have no mint", ErrVerifyFundingRecord)
	}
	if envelope.Unit == "" {
		return verifyParams{}, fmt.Errorf("%w: params have no unit", ErrVerifyFundingRecord)
	}

	// params.rs:472-485: the two sources of keyset identity must agree.
	if keysetID != keyset.ID {
		return verifyParams{}, fmt.Errorf("%w: params keyset_id %q != keyset info %q",
			ErrVerifyFundingRecord, keysetID, keyset.ID)
	}
	if *fee != keyset.InputFeePPK {
		return verifyParams{}, fmt.Errorf("%w: params input_fee_ppk %d != keyset info %d",
			ErrVerifyFundingRecord, *fee, keyset.InputFeePPK)
	}

	// params.rs:488-535: the party pubkeys must parse as curve points. The
	// channel id hashes their hex, but every blinding derivation multiplies them,
	// so a non-point has to fail here rather than three derivations later.
	if _, err := parseCompressedPoint(envelope.SenderPubkey); err != nil {
		return verifyParams{}, fmt.Errorf("%w: sender_pubkey: %v", ErrVerifyFundingRecord, err)
	}
	if _, err := parseCompressedPoint(envelope.ReceiverPubkey); err != nil {
		return verifyParams{}, fmt.Errorf("%w: receiver_pubkey: %v", ErrVerifyFundingRecord, err)
	}

	keyset.maximumLimit = *envelope.MaximumAmountForOneOutput

	return verifyParams{
		Mint:                      envelope.Mint,
		Unit:                      envelope.Unit,
		Capacity:                  envelope.Capacity,
		FundingTokenAmount:        envelope.FundingTokenAmount,
		KeysetID:                  keysetID,
		InputFeePPK:               *fee,
		MaximumAmountForOneOutput: *envelope.MaximumAmountForOneOutput,
		SetupTimestamp:            *envelope.SetupTimestamp,
		SenderPubkey:              envelope.SenderPubkey,
		ReceiverPubkey:            envelope.ReceiverPubkey,
		ExpiryTimestamp:           *envelope.ExpiryTimestamp,
		ChannelSecret:             secret,
		keyset:                    keyset,
	}, nil
}

// channelIDPreimage is the exact UTF-8 string params.rs:562 hashes. It is
// exposed so a test can diff it byte-for-byte against the Rust fixture rather
// than only comparing the hash.
//
// Two Rust details matter: `new` trims trailing '/' from the mint (params.rs:311)
// and rejects a mint or unit containing '|'; `unit_name()` is the Display form,
// which lowercases for the known units. Both are reproduced, but the mint is
// taken as already-normalised because the JSON we hash is the value that was
// persisted after normalisation.
func (p verifyParams) channelIDPreimage() string {
	return fmt.Sprintf("%s|%s|%d|%d|%s|%d|%d|%d|%s|%s|%d|%s",
		p.Mint,
		p.Unit,
		p.Capacity,
		p.FundingTokenAmount,
		p.KeysetID,
		p.InputFeePPK,
		p.MaximumAmountForOneOutput,
		p.SetupTimestamp,
		p.SenderPubkey,
		p.ReceiverPubkey,
		p.ExpiryTimestamp,
		hex.EncodeToString(p.ChannelSecret[:]),
	)
}

// channelID is params.rs:562 get_channel_id.
func (p verifyParams) channelID() string {
	sum := sha256Sum([]byte(p.channelIDPreimage()))
	return hex.EncodeToString(sum[:])
}

// valueAfterStage1 is params.rs:1032: forward-apply the input fee to the funding
// token amount, i.e. what the commitment outputs have to distribute.
func (p verifyParams) valueAfterStage1() (uint64, error) {
	return p.keyset.deterministicValueAfterFees(p.FundingTokenAmount, p.MaximumAmountForOneOutput)
}

// ---------------------------------------------------------------------------
// Key derivation
// ---------------------------------------------------------------------------

// Derivation domain separators. They are part of the wire format: changing one
// byte changes every blinded message and therefore every signature.
const (
	stage1TweakPrefix     = "Cashu_Spilman_stage1_key_tweak_v1"
	stage2EphemeralPrefix = "Cashu_Spilman_P2BK_ephemeral_v1"
	nut28P2BKPrefix       = "Cashu_P2BK_v1"
	stage2SenderContext   = "sender_stage2"
	stage2ReceiverContext = "receiver_stage2"
	// stage1SenderContext is the stage-1 context used for the key that verifies
	// balance updates (params.rs:715, "sender_stage1").
	stage1SenderContext = "sender_stage1"
	// stage1ReceiverContext is the receiver's stage-1 context. The verifier never
	// uses it — its whole point is that the receiver's key must NOT authenticate a
	// balance — but the tamper table needs it to build the strongest form of the
	// wrong-signer case: a genuine signature by the receiver's blinded key.
	stage1ReceiverContext = "receiver_stage1"
)

// stage1BlindingScalar is params.rs:624 derive_blinding_scalar:
//
//	HMAC-SHA256(channel_secret, "Cashu_Spilman_stage1_key_tweak_v1" || "{id}|{ctx}")
//	-> hash_to_secp_scalar
func (p verifyParams) stage1BlindingScalar(context string) (*secp.ModNScalar, error) {
	message := append([]byte(stage1TweakPrefix), []byte(p.channelID()+"|"+context)...)
	return hmacScalar(p.ChannelSecret[:], message)
}

// deriveBlindedPubkey is params.rs:247: P' = (+/-P) + r*G, where P is negated
// when its Y parity is odd. The parity handling is what makes P' the public key
// of the *blinded secret key* the signer actually uses, and skipping it (the
// "BIP-340 parity trap") yields a point that is a valid curve point and simply
// never verifies.
func deriveBlindedPubkey(pubkey string, tweak *secp.ModNScalar) (*secp.PublicKey, error) {
	point, err := parseCompressedPoint(pubkey)
	if err != nil {
		return nil, err
	}

	// A compressed key's prefix is 0x02 for even Y and 0x03 for odd Y, which is
	// exactly the parity `x_only_public_key()` reports in the reference.
	var effective secp.JacobianPoint
	point.AsJacobian(&effective)
	raw, err := hex.DecodeString(pubkey)
	if err != nil {
		return nil, fmt.Errorf("%w: pubkey hex: %v", ErrVerifyFundingRecord, err)
	}
	if raw[0] == 0x03 {
		effective.Y.Negate(1)
		effective.Y.Normalize()
	}

	var tweakPoint, sum secp.JacobianPoint
	secp.ScalarBaseMultNonConst(tweak, &tweakPoint)
	secp.AddNonConst(&effective, &tweakPoint, &sum)
	sum.ToAffine()
	if (sum.X.IsZero() && sum.Y.IsZero()) || sum.Z.IsZero() {
		return nil, fmt.Errorf("%w: blinded pubkey is the point at infinity", ErrVerifyFundingRecord)
	}
	return secp.NewPublicKey(&sum.X, &sum.Y), nil
}

// stage2TweakScalar is params.rs:630 stage2_tweak_info_for_role, reduced to the
// one scalar it produces:
//
//	e        = hash_to_secp_scalar(HMAC-SHA256(secret, "P2BK_ephemeral_v1" || "{id}|{ctx}|{amount}|{index}"))
//	E        = e*G
//	shared   = role_pubkey * e                       (params.rs:688)
//	zx       = x(shared)                             (x-only, 32 bytes)
//	tweak    = hash_to_secp_scalar(SHA256("Cashu_P2BK_v1" || zx || 0x00))
//
// The 0x00 is the NUT-28 index byte for the first (and here only) tweak.
func (p verifyParams) stage2TweakScalar(rolePubkey, context string, amount uint64, index int) (*secp.ModNScalar, error) {
	message := []byte(fmt.Sprintf("%s%s|%s|%d|%d", stage2EphemeralPrefix, p.channelID(), context, amount, index))
	ephemeral, err := hmacScalar(p.ChannelSecret[:], message)
	if err != nil {
		return nil, err
	}

	rolePoint, err := parseCompressedPoint(rolePubkey)
	if err != nil {
		return nil, err
	}
	var roleJacobian, shared secp.JacobianPoint
	rolePoint.AsJacobian(&roleJacobian)
	secp.ScalarMultNonConst(ephemeral, &roleJacobian, &shared)
	shared.ToAffine()
	if (shared.X.IsZero() && shared.Y.IsZero()) || shared.Z.IsZero() {
		return nil, fmt.Errorf("%w: ephemeral shared point is the point at infinity", ErrVerifyFundingRecord)
	}

	zx := shared.X.Bytes()
	p2bkInput := make([]byte, 0, len(nut28P2BKPrefix)+32+1)
	p2bkInput = append(p2bkInput, []byte(nut28P2BKPrefix)...)
	p2bkInput = append(p2bkInput, zx[:]...)
	p2bkInput = append(p2bkInput, 0x00)

	return hashToSecpScalar(p2bkInput, sha256Sum)
}

// senderBlindedPubkeyForStage1 is params.rs:715
// get_sender_blinded_pubkey_for_stage1: the key every balance update must verify
// under. NOTE: stage 1 (shared blinding), NOT the stage-2 per-output keys the
// commitment outputs are locked to.
func (p verifyParams) senderBlindedPubkeyForStage1() (*secp.PublicKey, error) {
	tweak, err := p.stage1BlindingScalar(stage1SenderContext)
	if err != nil {
		return nil, err
	}
	return deriveBlindedPubkey(p.SenderPubkey, tweak)
}

// ---------------------------------------------------------------------------
// Commitment outputs
// ---------------------------------------------------------------------------

// commitmentOutput is one blinded message the sender signs over.
type commitmentOutput struct {
	Owner       string // "receiver" or "sender"
	Amount      uint64
	Index       int
	Secret      string
	Blinded     string // compressed hex, lowercase
	BlindingHex string
}

// hashToCurve is cdk dhke.rs:43: the Cashu NUMS point for a message.
//
//	SHA-256("Secp256k1_HashToCurve_Cashu_" || msg), then for counter 0..2^16:
//	  candidate = SHA-256(that_digest || counter_le_u32)
//	  lift candidate as an x-only coordinate with EVEN Y; first success wins
//
// The even-Y choice is not a coin flip: `blind_message` produces B = Y + r*G and
// the mint later computes C_ - r*K from the same Y, so both sides must agree on
// the lift. dcrd has no x-only lift, so the y = sqrt(x^3 + 7) root with even
// parity is computed here.
func hashToCurve(message []byte) (*secp.PublicKey, error) {
	digest := sha256Sum(append([]byte("Secp256k1_HashToCurve_Cashu_"), message...))

	var counterBytes [4]byte
	for counter := uint32(0); counter < 1<<16; counter++ {
		counterBytes[0] = byte(counter)
		counterBytes[1] = byte(counter >> 8)
		counterBytes[2] = byte(counter >> 16)
		counterBytes[3] = byte(counter >> 24)

		attemptInput := make([]byte, 0, 36)
		attemptInput = append(attemptInput, digest[:]...)
		attemptInput = append(attemptInput, counterBytes[:]...)
		candidate := sha256Sum(attemptInput)

		if point, ok := liftX(&candidate); ok {
			return point, nil
		}
	}
	return nil, fmt.Errorf("%w: no valid curve point for a blinded message", ErrVerifyFundingRecord)
}

// liftX turns a 32-byte candidate into a curve point with even Y, or reports
// that the x has no square root (the case the counter loop skips).
func liftX(xBytes *[32]byte) (*secp.PublicKey, bool) {
	var x, y, seven, rhs secp.FieldVal
	if overflow := x.SetBytes(xBytes); overflow != 0 {
		return nil, false
	}
	x.Normalize()

	// rhs = x^3 + 7
	rhs.SquareVal(&x).Mul(&x)
	seven.SetInt(7)
	rhs.Add(&seven)

	if !y.SquareRootVal(&rhs) {
		return nil, false
	}
	y.Normalize()
	if y.IsOdd() {
		y.Negate(1)
		y.Normalize()
	}
	return secp.NewPublicKey(&x, &y), true
}

// blindMessage is cdk dhke.rs:87 blind_message(secret, r) with an explicit
// blinding factor: B = hash_to_curve(secret) + r*G. It is the same shape as
// deriveBlindedPubkey (add a tweak to a point) but without the parity negation,
// because Y is already guaranteed even.
func blindMessage(secret string, blinding *secp.ModNScalar) (*secp.PublicKey, error) {
	base, err := hashToCurve([]byte(secret))
	if err != nil {
		return nil, err
	}

	var baseJacobian, tweakPoint, sum secp.JacobianPoint
	base.AsJacobian(&baseJacobian)
	secp.ScalarBaseMultNonConst(blinding, &tweakPoint)
	secp.AddNonConst(&baseJacobian, &tweakPoint, &sum)
	sum.ToAffine()
	if (sum.X.IsZero() && sum.Y.IsZero()) || sum.Z.IsZero() {
		return nil, fmt.Errorf("%w: blinded message is the point at infinity", ErrVerifyFundingRecord)
	}
	return secp.NewPublicKey(&sum.X, &sum.Y), nil
}

// deterministicOutput builds one commitment output exactly as params.rs:920
// create_deterministic_output_with_blinding does for a "sender"/"receiver"
// context:
//
//	nonce    = hex(hash_to_secp_scalar(HMAC(secret, "{id}|{ctx}|{amount}|nonce|{index}")))
//	blinding = hash_to_secp_scalar(HMAC(secret, "{id}|{ctx}|{amount}|blinding|{index}"))
//	pubkey   = derive_blinded_pubkey(role_pubkey, stage2_tweak_scalar(...))
//	secret   = ["P2PK",{"data":"<pubkey>","nonce":"<nonce>","tags":[]}]
//	blinded  = blind_message(sha256-free secret string, blinding)
//
// The secret string's exact bytes are load-bearing: they are hashed to a curve
// point, so a single byte of formatting drift changes B_ and the signature stops
// verifying. The format is the raw-string literal from deterministic.rs:63
// (new_p2pk), reproduced character for character.
func (p verifyParams) deterministicOutput(owner string, amount uint64, index int) (commitmentOutput, error) {
	var context, stage2Context, rolePubkey string
	switch owner {
	case "receiver":
		context, stage2Context, rolePubkey = "receiver", stage2ReceiverContext, p.ReceiverPubkey
	case "sender":
		context, stage2Context, rolePubkey = "sender", stage2SenderContext, p.SenderPubkey
	default:
		return commitmentOutput{}, fmt.Errorf("%w: unknown output owner %q", ErrVerifyFundingRecord, owner)
	}

	channelID := p.channelID()

	// The nonce and blinding factor use the PLAIN context ("receiver"/"sender"),
	// because create_deterministic_output_with_blinding receives that string and
	// interpolates it directly (params.rs:940/950). Only the stage-2 ephemeral
	// secret switches to the "stage2" context, via Stage2Role::stage2_context
	// inside get_stage2_blinded_pubkey_for_stage1_output. Using the stage-2
	// string here instead produces a perfectly valid, completely different output.
	nonceMessage := []byte(fmt.Sprintf("%s|%s|%d|nonce|%d", channelID, context, amount, index))
	nonce, err := hmacScalar(p.ChannelSecret[:], nonceMessage)
	if err != nil {
		return commitmentOutput{}, err
	}
	nonceBytes := nonce.Bytes()

	blindingMessage := []byte(fmt.Sprintf("%s|%s|%d|blinding|%d", channelID, context, amount, index))
	blinding, err := hmacScalar(p.ChannelSecret[:], blindingMessage)
	if err != nil {
		return commitmentOutput{}, err
	}

	tweak, err := p.stage2TweakScalar(rolePubkey, stage2Context, amount, index)
	if err != nil {
		return commitmentOutput{}, err
	}
	pubkey, err := deriveBlindedPubkey(rolePubkey, tweak)
	if err != nil {
		return commitmentOutput{}, err
	}
	pubkeyHex := hex.EncodeToString(pubkey.SerializeCompressed())

	secret := fmt.Sprintf(`["P2PK",{"data":"%s","nonce":"%s","tags":[]}]`,
		pubkeyHex, hex.EncodeToString(nonceBytes[:]))

	blinded, err := blindMessage(secret, blinding)
	if err != nil {
		return commitmentOutput{}, err
	}

	blindingBytes := blinding.Bytes()

	return commitmentOutput{
		Owner:       owner,
		Amount:      amount,
		Index:       index,
		Secret:      secret,
		Blinded:     hex.EncodeToString(blinded.SerializeCompressed()),
		BlindingHex: hex.EncodeToString(blindingBytes[:]),
	}, nil
}

// outputsForContext is deterministic.rs:352 get_secrets_with_blinding: iterate
// the decomposition SMALLEST-first, and within one denomination the outputs are
// indexed 0..count-1. That iteration order is the order the receiver's outputs
// precede the sender's before the final sort.
func (p verifyParams) outputsForContext(owner string, target uint64) ([]commitmentOutput, error) {
	counts, err := p.keyset.fromTarget(target, p.MaximumAmountForOneOutput)
	if err != nil {
		return nil, err
	}

	amountsAscending := make([]uint64, 0, len(counts))
	for amount := range counts {
		amountsAscending = append(amountsAscending, amount)
	}
	sort.Slice(amountsAscending, func(i, j int) bool { return amountsAscending[i] < amountsAscending[j] })

	var outputs []commitmentOutput
	for _, amount := range amountsAscending {
		for index := 0; index < counts[amount]; index++ {
			output, err := p.deterministicOutput(owner, amount, index)
			if err != nil {
				return nil, err
			}
			outputs = append(outputs, output)
		}
	}
	return outputs, nil
}

// commitmentForBalance is deterministic.rs:286 CommitmentOutputs::for_balance:
//
//	receiver_nominal = inverse_deterministic_value_after_fees(balance, max)
//	sender_nominal   = value_after_stage1 - receiver_nominal
//
// and then the swap request's outputs (deterministic.rs:343 create_swap_request):
// receiver outputs, then sender outputs, then a STABLE sort by amount.
func (p verifyParams) commitmentForBalance(balance uint64) ([]commitmentOutput, error) {
	if balance > p.Capacity {
		return nil, fmt.Errorf("%w: %d > capacity %d", ErrVerifyBalanceRange, balance, p.Capacity)
	}

	amountAfterStage1, err := p.valueAfterStage1()
	if err != nil {
		return nil, err
	}
	receiverNominal, err := p.keyset.inverseDeterministicValueAfterFees(balance, p.MaximumAmountForOneOutput)
	if err != nil {
		return nil, err
	}
	if receiverNominal > amountAfterStage1 {
		return nil, fmt.Errorf("%w: balance %d needs nominal %d but only %d is available after stage-1 fees",
			ErrVerifyBalanceRange, balance, receiverNominal, amountAfterStage1)
	}
	senderNominal := amountAfterStage1 - receiverNominal

	receiverOutputs, err := p.outputsForContext("receiver", receiverNominal)
	if err != nil {
		return nil, err
	}
	senderOutputs, err := p.outputsForContext("sender", senderNominal)
	if err != nil {
		return nil, err
	}

	outputs := make([]commitmentOutput, 0, len(receiverOutputs)+len(senderOutputs))
	outputs = append(outputs, receiverOutputs...)
	outputs = append(outputs, senderOutputs...)

	// STABLE, and it must stay stable: on an amount tie the receiver's output
	// must precede the sender's, which is the order the signer saw.
	sort.SliceStable(outputs, func(i, j int) bool { return outputs[i].Amount < outputs[j].Amount })
	return outputs, nil
}

// ---------------------------------------------------------------------------
// The signed message
// ---------------------------------------------------------------------------

// signedMessage is nut03.rs:99 sig_all_msg_to_sign over the funding proofs as
// inputs and the commitment outputs as outputs:
//
//	for each input:  secret || hex(C)
//	for each output: decimal(amount) || hex(B_)
//
// All hex is lowercase with no 0x, and the amount is Rust's `{}` for u64, i.e.
// plain decimal — both reproduced by encoding/hex and %d.
//
// The funding proofs must be in STORED order. EstablishedChannel keeps
// `funding_proofs` as deserialised from the persisted JSON and SIG_ALL iterates
// that slice, so reordering the array (or re-sorting it) changes the message.
func signedMessage(proofs []fundingProof, outputs []commitmentOutput) string {
	var builder []byte
	for _, proof := range proofs {
		builder = append(builder, []byte(proof.Secret)...)
		builder = append(builder, []byte(lowerHex(proof.Signature))...)
	}
	for _, output := range outputs {
		builder = append(builder, []byte(fmt.Sprintf("%d", output.Amount))...)
		builder = append(builder, []byte(output.Blinded)...)
	}
	return string(builder)
}

// lowerHex normalises a hex string to lowercase without changing its bytes.
// Rust's `to_hex()` always emits lowercase; a persisted proof with uppercase
// hex would otherwise hash differently, so normalising here is a fidelity fix,
// not a convenience.
func lowerHex(s string) string {
	raw, err := hex.DecodeString(s)
	if err != nil {
		// Not hex at all: return as-is and let the point parse reject it, so the
		// refusal names the real problem.
		return s
	}
	return hex.EncodeToString(raw)
}

// messageDigest is the single, canonical place the signature digest is computed:
// nut01/public_key.rs:95 hashes the message with SHA-256 and BIP-340-verifies
// the result. The signer (nut01/secret_key.rs:88) hashes the same string, so one
// hash on each side is correct and two would verify nothing.
func messageDigest(message string) [32]byte {
	return sha256Sum([]byte(message))
}

// verifyBIP340 checks a 64-byte Schnorr signature under the x-only projection of
// pubkey. btcec's verifier does the full BIP-340 lift/challenge/e/s*G - e*P
// sequence; an x-only key with odd Y is the same key here, which is why the
// reference passes `x_only_public_key().0` too.
func verifyBIP340(pubkey *secp.PublicKey, digest [32]byte, signature string) error {
	raw, err := hex.DecodeString(signature)
	if err != nil {
		return fmt.Errorf("%w: signature is not hex", ErrVerifySignature)
	}
	if len(raw) != 64 {
		return fmt.Errorf("%w: signature is %d bytes, want 64", ErrVerifySignature, len(raw))
	}
	parsed, err := schnorr.ParseSignature(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrVerifySignature, err)
	}

	compressed := pubkey.SerializeCompressed()
	if len(compressed) != 33 {
		return fmt.Errorf("%w: pubkey is not a compressed point", ErrVerifySignature)
	}
	xOnly, err := schnorr.ParsePubKey(compressed[1:])
	if err != nil {
		return fmt.Errorf("%w: x-only pubkey: %v", ErrVerifySignature, err)
	}
	if !parsed.Verify(digest[:], xOnly) {
		return fmt.Errorf("%w: BIP-340 verification failed", ErrVerifySignature)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Public surface
// ---------------------------------------------------------------------------

// BalanceUpdateMessageDigest returns the exact 32-byte digest the sender's
// signature must cover, for a claimed balance. It exists so a test (or an
// operator debugging a rejection) can compare the Go derivation against the
// reference implementation's `sig_all_msg_to_sign` byte-for-byte instead of only
// observing that verification failed.
//
// It returns the message string as well, because "the hash disagrees" and "the
// message disagrees" are different bugs.
func BalanceUpdateMessageDigest(funding FundingRecord, balance uint64) (message string, digest [32]byte, err error) {
	params, proofs, err := verifyInputsFromFunding(funding)
	if err != nil {
		return "", [32]byte{}, err
	}
	outputs, err := params.commitmentForBalance(balance)
	if err != nil {
		return "", [32]byte{}, err
	}
	message = signedMessage(proofs, outputs)
	return message, messageDigest(message), nil
}

// VerifyBalanceUpdate is the pure-Go Spilman balance-update verifier.
//
// It returns nil ONLY when `signature` is a valid BIP-340 Schnorr signature by
// the channel's stage-1 blinded sender key over the commitment that pays
// `balance` to the receiver. Every other outcome is a non-nil error.
//
// What it checks, in order (the same order as bridge.rs:1763 verify_signature):
//
//  1. the funding record is complete and parses;
//  2. the channel id re-derived from the record matches the id the caller
//     presents — a record and a claimed id that disagree are unverifiable;
//  3. (caller's concern, before this call) the balance is within capacity;
//     here, a commitment exists for that balance;
//  4. the signature verifies under the derived sender blinded pubkey.
//
// What it deliberately does NOT check: the mint's signatures on the funding
// proofs (that is the funding swap's job, and the Go host has already done it
// when it registered the channel), the expiry timestamp, or the receiver's
// amount due. Those are the host's byte accounting, not this crypto.
func VerifyBalanceUpdate(funding FundingRecord, balance uint64, signature string) error {
	if !funding.Complete() {
		return fmt.Errorf("%w: channel %q has an incomplete funding record",
			ErrVerifyFundingRecord, funding.ChannelID)
	}

	params, proofs, err := verifyInputsFromFunding(funding)
	if err != nil {
		return err
	}

	// The record must describe the channel the payment names. Without this, a
	// payee that looked a record up by a caller-supplied id would verify a
	// signature over a different channel's commitment.
	if derived := params.channelID(); derived != funding.ChannelID {
		return fmt.Errorf("%w: derived %s, record says %s",
			ErrVerifyChannelID, derived, funding.ChannelID)
	}

	outputs, err := params.commitmentForBalance(balance)
	if err != nil {
		return err
	}

	digest := messageDigest(signedMessage(proofs, outputs))

	pubkey, err := params.senderBlindedPubkeyForStage1()
	if err != nil {
		return err
	}
	return verifyBIP340(pubkey, digest, signature)
}

// BalanceUpdateSignatureCovers is the honest-signature shape of the question
// "would this signature be valid for a DIFFERENT balance?". It returns the
// balances in [0, capacity] for which `signature` verifies.
//
// It is a diagnostic, not a security control: a real payee calls
// VerifyBalanceUpdate once for the balance it was offered. The bound is the
// channel capacity, so it is only affordable on a channel small enough to
// enumerate — a test or a forensic tool.
func BalanceUpdateSignatureCovers(funding FundingRecord, signature string, limit uint64) ([]uint64, error) {
	if !funding.Complete() {
		return nil, fmt.Errorf("%w: channel %q has an incomplete funding record",
			ErrVerifyFundingRecord, funding.ChannelID)
	}
	params, _, err := verifyInputsFromFunding(funding)
	if err != nil {
		return nil, err
	}
	if limit > params.Capacity {
		limit = params.Capacity
	}
	var covers []uint64
	for balance := uint64(0); balance <= limit; balance++ {
		if err := VerifyBalanceUpdate(funding, balance, signature); err == nil {
			covers = append(covers, balance)
		}
	}
	return covers, nil
}

// verifyInputsFromFunding parses the funding record into the two things every
// derivation needs and, on the way, applies the structural checks that make a
// later failure meaningful:
//
//   - the funding proofs' total must equal funding_token_amount and every proof
//     must carry the channel's keyset id (established_channel.rs:196
//     EstablishedChannel::new). A mismatched total means the record is not this
//     channel's funding, and the commitment would be signed over the wrong stake.
//   - every proof's C must be a curve point, because SIG_ALL hashes its hex and
//     a malformed C must not silently hash to something that still verifies.
func verifyInputsFromFunding(funding FundingRecord) (verifyParams, []fundingProof, error) {
	params, err := parseVerifyParams(funding.ParamsJSON, funding.KeysetInfoJSON, funding.ChannelSecretHex)
	if err != nil {
		return verifyParams{}, nil, err
	}

	var proofs []fundingProof
	if err := json.Unmarshal([]byte(funding.FundingProofsJSON), &proofs); err != nil {
		return verifyParams{}, nil, fmt.Errorf("%w: parse funding proofs: %v", ErrVerifyFundingRecord, err)
	}
	if len(proofs) == 0 {
		return verifyParams{}, nil, fmt.Errorf("%w: channel %q has no funding proofs",
			ErrVerifyFundingRecord, funding.ChannelID)
	}

	var total uint64
	for i, proof := range proofs {
		if proof.KeysetID != params.KeysetID {
			return verifyParams{}, nil, fmt.Errorf(
				"%w: funding proof %d has keyset %q, channel expects %q",
				ErrVerifyFundingRecord, i, proof.KeysetID, params.KeysetID)
		}
		if _, err := parseCompressedPoint(proof.Signature); err != nil {
			return verifyParams{}, nil, fmt.Errorf("%w: funding proof %d C: %v",
				ErrVerifyFundingRecord, i, err)
		}
		total += proof.Amount
	}
	if total != params.FundingTokenAmount {
		return verifyParams{}, nil, fmt.Errorf(
			"%w: funding proofs total %d but funding_token_amount is %d",
			ErrVerifyFundingRecord, total, params.FundingTokenAmount)
	}

	return params, proofs, nil
}

// ---------------------------------------------------------------------------
// Point parsing
// ---------------------------------------------------------------------------

// parseCompressedPoint accepts a 33-byte compressed SEC1 point, strictly
// lowercase-or-uppercase hex with no prefix. Both the reference (`PublicKey::
// from_hex`, which requires exactly 33 bytes) and this function reject an
// x-only key, an uncompressed key, and the point at infinity.
func parseCompressedPoint(text string) (*secp.PublicKey, error) {
	raw, err := hex.DecodeString(text)
	if err != nil {
		return nil, fmt.Errorf("%w: pubkey is not hex", ErrVerifyFundingRecord)
	}
	if len(raw) != 33 {
		return nil, fmt.Errorf("%w: pubkey is %d bytes, want 33", ErrVerifyFundingRecord, len(raw))
	}
	if raw[0] != 0x02 && raw[0] != 0x03 {
		return nil, fmt.Errorf("%w: pubkey prefix 0x%02x is not a compressed point", ErrVerifyFundingRecord, raw[0])
	}
	point, err := secp.ParsePubKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrVerifyFundingRecord, err)
	}
	return point, nil
}
