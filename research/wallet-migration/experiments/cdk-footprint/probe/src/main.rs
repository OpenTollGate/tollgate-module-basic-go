// Experiment probe: the smallest CDK program that actually links the wallet
// stack (cdk wallet feature + cdk-sqlite), WITHOUT the nostr stack.
//
// Purpose: (1) measure the real stripped footprint of a wallet-only CDK binary;
// (2) test whether it cross-builds for musl aarch64 and mipsel (the mipsel
// failure in T5c was nostr-relay-pool's std AtomicU64 — this probe removes it).
//
// This is OUR experiment file; it lives in the cdk workspace only to borrow the
// workspace dependency table. It is never part of a shipped binary.

use std::sync::Arc;

use cdk::cdk_database::{Error as DbError, WalletDatabase};
use cdk::nuts::CurrencyUnit;
use cdk::wallet::Wallet;
use cdk_sqlite::WalletSqliteDatabase;

#[tokio::main(flavor = "current_thread")]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let path = std::env::temp_dir().join("cdk-probe.sqlite");
    let _ = std::fs::remove_file(&path);

    let db: Arc<dyn WalletDatabase<DbError> + Send + Sync> =
        Arc::new(WalletSqliteDatabase::new(&path).await?);

    // Constructing the wallet links the wallet + sqlite stack; LTO drops the
    // rest. This is a footprint *floor*, not a full payment path.
    let _wallet = Wallet::new(
        "https://mint.example.com",
        CurrencyUnit::Sat,
        db,
        [0u8; 64],
        Some(3),
    )?;

    println!("cdk-probe ok");
    Ok(())
}
