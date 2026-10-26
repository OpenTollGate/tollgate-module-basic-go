# nut19-restore-research — research writeup (NUT-09 probe + lane rc1-866)

Provenance: produced 2026-10-11 during the PR #793 crash-lane follow-up —
the NUT-09/NUT-19 replay-window research behind tollgate issues #866/#867
and the gonuts-tollgate restore-fallback PR #40. Paths under `/root/src/…`
refer to the lab host's working trees. GitHub artifacts: issues #866 and
#867, gonuts-tollgate PR #40 (implementation + dual-mint validation
comments with full run logs).

---

# Beyond the replay window: what a TollGate operator can do about swap intents stuck past a mint's NUT-19 TTL

Research writeup for the `OpenTollGate/tollgate-module-basic-go` PR #793 crash lane.
Scope: swap-intents that journal fine, replay as byte-identical `/v1/swap` POSTs, but are
refused by cdk-mintd with `HTTP 400 {"code":11001,"detail":"Token Already Spent"}` once the
mint's NUT-19 response-cache TTL has expired — and what an operator can actually do about them.

**Evidence conventions.** cdk claims cite the local authoritative tree `/root/src/cdk-0.18.1`
(workspace version `0.18.1`, root `Cargo.toml:43`) as `cdk-0.18.1/<path>:<line>`, and the
upstream `v0.17.6` git tag (the version the lab pins) as `cdk@v0.17.6/<path>` where fetched.
Lab/PR claims cite `/root/src/cdk-replay-lab` and `/root/src/rel-793-*`. Spec and other-mint
claims cite GitHub URLs. Anything not backed by one of these is marked **UNVERIFIED**.

---

## 0. Executive summary

1. **The stuck intents are recoverable in principle — the mechanism exists today.** NUT-09
   `/v1/restore` is specced *specifically* for "recovering the response of an interrupted swap
   request" (NUT-09 spec, link below). cdk-mintd implements `/v1/restore` **unconditionally,
   by default**, backed by the mint's **persistent database** (sqlite/postgres blind-signature
   table), **not** by the 60-second response cache. The recovery is not TTL-bounded.
2. **The wallet/router side is what's missing** — PR #793 deliberately defers it
   ("NUT-09 /restore fallback (fork-level follow-up)"). The gonuts fork already ships the
   client call (`client.PostRestore` → `POST /v1/restore`) and a local restore implementation;
   **verified end-to-end on 2026-10-11** — see §9 (restore of a live 11001-stuck intent
   returned byte-identical signatures; restored proofs unblinded, unspent, and re-spent).
3. **The 60 s TTL is configurable**, but the knob moved: in **0.17.6** it's the env var
   `CDK_MINTD_CACHE_TTL` (honored at every startup); in **0.18.x** normal startup no longer
   applies `CDK_MINTD_*` overrides — the TTL lives in the DB-stored settings document
   (`[info.http_cache] ttl`), changed via `cdk-mintd config apply`.
4. **Other mints differ sharply**: nutshell ships NUT-19 **disabled by default**, and when
   enabled it is Redis-backed with a default TTL of **one week** (vs cdk's 60 s memory cache).
   cashu-ts is a wallet SDK, not a mint.
5. **Ranked mitigations**: (1) implement the fork's `/restore` fallback (the real fix),
   (2) raise the TTL, (3) switch cache backend to Redis (survives mint restarts),
   (4) keep reboots inside the TTL, (5) monitor the `not recoverable this pass` line and
   reconcile the journal. See §6.

---

## 1. The failure, as reproduced (context)

The PR #793 crash-injection lane (`/root/src/rel-793-transcript.md`) pinned
cdk-mintd 0.17.6 (`cargo install cdk-mintd --version 0.17.6 --no-default-features
--features fakewallet,sqlite`, `/root/src/cdk-replay-lab/Dockerfile.mint:20`,
identical in `/root/src/rel-793-tg/tests/cloud-lab/Dockerfile.mint`) with
`CDK_MINTD_CACHE_BACKEND=memory`, `CDK_MINTD_DATABASE=sqlite`
(`/root/src/rel-793-tg/tests/cloud-lab/docker-compose.yml`, mint service env; the same
env set is embedded in the opt-in test `/root/src/cdk-replay-lab/gonuts/wallet/cdk_replay_behavior_test.go:11-17`).

Scenario (e): the wallet is kept down 95 s — past the 60 s in-memory NUT-19 TTL — then
restarted. The boot-resume goroutine re-POSTs the journaled swap bytes
(`ResumePendingOperations` in gonuts `wallet/pending_swaps.go`; byte-identity is
sha256-pinned in the lane) and the mint refuses:

```
REFUSED: 2026/10/09 18:07:38 wallet: pending swap a614202595556b2ca2299aeac7d09d6d (http://killer:8085)
         not recoverable this pass: mint answered replay with HTTP 400: {"code":11001,"detail":"Token Already Spent"}
```
(`/root/src/rel-793-logs/e-run.log:135`; reproduced at `r2-e-run.log:132` and `r4-e-run.log:131`).

Lane-verified properties of the stuck state (`/root/src/rel-793-transcript.md` §4.5, lines 256–301):

- **Journaled, not lost**: the intent's bytes stay in the bbolt `pending_swaps` bucket
  (`/etc/tollgate/wallet.db`) after every refused load; the journal policy in
  `gonuts/wallet/pending_swaps.go:38-49` is "LEFT in place and reported — never deleted on a
  bad guess", with the `not recoverable this pass` log line at :49.
- **Bounded retries**: exactly one recovery attempt per boot (4 attempts across 4 loads);
  no busy-loop (CPU ~0.05 % at rest).
- **Customer converged anyway**: NUT-07 checkstate showed proofs spent → owed-entitlement
  recorded and granted ~10 s after the first refusal; the customer does not wait for the
  operator's NUT-09 fallback (transcript lines 298–301).
- **The value is operator-side stuck**: the 50 sats are neither in the operator wallet nor
  spendable by the customer — "exactly the journaled-not-lost posture pending NUT-09 /restore"
  (transcript lines 288–291).
- **No `/restore` was ever attempted in the lane** — zero `/v1/restore` calls appear in any
  captured log under `/root/src/rel-793-logs/`. The fallback is implemented in the fork but
  untested against this failure shape.

Within the TTL the same replay works: scenario (b)/(d) replays answered `200` from the
NUT-19 cache with byte-identical signatures (transcript §4.1 line ~136, §4.4 lines 224–232).

---

## 2. Q1 — NUT-09 spec: `/v1/restore` today

Source: <https://github.com/cashubtc/nuts/blob/main/09.md> (fetched 2026-10-11; `optional`, "used in: NUT-13").

**Request** — `POST /v1/restore`, body `PostRestoreRequest`:

```json
{ "outputs": <Array[BlindedMessages]> }
```

Note the payload is **blinded messages** (`{amount, id, B_}`), *not* raw secrets. The
blinding factors live wallet-side; a deterministic (NUT-13) wallet can regenerate the whole
batch from a seed, which is why NUT-09 also serves full wallet backup.

**Response** — `PostRestoreResponse`:

```json
{ "outputs": <Array[BlindedMessages]>, "signatures": <Array[BlindSignature]> }
```

"The returned arrays `outputs` and `signatures` are of the same length and for every entry
`outputs[i]`, there is a corresponding entry `signatures[i]`."

**Semantics for outputs whose signatures were never delivered** (the crash-lane case):
the spec requires that *"Mints must store the `BlindedMessage` and the corresponding
`BlindSignature` in their database every time they issue a `BlindSignature`"* and that
*"Mints only respond with a `BlindSignature`, if they have previously signed the
`BlindedMessage`."* Concretely:

- Swap executed server-side (inputs consumed, outputs signed, response lost in transit) →
  the mint **has** the `(B_, C_)` pair and returns it. This is precisely the recovery for a
  replay that died with 11001.
- Swap never reached execution → mint returns nothing for those outputs (empty or partial
  arrays), and the inputs are still unspent — the wallet knows it may safely re-swap or
  treat the intent as never-sent. Replay-then-restore is therefore race-free: the two
  outcomes (11001 → restore-hit, or clean rejection → inputs unspent) cover the ambiguity
  window of an interrupted swap. cdk's own wallet implements exactly this strategy
  (`cdk-0.18.1/crates/cdk/src/wallet/recovery.rs:201-269` `try_replay_swap_request`, then
`restore_outputs` at :176-198; doc comment at :134-140: "This leverages NUT-19 caching:
if the mint has a cached response for the exact request, it will return the signatures
immediately.").

**Empirical addendum (2026-10-11)** — §9 below verifies this fallback end-to-end against
cdk-mintd 0.17.6: on a 11001 refusal, `POST /v1/restore` with the journaled outputs
returned byte-identical signatures; unblinded proofs were NUT-07 unspent and were
successfully re-spent. The engineering task that remains is wiring it into the fork's
boot-resume path — the evidence gap is closed.

**Limits / lookback window**: the spec defines **none** — no TTL, no index window, no
lookback bound. Storage lifetime is purely an implementation concern. (Implementation-side
limits do exist: cdk caps the request at `max_outputs` — §3; nutshell caps request arrays at
`settings.mint_max_request_length`, default 1000 — §5.)

**Mint-info setting**: `nuts` → `"9": {"supported": true}` (NUT-06 `MintMethodSetting`).

**Interaction with NUT-19** (<https://github.com/cashubtc/nuts/blob/main/19.md>):
NUT-19 lets a mint return a *cached copy of the original successful response* for a
method+path+payload-derived key `k`, with a **mint-decided TTL** (`"ttl": <int|null>`;
`null` means "cached indefinitely"), announced via `nuts.19` in mint info. NUT-19 caching is
the fast path (byte-identical swap response replay) and NUT-09 is the durable fallback
(re-issue of the stored blind signatures from the database). They are complementary: NUT-19
protects only within the TTL and only what the cache still holds; NUT-09 protects everything
the mint ever signed, for as long as its DB retains the signatures. Note also that `11001`
("Proofs already spent", <https://github.com/cashubtc/nuts/blob/main/error_codes.md>) is a
NUT-03/05 spend-state error, not a NUT-19 error — after cache expiry the replayed swap is
simply *processed again* and fails the spend check.

---

## 3. Q2 — What cdk-mintd actually implements for `/restore` (0.17.x / 0.18.x)

All citations from the local tree `cdk-0.18.1` (workspace `0.18.1`); behavior at the lab's
`0.17.6` is the same in every observed respect (see §4 for the config-model difference;
no mint-side restore change is listed in the CHANGELOG between 0.17.6 and 0.18.1 —
**not line-diffed across tags**, UNVERIFIED beyond that).

### Route and enabling

- `/v1/restore` is registered **unconditionally** in the mint router:
  `cdk-0.18.1/crates/cdk-axum/src/lib.rs:110` (`.route("/restore", post(post_restore))`,
  nested under `/v1` at :112). No feature flag, no config gate. Note the deliberate
  asymmetry at :106: `/swap` is wrapped in the cache handler `cache_post_swap`; `/restore`
  is bare — restore is never served from (or stored into) the NUT-19 response cache.
- The only "restore" config in cdk-mintd is NUT-22 *auth* for the route
  (`cdk-0.18.1/crates/cdk-mintd/src/config.rs:1085-1112`, `AuthType::default_blind()`),
  applied only when auth is enabled at all (`cdk-mintd/src/lib.rs:1824-1829`). With auth
  off (the default, `auth_enabled=false`), `/restore` is open like any other endpoint.
- Mint info advertises it: `cdk-0.18.1/crates/cdk-axum/src/router_handlers.rs:813`
  (`(9, "Signature restore")`), i.e. `nuts.9.supported = true`.

### The handler and what it does

`cdk-0.18.1/crates/cdk-axum/src/router_handlers.rs:221-243` (`post_restore`) →
`cdk-0.18.1/crates/cdk/src/mint/mod.rs:1363-1452` (`Mint::restore`):

1. Size guard: `output_len > max_outputs` → `Error::MaxOutputsExceeded` (:1373-1383).
2. **Identifier derivation — there is none on the mint side.** The mint uses each request
   output's `blinded_secret` (`B_`, a secp256k1 point) *directly* as the DB lookup key
   (:1396-1397). It does **not** hash secrets and does **not** need them; `B_ =
   hash_to_curve(secret)`-derived material is the wallet's job
   (`cdk-0.18.1/crates/cashu/src/dhke.rs:43`; used wallet-side e.g.
   `crates/cdk/src/wallet/mod.rs:1818`). This is exactly why the router's journaled swap
   bytes (which contain the `outputs` array) are sufficient raw material for a later
   `/restore` call.
3. Store query: `localstore.get_blind_signatures(&blinded_message)` (:1399-1402) — the
   `SignaturesDatabase` trait method, `cdk-0.18.1/crates/cdk-common/src/database/mint/mod.rs:543-546`,
   returning `Vec<Option<BlindSignature>>` (one slot per queried `B_`). SQL implementation
   shared by sqlite and postgres: `cdk-0.18.1/crates/cdk-sql-common/src/mint/signatures.rs:219,271`.
4. This table is written **at swap finalization**: `cdk-0.18.1/crates/cdk/src/mint/swap/swap_saga/mod.rs:378-380`
   (`tx.add_blind_signatures(&blinded_secrets, &self.state_data.signatures, None)`), and on
   mint issuance — i.e. every `BlindSignature` the mint ever issued is in the persistent DB,
   per the NUT-09 MUST above.
5. Response assembly: signatures for **expired keysets are skipped** (:1412-1419); matched
   outputs and their signatures are returned in request order (:1421-1447). A `B_` the mint
   never signed is simply **omitted**; if nothing matches, the mint answers
   `200` with `{"outputs":[],"signatures":[]}` — *no error* (:1449-1452). (A length
   contract violation from the DB layer is an `Internal` error, :1404-1406.)
6. Request/response types: `cdk-0.18.1/crates/cashu/src/nuts/nut09.rs:11-23`
   (`RestoreRequest { outputs: Vec<BlindedMessage> }`,
   `RestoreResponse { outputs, signatures }`).

### Store backing and the TTL question

- **Restore reads the mint's primary store, not the response cache.** It is therefore
  **not bounded by the NUT-19 TTL in any way** — no time filter exists in
  `get_blind_signatures`. With cdk-mintd's production backings — **sqlite (default) or
  postgres** (`cdk-0.18.1/crates/cdk-mintd/src/lib.rs:2689-2702`; the lab runs sqlite) —
  the blind signatures persist indefinitely alongside proofs/keysets. The response-cache
  backend choice (`CDK_MINTD_CACHE_BACKEND=memory|redis`) is irrelevant to restore.
- The only in-memory mint store in the tree is an in-memory **SQLite** used for tests
  ("mainly for testing purposes", `cdk-0.18.1/crates/cdk-sqlite/src/mint/memory.rs:16-23`);
  cdk-redb is wallet-only (no mint module). No cdk-mintd deployment runs a mint DB that
  loses blind signatures on restart.

### Where 11001 comes from (the refusal path)

- The `/swap` wrapper `post_cache_wrapper!` (`cdk-0.18.1/crates/cdk-axum/src/router_handlers.rs:21-51`,
  instantiated for swap at :88): compute the cache key (double-SHA256 of the request,
  `crates/cdk-axum/src/cache/mod.rs` `calculate_key`); on **hit** return the cached
  `SwapResponse` JSON verbatim; on **miss** execute the swap and cache **only successful
  responses** — errors are never cached (:45-46).
- On a miss after the inputs were already spent, the swap saga fails the DB update with
  `AttemptUpdateSpentProof` → `Error::TokenAlreadySpent`
  (`cdk-0.18.1/crates/cdk/src/mint/swap/swap_saga/mod.rs:194`, `crates/cdk/src/mint/proofs.rs:27,36`),
  which serializes to exactly the observed body: `cdk-0.18.1/crates/cdk-common/src/error.rs:356-357`
  (`#[error("Token Already Spent")] TokenAlreadySpent`), mapped to `ErrorResponse` code
  **11001** (:927-930, enum :1322, code maps :1413/:1462).

---

## 4. Q3 — Is the NUT-19 response-cache TTL configurable? (0.17.6 and later)

**Yes — in both versions, but through different doors.** The default is 60 s everywhere:
`DEFAULT_TTL_SECS: u64 = 60` (`cdk-0.18.1/crates/cdk-axum/src/cache/mod.rs:74`; identical at
`cdk@v0.17.6/crates/cdk-axum/src/cache/mod.rs`), with `config.ttl.unwrap_or(DEFAULT_TTL_SECS)`
in `HttpCache::from_config`.

### cdk-mintd 0.17.6 (the lab's pin) — env vars, honored at startup

Fetched from the `v0.17.6` tag:

- `CDK_MINTD_CACHE_TTL` / `CDK_MINTD_CACHE_TTI` (seconds) — parsed in
  `Config::from_env`, `cdk@v0.17.6/crates/cdk-axum/src/cache/config.rs`
  (`ENV_CDK_MINTD_CACHE_TTL`/`_TTI` consts at the top; parse near the end of `from_env`).
- Legacy aliases: `CDK_MINTD_CACHE_SECONDS` → `http_cache.ttl` and
  `CDK_MINTD_EXTEND_CACHE_SECONDS` → `http_cache.tti`, parsed in
  `cdk@v0.17.6/crates/cdk-mintd/src/env_vars/info.rs` (`ENV_CACHE_SECONDS` /
  `ENV_EXTEND_CACHE_SECONDS` blocks), followed by
  `self.http_cache = self.http_cache.from_env()` which layers the `CDK_MINTD_CACHE_*`
  vars on top.
- Backend: `CDK_MINTD_CACHE_BACKEND=memory|redis` (same file/consts), plus
  `CDK_MINTD_CACHE_REDIS_URL`, `CDK_MINTD_CACHE_REDIS_KEY_PREFIX`,
  `CDK_MINTD_CACHE_REDIS_USE_CLUSTER`, `CDK_MINTD_CACHE_REDIS_CLUSTER_NODES`
  (redis is feature-gated: `cdk-0.18.1/crates/cdk-axum/Cargo.toml:13-14`,
  `crates/cdk-mintd/Cargo.toml:27`).
- These are **live at normal startup** in 0.17.6: `cdk@v0.17.6/crates/cdk-mintd/src/main.rs`
  calls `load_settings_from_args(...)` → `run_mintd(&work_dir, &settings, ...)` — the
  args/TOML/env settings model. The 0.18.0 CHANGELOG confirms this model was still in force:
  "Version 0.18.0 moves `cdk-mintd` to a database-authoritative configuration model …"
  (`cdk-0.18.1/CHANGELOG.md:54`). The lane's own mint was configured purely through
  `CDK_MINTD_*` env vars on 0.17.6 (compose file above), so the path demonstrably works.

**So the lab could have set `CDK_MINTD_CACHE_TTL=604800` (or `CDK_MINTD_CACHE_SECONDS=…`)
and turned the 60-second window into a week — it just didn't.**

### cdk-mintd 0.18.x — same knobs, but the settings document owns them

- Normal startup is `run_mintd_from_database` (`cdk-0.18.1/crates/cdk-mintd/src/main.rs:170`,
  `lib.rs:2794`); `load_database_bootstrap_settings` reads only the database env var
  (`lib.rs:2689-2702`), and the operational settings — including `info.http_cache`
  (`cdk-mintd/src/config.rs:64`, `pub http_cache: cdk_axum::cache::Config`, serde-serialized
  into the stored document) — come from the DB. `Settings::from_env` / `load_settings` are
  reached only from tests in this tree (`lib.rs:4287, 4349, 4401, 5045`, all `#[cfg(test)]`).
- Operators change the TTL via the config lifecycle (`cdk-mintd config apply` /
  migration tooling, `cdk-mintd/src/main.rs:112-134`; the migration even externalizes the
  Redis connection secret, `cdk-mintd/src/config_migration.rs:394-396`) or by editing the
  `[info.http_cache]` section (`ttl`, `tti`, `backend`) in the imported TOML.
- The advertised `nuts.19.ttl` in `/v1/info` equals the cache TTL:
  `cdk-0.18.1/crates/cdk-mintd/src/lib.rs:1685-1704`
  (`mint_builder.with_cache(Some(cache.ttl.as_secs()), cached_endpoints)`);
  cached endpoints are swap + mint/melt quote routes — **not** restore. (Upstream test
  asserting `ttl == Some(60)`: `crates/cashu/src/nuts/nut06.rs` `nut19` test.)

### Is raising it *safe*?

Yes, with bounded costs and two operational notes:

- **Semantics**: only already-successful (200) swap/mint/melt responses are replayed from
  the cache (`post_cache_wrapper!` caches `Ok` only). Since cdk signs deterministically
  (verified against cdk-mintd 0.17.6 in the lab, `gonuts/wallet/pending_swaps.go:29-36`),
  a cache hit returns byte-identical signatures — there is no fund-safety hazard, only
  idempotency. NUT-19 explicitly blesses `ttl: null` (indefinite).
- **Memory backend**: `InMemoryHttpCache` is a moka cache hard-capped at ~10,000 entries,
  "not shared between instances nor persisted"
  (`cdk-0.18.1/crates/cdk-axum/src/cache/backend/memory.rs:9-13`). Raising the TTL
  increases per-entry residence; under sustained swap volume >10k distinct requests per
  TTL, LRU eviction silently reintroduces replay misses *inside* the window. A large TTL
  on the memory backend is therefore best paired with…
- **Redis backend**: entries are written with `SETEX` under the same TTL
  (`crates/cdk-axum/src/cache/backend/redis.rs:120-146`), survive **mint** restarts, and
  are shared across replicas. It needs the `redis` feature compiled in — the lab image
  builds `--no-default-features --features fakewallet,sqlite` and would need `,redis`
  added. Dev recipes exist upstream (`cdk-0.18.1/justfile:294,302`,
  `CDK_MINTD_CACHE_BACKEND=redis`).
- **Mint restarts are a separate hazard**: with `memory`, a restart of the *mint itself*
  wipes the whole replay window instantly, even mid-TTL — the next replay goes straight to
  11001. (Code-derived from `memory.rs`; the lab only tested *wallet* downtime, not mint
  restarts — the restart variant is UNVERIFIED empirically.)

---

## 5. Q4 — What nutshell and cashu-ts do about replay beyond cache TTL

### Nutshell (reference Python mint, `cashubtc/nutshell` @ main)

- **NUT-19 is implemented but OFF by default.** `MintRedisCache` settings
  (`cashu/core/settings.py`, `MintRedisCache` class):
  `mint_redis_cache_enabled: bool = False`, `mint_redis_cache_url: Optional[str] = None`,
  `mint_redis_cache_ttl: Optional[int] = 60*60*24*7  # 1 week`,
  `mint_redis_cache_cluster: bool = False`.
  <https://github.com/cashubtc/nutshell/blob/main/cashu/core/settings.py>
- When enabled, the cache is **Redis-backed** (`cashu/mint/cache.py`, `RedisCache`;
  instantiated once in `cashu/mint/router.py`), and `nuts.19` is announced with
  `cached_endpoints = [POST /v1/mint/bolt11, POST /v1/melt/bolt11, POST /v1/swap]` and
  `ttl` included only if set — `cashu/mint/features.py`, `add_cache_features`.
  <https://github.com/cashubtc/nutshell/blob/main/cashu/mint/features.py>
- **Spend state is DB-permanent regardless of the cache**: replaying a completed swap
  once the cache is off/expired hits `_verify_proofs_spendable`, which raises
  `ProofsAlreadySpentError` from the database
  (`cashu/mint/verification.py:~142`, `cashu/mint/db/read.py:~77`).
  <https://github.com/cashubtc/nutshell/blob/main/cashu/mint/verification.py>
- **`/v1/restore` is always on** (`cashu/mint/router.py:658`, `ledger.restore(payload.outputs)`
  in `cashu/mint/ledger.py:~1383`), documented as "Two lists with the first being the list
  of the provided outputs that have an associated blinded signature which is given in the
  second list", reading the DB-backed promises. Request arrays are capped at
  `settings.mint_max_request_length` (default **1000**,
  `cashu/core/settings.py`), and the wallet sweeps deterministically in batches
  (`cashu/wallet/wallet.py`, `restore_tokens_for_keyset(keyset_id, to=2, batch=25)`).
  <https://github.com/cashubtc/nutshell/blob/main/cashu/mint/router.py>

**Net difference vs cdk**: both mints make replay fail permanently once inputs are spent
(11001 / `ProofsAlreadySpentError`) — that part is spec-consistent. The difference is the
*window*: nutshell's default posture is "no cache at all, restore-from-DB is the only
recovery," and its optional cache window is a week of Redis, not 60 s of RAM.

### cashu-ts

- cashu-ts (`cashubtc/cashu-ts`) is a **wallet/SDK, not a mint** — it has a `Mint` API
  *client* class. It implements the NUT-09 client side: `restore(restorePayload)` —
  "Restores proofs from the provided blinded messages" (`src/mint/Mint.ts`, ~line 1028;
  `PostRestorePayload`/`PostRestoreResponse` types in `src/mint/types`).
  <https://github.com/cashubtc/cashu-ts/blob/main/src/mint/Mint.ts>
- Server-side NUT-19 TTL behavior is therefore **not applicable** to cashu-ts itself;
  whether any production mint runs on cashu-ts server code is **UNVERIFIED** (no official
  cashu-ts mint server ships in the repo). For the router question, the relevant cashu-ts
  fact is: the restore client primitive exists in the TS ecosystem too, so a TollGate-side
  fallback would have prior art on both SDKs it might use.

### Comparison table

| | cdk-mintd 0.17.6 | cdk-mintd 0.18.x | nutshell | cashu-ts |
|---|---|---|---|---|
| NUT-19 default | on, memory, **TTL 60 s** | on, memory, **TTL 60 s** | **off** | n/a (wallet SDK) |
| TTL knob | `CDK_MINTD_CACHE_TTL` env (live) | settings doc `[info.http_cache] ttl` | `MINT_REDIS_CACHE_TTL` (default 1 wk) | n/a |
| Cache backend | memory / redis (feature) | memory / redis (feature) | **redis only** | n/a |
| Cache survives restart | memory: no; redis: yes | memory: no; redis: yes | yes (redis) | n/a |
| Replay after window | 400 `{"code":11001,...}` | same | `ProofsAlreadySpentError` | n/a |
| `/restore` | always registered; sqlite/pg-backed, unbounded | same | always on; DB-backed, unbounded | client-side `restore()` |
| Restore request cap | `max_outputs` (mint limit) | same | 1000 outputs/request | n/a |

---

## 6. Q5 — Practical ops guidance for the router operator (ranked)

**#1 — Implement the NUT-09 `/restore` fallback in the fork (the real fix; PR #793's own
deferred follow-up).**
The mint side is ready today: `/v1/restore` is unconditional on cdk-mintd 0.17.6/0.18.x,
reads the persistent blind-signature table, and is not TTL-bounded (§3). The wallet side
has all the pieces: the journaled intent *contains the swap POST body* (the lane proves
byte-identical re-POSTs), hence the `outputs` (`B_`) array; blinding factors are held by
the deterministic NUT-13 derivation; the fork already ships
`client.PostRestore` (`/root/src/cdk-replay-lab/gonuts/wallet/client/client.go:416`) and
`wallet/restore.go` (`RestoreFromMint`, documented in
`gonuts/docs/wallet-operations.md:548-559`). Recommended flow on a 11001 refusal:
`POST /v1/restore {outputs: <journaled B_ list>}` → non-empty signatures ⇒ unblind, credit,
delete intent; empty ⇒ swap never landed server-side (inputs unspent — reconcile via NUT-07
checkstate instead of re-swapping blind). cdk's own wallet is prior art for
replay-then-restore (`cdk-0.18.1/crates/cdk/src/wallet/recovery.rs:115-269`).
**Open item: no lab run has exercised this path against a stuck intent — add it to the
crash-lane before trusting it (the lane's own "pending NUT-09 /restore" note).**

**#2 — Raise the cache TTL (cheap, spec-visible, buys time).**
On 0.17.6: `CDK_MINTD_CACHE_TTL=86400` (or the legacy `CDK_MINTD_CACHE_SECONDS`). On
0.18.x: set `[info.http_cache] ttl` in the settings document via `cdk-mintd config apply`.
Effects: any replay inside the TTL gets the byte-identical 200 — the crash lane's (b)/(d)
scenarios instead of (e). The value is announced in `/v1/info` (`nuts.19.ttl`), so wallets
can even discover it. Costs: RAM residence on the memory backend (moka cap ~10k entries —
pair large TTLs with redis under volume); no fund-safety downside since only prior 200s are
replayed and errors are never cached. A week (nutshell's default) is a defensible target.

**#3 — Move the cache backend to Redis (kills the restart hazard, enables replicas).**
`CDK_MINTD_CACHE_BACKEND=redis` + `CDK_MINTD_CACHE_REDIS_URL` (0.17.6 env; 0.18.x settings
doc; requires the `redis` compile feature — the lab image currently omits it). Benefits:
entries survive **mint** restarts (with `memory`, *any* mint restart instantly 11001s every
in-flight replay window, independent of elapsed time), survive TTL-elapsed wallet restarts
for as long as the TTL, and are shared across mint replicas. This is nutshell's model and
matches NUT-19's own suggestion ("a dedicated caching service like Redis").

**#4 — Keep reboots inside the window (works today, brittle).**
The lane shows restart-to-replay in ~1–2 s succeeds within the 60 s default. Ops levers:
`restart=unless-stopped`, healthchecks that fail fast, image pre-pull, and avoiding
coordinated restarts of wallet+mint. This is a mitigation, not a fix — one slow boot, one
OOM-loop, or one mint restart and you're in scenario (e) again.

**#5 — Monitor the refusal and reconcile the journal.**
- Alert on the wallet line `not recoverable this pass` (`gonuts/wallet/pending_swaps.go:49`)
  — it is emitted **exactly once per boot per stuck OpID** (lane-verified, no busy-loop),
  so it's a clean alert key, not noise.
- Alert mint-side on `Token Already Spent` / code 11001 ERROR lines (expected once per
  replay attempt — treat sustained growth as stuck-intent accumulation).
- Reconcile: bbolt `pending_swaps` bucket vs operator wallet balance — the delta is
  stuck-but-recoverable value ("journaled-not-lost"); today it needs a human with a
  `/restore` tool, which is exactly what #1 automates.
- Watch `/v1/info`'s `nuts.19.ttl` so a config regression (someone resets the document)
  is detected before a crash is.

**Posture note**: per the lane, the *customer* is already converged in this failure shape
(owed-grant applied ~10 s after the first refusal); the exposure is operator-side value and
journal hygiene, which is why a deliberate, monitored, eventually-restored posture is
acceptable — but only with #1 on the roadmap.

---

## 7. Source index

**Spec (fetched 2026-10-11):**
- NUT-09 Restore signatures — <https://github.com/cashubtc/nuts/blob/main/09.md>
- NUT-19 Cached Responses — <https://github.com/cashubtc/nuts/blob/main/19.md>
- NUT-03 Swap tokens — <https://github.com/cashubtc/nuts/blob/main/03.md>
- NUT error codes (11001) — <https://github.com/cashubtc/nuts/blob/main/error_codes.md>

**cdk 0.18.1 (local, authoritative):** `/root/src/cdk-0.18.1/` —
`crates/cdk-axum/src/lib.rs:106-112`; `crates/cdk-axum/src/router_handlers.rs:21-51,88,221-243,813`;
`crates/cdk-axum/src/cache/mod.rs:74,77`; `crates/cdk-axum/src/cache/config.rs`;
`crates/cdk-axum/src/cache/backend/memory.rs`; `crates/cdk-axum/src/cache/backend/redis.rs`;
`crates/cdk/src/mint/mod.rs:1363-1452`; `crates/cdk/src/mint/swap/swap_saga/mod.rs:194,378-380`;
`crates/cdk/src/mint/proofs.rs:27,36`; `crates/cdk/src/wallet/recovery.rs:115-269`;
`crates/cashu/src/nuts/nut09.rs:11-23`; `crates/cashu/src/dhke.rs:43`;
`crates/cdk-common/src/error.rs:356-357,927-930,1322,1413,1462`;
`crates/cdk-common/src/database/mint/mod.rs:543-546`;
`crates/cdk-sql-common/src/mint/signatures.rs:219,271`;
`crates/cdk-sqlite/src/mint/memory.rs:16-23`;
`crates/cdk-mintd/src/{main.rs:112-134,170, lib.rs:1685-1704,1824-1829,2689-2702,2794, config.rs:64,1085-1112, env_vars/common.rs:17-18, env_vars/info.rs:36-46,86, config_migration.rs:394-396}`;
`CHANGELOG.md:54` (+0.17.x/0.18.x entries noted in §3–4); `justfile:294,302`; crate
`Cargo.toml` feature lines as cited.

**cdk v0.17.6 (upstream tag, fetched):** `crates/cdk-axum/src/cache/mod.rs`;
`crates/cdk-axum/src/cache/config.rs`; `crates/cdk-mintd/src/env_vars/info.rs`;
`crates/cdk-mintd/src/main.rs` — <https://github.com/cashubtc/cdk/tree/v0.17.6>

**Lab / PR #793 (local):** `/root/src/cdk-replay-lab/` (Dockerfile.mint:20; CLEANUP-REQUEST.md;
`gonuts/wallet/{pending_swaps.go,restore.go,client/client.go:416,cdk_replay_behavior_test.go}`;
`gonuts/docs/{api-reference.md:426,wallet-operations.md:548-559}`);
`/root/src/rel-793-transcript.md` (§2 lines 49-55, §4.5 lines 256-301, verdicts);
`/root/src/rel-793-logs/{e-run.log:128-139, r2-e-run.log:132, r4-e-run.log:131}`;
`/root/src/rel-793-tg/tests/cloud-lab/{docker-compose.yml, run-crash-injection-e.sh:225,241}`.

**Nutshell / cashu-ts (upstream @ main, URL-cited in §5).**

**Empirical probe (local, 2026-10-11):** `/tmp/opencode/restore-probe/` —
`run.sh` (driver), `restore-fallback-run.log` (captured PASS transcript),
`gonuts/wallet/restore_fallback_probe_test.go` (probe, in a throwaway copy of the lab
tree — no repo under `/root/src` was modified), `mint-data/` (the probe mint's sqlite
volume).

## 8. Explicitly UNVERIFIED / open items

- ~~**`/restore` against a 11001-stuck intent has never been executed in any captured lab
  run**~~ → **VERIFIED 2026-10-11**, see §9: the fallback now proven end-to-end on
  cdk-mintd 0.17.6 (restore → unblind → checkstate unspent → re-spend 200). What remains
  open is *engineering*, not evidence: wiring the fallback into the fork's boot-resume
  path behind the 11001 refusal.
- ~~Mint-*restart* (as opposed to wallet downtime) wiping the memory cache and instantly
  refusing replays~~ → **VERIFIED 2026-10-11**, see §9 step (5): replay refused with
  400/11001 seconds after a mint restart, well inside the 60 s TTL.
- cdk mint-side `Mint::restore` was not line-diffed between v0.17.6 and v0.18.1; the
  0.18.1 tree is cited, and no restore change appears in the CHANGELOG between them.
  (The §9 probe ran against the 0.17.6 binary itself. **Update 2026-10-11**: 0.18.1 is
  now live-verified end-to-end — fork e2e + full-stack cloud-lab recovery — see §10.)
- Whether any production mint runs on cashu-ts server code (no official cashu-ts mint
  server exists in-repo).
- moka ~10k-entry eviction causing in-window replay misses under high volume: code-derived
  bound, not load-tested here.
- The gonuts `restore.go`/`RestoreFromMint` implementation details (exact unblinding flow)
  were inventoried but not read end-to-end for this note (the §9 probe used the same
  primitives directly: `crypto.BlindMessage`/`UnblindSignature`/`HashToCurve`).

---

## 9. Empirical addendum (2026-10-11): the `/restore` fallback, proven end-to-end

The top open item above ("no lab run has exercised `/restore` against a stuck intent")
was closed with a dedicated probe run on this host. Artifacts:

- Probe (test file, in a throwaway copy of the lab's gonuts — no repo was modified):
  `/tmp/opencode/restore-probe/gonuts/wallet/restore_fallback_probe_test.go`
- Driver + captured log: `/tmp/opencode/restore-probe/run.sh`,
  `/tmp/opencode/restore-probe/restore-fallback-run.log`

**Environment**: fresh `cdk-replay-mint:latest` container (= cdk-mintd **0.17.6**,
fakewallet+sqlite, image sha `0ac72306a9b3`), `CDK_MINTD_CACHE_BACKEND=memory`,
loopback :8095, sqlite in a host volume. Wallet side: the lab's own gonuts primitives
(`createSwapRequest`, `constructProofs`, `crypto.BlindMessage`/`UnblindSignature`).
Probe verdict: **PASS (14.6 s)**, phases and observed results:

| Phase | Action | Observed |
|---|---|---|
| 0 | `GET /v1/info` | `nuts[9] = {"supported":true}`; `nuts[19] = {"ttl":60,…}` with `/v1/swap` among 9 cached endpoints (also mint/melt bolt11/bolt12/onchain/paypal) |
| 1 | mint 64 sats (FakeWallet) | 15 proofs |
| 2 | first `POST /v1/swap` (response discarded) | `200`, 3 signatures — inputs consumed server-side |
| 3 | immediate re-POST | `200` **byte-identical** — NUT-19 cache hit (baseline) |
| 4 | `docker restart` mint (volume persists sqlite) | healthy again in seconds |
| 5 | re-POST same bytes | **`400 {"code":11001,"detail":"Token Already Spent"}`** — stuck state live, *within* the 60 s TTL ⇒ memory-cache loss on mint restart verified |
| 6 | `POST /v1/restore` with the journaled outputs | `200`, **3/3 signatures, byte-identical to the lost swap answer** |
| 7 | unblind → proofs → NUT-07 checkstate | 3 proofs = 64 sats, **all unspent** |
| 8 | spend the restored proofs in a fresh swap | **`200`** — value fully recovered end-to-end |
| 9 | control: restore of never-signed outputs | `200` with **empty** signature list — disambiguates "swap never landed" |

**Conclusions drawn:**

1. The deferred PR #793 fallback is not just viable — on cdk-mintd 0.17.6 it returns the
   *exact* blind signatures the lost response carried (byte-identical `C_`), so recovery
   is lossless and the restored proofs are immediately spendable.
2. The fallback needs exactly what the fork already journals per intent
   (`PendingSwapIntent`: outputs, secrets, serialized blinding factors `Rs` —
   `gonuts/wallet/wallet.go:988-1005`), so the fork-level implementation is a small,
   well-fed state machine: on 11001 refusal → `client.PostRestore(journaled outputs)` →
   `constructProofs` → credit → delete intent; empty restore → inputs were never
   consumed → reconcile via NUT-07 instead.
3. Mint restarts are confirmed as a first-class trigger for the stuck state (not just
   TTL expiry): with `CDK_MINTD_CACHE_BACKEND=memory`, any mint restart mid-window
   converts every in-flight replay into a 11001. Redis backend or a fork-side restore
   fallback covers this case; a longer TTL alone does **not** (the restart wipes the
   cache regardless of TTL).

---

## 10. Dual-mint matrix addendum (2026-10-11, lane rc1-866): PR #40 through the full tollgate stack

The #866 fallback (gonuts-tollgate **PR #40**, commit `35553f0`) was validated
end-to-end at the *tollgate* layer against **both** mint versions — the gap the
0.17.6-only evidence left open. Scratch tree `/root/src/lane866-tg` (tollgate
PR #793 head `e1f8330` + rel-793 lane patches + lane-local `src/go.mod` replace
to the PR #40 pseudo-version). Evidence: `/root/src/nut19-restore-research/lane866/`.

| Scenario | cdk-mintd | Verdict |
|---|---|---|
| (b) crash → replay within window (regression) | 0.17.6 | **PASS** — plain replay recovery preserved (`recovered 50 sats from http://killer:8085`), post-recovery payment 200 |
| (e′) crash → 95 s down → replay refused 11001 → boot resume | 0.17.6 | **PASS** — `recovered 50 sats … via NUT-09 restore`; replay sha-pinned byte-identical and refused HTTP 400; 3 repeated loads silent (nothing left journaled); mint 0×10002/duplicate |
| (e′) same | **0.18.1** (current stable, DB-authoritative config) | **PASS** — identical shape; mint log exactly 1×"Token Already Spent" (the beyond-window replay) |
| Fork e2e (in-process) | 0.17.6 / 0.18.1 | PASS / PASS (0.18.1: 14.06 s) |

With this, the last §8 gap (0.18.x live behavior) is closed: live parity confirmed
for `nuts.9`/`nuts.19` (ttl 60, 9 cached endpoints), the 11001 refusal shape, and
the `/restore` recovery path on both versions.

**New operational findings:**

1. **0.18's DB-authoritative config refuses literal secrets** — `config validate`
   rejects `info.mnemonic = "<words>"` with "must use an `env:VARIABLE` or
   `file:/absolute/path` secret reference"; the document stores a reference and the
   daemon resolves it at startup. This refines §4's 0.18.x migration guidance: not
   only do env vars stop being *applied* at startup — mnemonic-class values cannot
   even be *imported* literally; the env var (or secret file) must stay alive
   alongside the settings document. The lab bridges the 0.17.6 compose flow with an
   entrypoint rendering `mnemonic = "env:CDK_MINTD_MNEMONIC"`
   (`lane866/mint018-entrypoint.sh`).
2. **bbolt byte-grep is presence-only evidence** — after a real `DeletePendingSwap`,
   freed pages keep the OpID bytes (both (e′) legs read 1 byte-hit post-recovery
   while the in-process `GetPendingSwaps()==0` proof and the silent repeated loads
   confirm deletion). rel-793's (e) presence check remains valid; absence must be
   proven behaviorally.
