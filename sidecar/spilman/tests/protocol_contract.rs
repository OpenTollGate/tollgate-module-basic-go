//! Wire-contract tests for the capabilities-only milestone.
//!
//! Every test here asserts a REFUSAL as often as it asserts acceptance: the
//! point of the milestone is that a peer can distinguish "transport works" from
//! "settlement works", and that a malformed, oversized, mis-versioned or
//! uncorrelated frame never gets silently accepted.

use serde_json::{json, Value};
use tollgate_spilman_sidecar::protocol::{
    handle_frame, CAPABILITIES, ERR_FRAME_TOO_LARGE, ERR_INVALID_REQUEST,
    ERR_UNSUPPORTED_CAPABILITY, ERR_UNSUPPORTED_VERSION, MAX_FRAME_BYTES, PROTOCOL_VERSION,
};
use tollgate_spilman_sidecar::Outcome;

const GOOD_ID: &str = "close-01H8Z-0001";

fn frame(value: &Value) -> Vec<u8> {
    let mut bytes = serde_json::to_vec(value).expect("serialize fixture");
    bytes.push(b'\n');
    bytes
}

fn valid_request() -> Value {
    json!({
        "version": PROTOCOL_VERSION,
        "request_id": "req-0001",
        "operation_id": GOOD_ID,
        "method": "capabilities",
    })
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

// --- acceptance -----------------------------------------------------------

#[test]
fn capabilities_request_advertises_transport_and_validation_only() {
    let (response, close) = reply(handle_frame(&frame(&valid_request())));
    assert!(
        !close,
        "a well-formed request must not close the connection"
    );
    assert_eq!(response["version"], json!(PROTOCOL_VERSION));
    assert_eq!(response["request_id"], json!("req-0001"));
    assert_eq!(response["operation_id"], json!(GOOD_ID));
    assert_eq!(response["error"], json!(""));
    let advertised: Vec<&str> = response["capabilities"]
        .as_array()
        .expect("capabilities array")
        .iter()
        .map(|v| v.as_str().expect("capability strings"))
        .collect();
    assert_eq!(advertised, CAPABILITIES.to_vec());
    // The set is pinned LITERALLY per build, not just compared to the constant,
    // so a capability cannot be added by editing the constant alone, and the
    // transport-only build's shorter list is asserted rather than assumed.
    #[cfg(feature = "protocol")]
    assert_eq!(advertised, vec!["transport.v1", "validate_registration.v1"]);
    #[cfg(not(feature = "protocol"))]
    assert_eq!(advertised, vec!["transport.v1"]);
}

#[test]
fn capabilities_reply_never_advertises_settlement() {
    // The milestone gate: every advertised capability names a pure, offline
    // operation. `validate_registration.v1` derives a channel id and proves
    // agreement; it does not sign, swap, or restore, so settlement stays
    // unadvertised and a peer gating on it must keep refusing this build.
    let (response, _) = reply(handle_frame(&frame(&valid_request())));
    let advertised = response["capabilities"]
        .as_array()
        .expect("capabilities array");
    for capability in advertised {
        let name = capability.as_str().expect("capability string");
        assert!(
            !name.starts_with("settlement."),
            "no settlement capability may be advertised, got {name:?}"
        );
        assert!(
            name.starts_with("transport.") || name.starts_with("validate_registration."),
            "capability {name:?} is outside the two known pure-operation families"
        );
    }
}

#[test]
fn exactly_max_frame_is_accepted() {
    // Build a frame whose serialized length INCLUDING the newline is exactly
    // MAX_FRAME_BYTES: the bound is inclusive, so an off-by-one refusal would
    // make an honest client fail at its own documented limit.
    let overhead = frame(&json!({
        "version": PROTOCOL_VERSION,
        "request_id": "req-0001",
        "operation_id": GOOD_ID,
        "method": "capabilities",
        "params": { "pad": "" },
    }))
    .len();
    let pad_len = MAX_FRAME_BYTES - overhead;
    let padded = json!({
        "version": PROTOCOL_VERSION,
        "request_id": "req-0001",
        "operation_id": GOOD_ID,
        "method": "capabilities",
        "params": { "pad": "x".repeat(pad_len) },
    });
    let bytes = frame(&padded);
    assert_eq!(bytes.len(), MAX_FRAME_BYTES);
    let (response, close) = reply(handle_frame(&bytes));
    assert!(!close);
    assert_eq!(response["error"], json!(""));
}

// --- version / method refusals -------------------------------------------

#[test]
fn rejects_unsupported_version_and_echoes_ids() {
    let mut request = valid_request();
    request["version"] = json!(2);
    let (response, close) = reply(handle_frame(&frame(&request)));
    assert_eq!(response["error"], json!(ERR_UNSUPPORTED_VERSION));
    assert_eq!(response["version"], json!(PROTOCOL_VERSION));
    assert_eq!(response["request_id"], json!("req-0001"));
    assert_eq!(response["operation_id"], json!(GOOD_ID));
    assert!(
        response["capabilities"].as_array().is_some(),
        "even a refusal must state what this build does support"
    );
    assert!(!close, "a correlatable refusal keeps the connection usable");
}

#[test]
fn rejects_version_zero() {
    let mut request = valid_request();
    request["version"] = json!(0);
    let (response, _) = reply(handle_frame(&frame(&request)));
    assert_eq!(response["error"], json!(ERR_UNSUPPORTED_VERSION));
}

#[test]
fn rejects_version_wrong_type() {
    let mut request = valid_request();
    request["version"] = json!("1");
    let (response, close) = reply(handle_frame(&frame(&request)));
    assert_eq!(response["error"], json!(ERR_INVALID_REQUEST));
    assert!(close);
}

#[test]
fn rejects_unknown_method_as_unsupported_capability() {
    let mut request = valid_request();
    request["method"] = json!("prepare_close");
    let (response, close) = reply(handle_frame(&frame(&request)));
    assert_eq!(response["error"], json!(ERR_UNSUPPORTED_CAPABILITY));
    assert_eq!(response["request_id"], json!("req-0001"));
    assert!(!close);
}

#[test]
fn rejects_every_settlement_shaped_method() {
    for method in [
        "prepare_close",
        "verify_prepared",
        "complete_swap",
        "complete_restore",
        "settle",
    ] {
        let mut request = valid_request();
        request["method"] = json!(method);
        let (response, close) = reply(handle_frame(&frame(&request)));
        assert_eq!(
            response["error"],
            json!(ERR_UNSUPPORTED_CAPABILITY),
            "{method} must be refused in this milestone"
        );
        assert!(!close);
        assert!(
            response["capabilities"]
                .as_array()
                .expect("capabilities")
                .iter()
                .filter_map(|c| c.as_str())
                .all(|s| !s.starts_with("settlement.")),
            "a refused method must not imply settlement readiness"
        );
    }
}

// --- identifier refusals --------------------------------------------------

#[test]
fn rejects_missing_ids() {
    for field in ["request_id", "operation_id"] {
        let mut request = valid_request();
        request.as_object_mut().expect("object").remove(field);
        let (response, close) = reply(handle_frame(&frame(&request)));
        assert_eq!(
            response["error"],
            json!(ERR_INVALID_REQUEST),
            "missing {field}"
        );
        assert!(close, "an uncorrelatable frame closes the connection");
    }
}

#[test]
fn rejects_empty_ids() {
    for field in ["request_id", "operation_id"] {
        let mut request = valid_request();
        request[field] = json!("");
        let (response, _) = reply(handle_frame(&frame(&request)));
        assert_eq!(
            response["error"],
            json!(ERR_INVALID_REQUEST),
            "empty {field}"
        );
    }
}

#[test]
fn rejects_ids_with_disallowed_characters() {
    for bad in [
        "has space",
        "has\nnewline",
        "has/slash",
        "has.dot",
        "emoji-🚀",
    ] {
        let mut request = valid_request();
        request["request_id"] = json!(bad);
        let (response, _) = reply(handle_frame(&frame(&request)));
        assert_eq!(
            response["error"],
            json!(ERR_INVALID_REQUEST),
            "{bad:?} must be refused (ids are transport-safe ASCII only)"
        );
    }
}

#[test]
fn rejects_overlong_ids() {
    let mut request = valid_request();
    request["operation_id"] = json!("a".repeat(129));
    let (response, _) = reply(handle_frame(&frame(&request)));
    assert_eq!(response["error"], json!(ERR_INVALID_REQUEST));

    let mut ok = valid_request();
    ok["operation_id"] = json!("a".repeat(128));
    let (response, close) = reply(handle_frame(&frame(&ok)));
    assert_eq!(
        response["error"],
        json!(""),
        "128 characters is the inclusive limit"
    );
    assert!(!close);
}

#[test]
fn accepts_hyphen_and_underscore_ids() {
    let mut request = valid_request();
    request["request_id"] = json!("Req_a-1");
    let (response, _) = reply(handle_frame(&frame(&request)));
    assert_eq!(response["error"], json!(""));
}

// --- framing refusals -----------------------------------------------------

#[test]
fn rejects_oversize_frame() {
    let oversized = vec![b' '; MAX_FRAME_BYTES + 1];
    let (response, close) = reply(handle_frame(&oversized));
    assert_eq!(response["error"], json!(ERR_FRAME_TOO_LARGE));
    assert!(
        close,
        "an oversize frame cannot be correlated; the connection closes"
    );
}

#[test]
fn rejects_unterminated_frame() {
    let mut bytes = serde_json::to_vec(&valid_request()).expect("serialize");
    bytes.push(b' ');
    let (response, close) = reply(handle_frame(&bytes));
    assert_eq!(response["error"], json!(ERR_INVALID_REQUEST));
    assert!(close);
}

#[test]
fn rejects_trailing_data_after_one_frame() {
    let mut bytes = frame(&valid_request());
    bytes.extend_from_slice(b"{\"version\":1}\n");
    let (response, close) = reply(handle_frame(&bytes));
    assert_eq!(response["error"], json!(ERR_INVALID_REQUEST));
    assert!(
        close,
        "one frame per line: extra data is not a second request"
    );
}

#[test]
fn rejects_empty_frame() {
    let (response, close) = reply(handle_frame(b"\n"));
    assert_eq!(response["error"], json!(ERR_INVALID_REQUEST));
    assert!(close);
}

#[test]
fn rejects_non_object_json() {
    let (response, close) = reply(handle_frame(b"[]\n"));
    assert_eq!(response["error"], json!(ERR_INVALID_REQUEST));
    assert!(close);
}

#[test]
fn rejects_unknown_fields() {
    let mut request = valid_request();
    request["receiver_secret"] = json!("not-accepted-here");
    let (response, close) = reply(handle_frame(&frame(&request)));
    // The ids and version parsed, so the refusal IS correlatable: reply and
    // keep the connection (a client on a different protocol revision is a
    // deploy problem to read, not a desync to punish). `close` stays false.
    assert_eq!(
        response["error"],
        json!(ERR_INVALID_REQUEST),
        "this milestone accepts no secret or extra field"
    );
    assert_eq!(response["request_id"], json!("req-0001"));
    assert!(!close);
}

#[test]
fn rejects_missing_method() {
    let mut request = valid_request();
    request.as_object_mut().expect("object").remove("method");
    let (response, _) = reply(handle_frame(&frame(&request)));
    assert_eq!(response["error"], json!(ERR_INVALID_REQUEST));
}
