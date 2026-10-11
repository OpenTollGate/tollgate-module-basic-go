# Wallet recovery runbook — wallet.db on flash

How to answer "is the wallet intact?" and what to do when it is not.
Scope: the bbolt `wallet.db` under `/etc/tollgate/` on router flash.
The storage-filesystem requirements (jffs2 cannot back the wallet at all —
#583) are in the README's *Wallet storage requirements* section; this
runbook starts from a wallet that initialized and is now suspect.

## Is the wallet intact?

```sh
tollgate wallet check                 # checks /etc/tollgate/wallet.db
tollgate wallet check --path FILE     # any copy, e.g. a backup
```

Read-only, safe to run alongside the live daemon; the check itself runs in
an isolated worker process, because bbolt panics or faults (SIGBUS) on
torn files instead of erroring — a worker death is itself reported as a
corruption verdict. Exit 0 = structurally consistent; 1 = problems found
or no database at the path (the message says which).

**What a clean check proves — and what it does not.** `wallet check` runs
bbolt's structural consistency check: page layout, freelist, key ordering,
bucket references. bbolt v1.4 has **no data-page checksums**, so silent
bit-rot inside leaf *values* (proofs, keyset metadata) is not detectable by
it. A clean check means "the B+tree is walkable", not "every byte is what
was written". For byte-level doubt, compare file hashes against a known
backup.

## Recovery flow

1. **Stop the service** (`/etc/init.d/tollgate stop`) — nothing below
   should race a live writer.
2. **Back up before anything else**, timestamps in the name, flash
   permitting:
   ```sh
   cp -a /etc/tollgate/wallet.db /etc/tollgate/wallet.db.bak-$(date +%Y%m%d-%H%M%S)
   ```
3. **Check the suspect file** (`tollgate wallet check --path …`).
4. **Intact** → restart the service; you are done. Keep the backup.
5. **Corrupt or doubtful** → do NOT delete the file. Move it aside and let
   the daemon re-initialize a fresh wallet:
   ```sh
   mv /etc/tollgate/wallet.db /etc/tollgate/wallet.db.corrupt-$(date +%Y%m%d-%H%M%S)
   /etc/init.d/tollgate start
   ```
   Funds recovery then happens **out of band**, never by re-feeding the
   corrupt DB:
   - Tokens exported earlier (`tollgate wallet drain` output files) re-enter
     through the normal fund flow.
   - The wallet's mnemonic restores the derivation lineage; see the
     migration tooling docs before touching either file.
6. **Keep the corrupt copy** until funds are reconciled — it is evidence,
   and partial extraction (bucket-level) may still be possible offline.

## Why the other /etc/tollgate files are not in this flow

`config.json`, `identities.json` and `install.json` are written
atomically (temp file + fsync + rename, with an in-place fallback for
bind-mounted files) and self-heal on load failure: a torn file is moved to
`/etc/tollgate/config_backups/` and factory defaults are written in its
place (#402, #505). The self-heal is also the hazard — a factory
`identities.json` silently swaps the router's Nostr identity and payout
addresses — so the atomic writes exist to keep that path from ever being
taken by a power cut.

## See also

- README — *Wallet storage requirements* (jffs2/ext4/f2fs/ubifs support).
- #505 — the flash-durability audit this tooling came out of.
- The power-loss campaign and write-amplification budgets named in #505
  are tracked there; this runbook covers the operator side.
