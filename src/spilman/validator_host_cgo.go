//go:build spilman

package spilman

// The Go half of the SpilmanHostCallbacks struct that upstream's bridge calls
// back into while it validates a payment.
//
// This is a separate file because cgo requires that a file containing //export
// directives has a preamble with DECLARATIONS ONLY: it is copied into two
// different C translation units (one of which has no access to the other's
// definitions). The struct type and the wrapper that assembles the callbacks
// therefore live in rail_cgo.go, which has no //export.
//
// Every callback here answers from ONE channel's funding record, passed in as
// the cgo.Handle in user_data. There is no shared or process-global state, so
// two concurrent validations cannot see each other's channel.

/*
#include <stdlib.h>
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

// spilman_dup_cstr copies into memory upstream's spilman_free_string can
// release. C.CString buffers come from Go's allocator and must never be handed
// to the Rust free function.
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
*/
import "C"

import (
	"encoding/json"
	"runtime/cgo"
	"unsafe"
)

// validatorHostOf recovers the per-channel host from the cgo handle upstream
// passes back as user_data.
func validatorHostOf(userData unsafe.Pointer) *cgoValidatorHost {
	handle := cgo.Handle(userData)
	host, _ := handle.Value().(*cgoValidatorHost)
	return host
}

// dupCString copies s into memory upstream's spilman_free_string can release,
// and frees the temporary Go-allocated buffer immediately. Upstream frees every
// string it receives with CString::from_raw, which would be undefined behaviour
// on a C.CString buffer, so no callback below may hand one over directly.
func dupCString(s string) *C.char {
	temporary := C.CString(s)
	duplicate := C.spilman_dup_cstr(temporary)
	C.free(unsafe.Pointer(temporary))
	return duplicate
}

//export go_spilman_receiver_key_is_acceptable
func go_spilman_receiver_key_is_acceptable(userData unsafe.Pointer, pubkeyHex *C.char) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	if host.ReceiverKeyIsAcceptable(C.GoString(pubkeyHex)) {
		return 1
	}
	return 0
}

//export go_spilman_mint_and_keyset_is_acceptable
func go_spilman_mint_and_keyset_is_acceptable(userData unsafe.Pointer, mint *C.char, keysetID *C.char) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	if host.MintAndKeysetIsAcceptable(C.GoString(mint), C.GoString(keysetID)) {
		return 1
	}
	return 0
}

// get_funding_and_params is the callback upstream's validate_payment depends on
// for authenticity: returning 0 makes upstream refuse with UnknownChannel, so a
// missing or mismatched funding record can never be treated as verifiable.
//
// The four output strings are malloc'd copies, because upstream frees them with
// CString::from_raw.
//
//export go_spilman_get_funding_and_params
func go_spilman_get_funding_and_params(userData unsafe.Pointer, channelID *C.char, paramsOut, proofsOut, secretOut, keysetOut **C.char) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	params, proofs, secret, keyset, ok := host.GetFundingAndParams(C.GoString(channelID))
	if !ok {
		return 0
	}
	*paramsOut = dupCString(params)
	*proofsOut = dupCString(proofs)
	*secretOut = dupCString(secret)
	*keysetOut = dupCString(keyset)
	return 1
}

//export go_spilman_save_funding
func go_spilman_save_funding(userData unsafe.Pointer, channelID, paramsJSON, proofsJSON, channelSecretHex, keysetInfoJSON *C.char, initialBalance C.uint64_t, initialSignature *C.char) {
	host := validatorHostOf(userData)
	if host == nil {
		return
	}
	host.SaveFunding(C.GoString(channelID), C.GoString(paramsJSON), C.GoString(proofsJSON),
		C.GoString(channelSecretHex), C.GoString(keysetInfoJSON),
		uint64(initialBalance), C.GoString(initialSignature))
}

//export go_spilman_get_amount_due
func go_spilman_get_amount_due(userData unsafe.Pointer, channelID, contextJSON *C.char) C.uint64_t {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	var context *string
	if contextJSON != nil {
		text := C.GoString(contextJSON)
		context = &text
	}
	return C.uint64_t(host.GetAmountDue(C.GoString(channelID), context))
}

//export go_spilman_record_payment
func go_spilman_record_payment(userData unsafe.Pointer, channelID *C.char, balance C.uint64_t, signature, contextJSON *C.char) {
	host := validatorHostOf(userData)
	if host == nil {
		return
	}
	host.RecordPayment(C.GoString(channelID), uint64(balance), C.GoString(signature), C.GoString(contextJSON))
}

// get_channel_state answers the upstream result envelope: {"Ok":null} for an
// unknown channel, {"Ok":"open"} for a known one, {"Err":"..."} on failure. A
// null pointer is a lookup error on the Rust side, never "unknown", so the
// failure case must still return a body.
//
//export go_spilman_get_channel_state
func go_spilman_get_channel_state(userData unsafe.Pointer, channelID *C.char) *C.char {
	host := validatorHostOf(userData)
	if host == nil {
		return dupCString(`{"Err":"validation host missing"}`)
	}
	state, err := host.GetChannelState(C.GoString(channelID))

	var envelope map[string]any
	switch {
	case err != nil:
		envelope = map[string]any{"Err": err.Error()}
	case state == "":
		envelope = map[string]any{"Ok": nil}
	default:
		envelope = map[string]any{"Ok": state}
	}
	data, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		return dupCString(`{"Err":"encode channel state"}`)
	}
	return dupCString(string(data))
}

//export go_spilman_mark_channel_closing
func go_spilman_mark_channel_closing(userData unsafe.Pointer, channelID *C.char, expiryTimestamp, balance C.uint64_t, signature *C.char) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	if err := host.MarkChannelClosing(C.GoString(channelID), uint64(expiryTimestamp), uint64(balance), C.GoString(signature)); err != nil {
		return 0
	}
	return 1
}

//export go_spilman_get_closing_data
func go_spilman_get_closing_data(userData unsafe.Pointer, channelID *C.char, expiryTimestampOut, balanceOut *C.uint64_t, signatureOut **C.char) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	data := host.GetClosingData(C.GoString(channelID))
	if data == nil {
		return 0
	}
	*expiryTimestampOut = C.uint64_t(data.ExpiryTimestamp)
	*balanceOut = C.uint64_t(data.Balance)
	*signatureOut = dupCString(data.Signature)
	return 1
}

//export go_spilman_get_channel_policy
func go_spilman_get_channel_policy(userData unsafe.Pointer, unit *C.char, minExpiryOut, minCapacityOut *C.uint64_t, maxAmountOut *C.int64_t) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	policy := host.GetChannelPolicy(C.GoString(unit))
	if policy == nil {
		return 0
	}
	*minExpiryOut = C.uint64_t(policy.MinExpiryInSeconds)
	*minCapacityOut = C.uint64_t(policy.MinCapacity)
	if policy.MaxAmountPerOutput != nil {
		*maxAmountOut = C.int64_t(*policy.MaxAmountPerOutput)
	} else {
		*maxAmountOut = -1
	}
	return 1
}

//export go_spilman_now_seconds
func go_spilman_now_seconds(userData unsafe.Pointer) C.uint64_t {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	return C.uint64_t(host.NowSeconds())
}

//export go_spilman_get_balance_and_signature_for_unilateral_exit
func go_spilman_get_balance_and_signature_for_unilateral_exit(userData unsafe.Pointer, channelID *C.char, balanceOut *C.uint64_t, signatureOut **C.char) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	balance, signature, ok := host.GetBalanceAndSignatureForUnilateralExit(C.GoString(channelID))
	if !ok {
		return 0
	}
	*balanceOut = C.uint64_t(balance)
	*signatureOut = dupCString(signature)
	return 1
}

//export go_spilman_get_active_keyset_ids
func go_spilman_get_active_keyset_ids(userData unsafe.Pointer, mint, unit *C.char) *C.char {
	host := validatorHostOf(userData)
	if host == nil {
		return dupCString(`[]`)
	}
	ids := host.GetActiveKeysetIds(C.GoString(mint), C.GoString(unit))
	if ids == nil {
		ids = []string{}
	}
	data, err := json.Marshal(ids)
	if err != nil {
		return dupCString(`[]`)
	}
	return dupCString(string(data))
}

//export go_spilman_get_keyset_info
func go_spilman_get_keyset_info(userData unsafe.Pointer, mint, keysetID *C.char) *C.char {
	host := validatorHostOf(userData)
	if host == nil {
		return dupCString("")
	}
	info, ok := host.GetKeysetInfo(C.GoString(mint), C.GoString(keysetID))
	if !ok {
		return dupCString("")
	}
	return dupCString(info)
}

//export go_spilman_call_mint_swap
func go_spilman_call_mint_swap(userData unsafe.Pointer, mintURL, swapRequestJSON *C.char, responseOut **C.char) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		*responseOut = dupCString("validation host missing")
		return 0
	}
	response, err := host.CallMintSwap(C.GoString(mintURL), C.GoString(swapRequestJSON))
	if err != nil {
		*responseOut = dupCString(err.Error())
		return 0
	}
	*responseOut = dupCString(response)
	return 1
}

//export go_spilman_refresh_all_keysets
func go_spilman_refresh_all_keysets(userData unsafe.Pointer, mintURL *C.char) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	if err := host.RefreshAllKeysets(C.GoString(mintURL)); err != nil {
		return 0
	}
	return 1
}

//export go_spilman_compute_channel_secret
func go_spilman_compute_channel_secret(userData unsafe.Pointer, receiverPubkeyHex, senderPubkeyHex *C.char, resultOut **C.char) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	secret, err := host.ComputeChannelSecret(C.GoString(senderPubkeyHex), C.GoString(receiverPubkeyHex))
	if err != nil {
		return 0
	}
	*resultOut = dupCString(secret)
	return 1
}

//export go_spilman_sign_with_tweaked_key
func go_spilman_sign_with_tweaked_key(userData unsafe.Pointer, signerPubkeyHex, messageHex, tweakScalarHex *C.char, resultOut **C.char) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	signature, err := host.SignWithTweakedKey(C.GoString(signerPubkeyHex), C.GoString(messageHex), C.GoString(tweakScalarHex))
	if err != nil {
		return 0
	}
	*resultOut = dupCString(signature)
	return 1
}

//export go_spilman_mark_channel_closed
func go_spilman_mark_channel_closed(userData unsafe.Pointer, channelID *C.char, expiryTimestamp, balance C.uint64_t, receiverProofsJSON, senderProofsJSON *C.char, receiverSum, senderSum C.uint64_t) C.int {
	host := validatorHostOf(userData)
	if host == nil {
		return 0
	}
	if err := host.MarkChannelClosed(C.GoString(channelID), uint64(expiryTimestamp), uint64(balance),
		C.GoString(receiverProofsJSON), C.GoString(senderProofsJSON),
		uint64(receiverSum), uint64(senderSum)); err != nil {
		return 0
	}
	return 1
}
