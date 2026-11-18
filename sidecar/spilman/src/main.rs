//! `tollgate-spilman-sidecar` — the Spilman protocol sidecar binary.
//!
//! Usage: `tollgate-spilman-sidecar --socket <path> [--uid <uid>]`
//!
//! The binary is deliberately minimal: it binds one AF_UNIX socket, serves the
//! capabilities-only v1 contract, and exits on SIGTERM/SIGINT. It has no
//! configuration file, no mint access, no wallet access and no settlement
//! state. Readiness means "the socket is bound and answering `capabilities`",
//! and that must never be read as "settlement is available".

use std::path::PathBuf;
use std::process::ExitCode;
use std::sync::atomic::{AtomicBool, Ordering};

use tollgate_spilman_sidecar::server::{Server, ServerError};

/// Set from the signal handler; read between connections by the serve loop.
static SHUTDOWN: AtomicBool = AtomicBool::new(false);

extern "C" fn on_signal(_: libc::c_int) {
    SHUTDOWN.store(true, Ordering::Relaxed);
}

fn usage() -> &'static str {
    "usage: tollgate-spilman-sidecar --socket <path> [--uid <uid>]"
}

fn main() -> ExitCode {
    let mut socket: Option<PathBuf> = None;
    let mut uid: Option<u32> = None;

    let mut args = std::env::args_os().skip(1);
    while let Some(arg) = args.next() {
        match arg.to_string_lossy().as_ref() {
            "--socket" => match args.next() {
                Some(value) => socket = Some(PathBuf::from(value)),
                None => {
                    eprintln!("{}", usage());
                    return ExitCode::from(2);
                }
            },
            "--uid" => match args.next().and_then(|v| v.to_str()?.parse::<u32>().ok()) {
                Some(value) => uid = Some(value),
                None => {
                    eprintln!("{}", usage());
                    return ExitCode::from(2);
                }
            },
            "--help" | "-h" => {
                println!("{}", usage());
                return ExitCode::SUCCESS;
            }
            other => {
                eprintln!("unknown argument {other:?}\n{}", usage());
                return ExitCode::from(2);
            }
        }
    }

    let socket = match socket {
        Some(path) => path,
        None => {
            eprintln!("{}", usage());
            return ExitCode::from(2);
        }
    };
    // The expected peer/owner identity defaults to this process's own uid: a
    // sidecar that cannot say who it is cannot authenticate anyone.
    let expected_uid = uid.unwrap_or_else(|| unsafe { libc::getuid() });
    let runtime_dir = match socket.parent() {
        Some(dir) if !dir.as_os_str().is_empty() => dir.to_path_buf(),
        _ => {
            eprintln!("--socket needs a path with a parent directory");
            return ExitCode::from(2);
        }
    };

    let server = match Server::bind(&runtime_dir, &socket, expected_uid) {
        Ok(server) => server,
        Err(ServerError::AlreadyServing { .. }) => {
            // A live sidecar owns this endpoint. Do not fight it.
            eprintln!("refusing to start: socket is already served");
            return ExitCode::from(3);
        }
        Err(error) => {
            eprintln!("refusing to start: {error}");
            return ExitCode::from(1);
        }
    };

    // The process is expected to be supervised (procd/systemd), which restarts
    // it; the handler exists so an orderly stop removes its socket path instead
    // of leaving a stale file for the next bind to reclaim.
    install_signal_handlers();

    if let Err(error) = server.serve(|| SHUTDOWN.load(Ordering::Relaxed)) {
        eprintln!("sidecar transport error: {error}");
        return ExitCode::from(1);
    }
    ExitCode::SUCCESS
}

fn install_signal_handlers() {
    // SAFETY: `on_signal` only stores into a static AtomicBool, which is
    // async-signal-safe; installation happens once, before serving.
    unsafe {
        libc::signal(libc::SIGTERM, on_signal as *const () as libc::sighandler_t);
        libc::signal(libc::SIGINT, on_signal as *const () as libc::sighandler_t);
    }
}
