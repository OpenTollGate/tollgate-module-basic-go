//! The other side of the `protocol` feature fence.
//!
//! `registration_contract.rs` proves what a `protocol` build does. This file
//! proves what a `--no-default-features` build does, and the important part is
//! not that it refuses — it is that it **does not advertise**. A transport-only
//! binary that advertised `validate_registration.v1` and then refused every
//! call would be worse than one that stays quiet: a peer would gate its
//! readiness on a capability that can never succeed.
//!
//! The assertion is therefore "absent from CAPABILITIES", not "returns an
//! error". Both legs run in the same CI invocation, so the fence cannot be
//! satisfied by deleting one side.

#![cfg(not(feature = "protocol"))]

use serde_json::{json, Value};
use tollgate_spilman_sidecar::protocol::{
    handle_frame, CAPABILITIES, CAPABILITY_VALIDATE_REGISTRATION, ERR_UNSUPPORTED_CAPABILITY,
    MAX_FRAME_BYTES, METHOD_VALIDATE_REGISTRATION, PROTOCOL_VERSION,
};
use tollgate_spilman_sidecar::Outcome;

fn frame(value: &Value) -> Vec<u8> {
    let mut bytes = serde_json::to_vec(value).expect("serialize fixture");
    bytes.push(b'\n');
    bytes
}

fn reply(outcome: Outcome) -> (Value, bool) {
    match outcome {
        Outcome::Reply { frame, close } => {
            let text = String::from_utf8(frame).expect("UTF-8 reply");
            assert_eq!(text.matches('\n').count(), 1);
            (
                serde_json::from_str(text.trim_end()).expect("reply is JSON"),
                close,
            )
        }
        Outcome::Fatal { error, close } => (json!({ "error": error }), close),
    }
}

fn request(method: &str) -> Value {
    json!({
        "version": PROTOCOL_VERSION,
        "request_id": "req-transport-only",
        "operation_id": "op-transport-only",
        "method": method,
        "params": {},
    })
}

#[test]
fn a_transport_only_build_does_not_advertise_validation() {
    assert_eq!(CAPABILITIES, &["transport.v1"]);
    assert!(
        !CAPABILITIES.contains(&CAPABILITY_VALIDATE_REGISTRATION),
        "a build without the protocol feature must not advertise validation"
    );
    // The constant still exists so the two legs of the fence can name the same
    // string; advertising it is what must not happen.
    assert_eq!(CAPABILITY_VALIDATE_REGISTRATION, "validate_registration.v1");
}

#[test]
fn a_transport_only_build_serves_capabilities_unchanged() {
    let (response, close) = reply(handle_frame(&frame(&request("capabilities"))));
    assert!(!close);
    assert_eq!(response["error"], json!(""));
    assert_eq!(response["capabilities"], json!(["transport.v1"]));
}

#[test]
fn a_transport_only_build_refuses_validation_by_capability() {
    // Refused as `unsupported_capability`, NOT as `invalid_request`: the caller's
    // frame is fine, this build simply cannot answer it. The distinction is what
    // lets a client tell "wrong deploy" from "malformed request".
    let (response, close) = reply(handle_frame(&frame(&request(METHOD_VALIDATE_REGISTRATION))));
    assert_eq!(response["error"], json!(ERR_UNSUPPORTED_CAPABILITY));
    assert_eq!(response["request_id"], json!("req-transport-only"));
    assert!(!close, "a correlatable refusal keeps the connection usable");
    assert!(
        response.get("validated").is_none(),
        "no validated values from a build that cannot validate"
    );
}

#[test]
fn a_transport_only_build_still_applies_the_frame_bound() {
    let oversized = vec![b' '; MAX_FRAME_BYTES + 1];
    let (response, close) = reply(handle_frame(&oversized));
    assert_eq!(response["error"], json!("frame_too_large"));
    assert!(close);
}
