//! Fatal refusals must REACH the peer.
//!
//! `Outcome::Fatal` is what the sidecar produces for input it cannot correlate
//! at all: an oversized frame, a malformed one, prose on the wire. The design
//! says these close the connection *after* refusing with an uncorrelated error,
//! so the client learns why instead of seeing a bare EOF.
//!
//! `serve_stream` used to drop that error on the floor — the match arm named
//! `Outcome::Fatal { close, .. }` and returned `Ok(())` when `close` was set,
//! writing nothing. A client then saw "connection reset" for both a 1 MB flood
//! and a one-byte typo, which is indistinguishable from the sidecar crashing.
//! These tests exist so that regression cannot come back: they assert on BYTES
//! THE CLIENT RECEIVES over a real `UnixStream`, never on the enum.

use std::io::{BufRead, BufReader, Write};
use std::os::unix::net::UnixStream;

use tollgate_spilman_sidecar::protocol::{MAX_FRAME_BYTES, PROTOCOL_VERSION};
use tollgate_spilman_sidecar::server::serve_stream;

/// Run `serve_stream` on one end of a socketpair, write `payload`, and return
/// everything the peer read before EOF.
fn exchange(payload: &[u8]) -> String {
    let (mut client, server) = UnixStream::pair().expect("socketpair");
    let handle = std::thread::spawn(move || {
        let _ = serve_stream(server);
    });
    client.write_all(payload).expect("write payload");
    client.flush().expect("flush");
    // Without `shutdown(Write)` the peer is still a WRITER that has sent nothing
    // more, so `read_line` blocks until the server's 10 s timeout — and a
    // regression then hangs the whole suite instead of failing one test. Half-
    // closing says "that is the whole frame", which is exactly the client's
    // intent, and makes a missing refusal an EOF that fails fast.
    client
        .shutdown(std::net::Shutdown::Write)
        .expect("half-close");

    let mut reader = BufReader::new(client.try_clone().expect("clone"));
    let mut received = String::new();
    let mut line = String::new();
    // One line: the refusal frame. EOF after it is the expected close.
    if reader.read_line(&mut line).unwrap_or(0) > 0 {
        received.push_str(&line);
    }
    drop(reader);
    drop(client);
    let _ = handle.join();
    received
}

fn error_of(frame: &str) -> String {
    let value: serde_json::Value = serde_json::from_str(frame.trim_end())
        .unwrap_or_else(|e| panic!("refusal is not JSON ({e}): {frame:?}"));
    value
        .get("error")
        .and_then(|v| v.as_str())
        .unwrap_or_else(|| panic!("refusal has no error field: {frame:?}"))
        .to_string()
}

#[test]
fn an_oversized_frame_is_refused_with_its_own_code_over_the_wire() {
    let mut payload = vec![b' '; MAX_FRAME_BYTES + 1];
    payload.push(b'\n');
    let received = exchange(&payload);
    assert!(
        !received.is_empty(),
        "an oversized frame must be refused with a frame, not a bare close:                  a client cannot tell a size violation from a crashed sidecar"
    );
    assert_eq!(error_of(&received), "frame_too_large");
    // Uncorrelated by construction: the frame was never parseable.
    let value: serde_json::Value = serde_json::from_str(received.trim_end()).expect("json");
    assert_eq!(
        value["request_id"], "",
        "an unparseable frame has no id to echo"
    );
}

#[test]
fn a_malformed_frame_is_refused_with_invalid_request() {
    let received = exchange(b"{not json}\n");
    assert!(
        !received.is_empty(),
        "a malformed frame must be refused with a frame, not a bare close"
    );
    assert_eq!(error_of(&received), "invalid_request");
}

#[test]
fn prose_without_a_terminator_is_refused_rather_than_hanging() {
    let received = exchange(b"hello there");
    assert!(
        !received.is_empty(),
        "prose must be refused, not silently dropped"
    );
    assert_eq!(error_of(&received), "invalid_request");
}

#[test]
fn a_readable_frame_still_works_after_the_fix() {
    // Guard against "fix the fatal path by refusing everything": a valid
    // request must still be answered, with its ids echoed.
    let frame = format!(
        "{{\"version\":{PROTOCOL_VERSION},\"request_id\":\"req-ok\",\"operation_id\":\"op-ok\",\"method\":\"capabilities\"}}\n"
    );
    let received = exchange(frame.as_bytes());
    let value: serde_json::Value = serde_json::from_str(received.trim_end()).expect("json reply");
    assert_eq!(value["error"], "");
    assert_eq!(value["request_id"], "req-ok");
    assert_eq!(value["operation_id"], "op-ok");
    assert!(
        value["capabilities"]
            .as_array()
            .is_some_and(|c| !c.is_empty()),
        "capabilities must be advertised: {value:?}"
    );
}

#[test]
fn a_fatal_refusal_closes_the_connection() {
    // The refusal must be followed by EOF: a client that keeps the connection
    // would send a second frame into a stream the peer has abandoned.
    let (mut client, server) = UnixStream::pair().expect("socketpair");
    let handle = std::thread::spawn(move || {
        let _ = serve_stream(server);
    });
    client.write_all(b"{not json}\n").expect("write");
    client.flush().expect("flush");
    let mut reader = BufReader::new(client.try_clone().expect("clone"));
    let mut line = String::new();
    let n = reader.read_line(&mut line).expect("read refusal");
    assert!(n > 0, "expected a refusal line");
    // The next read must be EOF, not a hang: serve_stream returned.
    let mut tail = String::new();
    let after = reader.read_line(&mut tail).unwrap_or(0);
    assert_eq!(after, 0, "the sidecar must close after a fatal refusal");
    drop(reader);
    drop(client);
    handle.join().expect("server thread");
}
