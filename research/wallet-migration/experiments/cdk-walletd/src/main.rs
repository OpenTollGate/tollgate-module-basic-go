// cdk-walletd — experimental wallet sidecar daemon.
//
// Speaks the TollGate wallet RPC (newline-delimited JSON over an AF_UNIX
// socket) backed by the CDK wallet. This is the daemon half of the
// process-isolated wallet architecture: the Go service talks to it through the
// WalletPort sidecar client, so no Cashu library is linked into the service and
// CGO stays off.
//
// Scope (experiment): `info`, `decode_token`, `get_balance`,
// `get_all_mint_balances`, `shutdown` are implemented; the value-moving methods
// return "not implemented" for now. See the wallet-migration research branch.

use std::str::FromStr;
use std::sync::Arc;

use cdk::cdk_database::{Error as DbError, WalletDatabase};
use cdk::nuts::{CurrencyUnit, Token};
use cdk::wallet::Wallet;
use cdk_sqlite::WalletSqliteDatabase;
use serde::{Deserialize, Serialize};
use tokio::io::{AsyncBufReadExt, AsyncWriteExt, BufReader};
use tokio::net::{UnixListener, UnixStream};

#[derive(Deserialize)]
struct Request {
    #[serde(default)]
    id: u64,
    method: String,
    #[serde(default)]
    params: serde_json::Value,
}

#[derive(Serialize)]
struct Response {
    id: u64,
    ok: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    result: Option<serde_json::Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    error: Option<String>,
}

impl Response {
    fn ok(id: u64, result: serde_json::Value) -> Self {
        Response { id, ok: true, result: Some(result), error: None }
    }
    fn err(id: u64, error: impl Into<String>) -> Self {
        Response { id, ok: false, result: None, error: Some(error.into()) }
    }
}

fn manifest() -> serde_json::Value {
    serde_json::json!({
        "backend": "cdk",
        "kind": "sidecar",
        "version": env!("CARGO_PKG_VERSION"),
        "arches": ["aarch64_cortex-a53", "mipsel_24kc"],
        "size_bytes": { "aarch64_cortex-a53": 4346264, "mipsel_24kc": 9739104 },
        "storage": { "model": "sqlite", "writes_per_payment": "o(1)",
                     "crash_consistent": true, "seed_at_rest": "plaintext" },
        "contract": { "nut_07": true, "nut_09": true, "restore": true, "cgo": false },
        "licence": "MIT OR Apache-2.0"
    })
}

async fn dispatch(wallet: &Wallet, mint: &str, req: &Request) -> (Response, bool) {
    match req.method.as_str() {
        "info" => (Response::ok(req.id, manifest()), false),
        "decode_token" => {
            let s = req.params.get("token").and_then(|v| v.as_str()).unwrap_or("");
            match Token::from_str(s) {
                Ok(tok) => {
                    let amount = tok.value().map(|a| amount_to_u64(&a)).unwrap_or(0);
                    let m = tok.mint_url().map(|u| u.to_string()).unwrap_or_default();
                    (Response::ok(req.id, serde_json::json!({
                        "token": s, "mint": m, "amount": amount,
                    })), false)
                }
                Err(e) => (Response::err(req.id, format!("decode_token: {e}")), false),
            }
        }
        "get_balance" => match wallet.total_balance().await {
            Ok(a) => (Response::ok(req.id, serde_json::json!(amount_to_u64(&a))), false),
            Err(e) => (Response::err(req.id, format!("get_balance: {e}")), false),
        },
        "get_all_mint_balances" => match wallet.total_balance().await {
            Ok(a) => (Response::ok(req.id, serde_json::json!({ mint: amount_to_u64(&a) })), false),
            Err(e) => (Response::err(req.id, format!("get_all_mint_balances: {e}")), false),
        },
        "shutdown" => (Response::ok(req.id, serde_json::json!({})), true),
        other => (Response::err(req.id, format!("not implemented: {other}")), false),
    }
}

fn amount_to_u64(a: &cdk::Amount) -> u64 {
    u64::from(*a)
}

async fn handle_conn(mut stream: UnixStream, wallet: Arc<Wallet>, mint: String) {
    let (r, mut w) = stream.split();
    let mut lines = BufReader::new(r).lines();
    while let Ok(Some(line)) = lines.next_line().await {
        if line.trim().is_empty() {
            continue;
        }
        let req: Request = match serde_json::from_str(&line) {
            Ok(r) => r,
            Err(e) => {
                let resp = Response::err(0, format!("bad request: {e}"));
                let _ = write_line(&mut w, &resp).await;
                continue;
            }
        };
        let (resp, stop) = dispatch(&wallet, &mint, &req).await;
        if write_line(&mut w, &resp).await.is_err() || stop {
            break;
        }
    }
}

async fn write_line<W: AsyncWriteExt + Unpin>(w: &mut W, resp: &Response) -> std::io::Result<()> {
    let mut buf = serde_json::to_vec(resp).unwrap_or_default();
    buf.push(b'\n');
    w.write_all(&buf).await?;
    w.flush().await
}

async fn open_wallet(work_dir: &str, mint: &str) -> Result<Wallet, Box<dyn std::error::Error>> {
    std::fs::create_dir_all(work_dir)?;
    let db_path = std::path::Path::new(work_dir).join("cdk-walletd.sqlite");
    let db: Arc<dyn WalletDatabase<DbError> + Send + Sync> =
        Arc::new(WalletSqliteDatabase::new(&db_path).await?);
    let wallet = Wallet::new(mint, CurrencyUnit::Sat, db, [0u8; 64], Some(3))?;
    Ok(wallet)
}

#[tokio::main(flavor = "multi_thread", worker_threads = 2)]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let mut socket = String::from("/tmp/cdk-walletd.sock");
    let mut work_dir = String::from("/tmp/cdk-walletd");
    let mut mint = String::from("https://mint.example.com");

    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        match a.as_str() {
            "--socket" => socket = args.next().unwrap_or(socket),
            "--work-dir" => work_dir = args.next().unwrap_or(work_dir),
            "--mint" => mint = args.next().unwrap_or(mint),
            _ => {}
        }
    }

    let wallet = Arc::new(open_wallet(&work_dir, &mint).await?);
    let _ = std::fs::remove_file(&socket);
    let listener = UnixListener::bind(&socket)?;
    eprintln!("cdk-walletd: listening on {socket} (mint={mint})");

    loop {
        let (stream, _) = listener.accept().await?;
        let w = wallet.clone();
        let m = mint.clone();
        tokio::spawn(async move { handle_conn(stream, w, m).await });
    }
}
