//! Transport-level contract: the socket is exclusive, correctly permissioned,
//! authenticated by peer credential, and bounded.
//!
//! These tests use real AF_UNIX sockets in a temp directory — no mocks — because
//! every property under test (directory mode, live-listener refusal, stale-path
//! reclaim, `SO_PEERCRED`) is a kernel/filesystem behaviour that a fake would
//! assert about itself.

use std::io::{BufRead, BufReader, Write};
use std::os::unix::fs::PermissionsExt;
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::time::Duration;

use tollgate_spilman_sidecar::server::{peer_uid, Server, ServerError, IO_TIMEOUT};

const GOOD_REQUEST: &str = concat!(
    "{\"version\":1,\"request_id\":\"req-1\",\"operation_id\":\"op-1\",",
    "\"method\":\"capabilities\"}\n"
);

fn own_uid() -> u32 {
    // SAFETY: getuid has no preconditions and cannot fail.
    unsafe { libc::getuid() }
}

/// A private runtime directory, removed on drop.
struct TempDir(PathBuf);

impl TempDir {
    fn new(label: &str) -> Self {
        let unique = format!(
            "tg-spilman-{label}-{}-{}",
            std::process::id(),
            std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .expect("clock after epoch")
                .as_nanos()
        );
        let path = std::env::temp_dir().join(unique);
        std::fs::create_dir(&path).expect("create temp dir");
        std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o700))
            .expect("chmod 0700");
        Self(path)
    }

    fn path(&self) -> &Path {
        &self.0
    }
}

impl Drop for TempDir {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

fn roundtrip(socket: &Path, request: &[u8]) -> std::io::Result<String> {
    let mut stream = UnixStream::connect(socket)?;
    stream.set_read_timeout(Some(IO_TIMEOUT))?;
    stream.write_all(request)?;
    stream.flush()?;
    let mut line = String::new();
    BufReader::new(stream).read_line(&mut line)?;
    Ok(line)
}

/// Serve exactly one connection in a thread, then return.
fn serve_one(server: Server) -> std::thread::JoinHandle<()> {
    std::thread::spawn(move || {
        let mut served = 0;
        let _ = server.serve(|| {
            served += 1;
            served > 1
        });
    })
}

#[test]
fn bounds_are_declared_once() {
    assert_eq!(tollgate_spilman_sidecar::MAX_FRAME_BYTES, 65536);
    assert_eq!(tollgate_spilman_sidecar::PROTOCOL_VERSION, 1);
    // Pinned literally so the shipped set cannot drift by editing the constant
    // alone, and so the transport-only build's SHORTER list is a visible
    // difference rather than an accident.
    #[cfg(feature = "protocol")]
    assert_eq!(
        tollgate_spilman_sidecar::CAPABILITIES,
        &["transport.v1", "validate_registration.v1"]
    );
    #[cfg(not(feature = "protocol"))]
    assert_eq!(tollgate_spilman_sidecar::CAPABILITIES, &["transport.v1"]);
}

#[test]
fn refuses_world_accessible_runtime_directory() {
    let dir = TempDir::new("loose");
    std::fs::set_permissions(dir.path(), std::fs::Permissions::from_mode(0o755))
        .expect("chmod 0755");
    let socket = dir.path().join("s.sock");
    match Server::bind(dir.path(), &socket, own_uid()) {
        Err(ServerError::UnsafeDirectory { .. }) => {}
        other => panic!("expected UnsafeDirectory, got {other:?}"),
    }
    assert!(
        !socket.exists(),
        "a refused bind must not leave a socket behind"
    );
}

#[test]
fn refuses_socket_outside_the_runtime_directory() {
    let dir = TempDir::new("outside");
    let elsewhere = dir.path().join("nested");
    std::fs::create_dir(&elsewhere).expect("nested dir");
    std::fs::set_permissions(&elsewhere, std::fs::Permissions::from_mode(0o700)).expect("chmod");
    let socket = elsewhere.join("s.sock");
    match Server::bind(dir.path(), &socket, own_uid()) {
        Err(ServerError::UnsafeDirectory { .. }) => {}
        other => panic!("expected UnsafeDirectory, got {other:?}"),
    }
}

#[test]
fn refuses_a_directory_owned_by_another_uid() {
    let dir = TempDir::new("owner");
    let socket = dir.path().join("s.sock");
    // Ask for an owner we provably are not.
    let foreign = if own_uid() == 0 { 1 } else { 0 };
    match Server::bind(dir.path(), &socket, foreign) {
        Err(ServerError::UnsafeDirectory { .. }) => {}
        other => panic!("expected UnsafeDirectory, got {other:?}"),
    }
}

#[test]
fn binds_with_restrictive_socket_mode_and_cleans_up() {
    let dir = TempDir::new("bind");
    let socket = dir.path().join("s.sock");
    {
        let server = Server::bind(dir.path(), &socket, own_uid()).expect("bind");
        let mode = std::fs::metadata(&socket)
            .expect("socket metadata")
            .permissions()
            .mode()
            & 0o777;
        assert_eq!(mode, 0o600, "socket file must not be group/world writable");
        assert_eq!(
            peer_uid(&UnixStream::connect(&socket).expect("connect")),
            Some(own_uid())
        );
        assert_eq!(server.socket_path(), socket.as_path());
        assert_eq!(server.service_uid(), own_uid());
    }
    assert!(!socket.exists(), "dropping the server removes its socket");
}

#[test]
fn refuses_to_steal_a_live_listener() {
    let dir = TempDir::new("live");
    let socket = dir.path().join("s.sock");
    let first = Server::bind(dir.path(), &socket, own_uid()).expect("first bind");
    match Server::bind(dir.path(), &socket, own_uid()) {
        Err(ServerError::AlreadyServing { .. }) => {}
        other => panic!("expected AlreadyServing, got {other:?}"),
    }
    drop(first);
}

#[test]
fn reclaims_a_stale_socket_path() {
    let dir = TempDir::new("stale");
    let socket = dir.path().join("s.sock");
    // A listener bound and dropped without unlinking leaves exactly the crash
    // residue a killed sidecar leaves.
    {
        let stale = UnixListener::bind(&socket).expect("stale bind");
        drop(stale);
    }
    assert!(socket.exists(), "fixture must leave the stale path");
    let server = Server::bind(dir.path(), &socket, own_uid()).expect("reclaim");
    drop(server);
}

#[test]
fn serves_a_capabilities_request_over_the_real_socket() {
    let dir = TempDir::new("serve");
    let socket = dir.path().join("s.sock");
    let server = Server::bind(dir.path(), &socket, own_uid()).expect("bind");
    let handle = serve_one(server);

    let reply = roundtrip(&socket, GOOD_REQUEST.as_bytes()).expect("roundtrip");
    assert!(reply.ends_with('\n'), "reply must be one line: {reply:?}");
    let value: serde_json::Value = serde_json::from_str(reply.trim_end()).expect("json");
    assert_eq!(value["request_id"], serde_json::json!("req-1"));
    assert_eq!(
        value["capabilities"],
        serde_json::to_value(tollgate_spilman_sidecar::CAPABILITIES).expect("serializable"),
        "the served list is the build's advertised one"
    );
    assert!(
        value["capabilities"]
            .as_array()
            .expect("array")
            .iter()
            .all(|c| c.as_str().is_some_and(|s| !s.starts_with("settlement."))),
        "serving a request must never imply settlement"
    );
    handle.join().expect("server thread");
}

#[test]
fn drops_a_peer_whose_credential_does_not_match() {
    let dir = TempDir::new("peer");
    let socket = dir.path().join("s.sock");
    // We connect as ourselves, but the server accepts only another uid: the
    // connection must be closed without a reply, not answered and not hung.
    // (The directory owner stays us — a server legitimately refuses a peer on
    // credential grounds even when its own filesystem ownership is correct.)
    let foreign = if own_uid() == 0 { 1 } else { 0 };
    let server = Server::bind(dir.path(), &socket, own_uid())
        .expect("bind")
        .allowing_peer_uid(foreign);
    let handle = serve_one(server);

    let mut stream = UnixStream::connect(&socket).expect("connect");
    stream.set_read_timeout(Some(IO_TIMEOUT)).expect("timeout");
    stream.write_all(GOOD_REQUEST.as_bytes()).expect("write");
    let mut line = String::new();
    // Either a clean EOF or a reset is a refusal; what must never happen is a
    // reply. The peer wrote before reading, so the kernel may legitimately
    // answer the unread bytes with RST instead of FIN.
    match BufReader::new(stream).read_line(&mut line) {
        Ok(read) => assert_eq!(read, 0, "unauthorized peer must get no reply, got {line:?}"),
        Err(error) => assert_eq!(
            error.kind(),
            std::io::ErrorKind::ConnectionReset,
            "unexpected refusal shape: {error:?}"
        ),
    }
    assert!(
        line.is_empty(),
        "unauthorized peer must get no reply, got {line:?}"
    );
    handle.join().expect("server thread");
}

#[test]
fn closes_on_an_oversize_frame_instead_of_truncating() {
    let dir = TempDir::new("oversize");
    let socket = dir.path().join("s.sock");
    let server = Server::bind(dir.path(), &socket, own_uid()).expect("bind");
    let handle = serve_one(server);

    let mut stream = UnixStream::connect(&socket).expect("connect");
    stream.set_read_timeout(Some(IO_TIMEOUT)).expect("timeout");
    // No newline: the bounded reader must refuse rather than buffer forever.
    let chunk = vec![b'x'; 4096];
    let mut written = 0usize;
    while written <= tollgate_spilman_sidecar::MAX_FRAME_BYTES {
        match stream.write_all(&chunk) {
            Ok(()) => written += chunk.len(),
            Err(_) => break,
        }
    }
    let mut rest = Vec::new();
    let _ = std::io::Read::read_to_end(&mut stream, &mut rest);

    // The property this test exists for is TRUNCATION and correlation: the
    // sidecar must not buffer forever, must not answer part of the flood as if
    // it were a request, and must not echo ids it could not read. It is
    // allowed — required, in fact — to say WHICH refusal happened, because a
    // bare close is indistinguishable from a crashed daemon. (An earlier
    // version asserted "no bytes at all"; that pinned the old silent-close
    // bug in place. The refusal is now written and then the peer is closed.)
    assert!(
        !rest.is_empty(),
        "an oversize frame must be refused with its own code, not a bare close"
    );
    let text = String::from_utf8_lossy(&rest);
    let mut lines = text.split_inclusive('\n');
    let refusal = lines.next().expect("one refusal frame").to_string();
    assert_eq!(
        lines.count(),
        0,
        "exactly one refusal frame, nothing after it: {text:?}"
    );
    let value: serde_json::Value =
        serde_json::from_str(refusal.trim_end()).expect("refusal is JSON");
    let code = value["error"].as_str().unwrap_or_default();
    assert!(
        code == "frame_too_large" || code == "invalid_request",
        "unexpected refusal code: {code:?}"
    );
    assert_eq!(
        value["request_id"], "",
        "an unreadable flood has no id to echo honestly"
    );
    handle.join().expect("server thread");
}

#[test]
fn closes_when_the_peer_disconnects() {
    let dir = TempDir::new("eof");
    let socket = dir.path().join("s.sock");
    let server = Server::bind(dir.path(), &socket, own_uid()).expect("bind");
    let handle = serve_one(server);
    {
        let stream = UnixStream::connect(&socket).expect("connect");
        drop(stream);
    }
    handle.join().expect("server must return on EOF, not block");
}

#[test]
fn responds_to_two_sequential_connections() {
    let dir = TempDir::new("sequential");
    let socket = dir.path().join("s.sock");
    let server = Server::bind(dir.path(), &socket, own_uid()).expect("bind");
    let handle = std::thread::spawn(move || {
        let mut served = 0;
        let _ = server.serve(|| {
            served += 1;
            served > 2
        });
    });
    for _ in 0..2 {
        let reply = roundtrip(&socket, GOOD_REQUEST.as_bytes()).expect("roundtrip");
        assert!(reply.contains("\"transport.v1\""), "reply was {reply:?}");
    }
    handle.join().expect("server thread");
}

#[test]
fn io_timeout_is_finite() {
    assert!(
        IO_TIMEOUT > Duration::ZERO && IO_TIMEOUT <= Duration::from_secs(30),
        "a stalled peer must not pin a worker indefinitely"
    );
}
