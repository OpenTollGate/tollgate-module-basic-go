//! The v1 wire contract: one newline-terminated JSON object per frame.
//!
//! Everything a peer can rely on in this milestone lives in this file, so the
//! Go client's expectations and the sidecar's behaviour are one artifact:
//!
//! * `version` must be exactly [`PROTOCOL_VERSION`] (integer `1`).
//! * `request_id` and `operation_id` are nonempty, at most 128 characters from
//!   `[A-Za-z0-9_-]`. They are echoed verbatim so a client can refuse a reply
//!   that answers a different request — the upstream wallet sidecar transport
//!   does not check reply ids, which is why this one does.
//! * `method` must be a known method of this version. The only one is
//!   `capabilities`; everything else is refused as
//!   [`ERR_UNSUPPORTED_CAPABILITY`] rather than guessed at.
//! * `params`, if present, must be an object. Unknown top-level fields are
//!   refused: this milestone accepts no secret, so a field it does not know is
//!   a client that expects a different protocol revision.
//!
//! Refusals split into two classes on purpose:
//!
//! * **correlatable** — the ids and version were readable, so the refusal
//!   echoes them and the connection stays usable;
//! * **fatal** — the frame could not be attributed to a request at all
//!   (unterminated, oversize, unparseable, bad ids), so no reply can be
//!   correlated and the connection closes instead of desynchronising.

use serde::{Deserialize, Serialize};
use serde_json::Value;

use crate::Outcome;

/// The only protocol version this build speaks.
pub const PROTOCOL_VERSION: u32 = 1;

/// Maximum frame size in bytes, INCLUDING the trailing newline. A frame of
/// exactly this size is accepted; one byte more is refused without being read
/// into memory beyond the bound.
pub const MAX_FRAME_BYTES: usize = 65536;

/// Longest accepted identifier, in characters.
pub const MAX_ID_CHARS: usize = 128;

/// `validate_registration.v1` — pure registration validation over supplied
/// bytes. It signs nothing, contacts no mint and moves no value.
pub const CAPABILITY_VALIDATE_REGISTRATION: &str = "validate_registration.v1";
pub const METHOD_VALIDATE_REGISTRATION: &str = "validate_registration";

/// What this build can do.
///
/// `transport.v1` proves framing and correlation. With the `protocol` feature,
/// `validate_registration.v1` additionally proves that a registration's outer
/// fields, its nested `ParamsJSON`, the trusted mint and the trusted keyset
/// agree and that a channel id can be derived — all offline, from supplied
/// bytes.
///
/// Neither is a settlement capability: no entry may ever start with
/// `settlement.` until signing, swap and restore actually exist, and the
/// contract test asserts the absence rather than trusting this comment. A peer
/// that requires settlement must not treat this list as satisfying its gate.
#[cfg(feature = "protocol")]
pub const CAPABILITIES: &[&str] = &["transport.v1", CAPABILITY_VALIDATE_REGISTRATION];

/// Without the `protocol` feature the sidecar advertises exactly what a
/// transport-only build can do. The list SHRINKS rather than lying: a peer that
/// gates on `validate_registration.v1` must see it absent.
#[cfg(not(feature = "protocol"))]
pub const CAPABILITIES: &[&str] = &["transport.v1"];

/// `capabilities` is always available; `validate_registration` additionally
/// requires the `protocol` feature.
pub const METHOD_CAPABILITIES: &str = "capabilities";

pub const ERR_INVALID_REQUEST: &str = "invalid_request";
pub const ERR_UNSUPPORTED_VERSION: &str = "unsupported_version";
pub const ERR_UNSUPPORTED_CAPABILITY: &str = "unsupported_capability";
pub const ERR_FRAME_TOO_LARGE: &str = "frame_too_large";

/// A parsed v1 request. `params` is kept opaque: no method in this version
/// reads it, and keeping it raw means an unimplemented method's payload is
/// never interpreted.
#[derive(Debug, Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Request {
    pub version: u32,
    pub request_id: String,
    pub operation_id: String,
    pub method: String,
    #[serde(default)]
    pub params: Option<Value>,
}

/// A v1 reply. `error` is `""` on success; the field is always present so a
/// client never has to distinguish "absent" from "empty".
///
/// `validated` is present ONLY on a successful `validate_registration`, and it
/// carries nothing but values derived by the sidecar. A refusal omits it
/// entirely (`skip_serializing_if`), so a caller cannot read a partial result
/// out of a failed validation — the field's presence IS the success signal.
#[derive(Debug, Clone, Serialize)]
pub struct Response {
    pub version: u32,
    pub request_id: String,
    pub operation_id: String,
    pub capabilities: Vec<&'static str>,
    pub error: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub validated: Option<Value>,
}

impl Response {
    /// Build a v1 reply. `pub(crate)` so `server` can encode a FATAL refusal
    /// (an uncorrelated one, where the input was never parseable) with the same
    /// shape every other reply uses, instead of hand-rolling a second framing.
    pub(crate) fn new(request_id: &str, operation_id: &str, error: &str) -> Self {
        Self {
            version: PROTOCOL_VERSION,
            request_id: request_id.to_string(),
            operation_id: operation_id.to_string(),
            capabilities: CAPABILITIES.to_vec(),
            error: error.to_string(),
            validated: None,
        }
    }

    /// Attach the derived values of a successful validation.
    ///
    /// Only a `protocol` build can produce one, so a transport-only build has
    /// no caller for this — and dead code in a money-adjacent module is worth
    /// refusing rather than tolerating.
    #[cfg(feature = "protocol")]
    fn with_validated(mut self, validated: Value) -> Self {
        self.validated = Some(validated);
        self
    }

    /// Encode as one newline-terminated frame. `pub(crate)` for the same reason
    /// as `new`: one framing implementation for every reply, fatal included.
    pub(crate) fn encode(&self) -> Vec<u8> {
        let mut frame = serde_json::to_vec(self).expect("a Response always serializes to JSON");
        frame.push(b'\n');
        frame
    }
}

/// True when `id` is a transport-safe identifier: 1..=128 characters, ASCII
/// alphanumeric plus `-` and `_`.
pub fn validate_id(id: &str) -> bool {
    !id.is_empty()
        && id.chars().count() <= MAX_ID_CHARS
        && id
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_')
}

/// Handle one complete inbound frame.
///
/// The caller has already bounded the frame (see [`MAX_FRAME_BYTES`]); this
/// function re-checks the bound so a caller that reads first cannot widen it.
pub fn handle_frame(frame: &[u8]) -> Outcome {
    if frame.len() > MAX_FRAME_BYTES {
        return Outcome::Fatal {
            error: ERR_FRAME_TOO_LARGE.to_string(),
            close: true,
        };
    }

    // Exactly one frame: the payload must be a single JSON object followed by
    // one newline and nothing else. Prose or a second object on the same line
    // is a protocol error, not a pipelined request.
    let body = match frame.strip_suffix(b"\n") {
        Some(body) => body,
        None => return fatal(ERR_INVALID_REQUEST),
    };
    if body.is_empty() || body.contains(&b'\n') || body.contains(&b'\r') {
        return fatal(ERR_INVALID_REQUEST);
    }

    let value: Value = match serde_json::from_slice(body) {
        Ok(value) => value,
        Err(_) => return fatal(ERR_INVALID_REQUEST),
    };
    if !value.is_object() {
        return fatal(ERR_INVALID_REQUEST);
    }

    // Parse leniently enough to recover the correlation fields for a
    // version/method refusal, but strictly about their shape.
    let request_id = value
        .get("request_id")
        .and_then(Value::as_str)
        .filter(|id| validate_id(id))
        .map(str::to_string);
    let operation_id = value
        .get("operation_id")
        .and_then(Value::as_str)
        .filter(|id| validate_id(id))
        .map(str::to_string);
    let (request_id, operation_id) = match (request_id, operation_id) {
        (Some(r), Some(o)) => (r, o),
        _ => return fatal(ERR_INVALID_REQUEST),
    };

    let version = match value.get("version").and_then(Value::as_u64) {
        Some(v) if v <= u64::from(u32::MAX) => v as u32,
        _ => return fatal_with_ids(&request_id, &operation_id, ERR_INVALID_REQUEST),
    };
    if version != PROTOCOL_VERSION {
        return reply(Response::new(
            &request_id,
            &operation_id,
            ERR_UNSUPPORTED_VERSION,
        ));
    }

    let request: Request = match serde_json::from_value(value) {
        Ok(request) => request,
        Err(_) => {
            return reply(Response::new(
                &request_id,
                &operation_id,
                ERR_INVALID_REQUEST,
            ))
        }
    };
    if let Some(params) = &request.params {
        if !params.is_object() {
            return reply(Response::new(
                &request_id,
                &operation_id,
                ERR_INVALID_REQUEST,
            ));
        }
    }

    match request.method.as_str() {
        METHOD_CAPABILITIES => reply(Response::new(&request_id, &operation_id, "")),
        METHOD_VALIDATE_REGISTRATION => validate_registration(&request_id, &operation_id, &request),
        _ => reply(Response::new(
            &request_id,
            &operation_id,
            ERR_UNSUPPORTED_CAPABILITY,
        )),
    }
}

/// Dispatch `validate_registration`.
///
/// Three refusals can happen here and each keeps a different meaning:
///
/// * no `params` at all / not an object -> `invalid_request` (the caller sent a
///   request for a method that requires a body and sent none);
/// * a `params` object that does not match the versioned schema (unknown field,
///   missing field, wrong type) -> `invalid_request` (a caller on another
///   revision, which must fail loudly rather than be partially honoured);
/// * a schema-valid request whose *content* is refused -> the specific
///   registration code from [`crate::registration`].
#[cfg(feature = "protocol")]
fn validate_registration(request_id: &str, operation_id: &str, request: &Request) -> Outcome {
    use crate::registration::{validate, ValidateRegistrationRequest};

    let params = match request.params.as_ref() {
        Some(params) if params.is_object() => params,
        _ => return reply(Response::new(request_id, operation_id, ERR_INVALID_REQUEST)),
    };
    let typed: ValidateRegistrationRequest = match serde_json::from_value(params.clone()) {
        Ok(typed) => typed,
        Err(_) => return reply(Response::new(request_id, operation_id, ERR_INVALID_REQUEST)),
    };
    match validate(&typed) {
        Ok(validated) => {
            let value = serde_json::to_value(&validated)
                .expect("ValidatedRegistration is a plain struct of owned values");
            reply(Response::new(request_id, operation_id, "").with_validated(value))
        }
        Err(code) => reply(Response::new(request_id, operation_id, &code)),
    }
}

/// A transport-only build (`--no-default-features`) refuses the method by
/// capability rather than by pretending the payload was malformed: the caller's
/// request may be perfectly well formed, and this build simply cannot answer it.
#[cfg(not(feature = "protocol"))]
fn validate_registration(request_id: &str, operation_id: &str, _request: &Request) -> Outcome {
    reply(Response::new(
        request_id,
        operation_id,
        ERR_UNSUPPORTED_CAPABILITY,
    ))
}

fn reply(response: Response) -> Outcome {
    Outcome::Reply {
        frame: response.encode(),
        close: false,
    }
}

fn fatal(error: &str) -> Outcome {
    Outcome::Fatal {
        error: error.to_string(),
        close: true,
    }
}

/// A fatal refusal that can still name the request: the client sent readable
/// ids, so the error is attributable even though the frame is unusable.
fn fatal_with_ids(request_id: &str, operation_id: &str, error: &str) -> Outcome {
    Outcome::Reply {
        frame: Response::new(request_id, operation_id, error).encode(),
        close: true,
    }
}
