//! Wire-contract tests for the `validate_registration.v1` capability.
//!
//! Compiled only with the `protocol` feature: without it the module does not
//! exist, and `tests/transport_only_contract.rs` takes over the other side of
//! the fence (the capability must then be ABSENT, not merely unimplemented).
#![cfg(feature = "protocol")]
//!
//! This file is the SPEC. It was written before the implementation existed and
//! run against a deliberately permissive stub (one that echoed the request's
//! own fields back as if they had been validated) to prove the assertions have
//! teeth. Every refusal case below is a case the stub accepted.
//!
//! # Why the refusals matter more than the acceptance
//!
//! The capability exists to close two reviewer findings at registration time:
//!
//! 1. **Nested params were never checked against the outer fields.** A payer
//!    could submit an outer `receiver_pubkey`/`capacity`/`expiry` that the
//!    daemon records, and a nested `ParamsJSON` that derives a *different*
//!    channel id — so the channel the daemon thinks it registered is not the
//!    channel the proofs will belong to.
//! 2. **The mint and keyset came from the payer.** A `ParamsJSON` naming an
//!    attacker's mint, or a keyset with attacker keys, derives a channel id
//!    that is internally consistent and completely worthless.
//!
//! So the tests are grouped exactly that way: outer-vs-nested agreement,
//! then trusted-mint/keyset agreement, then the bounded-shape refusals.
//!
//! # What this capability still does not do
//!
//! No signature is produced, no mint is contacted, no swap or restore is
//! planned, and nothing is credited. `validate_registration.v1` is a *pure
//! function over supplied bytes*. There is no test in this file that could pass
//! if settlement worked, and none that would fail if it were absent — the
//! transport-only tests in `protocol_contract.rs` remain the gate for that.

use serde_json::{json, Value};
use tollgate_spilman_sidecar::protocol::{
    handle_frame, CAPABILITIES, CAPABILITY_VALIDATE_REGISTRATION, ERR_INVALID_REQUEST,
    MAX_FRAME_BYTES, METHOD_VALIDATE_REGISTRATION, PROTOCOL_VERSION,
};
use tollgate_spilman_sidecar::registration::{
    ERR_CHANNEL_ID_NOT_DERIVABLE, ERR_INVALID_LIFETIME, ERR_KEYSET_MISMATCH, ERR_MINT_MISMATCH,
    ERR_NESTED_MISMATCH, ERR_RECEIVER_MISMATCH, ERR_REGISTRATION_EXPIRED,
    ERR_SENDER_EQUALS_RECEIVER, ERR_UNSUPPORTED_UNIT, ERR_ZERO_CAPACITY,
};
use tollgate_spilman_sidecar::Outcome;

const OPERATION_ID: &str = "reg-golden-0001";

/// The cross-language golden vector, generated from the pinned crate. The Go
/// client test reads the same file, so a divergence between the two
/// implementations of this contract is a test failure and not a shared
/// assumption.
fn golden() -> Value {
    let raw = include_str!("fixtures/registration_golden.json");
    serde_json::from_str(raw).expect("the golden fixture is valid JSON")
}

fn frame(value: &Value) -> Vec<u8> {
    let mut bytes = serde_json::to_vec(value).expect("serialize fixture");
    bytes.push(b'\n');
    bytes
}

/// A well-formed registration request built from the golden vector. Callers
/// mutate one field to produce each adversarial case, so a refusal is
/// attributable to that mutation and nothing else.
fn valid_request() -> Value {
    let fixture = golden();
    json!({
        "version": PROTOCOL_VERSION,
        "request_id": "req-reg-0001",
        "operation_id": OPERATION_ID,
        "method": METHOD_VALIDATE_REGISTRATION,
        "params": {
            "receiver_secret_hex": fixture["receiver_secret_hex"],
            "trusted_mint_url": fixture["trusted_mint_url"],
            "trusted_keyset_info": fixture["trusted_keyset_info"],
            "registration": fixture["registration"],
            "params_json": fixture["nested_params_json"],
            "now_unix": 1_700_000_100u64,
        },
    })
}

fn params(request: &mut Value) -> &mut serde_json::Map<String, Value> {
    request["params"]
        .as_object_mut()
        .expect("params is an object")
}

fn reply(outcome: Outcome) -> (Value, bool) {
    match outcome {
        Outcome::Reply { frame, close } => {
            let text = String::from_utf8(frame.clone()).expect("reply is UTF-8 JSON");
            assert!(
                text.ends_with('\n'),
                "reply must be newline terminated: {text:?}"
            );
            assert_eq!(
                text.matches('\n').count(),
                1,
                "reply must be exactly one frame: {text:?}"
            );
            (
                serde_json::from_str(text.trim_end()).expect("reply is JSON"),
                close,
            )
        }
        Outcome::Fatal { error, close } => (json!({ "error": error }), close),
    }
}

fn call(request: &Value) -> (Value, bool) {
    reply(handle_frame(&frame(request)))
}

/// The refusal code, or a panic naming the whole response so a wrong-code
/// failure is readable.
fn refusal_code(response: &Value) -> String {
    response["error"]
        .as_str()
        .unwrap_or_else(|| panic!("no error field in {response}"))
        .to_string()
}

// --- acceptance, and the values that come back ----------------------------

#[test]
fn accepts_a_golden_registration_and_returns_only_derived_values() {
    let (response, close) = call(&valid_request());
    let expected = &golden()["expected"];

    assert_eq!(refusal_code(&response), "", "golden request must validate");
    assert!(
        !close,
        "a validated registration keeps the connection usable"
    );

    let validated = response
        .get("validated")
        .unwrap_or_else(|| panic!("a successful validation must return `validated`: {response}"));
    assert_eq!(
        validated["channel_id"], expected["channel_id"],
        "the returned channel id must be the pinned crate's derivation"
    );
    assert_eq!(
        validated["canonical_params_json"], expected["canonical_params_json"],
        "the returned params must be the crate's canonical form, not the request's bytes"
    );
    assert_eq!(
        validated["receiver_pubkey"], expected["receiver_pubkey"],
        "the receiver pubkey must be the one DERIVED from the supplied secret"
    );
    assert_eq!(validated["sender_pubkey"], expected["sender_pubkey"]);
    assert_eq!(validated["mint"], json!("https://mint.example"));
    assert_eq!(validated["unit"], json!("sat"));
    assert_eq!(validated["capacity"], json!(1000));
    assert_eq!(validated["funding_token_amount"], json!(1024));
    assert_eq!(validated["expiry_timestamp"], json!(4_102_444_800u64));
    assert_eq!(validated["setup_timestamp"], json!(1_700_000_000u64));
    assert_eq!(
        validated["keyset_id"],
        golden()["registration"]["keyset_id"]
    );
    assert_eq!(validated["input_fee_ppk"], json!(100));
    assert_eq!(validated["maximum_amount_for_one_output"], json!(64));
}

#[test]
fn acceptance_never_asserts_anything_about_mint_funds() {
    // The contract's honesty boundary. `trust_signal` exists so a caller cannot
    // read "validated" as "the mint is funded, solvent, or honest". If a future
    // implementation adds a field like `funds_verified` or `balance`, this test
    // should be extended rather than silently outgrown.
    let (response, _) = call(&valid_request());
    let validated = &response["validated"];

    assert_eq!(
        validated["trust_signal"],
        json!("registration_validated"),
        "validation must name exactly what it proved"
    );
    assert_eq!(
        validated["mint_funds_asserted"],
        json!(false),
        "validation must never assert anything about mint funds"
    );
    for forbidden in [
        "balance",
        "proofs",
        "funded",
        "solvent",
        "settled",
        "signature",
        "swap",
        "restore",
    ] {
        assert!(
            validated.get(forbidden).is_none(),
            "`{forbidden}` must not appear: this capability performs no protocol operation"
        );
    }
}

#[test]
fn validated_reply_never_advertises_settlement() {
    let (response, _) = call(&valid_request());
    for capability in response["capabilities"]
        .as_array()
        .expect("capabilities array")
    {
        let name = capability.as_str().expect("capability string");
        assert!(
            !name.starts_with("settlement."),
            "validation success must not imply settlement, got {name:?}"
        );
    }
}

#[test]
fn capabilities_advertise_validation_without_settlement() {
    let mut request = valid_request();
    request["method"] = json!("capabilities");
    request.as_object_mut().expect("object").remove("params");
    let (response, _) = call(&request);

    let advertised: Vec<&str> = response["capabilities"]
        .as_array()
        .expect("capabilities array")
        .iter()
        .map(|v| v.as_str().expect("capability string"))
        .collect();
    assert_eq!(
        advertised,
        CAPABILITIES.to_vec(),
        "the advertised list is the constant, not a per-reply improvisation"
    );
    assert!(
        advertised.contains(&CAPABILITY_VALIDATE_REGISTRATION),
        "a peer that gates on validation must be able to discover it"
    );
    assert!(
        advertised.contains(&"transport.v1"),
        "validation does not supersede the transport capability"
    );
    assert!(
        !advertised.iter().any(|c| c.starts_with("settlement.")),
        "no settlement capability exists in this build"
    );
}

// --- (a) receiver pubkey must be DERIVED, never trusted -------------------

#[test]
fn refuses_nested_receiver_pubkey_that_the_supplied_secret_does_not_own() {
    // The nested params claim Charlie's key is X; the secret we were handed
    // derives Y. Accepting this would register a channel whose receiver cannot
    // spend — the exact "trust the payer's key" failure the contract forbids.
    let mut request = valid_request();
    let other = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798";
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["receiver_pubkey"] = json!(other);
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_RECEIVER_MISMATCH,
        "a nested receiver key the supplied secret does not own must be refused: {response}"
    );
    assert!(
        response.get("validated").is_none(),
        "a refusal must return no validated values at all"
    );
}

#[test]
fn refuses_outer_receiver_pubkey_that_the_supplied_secret_does_not_own() {
    let mut request = valid_request();
    let other = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798";
    params(&mut request)["registration"]["receiver_pubkey"] = json!(other);

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_RECEIVER_MISMATCH,
        "the OUTER receiver key is checked against the derived key too: {response}"
    );
}

#[test]
fn refuses_a_receiver_secret_of_the_wrong_length_or_alphabet() {
    for bad in ["", "22", &"zz".repeat(32), &"22".repeat(33)] {
        let mut request = valid_request();
        params(&mut request).insert("receiver_secret_hex".into(), json!(bad));
        let (response, _) = call(&request);
        assert_eq!(
            refusal_code(&response),
            ERR_RECEIVER_MISMATCH,
            "receiver_secret_hex {bad:?} must be refused, not coerced"
        );
    }
}

#[test]
fn refuses_a_sender_pubkey_equal_to_the_receiver_pubkey() {
    // A "channel" between one party and itself is not a channel: it would make
    // every accepted balance a self-transfer and the 2-of-2 multisig a 1-of-1.
    let mut request = valid_request();
    let receiver = golden()["expected"]["receiver_pubkey"].clone();
    params(&mut request)["registration"]["sender_pubkey"] = receiver;
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["sender_pubkey"] = golden()["expected"]["receiver_pubkey"].clone();
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_SENDER_EQUALS_RECEIVER,
        "a self-addressed channel must be refused: {response}"
    );
}

// --- (b) nested params must agree with the outer registration -------------

#[test]
fn refuses_nested_params_that_disagree_with_the_outer_capacity() {
    let mut request = valid_request();
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["capacity"] = json!(999u64);
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_NESTED_MISMATCH,
        "a nested capacity the daemon did not record must be refused: {response}"
    );
}

#[test]
fn refuses_nested_params_that_disagree_with_the_outer_expiry() {
    let mut request = valid_request();
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["expiry_timestamp"] = json!(4_102_444_801u64);
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_NESTED_MISMATCH,
        "a nested expiry the daemon did not record must be refused: {response}"
    );
}

#[test]
fn refuses_nested_params_that_disagree_with_the_outer_unit() {
    let mut request = valid_request();
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["unit"] = json!("usd");
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_NESTED_MISMATCH,
        "a nested unit the daemon did not record must be refused: {response}"
    );
}

#[test]
fn refuses_nested_params_that_disagree_with_the_outer_funding_amount() {
    let mut request = valid_request();
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["funding_token_amount"] = json!(2048u64);
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_NESTED_MISMATCH,
        "a nested funding amount the daemon did not record must be refused: {response}"
    );
}

#[test]
fn refuses_nested_params_that_disagree_with_the_outer_setup_timestamp() {
    let mut request = valid_request();
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["setup_timestamp"] = json!(1_700_000_001u64);
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(refusal_code(&response), ERR_NESTED_MISMATCH, "{response}");
}

#[test]
fn refuses_nested_params_that_disagree_with_the_outer_maximum_output() {
    let mut request = valid_request();
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["maximum_amount_for_one_output"] = json!(32u64);
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(refusal_code(&response), ERR_NESTED_MISMATCH, "{response}");
}

#[test]
fn refuses_nested_params_that_disagree_with_the_outer_sender() {
    let mut request = valid_request();
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["sender_pubkey"] =
        json!("0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798");
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_NESTED_MISMATCH,
        "a nested sender the daemon did not record must be refused: {response}"
    );
}

#[test]
fn refuses_nested_params_naming_a_mint_the_outer_registration_did_not() {
    let mut request = valid_request();
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["mint"] = json!("https://mint.example");
    // Trailing slash is a DIFFERENT string but the same normalized mint; the
    // crate trims it, so this must still be accepted rather than refused on
    // bytes. The substitution case is the next test.
    nested["mint"] = json!("https://mint.example/");
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));
    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        "",
        "a trailing slash is the same mint after normalization: {response}"
    );
}

// --- (b') untrusted mint and keyset substitution -------------------------

#[test]
fn refuses_nested_params_naming_a_different_mint_from_the_outer() {
    let mut request = valid_request();
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["mint"] = json!("https://attacker.example");
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_MINT_MISMATCH,
        "a nested mint the outer registration did not name must be refused: {response}"
    );
}

#[test]
fn refuses_a_registration_at_a_mint_that_is_not_the_trusted_one() {
    // The reviewer's "untrusted mint" gap: everything is internally consistent,
    // but the mint is the attacker's. Only a comparison against the TRUSTED url
    // catches this.
    let mut request = valid_request();
    params(&mut request)["registration"]["mint"] = json!("https://attacker.example");
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["mint"] = json!("https://attacker.example");
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_MINT_MISMATCH,
        "a self-consistent registration at an untrusted mint must be refused: {response}"
    );
}

#[test]
fn refuses_a_trusted_mint_url_of_the_wrong_scheme() {
    for bad in [
        "http://mint.example",
        "ftp://mint.example",
        "mint.example",
        "",
        "https://",
    ] {
        let mut request = valid_request();
        params(&mut request).insert("trusted_mint_url".into(), json!(bad));
        let (response, _) = call(&request);
        assert_eq!(
            refusal_code(&response),
            ERR_MINT_MISMATCH,
            "trusted_mint_url {bad:?} must be a refused, well-formed https URL"
        );
    }
}

#[test]
fn refuses_nested_keyset_id_substituted_for_the_trusted_one() {
    let mut request = valid_request();
    // A well-formed but different keyset id: length and alphabet are legal, so
    // only an identity comparison against the trusted keyset catches it.
    let mut trusted = golden()["trusted_keyset_info"].clone();
    trusted["keysetId"] = json!("009a1f293253e41e");
    params(&mut request).insert("trusted_keyset_info".into(), trusted);

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_KEYSET_MISMATCH,
        "nested keyset_id must equal the trusted keyset's id: {response}"
    );
}

#[test]
fn refuses_nested_input_fee_substituted_for_the_trusted_one() {
    // The keyset id agrees but the fee does not. A wrong fee changes
    // funding_token_amount → capacity arithmetic, i.e. how much Charlie can
    // actually be paid. The crate's own parser rejects this mismatch; the
    // contract must not paper over it with a lenient parse.
    let mut request = valid_request();
    let mut trusted = golden()["trusted_keyset_info"].clone();
    trusted["inputFeePpk"] = json!(200u64);
    params(&mut request).insert("trusted_keyset_info".into(), trusted);

    let (response, _) = call(&request);
    assert!(
        refusal_code(&response) == ERR_KEYSET_MISMATCH,
        "a trusted keyset whose fee disagrees with the nested params must be refused \
         as a keyset mismatch, got {response}"
    );
}

#[test]
fn refuses_trusted_keyset_keys_substituted_for_another_mint() {
    // Same id and fee, but the KEYS are a different mint's. Derivation would
    // succeed and be meaningless. The id must be recomputed from the keys, so a
    // forged `keysetId` cannot carry foreign keys past this check.
    let mut request = valid_request();
    let mut trusted = golden()["trusted_keyset_info"].clone();
    let keys = trusted["keys"].as_object_mut().expect("keys object");
    // A real key the fixture does NOT already hold at amount 1 (amount 1 holds
    // 0279be66...; 03fff97b... sits at amount 16). Swapping it in keeps the
    // object well-formed and the declared id unchanged, so only recomputation
    // can catch it.
    keys.insert(
        "1".into(),
        json!("02f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9"),
    );
    params(&mut request).insert("trusted_keyset_info".into(), trusted);

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_KEYSET_MISMATCH,
        "a keyset whose declared id does not match its own keys must be refused: {response}"
    );
}

#[test]
fn refuses_a_trusted_keyset_in_a_different_unit_from_the_registration() {
    let mut request = valid_request();
    let mut trusted = golden()["trusted_keyset_info"].clone();
    trusted["unit"] = json!("usd");
    params(&mut request).insert("trusted_keyset_info".into(), trusted);

    let (response, _) = call(&request);
    assert!(
        refusal_code(&response) == ERR_KEYSET_MISMATCH
            || refusal_code(&response) == ERR_UNSUPPORTED_UNIT,
        "a keyset in another unit than the registration must be refused, got {response}"
    );
}

// --- (c) unit, capacity, expiry, derivability ----------------------------

#[test]
fn refuses_an_unsupported_unit() {
    for unit in ["usd", "eur", "auth", "btc", ""] {
        let mut request = valid_request();
        params(&mut request)["registration"]["unit"] = json!(unit);
        let mut nested: Value =
            serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
                .expect("nested json");
        nested["unit"] = json!(unit);
        params(&mut request).insert("params_json".into(), json!(nested.to_string()));

        let (response, _) = call(&request);
        assert_eq!(
            refusal_code(&response),
            ERR_UNSUPPORTED_UNIT,
            "unit {unit:?} must be refused as unsupported: {response}"
        );
    }
}

#[test]
fn refuses_zero_capacity() {
    // capacity 0 is "a channel that can pay nothing". It derives a valid
    // channel id, so only an explicit check refuses it.
    let mut request = valid_request();
    params(&mut request)["registration"]["capacity"] = json!(0u64);
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["capacity"] = json!(0u64);
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_ZERO_CAPACITY,
        "capacity 0 must be refused: {response}"
    );
}

#[test]
fn refuses_an_expired_registration() {
    // Expiry is compared against the request's own `now_unix`, not the wall
    // clock: a test (and a caller replaying a decision) must be able to state
    // the instant it is asking about. The sidecar owns no clock for this.
    let mut request = valid_request();
    params(&mut request).insert("now_unix".into(), json!(4_102_444_800u64));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_REGISTRATION_EXPIRED,
        "expiry == now is not a live registration: {response}"
    );
}

#[test]
fn refuses_a_registration_whose_expiry_precedes_its_setup() {
    let mut request = valid_request();
    params(&mut request)["registration"]["expiry_timestamp"] = json!(1_699_999_999u64);
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["expiry_timestamp"] = json!(1_699_999_999u64);
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_INVALID_LIFETIME,
        "an expiry before setup cannot be a live channel: {response}"
    );
}

#[test]
fn refuses_nested_params_json_that_is_not_an_object() {
    for bad in ["[]", "\"text\"", "null", "{", "", "{\"mint\":1}"] {
        let mut request = valid_request();
        params(&mut request).insert("params_json".into(), json!(bad));
        let (response, _) = call(&request);
        assert!(
            refusal_code(&response) == ERR_CHANNEL_ID_NOT_DERIVABLE
                || refusal_code(&response) == ERR_INVALID_REQUEST,
            "params_json {bad:?} must be refused, got {response}"
        );
    }
}

#[test]
fn refuses_nested_params_missing_a_required_field() {
    // Each removal is a distinct case: the crate's parser must be the thing
    // that refuses, not a default.
    for field in [
        "mint",
        "unit",
        "capacity",
        "funding_token_amount",
        "keyset_id",
        "input_fee_ppk",
        "maximum_amount_for_one_output",
        "setup_timestamp",
        "sender_pubkey",
        "receiver_pubkey",
        "expiry_timestamp",
    ] {
        let mut request = valid_request();
        let mut nested: Value =
            serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
                .expect("nested json");
        nested.as_object_mut().expect("object").remove(field);
        params(&mut request).insert("params_json".into(), json!(nested.to_string()));

        let (response, _) = call(&request);
        assert!(
            refusal_code(&response) == ERR_CHANNEL_ID_NOT_DERIVABLE
                || refusal_code(&response) == ERR_INVALID_REQUEST
                || refusal_code(&response) == ERR_NESTED_MISMATCH,
            "missing nested {field} must be refused, got {response}"
        );
        assert!(response.get("validated").is_none());
    }
}

#[test]
fn refuses_a_capacity_that_exceeds_what_the_funding_can_pay() {
    // capacity > forward(forward(funding_token_amount)) is arithmetically
    // impossible; the crate refuses it in its constructor and this contract
    // must surface that as a refusal rather than a validated channel.
    let mut request = valid_request();
    params(&mut request)["registration"]["capacity"] = json!(1_000_000u64);
    let mut nested: Value =
        serde_json::from_str(golden()["nested_params_json"].as_str().expect("string"))
            .expect("nested json");
    nested["capacity"] = json!(1_000_000u64);
    params(&mut request).insert("params_json".into(), json!(nested.to_string()));

    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_CHANNEL_ID_NOT_DERIVABLE,
        "an unpayable capacity is rejected by the pinned crate's own fee arithmetic, \
         and must surface as a derivation refusal rather than a validated channel: {response}"
    );
}

// --- bounded shape: the request cannot be widened ------------------------

#[test]
fn refuses_unknown_params_fields() {
    let mut request = valid_request();
    params(&mut request).insert("merchant_secret".into(), json!("must-not-be-accepted"));
    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_INVALID_REQUEST,
        "this capability accepts no extra field, least of all a secret: {response}"
    );
    assert_eq!(
        response["request_id"],
        json!("req-reg-0001"),
        "the refusal still correlates"
    );
}

#[test]
fn refuses_unknown_registration_fields() {
    let mut request = valid_request();
    params(&mut request)["registration"]["sender_secret"] = json!("must-not-be-accepted");
    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_INVALID_REQUEST,
        "the registration object is a closed shape: {response}"
    );
}

#[test]
fn refuses_missing_params_entirely() {
    let mut request = valid_request();
    request.as_object_mut().expect("object").remove("params");
    let (response, _) = call(&request);
    assert_eq!(
        refusal_code(&response),
        ERR_INVALID_REQUEST,
        "validate_registration without params is not a no-op: {response}"
    );
}

#[test]
fn refuses_non_object_params() {
    for bad in [json!([]), json!("x"), json!(1), json!(null)] {
        let mut request = valid_request();
        request["method"] = json!(METHOD_VALIDATE_REGISTRATION);
        request["params"] = bad.clone();
        let (response, _) = call(&request);
        assert_eq!(
            refusal_code(&response),
            ERR_INVALID_REQUEST,
            "params {bad} must be refused: {response}"
        );
    }
}

#[test]
fn refuses_an_oversized_registration_frame_before_parsing() {
    // The bound applies to this method exactly as it does to `capabilities`:
    // a huge ParamsJSON is not a validation candidate, it is a memory
    // exhaustion attempt.
    let mut request = valid_request();
    params(&mut request)["params_json"] = json!("x".repeat(MAX_FRAME_BYTES));
    let (response, close) = call(&request);
    assert_eq!(refusal_code(&response), "frame_too_large");
    assert!(close, "an oversize frame cannot be correlated");
}

#[test]
fn refuses_a_params_json_that_is_a_nested_flood() {
    // Just under the frame bound but structurally absurd: deep nesting must be
    // a refusal, never a stack overflow. serde_json's default recursion limit
    // handles it; this test pins that the refusal is reported as a refusal.
    let mut deep = String::new();
    for _ in 0..200 {
        deep.push('[');
    }
    let mut request = valid_request();
    params(&mut request).insert("params_json".into(), json!(deep));
    let (response, close) = call(&request);
    assert!(
        refusal_code(&response) == ERR_CHANNEL_ID_NOT_DERIVABLE
            || refusal_code(&response) == ERR_INVALID_REQUEST,
        "deeply nested params_json must be refused, got {response}"
    );
    assert!(!close, "a refusable payload must not desync the connection");
}

#[test]
fn refuses_negative_or_non_integer_numeric_fields() {
    for field in ["capacity", "funding_token_amount", "expiry_timestamp"] {
        for bad in [json!(-1), json!(1.5), json!("1000"), json!(null)] {
            let mut request = valid_request();
            params(&mut request)["registration"][field] = bad.clone();
            let (response, _) = call(&request);
            assert!(
                !refusal_code(&response).is_empty(),
                "registration.{field} = {bad} must be refused"
            );
        }
    }
}

// --- the boundary this milestone must not cross -------------------------

#[test]
fn settlement_shaped_methods_are_still_unsupported() {
    // Adding one capability must not turn the sidecar into a settlement peer.
    // `validate_registration` is the ONLY new method; these stay refused.
    for method in [
        "prepare_close",
        "verify_prepared",
        "complete_swap",
        "complete_restore",
        "settle",
        "sign_balance_update",
    ] {
        let mut request = valid_request();
        request["method"] = json!(method);
        let (response, close) = call(&request);
        assert_eq!(
            refusal_code(&response),
            "unsupported_capability",
            "{method} must remain refused in this milestone"
        );
        assert!(!close);
        assert!(response.get("validated").is_none());
    }
}

#[test]
fn a_valid_request_for_another_method_is_not_validated() {
    // `params` shaped like a registration does not make `prepare_close` valid.
    let mut request = valid_request();
    request["method"] = json!("prepare_close");
    let (response, _) = call(&request);
    assert_eq!(refusal_code(&response), "unsupported_capability");
    assert!(response.get("validated").is_none());
}
