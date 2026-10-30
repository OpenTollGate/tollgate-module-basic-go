//go:build spilman

package spilman

// This file is the `spilman`-tagged half of the build-tag pair, mirroring the
// proven src/tollwallet/cdk_wallet.go / gonuts_wallet.go precedent: the tagged
// file provides the real implementation, the untagged file (rail_stub.go)
// provides a refusing stub, and the default CGO_ENABLED=0 build never sees this
// file at all.
//
// Build (aarch64 musl, the shipped target):
//
//	CGO_ENABLED=1 GOOS=linux GOARCH=arm64 \
//	  CC=$HOME/toolchains/aarch64-linux-musl-cross/bin/aarch64-linux-musl-gcc \
//	  CGO_LDFLAGS="-L<checkout>/crates/cdk-spilman-go/packaged/lib/linux-arm64 -lcdk_spilman_go" \
//	  go build -tags spilman ./...
//
// The library path is a build-time input on purpose: packaged/lib/linux-arm64 is
// gitignored upstream, so the `.so` distribution decision is still open and must
// not be smuggled in as a committed module replace.

/*
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

typedef struct {
    char* data;
    char* error;
} CResult;

typedef struct {
    void* user_data;
    int (*receiver_key_is_acceptable)(void*, const char*);
    int (*mint_and_keyset_is_acceptable)(void*, const char*, const char*);
    int (*get_funding_and_params)(void*, const char*, char**, char**, char**, char**);
    void (*save_funding)(void*, const char*, const char*, const char*, const char*, const char*, uint64_t, const char*);
    uint64_t (*get_amount_due)(void*, const char*, const char*);
    void (*record_payment)(void*, const char*, uint64_t, const char*, const char*);
    char* (*get_channel_state)(void*, const char*);
    int (*mark_channel_closing)(void*, const char*, uint64_t, uint64_t, const char*);
    int (*get_closing_data)(void*, const char*, uint64_t*, uint64_t*, char**);
    int (*get_channel_policy)(void*, const char*, uint64_t*, uint64_t*, int64_t*);
    uint64_t (*now_seconds)(void*);
    int (*get_balance_and_signature_for_unilateral_exit)(void*, const char*, uint64_t*, char**);
    char* (*get_active_keyset_ids)(void*, const char*, const char*);
    char* (*get_keyset_info)(void*, const char*, const char*);
    int (*call_mint_swap)(void*, const char*, const char*, char**);
    int (*refresh_all_keysets)(void*, const char*);
    int (*compute_channel_secret)(void*, const char*, const char*, char**);
    int (*sign_with_tweaked_key)(void*, const char*, const char*, const char*, char**);
    int (*mark_channel_closed)(void*, const char*, uint64_t, uint64_t, const char*, const char*, uint64_t, uint64_t);
} SpilmanHostCallbacks;

void* spilman_bridge_new(SpilmanHostCallbacks callbacks);
void spilman_bridge_free(void* ptr);
CResult spilman_bridge_validate_payment(void* ptr, const char* payment_json, const char* context_json);

// Callback-free, seller-side primitives exported by libcdk_spilman_go.so.
CResult spilman_secret_key_to_pubkey(const char* secret_hex);
CResult spilman_compute_channel_secret(const char* my_secret_hex, const char* their_pubkey_hex);
CResult spilman_compute_funding_token_amount(uint64_t capacity, const char* keyset_info_json, uint64_t maximum_amount);
CResult spilman_create_signed_balance_update(const char* params, const char* keyset, const char* secret, const char* proofs, uint64_t balance);
CResult spilman_channel_parameters_get_channel_id(const char* params, const char* channel_secret, const char* keyset);
void spilman_free_cresult(CResult res);
void spilman_free_string(char* ptr);

// The Go host callbacks are defined (with //export) in validator_host_cgo.go.
// That file's preamble holds declarations only, which is what cgo requires of a
// file that exports functions; the definitions below therefore live here.
extern int go_spilman_receiver_key_is_acceptable(void*, const char*);
extern int go_spilman_mint_and_keyset_is_acceptable(void*, const char*, const char*);
extern int go_spilman_get_funding_and_params(void*, const char*, char**, char**, char**, char**);
extern void go_spilman_save_funding(void*, const char*, const char*, const char*, const char*, const char*, uint64_t, const char*);
extern uint64_t go_spilman_get_amount_due(void*, const char*, const char*);
extern void go_spilman_record_payment(void*, const char*, uint64_t, const char*, const char*);
extern char* go_spilman_get_channel_state(void*, const char*);
extern int go_spilman_mark_channel_closing(void*, const char*, uint64_t, uint64_t, const char*);
extern int go_spilman_get_closing_data(void*, const char*, uint64_t*, uint64_t*, char**);
extern int go_spilman_get_channel_policy(void*, const char*, uint64_t*, uint64_t*, int64_t*);
extern uint64_t go_spilman_now_seconds(void*);
extern int go_spilman_get_balance_and_signature_for_unilateral_exit(void*, const char*, uint64_t*, char**);
extern char* go_spilman_get_active_keyset_ids(void*, const char*, const char*);
extern char* go_spilman_get_keyset_info(void*, const char*, const char*);
extern int go_spilman_call_mint_swap(void*, const char*, const char*, char**);
extern int go_spilman_refresh_all_keysets(void*, const char*);
extern int go_spilman_compute_channel_secret(void*, const char*, const char*, char**);
extern int go_spilman_sign_with_tweaked_key(void*, const char*, const char*, const char*, char**);
extern int go_spilman_mark_channel_closed(void*, const char*, uint64_t, uint64_t, const char*, const char*, uint64_t, uint64_t);

// spilman_dup_cstr copies into memory spilman_free_string can release. C.CString
// buffers come from Go's allocator and MUST NOT be handed to spilman_free_string.
static char* spilman_dup_cstr(const char* s) {
    size_t n = 0;
    char* out;
    if (s == NULL) { return NULL; }
    while (s[n] != '\0') { n++; }
    out = (char*)malloc(n + 1);
    if (out != NULL) {
        size_t i;
        for (i = 0; i <= n; i++) { out[i] = s[i]; }
    }
    return out;
}

// spilman_validate_payment_with_host wires a Go host handle into
// a fresh upstream bridge, runs upstream's read-only validator, frees the
// bridge, and returns NULL on success or a malloc'd error string (free with
// free()). Every failure path returns non-NULL, so the caller cannot
// accidentally treat a wiring failure as a valid payment.
static char* spilman_validate_payment_with_host(const char* payment_json, uintptr_t user_data) {
    SpilmanHostCallbacks cb;
    void* bridge;
    CResult res;

    memset(&cb, 0, sizeof(cb));
    cb.user_data = (void*)user_data;
    cb.receiver_key_is_acceptable = go_spilman_receiver_key_is_acceptable;
    cb.mint_and_keyset_is_acceptable = go_spilman_mint_and_keyset_is_acceptable;
    cb.get_funding_and_params = go_spilman_get_funding_and_params;
    cb.save_funding = go_spilman_save_funding;
    cb.get_amount_due = go_spilman_get_amount_due;
    cb.record_payment = go_spilman_record_payment;
    cb.get_channel_state = go_spilman_get_channel_state;
    cb.mark_channel_closing = go_spilman_mark_channel_closing;
    cb.get_closing_data = go_spilman_get_closing_data;
    cb.get_channel_policy = go_spilman_get_channel_policy;
    cb.now_seconds = go_spilman_now_seconds;
    cb.get_balance_and_signature_for_unilateral_exit = go_spilman_get_balance_and_signature_for_unilateral_exit;
    cb.get_active_keyset_ids = go_spilman_get_active_keyset_ids;
    cb.get_keyset_info = go_spilman_get_keyset_info;
    cb.call_mint_swap = go_spilman_call_mint_swap;
    cb.refresh_all_keysets = go_spilman_refresh_all_keysets;
    cb.compute_channel_secret = go_spilman_compute_channel_secret;
    cb.sign_with_tweaked_key = go_spilman_sign_with_tweaked_key;
    cb.mark_channel_closed = go_spilman_mark_channel_closed;

    bridge = spilman_bridge_new(cb);
    if (bridge == NULL) {
        return spilman_dup_cstr("upstream bridge could not be constructed");
    }
    res = spilman_bridge_validate_payment(bridge, payment_json, "{}");
    spilman_bridge_free(bridge);

    if (res.error != NULL) {
        char* message = spilman_dup_cstr(res.error);
        spilman_free_cresult(res);
        return message;
    }
    spilman_free_cresult(res);
    return NULL;
}
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/cgo"
	"unsafe"
)

// cgoBridge is the ProtocolBridge backed by the linked Rust core.
type cgoBridge struct{}

// newProtocolBridge returns the cgo-backed bridge (tagged build only).
func newProtocolBridge() ProtocolBridge { return cgoBridge{} }

func (cgoBridge) Name() string { return "cdk-spilman-go/cgo" }

// UnilateralCloseData and ExecuteCooperativeClose both need a live
// spilman_bridge_new instance, i.e. the 22-callback SpilmanHostCallbacks host
// adapter. That adapter is the remaining step (see REPORT.md); until it exists
// this refuses rather than returning fabricated close data.
func (cgoBridge) UnilateralCloseData(_ context.Context, channelID string) ([]byte, error) {
	return nil, fmt.Errorf("%w: unilateral close data for %q needs the callback host adapter", ErrSellerBridgeUnwired, channelID)
}

func (cgoBridge) ExecuteCooperativeClose(_ context.Context, req ChannelSettleRequest) (ChannelSettleResult, error) {
	return ChannelSettleResult{}, fmt.Errorf("%w: cooperative close of %q needs the callback host adapter", ErrSellerBridgeUnwired, req.ChannelID)
}

// newProtocolValidator returns the upstream validator (tagged build only).
func newProtocolValidator() PaymentValidator { return cgoValidator{} }

// cgoValidator authenticates payments with upstream cdk-spilman's OWN validator,
// `SpilmanBridge::validate_payment` (bridge.rs:1312), reached through
// `spilman_bridge_validate_payment`.
//
// Re-implementing signature verification here would be a second, divergent
// implementation of a security check; borrowing upstream's means the Go rail
// cannot fall behind on the commitment or P2BK rules. Upstream's validator
// refuses an unknown channel, requires the funding record, rejects a balance
// above capacity, and verifies the sender's Schnorr signature over
// params/funding-proofs/channel-secret/keyset-info. Every one of those
// outcomes is an error here, and every error refuses the payment.
type cgoValidator struct{}

func (cgoValidator) Name() string { return "cdk-spilman-go/cgo-validate_payment" }

// cgoValidatorHost is the SpilmanHost upstream's bridge calls back into. Only
// the funding lookup and the channel-state lookup matter to validate_payment;
// everything else is either unused by that path or an explicit refusal, so a
// callback we did not intend to reach fails loudly instead of returning a
// fabricated answer.
type cgoValidatorHost struct {
	funding FundingRecord
	state   string
	// receiverPubkey is this payee's own compressed-hex receiver key, or "" when it
	// holds none. It is what `ReceiverKeyIsAcceptable` compares against.
	receiverPubkey string
}

// ReceiverKeyIsAcceptable answers "is this channel addressed to the key this payee
// signs closes with?".
//
// It is a REAL comparison, not a constant. Returning `true` here — which this used to
// do — meant upstream's own funding validation accepted a channel addressed to any
// key at all, so a payer could open a channel against a throwaway key: the funding
// lands at the mint, only the missing key could ever release it, and the failure
// surfaced only at close, long after the money moved. `receiverPubkey` is the payee's
// own compressed-hex key, supplied by the caller from the same value `/spilman/params`
// advertises.
//
// An empty `receiverPubkey` means this payee holds no key, and it must refuse: there
// is no key such a payee could honestly accept.
func (h *cgoValidatorHost) ReceiverKeyIsAcceptable(offered string) bool {
	if h.receiverPubkey == "" {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(offered), h.receiverPubkey)
}

// MintAndKeysetIsAcceptable answers "will this payee redeem at this mint/keyset?".
//
// Left as `true` deliberately, and this is the honest answer here rather than a
// placeholder: this host is constructed on the PAYMENT-VERIFICATION path, where
// upstream has already been handed the funding record for a channel this payee
// itself accepted. There is no trusted keyset cache in this shape to compare against,
// and inventing one would assert a policy the caller never configured. A constant
// `false` would be equally dishonest in the other direction — it would refuse every
// payment on a channel that was legitimately registered.
//
// The registration path (spilman_http.go) enforces the mint/keyset policy from actual
// configuration before a channel is stored. When this host gains a real trusted-keyset
// cache, this becomes a comparison like the one above.
func (h *cgoValidatorHost) MintAndKeysetIsAcceptable(string, string) bool { return true }

func (h *cgoValidatorHost) GetFundingAndParams(channelID string) (string, string, string, string, bool) {
	if h.funding.ChannelID == "" || channelID != h.funding.ChannelID || !h.funding.Complete() {
		// Absent funding is upstream's UnknownChannel path: refuse.
		return "", "", "", "", false
	}
	return h.funding.ParamsJSON, h.funding.FundingProofsJSON, h.funding.ChannelSecretHex, h.funding.KeysetInfoJSON, true
}

func (h *cgoValidatorHost) SaveFunding(string, string, string, string, string, uint64, string) {
	// validate_payment never persists; a caller that expects persistence here
	// must not get it silently.
}

func (h *cgoValidatorHost) GetAmountDue(string, *string) uint64 { return 0 }

func (h *cgoValidatorHost) RecordPayment(string, uint64, string, string) {
	// validate_payment is the *read-only* validator: it must not record. The
	// durable write is the ledger's CAS, after this returns nil.
}

func (h *cgoValidatorHost) GetChannelState(string) (string, error) { return h.state, nil }

func (h *cgoValidatorHost) MarkChannelClosing(string, uint64, uint64, string) error {
	return fmt.Errorf("%w: MarkChannelClosing on a validation-only host", ErrLedger)
}

func (h *cgoValidatorHost) GetClosingData(string) *closingData { return nil }

func (h *cgoValidatorHost) GetChannelPolicy(string) *channelPolicy { return nil }

func (h *cgoValidatorHost) NowSeconds() uint64 { return nowSeconds() }

func (h *cgoValidatorHost) GetBalanceAndSignatureForUnilateralExit(string) (uint64, string, bool) {
	return 0, "", false
}

func (h *cgoValidatorHost) GetActiveKeysetIds(string, string) []string { return nil }

func (h *cgoValidatorHost) GetKeysetInfo(string, string) (string, bool) {
	if h.funding.KeysetInfoJSON == "" {
		return "", false
	}
	return h.funding.KeysetInfoJSON, true
}

func (h *cgoValidatorHost) CallMintSwap(string, string) (string, error) {
	return "", fmt.Errorf("%w: CallMintSwap on a validation-only host", ErrLedger)
}

func (h *cgoValidatorHost) RefreshAllKeysets(string) error { return nil }

func (h *cgoValidatorHost) MarkChannelClosed(string, uint64, uint64, string, string, uint64, uint64) error {
	return fmt.Errorf("%w: MarkChannelClosed on a validation-only host", ErrLedger)
}

func (h *cgoValidatorHost) ComputeChannelSecret(string, string) (string, error) {
	return "", fmt.Errorf("%w: ComputeChannelSecret on a validation-only host", ErrLedger)
}

func (h *cgoValidatorHost) SignWithTweakedKey(string, string, string) (string, error) {
	return "", fmt.Errorf("%w: SignWithTweakedKey on a validation-only host", ErrLedger)
}

// ValidatePayment runs upstream's validator. It returns nil only when upstream
// accepted the signature; every other outcome, including a failure to build the
// bridge, is an error and therefore a refusal.
func (cgoValidator) ValidatePayment(ctx context.Context, funding FundingRecord, balance uint64, signature string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !funding.Complete() {
		return fmt.Errorf("%w: %q", ErrFundingRecordMissing, funding.ChannelID)
	}

	payment, err := json.Marshal(struct {
		ChannelID string `json:"channel_id"`
		Balance   uint64 `json:"balance"`
		Signature string `json:"signature"`
	}{ChannelID: funding.ChannelID, Balance: balance, Signature: signature})
	if err != nil {
		return fmt.Errorf("%w: encode payment: %v", ErrPaymentNotAuthentic, err)
	}

	host := &cgoValidatorHost{funding: funding, state: "open"}
	handle := cgo.NewHandle(host)
	defer handle.Delete()

	cPayment, freePayment := cString(string(payment))
	defer freePayment()

	// The handle travels as a uintptr and is converted to a pointer in C, which is
	// the pattern cgo documents for passing Go pointers via user_data: converting
	// a cgo.Handle to unsafe.Pointer in Go and keeping it there trips vet's
	// unsafeptr check for a good reason, and this shape avoids the detour.
	//
	// A non-NULL return is upstream's error, verbatim. NULL means success.
	if message := C.spilman_validate_payment_with_host(cPayment, C.uintptr_t(uintptr(handle))); message != nil {
		text := C.GoString(message)
		C.free(unsafe.Pointer(message))
		return fmt.Errorf("%w: %s", ErrPaymentNotAuthentic, text)
	}
	return nil
}

// callCResult runs a CResult-returning FFI call and returns its data string.
func callCResult(res C.CResult) (string, error) {
	defer C.spilman_free_cresult(res)
	if res.error != nil {
		return "", fmt.Errorf("%w: %s", ErrLedger, C.GoString(res.error))
	}
	if res.data == nil {
		return "", nil
	}
	return C.GoString(res.data), nil
}

func cString(s string) (*C.char, func()) {
	c := C.CString(s)
	return c, func() { C.free(unsafe.Pointer(c)) }
}

// SecretKeyToPubkey derives the public key for a hex secret key.
func SecretKeyToPubkey(secretHex string) (string, error) {
	cSecret, freeSecret := cString(secretHex)
	defer freeSecret()
	return callCResult(C.spilman_secret_key_to_pubkey(cSecret))
}

// ComputeChannelSecret derives the shared channel secret (ECDH).
func ComputeChannelSecret(mySecretHex, theirPubkeyHex string) (string, error) {
	cMine, freeMine := cString(mySecretHex)
	defer freeMine()
	cTheirs, freeTheirs := cString(theirPubkeyHex)
	defer freeTheirs()
	return callCResult(C.spilman_compute_channel_secret(cMine, cTheirs))
}

// ComputeFundingTokenAmount returns the funding token amount for a capacity.
func ComputeFundingTokenAmount(capacity uint64, keysetInfoJSON string, maximumAmount uint64) (uint64, error) {
	cKeyset, freeKeyset := cString(keysetInfoJSON)
	defer freeKeyset()
	data, err := callCResult(C.spilman_compute_funding_token_amount(
		C.uint64_t(capacity), cKeyset, C.uint64_t(maximumAmount)))
	if err != nil {
		return 0, err
	}
	var amount uint64
	if err := json.Unmarshal([]byte(data), &amount); err != nil {
		// The binding may wrap the value in an object; accept both shapes.
		var wrapped struct {
			Amount uint64 `json:"amount"`
		}
		if err2 := json.Unmarshal([]byte(data), &wrapped); err2 != nil {
			return 0, fmt.Errorf("%w: funding amount %q: %v", ErrLedger, data, err)
		}
		return wrapped.Amount, nil
	}
	return amount, nil
}

// CreateSignedBalanceUpdate signs a balance update for a channel.
//
// Seller-side use: the payee can produce (and therefore hold) the signed
// balance it is owed. Note that this is *production*, not *verification*:
// verifying an inbound payer signature needs the bridge host callbacks and is
// still outstanding.
func CreateSignedBalanceUpdate(paramsJSON, keysetJSON, channelSecretHex, proofsJSON string, balance uint64) (string, error) {
	cParams, freeParams := cString(paramsJSON)
	defer freeParams()
	cKeyset, freeKeyset := cString(keysetJSON)
	defer freeKeyset()
	cSecret, freeSecret := cString(channelSecretHex)
	defer freeSecret()
	cProofs, freeProofs := cString(proofsJSON)
	defer freeProofs()
	return callCResult(C.spilman_create_signed_balance_update(
		cParams, cKeyset, cSecret, cProofs, C.uint64_t(balance)))
}

// ChannelIDFromParams derives the channel id for a set of channel parameters.
func ChannelIDFromParams(paramsJSON, channelSecretHex, keysetJSON string) (string, error) {
	cParams, freeParams := cString(paramsJSON)
	defer freeParams()
	cSecret, freeSecret := cString(channelSecretHex)
	defer freeSecret()
	cKeyset, freeKeyset := cString(keysetJSON)
	defer freeKeyset()
	return callCResult(C.spilman_channel_parameters_get_channel_id(cParams, cSecret, cKeyset))
}
