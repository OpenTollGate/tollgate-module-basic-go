//! `validate_registration.v1` — pure, offline registration validation.
//!
//! # What this proves, and what it cannot
//!
//! Given (a) an outer registration record, (b) the nested `ParamsJSON` the
//! payer will sign against, (c) a trusted mint URL, (d) a trusted keyset, and
//! (e) the receiver secret the daemon derived from its own merchant identity,
//! this module proves **the five agree and that a channel id follows**.
//!
//! It does NOT prove the mint holds funds, honours its keyset, or will swap
//! anything. `ValidatedRegistration::mint_funds_asserted` is `false` and is
//! serialized so a caller cannot omit the distinction.
//!
//! It performs no I/O of any kind: no socket, no mint, no clock. `now_unix`
//! is supplied by the caller because the sidecar owns no clock — a validation
//! decision must be replayable and attributable to a stated instant, not to
//! whenever this process happened to run.
//!
//! # The two holes this closes
//!
//! 1. **Nested params were never compared to the outer record.** Every field
//!    that contributes to the channel id is required to be byte-equal between
//!    the two, after the crate's own normalization (which trims a mint URL's
//!    trailing slashes). A registration whose outer capacity is 1000 and whose
//!    nested capacity is 999 registers a channel that cannot be closed for the
//!    balance the daemon believes it accepted.
//! 2. **The mint and keyset came from the payer.** `mint` must equal the
//!    trusted URL, `keyset_id` must equal the trusted keyset's id, and that id
//!    is **recomputed from the supplied keys** so a forged `keysetId` cannot
//!    carry a foreign mint's keys past the check.
//!
//! # Key direction
//!
//! The receiver's *public* key is derived FROM the supplied secret
//! ([`SecretKey::public_key`]) and compared to the claimed one. The reverse —
//! manufacturing a private scalar out of a public key — never happens here and
//! must never be added: it is not possible, and a "recovery" that appeared to
//! work would be a backdoor rather than a derivation.
//!
//! # Check order matters
//!
//! Each refusal names *one* cause. The order below is chosen so a failure never
//! masquerades as a different refusal: unit support is decided before the
//! nested-vs-outer comparison (so a wholly unsupported unit reports
//! `unsupported_unit`, not `nested_params_mismatch`), lifetime consistency
//! before expiry (so expiry-before-setup is not reported as merely "expired"),
//! and the trusted-mint/keyset checks before nested agreement (so an untrusted
//! mint is named as such rather than as an internal inconsistency).

use std::str::FromStr;

use cashu::nuts::{Id, PublicKey, SecretKey};
use serde::{Deserialize, Serialize};
use serde_json::Value;

use cdk_spilman::{
    compute_channel_secret, parse_keyset_info_from_json, ChannelParameters, KeysetInfo,
};

/// The claimed receiver key is not the payee's derived v2 receiver key.
pub const ERR_RECEIVER_MISMATCH: &str = "receiver_pubkey_mismatch";
/// A field that contributes to the channel id differs between the outer record
/// and the nested `ParamsJSON`.
pub const ERR_NESTED_MISMATCH: &str = "nested_params_mismatch";
/// The registration names a mint other than the configured trusted mint.
pub const ERR_MINT_MISMATCH: &str = "mint_not_trusted";
/// The keyset is not the trusted one, its id does not match its own keys, or
/// its fee/unit disagrees with the registration.
pub const ERR_KEYSET_MISMATCH: &str = "keyset_mismatch";
/// A unit other than a supported one. Sat is the only unit this build accepts.
pub const ERR_UNSUPPORTED_UNIT: &str = "unsupported_unit";
/// A channel that can pay nothing.
pub const ERR_ZERO_CAPACITY: &str = "zero_capacity";
/// `expiry_timestamp` is not after the caller's stated `now_unix`.
pub const ERR_REGISTRATION_EXPIRED: &str = "registration_expired";
/// `expiry_timestamp` is not after `setup_timestamp`.
pub const ERR_INVALID_LIFETIME: &str = "invalid_lifetime";
/// No channel id follows from these inputs (bad pubkey, unpayable capacity,
/// malformed `ParamsJSON`, or a derivation the pinned crate rejects).
pub const ERR_CHANNEL_ID_NOT_DERIVABLE: &str = "channel_id_not_derivable";
/// Sender and receiver are the same key: not a channel.
pub const ERR_SENDER_EQUALS_RECEIVER: &str = "sender_equals_receiver";

/// The units this build validates. Deliberately one: TollGate prices forwarding
/// in sat, and accepting a second unit here would mean deriving channel ids the
/// daemon cannot account for.
pub const SUPPORTED_UNITS: &[&str] = &["sat"];

/// The exact string a successful validation returns in `trust_signal`. It names
/// the one thing that was checked so it cannot be read as a settlement verdict.
pub const TRUST_SIGNAL_REGISTRATION_VALIDATED: &str = "registration_validated";

/// The outer registration record — what the daemon would persist.
///
/// `deny_unknown_fields` on purpose: a field this build does not know is a
/// caller on a different protocol revision, and silently ignoring it would let
/// a future required field be absent while validation still reports success.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Registration {
    pub sender_pubkey: String,
    pub receiver_pubkey: String,
    pub mint: String,
    pub unit: String,
    pub capacity: u64,
    pub funding_token_amount: u64,
    pub expiry_timestamp: u64,
    pub setup_timestamp: u64,
    pub keyset_id: String,
    pub input_fee_ppk: u64,
    pub maximum_amount_for_one_output: u64,
}

/// The nested params as the protocol defines them — exactly the eleven fields
/// `get_channel_id_params_json()` emits, and no others, because those are the
/// only fields that can move the channel id.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
struct NestedParams {
    mint: String,
    unit: String,
    capacity: u64,
    funding_token_amount: u64,
    keyset_id: String,
    input_fee_ppk: u64,
    maximum_amount_for_one_output: u64,
    setup_timestamp: u64,
    sender_pubkey: String,
    receiver_pubkey: String,
    expiry_timestamp: u64,
}

/// The bounded, versioned request. Every field is required: a default here
/// would be a value nobody supplied being treated as agreement.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ValidateRegistrationRequest {
    /// The payee's receiver scalar, 32 bytes hex — the value the daemon derived
    /// from its own merchant identity. Used to derive the receiver public key
    /// and the channel secret; never echoed back.
    pub receiver_secret_hex: String,
    /// The mint URL this daemon is configured to trust.
    pub trusted_mint_url: String,
    /// The keyset this daemon fetched and trusts (NUT-02 keys + fee + unit).
    pub trusted_keyset_info: Value,
    /// The outer record the daemon would persist.
    pub registration: Registration,
    /// The nested params the payer signs against, as a JSON string.
    pub params_json: String,
    /// The instant to judge expiry against. Supplied, never read from a clock.
    pub now_unix: u64,
}

/// Only the values the sidecar derived, plus the two honesty markers.
///
/// Nothing here is echoed from the request: `canonical_params_json` comes from
/// the crate's own serializer, and every other field comes from the parsed
/// [`ChannelParameters`].
#[derive(Debug, Clone, Serialize)]
pub struct ValidatedRegistration {
    pub channel_id: String,
    pub canonical_params_json: String,
    pub receiver_pubkey: String,
    pub sender_pubkey: String,
    pub mint: String,
    pub unit: String,
    pub capacity: u64,
    pub funding_token_amount: u64,
    pub expiry_timestamp: u64,
    pub setup_timestamp: u64,
    pub keyset_id: String,
    pub input_fee_ppk: u64,
    pub maximum_amount_for_one_output: u64,
    pub trust_signal: String,
    /// Always `false`. Present so a caller cannot read success as a statement
    /// about the mint's funds.
    pub mint_funds_asserted: bool,
}

/// Validate one registration. Pure: no I/O, no clock, no value.
pub fn validate(request: &ValidateRegistrationRequest) -> Result<ValidatedRegistration, String> {
    // 1. The trusted mint must be a well-formed https URL before anything is
    //    compared against it: comparing a registration to `""` would "pass" for
    //    a caller who forgot to configure a mint.
    let trusted_mint =
        normalize_mint(&request.trusted_mint_url).ok_or_else(|| ERR_MINT_MISMATCH.to_string())?;

    // 2. The receiver key is DERIVED, never trusted. A secret that is not a
    //    valid 32-byte scalar cannot own any public key.
    let receiver_secret = parse_secret_hex(&request.receiver_secret_hex)
        .ok_or_else(|| ERR_RECEIVER_MISMATCH.to_string())?;
    let derived_receiver = receiver_secret.public_key();
    if !hex_eq(
        &derived_receiver.to_hex(),
        &request.registration.receiver_pubkey,
    ) {
        return Err(ERR_RECEIVER_MISMATCH.to_string());
    }

    // 3. A channel from a key to itself is not a channel: the 2-of-2 funding
    //    would be 1-of-1 and every accepted balance a self-transfer.
    let sender_pubkey = parse_public_hex(&request.registration.sender_pubkey)
        .ok_or_else(|| ERR_CHANNEL_ID_NOT_DERIVABLE.to_string())?;
    if sender_pubkey == derived_receiver {
        return Err(ERR_SENDER_EQUALS_RECEIVER.to_string());
    }

    // 4. Unit support is decided on the OUTER field. The nested field is judged
    //    by agreement below, so a wholly unsupported unit reports the real
    //    reason instead of an internal inconsistency.
    if !is_supported_unit(&request.registration.unit) {
        return Err(ERR_UNSUPPORTED_UNIT.to_string());
    }

    // 5. Capacity zero derives a perfectly valid channel id that can pay
    //    nothing, so only an explicit check refuses it.
    if request.registration.capacity == 0 {
        return Err(ERR_ZERO_CAPACITY.to_string());
    }

    // 6. Lifetime consistency before expiry, so an expiry that precedes setup
    //    is reported as a malformed registration rather than as merely stale.
    if request.registration.expiry_timestamp < request.registration.setup_timestamp {
        return Err(ERR_INVALID_LIFETIME.to_string());
    }
    if request.registration.expiry_timestamp <= request.now_unix {
        return Err(ERR_REGISTRATION_EXPIRED.to_string());
    }

    // 7. The outer mint must be the trusted one.
    let outer_mint =
        normalize_mint(&request.registration.mint).ok_or_else(|| ERR_MINT_MISMATCH.to_string())?;
    if outer_mint != trusted_mint {
        return Err(ERR_MINT_MISMATCH.to_string());
    }

    // 8. The trusted keyset must parse, and its declared id must be the id its
    //    OWN keys produce. Without the recomputation a caller could pair a
    //    legitimate id with a foreign mint's keys and every later derivation
    //    would agree with itself.
    let keyset_info = parse_keyset_info_from_json(&request.trusted_keyset_info.to_string())
        .map_err(|_| ERR_KEYSET_MISMATCH.to_string())?;
    if !keyset_id_matches_its_keys(&keyset_info) {
        return Err(ERR_KEYSET_MISMATCH.to_string());
    }
    // The keyset's own unit and fee are declarations about how much Charlie can
    // actually receive; they must agree with the registration that records them.
    if !is_supported_unit(&keyset_info.unit.to_string())
        || keyset_info.unit.to_string() != request.registration.unit
    {
        return Err(ERR_KEYSET_MISMATCH.to_string());
    }
    if keyset_info.input_fee_ppk != request.registration.input_fee_ppk {
        return Err(ERR_KEYSET_MISMATCH.to_string());
    }

    // 9. The nested params must be the same registration, field by field. The
    //    mint is compared to the TRUSTED one rather than to the outer claim, so
    //    "the nested params name a different mint" is reported as a mint problem
    //    with its own code instead of as an internal inconsistency.
    let nested: NestedParams = serde_json::from_str(&request.params_json)
        .map_err(|_| ERR_CHANNEL_ID_NOT_DERIVABLE.to_string())?;
    match normalize_mint(&nested.mint) {
        Some(mint) if mint == trusted_mint => {}
        _ => return Err(ERR_MINT_MISMATCH.to_string()),
    }
    compare_nested(&nested, request, &derived_receiver)?;

    // 10. The nested params must also name the trusted keyset, not merely agree
    //     with the outer record (both could be attacker-chosen and consistent).
    if nested.keyset_id.to_lowercase() != keyset_info.keyset_id.to_string() {
        return Err(ERR_KEYSET_MISMATCH.to_string());
    }
    if nested.input_fee_ppk != keyset_info.input_fee_ppk {
        return Err(ERR_KEYSET_MISMATCH.to_string());
    }

    // 11. Derivation. `compute_channel_secret` is ECDH(receiver, sender) — the
    //     only place a channel secret comes from, and the reason neither party
    //     can manufacture a channel id alone.
    let channel_secret = compute_channel_secret(&receiver_secret, &sender_pubkey);
    let params = ChannelParameters::from_json_with_channel_secret(
        &request.params_json,
        keyset_info,
        channel_secret,
    )
    .map_err(|_| ERR_CHANNEL_ID_NOT_DERIVABLE.to_string())?;

    // 12. A derived id is only useful if it is a full 32-byte digest.
    let channel_id = params.get_channel_id();
    if channel_id.len() != 64 || !channel_id.chars().all(|c| c.is_ascii_hexdigit()) {
        return Err(ERR_CHANNEL_ID_NOT_DERIVABLE.to_string());
    }

    Ok(ValidatedRegistration {
        channel_id,
        canonical_params_json: params.get_channel_id_params_json(),
        receiver_pubkey: params.receiver_pubkey.to_hex(),
        sender_pubkey: params.sender_pubkey.to_hex(),
        mint: params.mint.clone(),
        unit: params.unit_name(),
        capacity: params.capacity,
        funding_token_amount: params.funding_token_amount,
        expiry_timestamp: params.expiry_timestamp,
        setup_timestamp: params.setup_timestamp,
        keyset_id: params.keyset_info.keyset_id.to_string(),
        input_fee_ppk: params.keyset_info.input_fee_ppk,
        maximum_amount_for_one_output: params.maximum_amount_for_one_output,
        trust_signal: TRUST_SIGNAL_REGISTRATION_VALIDATED.to_string(),
        mint_funds_asserted: false,
    })
}

/// Every field that contributes to the channel id must be identical. Values are
/// compared after the same normalization the crate applies (mint trailing
/// slashes trimmed, hex case folded) so a semantically identical registration is
/// not refused on byte spelling alone.
fn compare_nested(
    nested: &NestedParams,
    request: &ValidateRegistrationRequest,
    derived_receiver: &PublicKey,
) -> Result<(), String> {
    let outer = &request.registration;

    // The receiver is checked against the DERIVED key, not against the outer
    // claim: two claims agreeing with each other proves nothing.
    if !hex_eq(&derived_receiver.to_hex(), &nested.receiver_pubkey) {
        return Err(ERR_RECEIVER_MISMATCH.to_string());
    }

    // A mint substitution inside the nested params is reported as a MINT
    // problem, not folded into the generic mismatch: "this channel names a
    // different mint" and "this channel disagrees with itself about capacity"
    // are different operator actions.
    let same_mint = match (normalize_mint(&nested.mint), normalize_mint(&outer.mint)) {
        (Some(a), Some(b)) => a == b,
        _ => false,
    };
    if !same_mint {
        return Err(ERR_MINT_MISMATCH.to_string());
    }

    let agree = nested.unit == outer.unit
        && nested.capacity == outer.capacity
        && nested.funding_token_amount == outer.funding_token_amount
        && nested.expiry_timestamp == outer.expiry_timestamp
        && nested.setup_timestamp == outer.setup_timestamp
        && nested.maximum_amount_for_one_output == outer.maximum_amount_for_one_output
        && nested.input_fee_ppk == outer.input_fee_ppk
        && hex_eq(&nested.sender_pubkey, &outer.sender_pubkey)
        && hex_eq(&nested.keyset_id, &outer.keyset_id);
    if !agree {
        return Err(ERR_NESTED_MISMATCH.to_string());
    }
    Ok(())
}

/// True when the keyset's declared id is the id its own keys produce.
///
/// Recomputing is what makes the check non-circular: `keysetId` and `keys` are
/// both caller-supplied, so comparing the declared id to a trusted string alone
/// would let a forged id carry a foreign mint's keys. Version is taken from the
/// declared id's own encoding (16 hex chars = v1, 66 = v2).
fn keyset_id_matches_its_keys(keyset_info: &KeysetInfo) -> bool {
    let declared = keyset_info.keyset_id.to_string().to_lowercase();
    let recomputed = match declared.len() {
        16 => Id::v1_from_keys(&keyset_info.active_keys),
        66 => Id::v2_from_data(
            &keyset_info.active_keys,
            &keyset_info.unit,
            keyset_info.input_fee_ppk,
            keyset_info.final_expiry,
        ),
        _ => return false,
    };
    recomputed.to_string().to_lowercase() == declared
}

/// Parse a 32-byte hex secp256k1 scalar. Rejects wrong length, non-hex, and the
/// all-zero scalar (which is not a key and would make ECDH produce garbage).
fn parse_secret_hex(value: &str) -> Option<SecretKey> {
    let trimmed = value.trim();
    if trimmed.len() != 64 {
        return None;
    }
    let secret = SecretKey::from_hex(trimmed).ok()?;
    if secret.secret_bytes() == [0u8; 32] {
        return None;
    }
    Some(secret)
}

fn parse_public_hex(value: &str) -> Option<PublicKey> {
    PublicKey::from_str(&lower(value.trim())).ok()
}

/// Trim trailing slashes and require an `https://` URL with a nonempty host.
/// `http://` is refused rather than upgraded silently: a registration at a
/// plaintext mint is a different (and worse) trust decision than one at the
/// configured mint.
fn normalize_mint(value: &str) -> Option<String> {
    let trimmed = value.trim().trim_end_matches('/');
    let rest = trimmed.strip_prefix("https://")?;
    if rest.is_empty() || rest.starts_with('/') {
        return None;
    }
    Some(trimmed.to_string())
}

fn is_supported_unit(unit: &str) -> bool {
    SUPPORTED_UNITS.contains(&unit)
}

fn lower(value: &str) -> String {
    value.to_ascii_lowercase()
}

/// Hex comparison that tolerates case, because a pubkey's capitalization is a
/// spelling and not a different key. Everything else must match exactly.
fn hex_eq(a: &str, b: &str) -> bool {
    lower(a.trim()) == lower(b.trim())
}

#[cfg(test)]
mod tests {
    use std::collections::BTreeMap;

    use cashu::nuts::{CurrencyUnit, Keys};
    use cashu::Amount;

    use super::*;

    /// Build a NUT-02 `Keys` from amount/pubkey pairs, so a keyset can be
    /// constructed without a mint fetch.
    fn keys_from_pairs(pairs: &[(u64, &str)]) -> Keys {
        let mut map: BTreeMap<Amount, PublicKey> = BTreeMap::new();
        for (amount, hex) in pairs {
            if let Some(pubkey) = parse_public_hex(hex) {
                map.insert(Amount::from(*amount), pubkey);
            }
        }
        Keys::new(map)
    }

    #[test]
    fn a_mint_url_must_be_https_with_a_host() {
        assert_eq!(
            normalize_mint("https://mint.example/"),
            Some("https://mint.example".to_string())
        );
        assert_eq!(normalize_mint("http://mint.example"), None);
        assert_eq!(normalize_mint("https://"), None);
        assert_eq!(normalize_mint("mint.example"), None);
    }

    #[test]
    fn a_zero_scalar_is_not_a_secret_key() {
        assert!(parse_secret_hex(&"00".repeat(32)).is_none());
        assert!(parse_secret_hex(&"22".repeat(31)).is_none());
        assert!(parse_secret_hex(&"zz".repeat(32)).is_none());
        assert!(parse_secret_hex(&"22".repeat(32)).is_some());
    }

    #[test]
    fn only_sat_is_a_supported_unit() {
        assert!(is_supported_unit("sat"));
        for unsupported in ["usd", "eur", "auth", "btc", "", "SAT"] {
            assert!(!is_supported_unit(unsupported), "{unsupported:?}");
        }
    }

    #[test]
    fn a_v1_keyset_id_is_recomputed_from_its_keys() {
        let keys = keys_from_pairs(&[
            (
                1,
                "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798",
            ),
            (
                2,
                "02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5",
            ),
        ]);
        let honest = Id::v1_from_keys(&keys);
        let info = KeysetInfo::new(honest, CurrencyUnit::Sat, keys.clone(), 0, None);
        assert!(
            keyset_id_matches_its_keys(&info),
            "an id derived from these keys must verify"
        );

        // Swap in another mint's key while keeping the declared id: the whole
        // point of recomputation is that this is refused.
        let foreign = keys_from_pairs(&[
            (
                1,
                "02f9308a019258c31049344f85f89d5229b531c845836f99b08601f113bce036f9",
            ),
            (
                2,
                "02c6047f9441ed7d6d3045406e95c07cd85c778e4b8cef3ca7abac09b95c709ee5",
            ),
        ]);
        let forged = KeysetInfo::new(honest, CurrencyUnit::Sat, foreign, 0, None);
        assert!(
            !keyset_id_matches_its_keys(&forged),
            "a declared id that its own keys do not produce must be refused"
        );
    }

    #[test]
    fn hex_comparison_tolerates_case_only() {
        assert!(hex_eq("ABCD", "abcd"));
        assert!(!hex_eq("abcd", "abce"));
        assert!(!hex_eq("", "abcd"));
    }
}
