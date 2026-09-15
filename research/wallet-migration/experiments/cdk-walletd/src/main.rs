// cdk-walletd — experimental wallet sidecar daemon.
//
// Speaks the TollGate wallet RPC (newline-delimited JSON over an AF_UNIX
// socket) backed by the CDK wallet. This is the daemon half of the
// process-isolated wallet architecture: the Go service talks to it through the
// WalletPort sidecar client, so no Cashu library is linked into the service and
// CGO stays off.
//
// Scope (experiment): info / decode_token / get_balance / get_all_mint_balances
// / request_mint_quote / mint_quote_state / mint_tokens / receive / shutdown are
// implemented. send/drain/melt*/send_with_overpayment return "not implemented"
// for now (they are saga-based). See the wallet-migration research branch.

use std::collections::HashMap;
use std::str::FromStr;
use std::sync::Arc;

use cdk::amount::SplitTarget;
use cdk::cdk_database::{Error as DbError, WalletDatabase};
use cdk::nuts::{CurrencyUnit, MeltQuoteState, MintQuoteState, PaymentMethod, Token};
use cdk::wallet::types::SendKind;
use cdk::wallet::{ReceiveOptions, SendOptions, Wallet};
use cdk::Amount;
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

fn amount_to_u64(a: &Amount) -> u64 {
    u64::from(*a)
}

// Map CDK's quote state to the WalletPort integer (Unpaid=0..Unknown=4).
fn state_code(s: MintQuoteState) -> i32 {
    match s {
        MintQuoteState::Unpaid => 0,
        MintQuoteState::Paid => 1,
        MintQuoteState::Issued => 2,
        _ => 4,
    }
}

fn melt_state_code(s: MeltQuoteState) -> i32 {
    match s {
        MeltQuoteState::Unpaid => 0,
        MeltQuoteState::Paid => 1,
        MeltQuoteState::Pending => 3,
        _ => 4,
    }
}

fn p_str<'a>(params: &'a serde_json::Value, key: &str) -> &'a str {
    params.get(key).and_then(|v| v.as_str()).unwrap_or("")
}

async fn dispatch(wallet: &Wallet, mint: &str, req: &Request) -> (Response, bool) {
    let p = &req.params;
    match req.method.as_str() {
        "info" => (Response::ok(req.id, manifest()), false),

        "decode_token" => {
            let s = p_str(p, "token");
            match Token::from_str(s) {
                Ok(tok) => {
                    let amount = tok.value().map(|a| amount_to_u64(&a)).unwrap_or(0);
                    let m = tok.mint_url().map(|u| u.to_string()).unwrap_or_default();
                    (Response::ok(req.id, serde_json::json!({"token": s, "mint": m, "amount": amount})), false)
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

        "receive" => {
            let s = p_str(p, "token");
            match wallet.receive(s, ReceiveOptions::default()).await {
                Ok(a) => (Response::ok(req.id, serde_json::json!(amount_to_u64(&a))), false),
                Err(e) => (Response::err(req.id, format!("receive: {e}")), false),
            }
        }

        "request_mint_quote" => {
            let amount = p.get("amount").and_then(|v| v.as_u64()).unwrap_or(0);
            match wallet
                .mint_quote(PaymentMethod::BOLT11, Some(Amount::from(amount)), None, None)
                .await
            {
                Ok(q) => {
                    let amt = q.amount.map(|a| a.to_u64()).unwrap_or(0);
                    (Response::ok(req.id, serde_json::json!({
                        "quote_id": q.id, "request": q.request,
                        "state": state_code(q.state), "amount": amt, "expiry": q.expiry,
                    })), false)
                }
                Err(e) => (Response::err(req.id, format!("request_mint_quote: {e}")), false),
            }
        }

        "mint_quote_state" => {
            let qid = p_str(p, "quote_id").to_string();
            match wallet.check_mint_quote_status(&qid).await {
                Ok(q) => (Response::ok(req.id, serde_json::json!(state_code(q.state))), false),
                Err(e) => (Response::err(req.id, format!("mint_quote_state: {e}")), false),
            }
        }

        "mint_tokens" => {
            let qid = p_str(p, "quote_id").to_string();
            match wallet.mint(&qid, SplitTarget::None, None).await {
                Ok(proofs) => {
                    let sum: u64 = proofs.iter().map(|pr| pr.amount.to_u64()).sum();
                    (Response::ok(req.id, serde_json::json!(sum)), false)
                }
                Err(e) => (Response::err(req.id, format!("mint_tokens: {e}")), false),
            }
        }

        "send" => {
            let amount = p.get("amount").and_then(|v| v.as_u64()).unwrap_or(0);
            let include_fee = p.get("include_fees").and_then(|v| v.as_bool()).unwrap_or(false);
            let opts = SendOptions { include_fee, ..Default::default() };
            match wallet.prepare_send(Amount::from(amount), opts).await {
                Ok(prepared) => match prepared.confirm(None).await {
                    Ok(tok) => (Response::ok(req.id, serde_json::json!({
                        "token": tok.to_string(), "mint": mint, "amount": amount,
                    })), false),
                    Err(e) => (Response::err(req.id, format!("send(confirm): {e}")), false),
                },
                Err(e) => (Response::err(req.id, format!("send(prepare): {e}")), false),
            }
        }

        "drain" => match wallet.total_balance().await {
            Ok(bal) => {
                let amount = amount_to_u64(&bal);
                let opts = SendOptions::default();
                match wallet.prepare_send(Amount::from(amount), opts).await {
                    Ok(prepared) => match prepared.confirm(None).await {
                        Ok(tok) => (Response::ok(req.id, serde_json::json!({
                            "token": tok.to_string(), "mint": mint, "amount": amount,
                        })), false),
                        Err(e) => (Response::err(req.id, format!("drain(confirm): {e}")), false),
                    },
                    Err(e) => (Response::err(req.id, format!("drain(prepare): {e}")), false),
                }
            }
            Err(e) => (Response::err(req.id, format!("drain(balance): {e}")), false),
        },

        "request_melt_quote" => {
            let invoice = p_str(p, "invoice").to_string();
            match wallet.melt_quote(PaymentMethod::BOLT11, invoice, None, None).await {
                Ok(q) => (Response::ok(req.id, serde_json::json!({
                    "quote_id": q.id, "amount": q.amount.to_u64(),
                    "fee_reserve": q.fee_reserve.to_u64(),
                    "state": melt_state_code(q.state), "expiry": q.expiry,
                })), false),
                Err(e) => (Response::err(req.id, format!("request_melt_quote: {e}")), false),
            }
        }

        "melt" => {
            let qid = p_str(p, "quote_id").to_string();
            match wallet.prepare_melt(&qid, HashMap::new()).await {
                Ok(prepared) => match prepared.confirm().await {
                    Ok(m) => (Response::ok(req.id, serde_json::json!({
                        "quote_id": m.quote_id(),
                        "paid": m.state() == MeltQuoteState::Paid,
                        "preimage": m.payment_proof().unwrap_or(""),
                    })), false),
                    Err(e) => (Response::err(req.id, format!("melt(confirm): {e}")), false),
                },
                Err(e) => (Response::err(req.id, format!("melt(prepare): {e}")), false),
            }
        }

        "send_with_overpayment" => {
            let amount = p.get("amount").and_then(|v| v.as_u64()).unwrap_or(0);
            let abs = p.get("max_overpayment_absolute").and_then(|v| v.as_u64()).unwrap_or(0);
            // Map overpayment tolerance to CDK's online-tolerance send kind.
            let opts = SendOptions {
                send_kind: SendKind::OnlineTolerance(Amount::from(abs)),
                ..Default::default()
            };
            match wallet.prepare_send(Amount::from(amount), opts).await {
                Ok(prepared) => match prepared.confirm(None).await {
                    Ok(tok) => (Response::ok(req.id, serde_json::json!(tok.to_string())), false),
                    Err(e) => (Response::err(req.id, format!("send_with_overpayment(confirm): {e}")), false),
                },
                Err(e) => (Response::err(req.id, format!("send_with_overpayment(prepare): {e}")), false),
            }
        }

        "melt_to_lightning" => {
            let lnurl = p_str(p, "lnurl").to_string();
            let target = p.get("target_amount").and_then(|v| v.as_u64()).unwrap_or(0);
            let max_cost = p.get("max_cost").and_then(|v| v.as_u64()).unwrap_or(0);
            let amount_msat = target.saturating_mul(1000);
            match wallet.melt_lightning_address_quote(&lnurl, Amount::from(amount_msat)).await {
                Ok(q) => {
                    if max_cost > 0 && q.fee_reserve.to_u64() > max_cost {
                        return (Response::err(req.id, format!(
                            "melt_to_lightning: fee reserve {} exceeds max_cost {}",
                            q.fee_reserve.to_u64(), max_cost)), false);
                    }
                    match wallet.prepare_melt(&q.id, HashMap::new()).await {
                        Ok(prepared) => match prepared.confirm().await {
                            Ok(m) if melt_state_code(m.state()) == 1 => (Response::ok(req.id, serde_json::json!({
                                "paid": true, "preimage": m.payment_proof().unwrap_or(""),
                            })), false),
                            Ok(m) => (Response::err(req.id, format!("melt_to_lightning: state {}", melt_state_code(m.state()))), false),
                            Err(e) => (Response::err(req.id, format!("melt_to_lightning(confirm): {e}")), false),
                        },
                        Err(e) => (Response::err(req.id, format!("melt_to_lightning(prepare): {e}")), false),
                    }
                }
                Err(e) => (Response::err(req.id, format!("melt_to_lightning(resolve): {e}")), false),
            }
        }

        "shutdown" => (Response::ok(req.id, serde_json::json!({})), true),

        other => (Response::err(req.id, format!("not implemented: {other}")), false),
    }
}

async fn handle_conn(r: UnixStream, wallet: Arc<Wallet>, mint: String) {
    let (rd, mut w) = r.into_split();
    let mut lines = BufReader::new(rd).lines();
    while let Ok(Some(line)) = lines.next_line().await {
        if line.trim().is_empty() {
            continue;
        }
        let req: Request = match serde_json::from_str(&line) {
            Ok(r) => r,
            Err(e) => {
                let _ = write_line(&mut w, &Response::err(0, format!("bad request: {e}"))).await;
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

async fn open_wallet(work_dir: &str, mint: &str, seed: [u8; 64]) -> Result<Wallet, Box<dyn std::error::Error>> {
    std::fs::create_dir_all(work_dir)?;
    let db_path = std::path::Path::new(work_dir).join("cdk-walletd.sqlite");
    let db: Arc<dyn WalletDatabase<DbError> + Send + Sync> =
        Arc::new(WalletSqliteDatabase::new(&db_path).await?);
    let wallet = Wallet::new(mint, CurrencyUnit::Sat, db, seed, Some(3))?;
    Ok(wallet)
}

// seed_from_mnemonic derives the 64-byte wallet seed from a BIP-39 mnemonic.
// A real mnemonic produces a valid signing key (needed for NUT-20 in-flight
// quotes); an all-zero seed does not.
fn seed_from_mnemonic(phrase: &str) -> Result<[u8; 64], Box<dyn std::error::Error>> {
    let m = bip39::Mnemonic::parse(phrase.trim())?;
    Ok(m.to_seed(""))
}

#[tokio::main(flavor = "multi_thread", worker_threads = 2)]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let mut socket = String::from("/tmp/cdk-walletd.sock");
    let mut work_dir = String::from("/tmp/cdk-walletd");
    let mut mint = String::from("https://mint.example.com");
    let mut mnemonic = String::new();

    let mut args = std::env::args().skip(1);
    while let Some(a) = args.next() {
        match a.as_str() {
            "--socket" => socket = args.next().unwrap_or(socket),
            "--work-dir" => work_dir = args.next().unwrap_or(work_dir),
            "--mint" => mint = args.next().unwrap_or(mint),
            "--mnemonic" => mnemonic = args.next().unwrap_or(mnemonic),
            _ => {}
        }
    }

    let seed: [u8; 64] = if mnemonic.is_empty() {
        // Generate a fresh random seed: a valid signing key (for NUT-20) and,
        // crucially, fresh deterministic outputs (NUT-13) so re-runs against the
        // same mint do not collide on "outputs have already been signed".
        // Print the generated mnemonic so a caller can reuse it across restarts
        // (e.g. crash-recovery tests that must continue the same wallet).
        let m = bip39::Mnemonic::generate(12)?;
        eprintln!("cdk-walletd: generated mnemonic: {m}");
        m.to_seed("")
    } else {
        seed_from_mnemonic(&mnemonic)?
    };

    let wallet = Arc::new(open_wallet(&work_dir, &mint, seed).await?);

    // Crash recovery: reconcile any sagas / pending proofs left by a previous
    // crash before serving, so interrupted swaps do not strand funds.
    match wallet.recover_incomplete_sagas().await {
        Ok(_) => eprintln!("cdk-walletd: recovered incomplete sagas"),
        Err(e) => eprintln!("cdk-walletd: saga recovery failed: {e}"),
    }
    match wallet.check_all_pending_proofs().await {
        Ok(a) => eprintln!("cdk-walletd: reconciled pending proofs ({})", u64::from(a)),
        Err(e) => eprintln!("cdk-walletd: pending-proof reconciliation failed: {e}"),
    }
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
