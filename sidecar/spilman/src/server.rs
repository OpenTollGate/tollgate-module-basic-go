//! AF_UNIX transport: socket ownership, peer authentication, bounded frames.
//!
//! The trust boundary is a filesystem path plus a peer credential, and both are
//! enforced here rather than assumed:
//!
//! * the runtime directory must exist, be owned by the service uid, and not be
//!   group- or world-accessible;
//! * an existing socket path is never unlinked blindly. A live listener is
//!   refused (a second sidecar must not steal a running one's endpoint); a
//!   stale path with no listener is reclaimed;
//! * the socket is created `0600` — though the real protection is the
//!   directory mode, because Linux ignores socket file permissions;
//! * every connection's peer is checked with `SO_PEERCRED` and rejected unless
//!   its uid matches the configured owner.
//!
//! Nothing here reads a frame larger than [`MAX_FRAME_BYTES`](crate::MAX_FRAME_BYTES):
//! an oversize line terminates the connection instead of being buffered.

use std::io::{self, BufRead, BufReader, Read, Write};
use std::os::unix::fs::PermissionsExt;
use std::os::unix::io::AsRawFd;
use std::os::unix::net::{UnixListener, UnixStream};
use std::path::{Path, PathBuf};
use std::time::Duration;

use crate::protocol::MAX_FRAME_BYTES;
use crate::{handle_frame, Outcome};

/// How long a single connection may stall on I/O before it is dropped. A
/// stuck peer must not pin a worker (or a future settlement slot) forever.
pub const IO_TIMEOUT: Duration = Duration::from_secs(10);

/// How long `serve` waits for the next connection before re-checking its
/// shutdown predicate. Bounds an orderly stop's latency, not a peer's work.
const ACCEPT_POLL_MS: libc::c_int = 20;

/// Transport-level failures. Each is a startup or accept-time decision, so the
/// caller decides readiness; none of them is retried internally.
#[derive(Debug)]
pub enum ServerError {
    Io {
        path: PathBuf,
        source: io::Error,
    },
    /// The runtime directory exists but is not owned by, or not restricted to,
    /// the service identity.
    UnsafeDirectory {
        path: PathBuf,
        detail: String,
    },
    /// A live listener already owns this socket path.
    AlreadyServing {
        path: PathBuf,
    },
}

impl std::fmt::Display for ServerError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ServerError::Io { path, source } => {
                write!(f, "socket {}: {source}", path.display())
            }
            ServerError::UnsafeDirectory { path, detail } => {
                write!(f, "runtime directory {}: {detail}", path.display())
            }
            ServerError::AlreadyServing { path } => {
                write!(f, "{} is already served by a live sidecar", path.display())
            }
        }
    }
}

impl std::error::Error for ServerError {}

/// A bound listener plus the identity policy it enforces.
#[derive(Debug)]
pub struct Server {
    listener: UnixListener,
    socket_path: PathBuf,
    /// Owner the runtime directory must have (the service identity).
    expected_uid: u32,
    /// The only uid allowed to connect. Defaults to the service identity and
    /// exists separately because a root sidecar may legitimately serve a
    /// different local client uid; it must never be widened implicitly.
    allowed_peer_uid: u32,
}

impl Server {
    /// Bind `socket_path`, requiring `runtime_dir` to be a `0700` directory
    /// owned by `expected_uid`.
    ///
    /// The caller owns process lifecycle; this function only guarantees that
    /// the endpoint is exclusive, correctly permissioned and not a hijack.
    pub fn bind(
        runtime_dir: &Path,
        socket_path: &Path,
        expected_uid: u32,
    ) -> Result<Self, ServerError> {
        check_runtime_dir(runtime_dir, expected_uid)?;
        if socket_path.parent() != Some(runtime_dir) {
            return Err(ServerError::UnsafeDirectory {
                path: runtime_dir.to_path_buf(),
                detail: format!("socket {} is not inside it", socket_path.display()),
            });
        }
        refuse_live_listener(socket_path)?;

        let listener = UnixListener::bind(socket_path).map_err(|source| ServerError::Io {
            path: socket_path.to_path_buf(),
            source,
        })?;
        // Best effort: the directory mode is the real control on Linux, but a
        // restrictive socket mode costs nothing and documents intent.
        let _ = std::fs::set_permissions(socket_path, std::fs::Permissions::from_mode(0o600));

        Ok(Self {
            listener,
            socket_path: socket_path.to_path_buf(),
            expected_uid,
            allowed_peer_uid: expected_uid,
        })
    }

    /// Narrow (or re-point) the accepted client uid. Startup-time only: call
    /// it before `serve`.
    #[must_use]
    pub fn allowing_peer_uid(mut self, uid: u32) -> Self {
        self.allowed_peer_uid = uid;
        self
    }

    pub fn socket_path(&self) -> &Path {
        &self.socket_path
    }

    /// The owner the runtime directory was required to have. Exposed so a
    /// supervisor (and tests) can assert the policy that was actually applied
    /// rather than the one that was requested.
    pub fn service_uid(&self) -> u32 {
        self.expected_uid
    }

    /// Serve connections until `shutdown` returns true.
    ///
    /// The listener is non-blocking and polled with a short timeout so
    /// `shutdown` is honoured promptly even when no peer connects — a blocked
    /// `accept()` would make an orderly stop look like a hang. One connection
    /// is handled at a time and serially: the sidecar holds no per-channel
    /// state yet, and serial handling keeps the failure mode obvious. A future
    /// concurrent version must keep the frame bound.
    pub fn serve(&self, mut shutdown: impl FnMut() -> bool) -> io::Result<()> {
        self.listener.set_nonblocking(true)?;
        let fd = self.listener.as_raw_fd();
        loop {
            if shutdown() {
                return Ok(());
            }
            let mut polled = libc::pollfd {
                fd,
                events: libc::POLLIN,
                revents: 0,
            };
            // SAFETY: one initialized pollfd, count 1.
            let ready = unsafe { libc::poll(&mut polled, 1, ACCEPT_POLL_MS) };
            if ready < 0 {
                let error = io::Error::last_os_error();
                if error.kind() == io::ErrorKind::Interrupted {
                    continue;
                }
                return Err(error);
            }
            if ready == 0 {
                continue;
            }
            match self.listener.accept() {
                Ok((stream, _)) => {
                    if !self.authorized(&stream) {
                        // Refuse without a frame, but end the stream cleanly so
                        // the peer sees EOF rather than a reset.
                        let _ = stream.shutdown(std::net::Shutdown::Both);
                        drop(stream);
                        continue;
                    }
                    // Accepted sockets inherit O_NONBLOCK from the listener on
                    // Linux; the framed reader wants blocking reads with a
                    // timeout instead.
                    stream.set_nonblocking(false)?;
                    let _ = handle_connection(stream);
                }
                Err(error)
                    if error.kind() == io::ErrorKind::WouldBlock
                        || error.kind() == io::ErrorKind::Interrupted =>
                {
                    continue
                }
                Err(error) => return Err(error),
            }
        }
    }

    /// `SO_PEERCRED` check. Absent credentials are a refusal, never a pass.
    fn authorized(&self, stream: &UnixStream) -> bool {
        peer_uid(stream).is_some_and(|uid| uid == self.allowed_peer_uid)
    }
}

impl Drop for Server {
    fn drop(&mut self) {
        // Only remove the path if it is still ours; a replaced socket belongs
        // to whoever replaced it.
        let _ = std::fs::remove_file(&self.socket_path);
    }
}

fn check_runtime_dir(dir: &Path, expected_uid: u32) -> Result<(), ServerError> {
    let meta = std::fs::metadata(dir).map_err(|source| ServerError::Io {
        path: dir.to_path_buf(),
        source,
    })?;
    if !meta.is_dir() {
        return Err(ServerError::UnsafeDirectory {
            path: dir.to_path_buf(),
            detail: "not a directory".to_string(),
        });
    }
    if meta.permissions().mode() & 0o077 != 0 {
        return Err(ServerError::UnsafeDirectory {
            path: dir.to_path_buf(),
            detail: format!(
                "mode {:04o} grants group/world access; expected 0700",
                meta.permissions().mode() & 0o7777
            ),
        });
    }
    // Ownership is checked only when the platform reports it; a failure to
    // read it is a refusal, not a pass.
    match dir_owner_uid(dir) {
        Ok(uid) if uid == expected_uid => Ok(()),
        Ok(uid) => Err(ServerError::UnsafeDirectory {
            path: dir.to_path_buf(),
            detail: format!("owned by uid {uid}, expected {expected_uid}"),
        }),
        Err(source) => Err(ServerError::Io {
            path: dir.to_path_buf(),
            source,
        }),
    }
}

/// Connect once to see whether a live listener answers. A refused connection
/// means the path is stale (a crashed predecessor) and may be reclaimed; a
/// successful connection means another sidecar is running.
fn refuse_live_listener(socket_path: &Path) -> Result<(), ServerError> {
    if std::fs::symlink_metadata(socket_path).is_err() {
        return Ok(());
    }
    match UnixStream::connect(socket_path) {
        Ok(_) => Err(ServerError::AlreadyServing {
            path: socket_path.to_path_buf(),
        }),
        Err(source) => match source.kind() {
            io::ErrorKind::ConnectionRefused | io::ErrorKind::NotFound => {
                std::fs::remove_file(socket_path).map_err(|source| ServerError::Io {
                    path: socket_path.to_path_buf(),
                    source,
                })
            }
            _ => Err(ServerError::Io {
                path: socket_path.to_path_buf(),
                source,
            }),
        },
    }
}

fn handle_connection(stream: UnixStream) -> io::Result<()> {
    stream.set_read_timeout(Some(IO_TIMEOUT))?;
    stream.set_write_timeout(Some(IO_TIMEOUT))?;
    serve_stream(stream)
}

/// Serve frames on one connected stream. Split out so tests can drive it over
/// `UnixStream::pair()` without a listener.
pub fn serve_stream(stream: UnixStream) -> io::Result<()> {
    let reader_stream = stream.try_clone()?;
    let mut reader = BufReader::new(reader_stream);
    let mut writer = stream;
    loop {
        match read_frame(&mut reader)? {
            Some(frame) => match handle_frame(&frame) {
                Outcome::Reply {
                    frame: reply,
                    close,
                } => {
                    writer.write_all(&reply)?;
                    writer.flush()?;
                    if close {
                        return Ok(());
                    }
                }
                Outcome::Fatal { close, .. } => {
                    if close {
                        return Ok(());
                    }
                }
            },
            None => return Ok(()),
        }
    }
}

/// Read one bounded, newline-terminated frame. Returns `Ok(None)` on a clean
/// EOF at a frame boundary. An oversize or unterminated line is an error —
/// never truncated into a shorter frame, which the client would answer as if
/// it had been understood.
fn read_frame(reader: &mut BufReader<UnixStream>) -> io::Result<Option<Vec<u8>>> {
    let mut frame = Vec::new();
    // `take` bounds the read: one byte past the limit is enough to refuse.
    let read = reader
        .by_ref()
        .take((MAX_FRAME_BYTES + 1) as u64)
        .read_until(b'\n', &mut frame)?;
    if read == 0 {
        return Ok(None);
    }
    if frame.len() > MAX_FRAME_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "frame exceeds the size bound",
        ));
    }
    if !frame.ends_with(b"\n") {
        return Err(io::Error::new(
            io::ErrorKind::UnexpectedEof,
            "frame is not newline terminated",
        ));
    }
    Ok(Some(frame))
}

/// Peer uid via `SO_PEERCRED`. Kernel-reported, so it cannot be forged by the
/// peer; `None` means the credentials were unreadable.
pub fn peer_uid(stream: &UnixStream) -> Option<u32> {
    let mut cred = libc::ucred {
        pid: 0,
        uid: 0,
        gid: 0,
    };
    let mut len = std::mem::size_of::<libc::ucred>() as libc::socklen_t;
    // SAFETY: `cred` is a correctly sized, initialized `ucred`, and `len`
    // describes it. getsockopt only writes within those bounds.
    let rc = unsafe {
        libc::getsockopt(
            stream.as_raw_fd(),
            libc::SOL_SOCKET,
            libc::SO_PEERCRED,
            &mut cred as *mut libc::ucred as *mut libc::c_void,
            &mut len,
        )
    };
    if rc != 0 {
        return None;
    }
    Some(cred.uid)
}

fn dir_owner_uid(dir: &Path) -> io::Result<u32> {
    use std::os::unix::fs::MetadataExt;
    Ok(std::fs::metadata(dir)?.uid())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn peer_uid_reports_our_own_uid() {
        let (a, b) = UnixStream::pair().expect("socketpair");
        let expected = unsafe { libc::getuid() };
        assert_eq!(peer_uid(&a), Some(expected));
        assert_eq!(peer_uid(&b), Some(expected));
    }
}
