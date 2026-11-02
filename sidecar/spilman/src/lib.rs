//! Spilman protocol sidecar — capabilities-only transport milestone.
//!
//! # What this crate is, and is not
//!
//! This is the local IPC boundary the Go daemon will use for Spilman protocol
//! work, but the only operation implemented here is `capabilities`. Nothing in
//! this crate signs, blinds, swaps, restores, contacts a mint, or moves value.
//! Any claim that settlement works cannot come from this crate: the wire
//! advertises `transport.v1` and nothing else, and a peer that wants settlement
//! must require a capability this build can never advertise.
//!
//! # Ownership
//!
//! The Go daemon owns durable settlement state (channel registration, accepted
//! balances, close authorization, the operation journal, proof custody). This
//! process owns no ledger and must never be given one; the decision record is
//! `docs/architecture/spilman-sidecar-decision.md`.

pub mod protocol;
pub mod server;

pub use protocol::{
    handle_frame, validate_id, Response, CAPABILITIES, ERR_FRAME_TOO_LARGE, ERR_INVALID_REQUEST,
    ERR_UNSUPPORTED_CAPABILITY, ERR_UNSUPPORTED_VERSION, MAX_FRAME_BYTES, PROTOCOL_VERSION,
};

/// What one inbound frame produced.
///
/// `Reply` always carries a complete, newline-terminated frame that echoes the
/// request's correlation fields when they were readable. `Fatal` means the
/// server could not correlate the input at all, so it refuses with an
/// uncorrelated error and must close; `close` on `Reply` exists so a *replying*
/// refusal can also terminate (never used today, kept explicit so a future
/// version cannot silently turn a close into a hang).
#[derive(Debug)]
pub enum Outcome {
    Reply { frame: Vec<u8>, close: bool },
    Fatal { error: String, close: bool },
}
