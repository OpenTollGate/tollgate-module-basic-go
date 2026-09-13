# T10 — Un-fork `gonuts`: provenance, fork delta, and what it would cost to stop

> **Task:** T10 (`TASKS.md`) — *"'Un-fork gonuts' option study: could we contribute
> the fork's content upstream, or vendor a pinned upstream release? What does the
> fork add, commit by commit?"*
> **Owner:** worker · **Date:** 2026-09-13 · **Branch:** `research/wallet-migration`
> **Method:** read-only. All repo facts come from `gh api` against the four
> public GitHub repos and from git queries against the existing local clones
> (`~/repos/gonuts-tollgate`, `~/repos/gonuts-pr23-red`,
> `~/repos/gonuts-pr23-build-2`) and `src/` of this worktree. No commits, no
> pushes, no working-tree changes.
> **Disclosure:** to run the merge-base queries I fetched upstream refs into
> `~/repos/gonuts-tollgate` under `refs/research/{amp,elnosh,origami,elnosh-tags}/*`.
> That clone is a scratch research checkout belonging to this programme; the
> fetches are read-only against the remotes and added no commits.
> **Supersedes:** the "Fork-ownership grading" row for gonuts in
> `03-baseline/fork-ownership.md` (T9) — this document is the parent evidence for
> that row. Everything labelled **UNVERIFIED** below was not established by
> inspection and must not be treated as a finding.

---

## 0. Verdict in six lines

1. The dependency is **not** a two-hop fork of `elnosh/gonuts`. It is a
   **three-generation fork chain**: `elnosh/gonuts` → `Origami74/gonuts-tollgate`
   → `Amperstrand/gonuts-tollgate` → `OpenTollGate/gonuts-tollgate`. The GitHub
   `.parent` field confirms each hop; the GitHub *fork parent* of the module we
   pin is **Amperstrand**, not Origami74.
2. The root upstream is **dormant**: `elnosh/gonuts` has **0 commits since
   2025-09-13**, its newest release is v0.4.2 (2025-08-16, tagged on a commit
   that is not even an ancestor of `main`), and its oldest open PR (#149) has
   been open for **12 months** with `mergeable_state: blocked`.
3. The fork's whole delta over the root is **40 commits (34 non-merge + 6
   merges)**; over its GitHub parent it is **31 commits (26 non-merge + 5
   merges)**. Only **one** capability the router actually calls is fork-only:
   `wallet.SendOptions` / `Wallet.SendWithOptions`.
4. **No repo in the chain has CI for our platform.** All four run
   `ubuntu-latest` (amd64/glibc) only. No musl, no arm, no OpenWrt anywhere.
5. **Un-fork path (i) has no counterparty** (nobody is merging at the root) and
   **un-fork path (ii) is disqualified by what it reverts** — a signature-bypass
   fix, a proof-loss fix, V2-keyset-ID support, and the reseller overpayment
   path. **Path (iii), keep the fork, remains the least-bad option today**, but
   it is a permanent tax and it is already being paid: 22 commits in this repo
   touched the wallet-dependency surface in the last 6 months, 8 of them pure
   pin surgery, and we have already had to fork the fork.
6. **Grade: C** (we own a downstream copy in practice), with the honest caveat
   that the immediate steward is an active external individual, which is what
   keeps the grade from being a dead-fork C. Evidence in §8.

---

## 1. Provenance chain

Resolved with `gh api repos/<owner>/<repo>`. The chain was established from the
`parent` / `source` fields, not assumed.

| # | Repository | Owner type | Created | Last push | `main` HEAD | Fork of (parent) | Module path in `go.mod` | Open issues/PRs | Tags | GitHub Releases | ★ | Forks | Licence | Archived | Issues enabled | CI |
|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 0 | **`elnosh/gonuts`** | User (`elnosh`) | 2023-10-10 | **2025-08-16** | `c0aac85a` 2025-08-13 | — (root, `fork: false`) | `github.com/elnosh/gonuts` | 16 open issues, **5 open PRs** | v0.1.0 … **v0.4.2** (6) | 6, newest **v0.4.2** 2025-08-16 | 39 | 16 | MIT | no | **yes** | `.github/workflows/ci.yml` |
| 1 | **`Origami74/gonuts-tollgate`** | User (`Origami74`) | 2025-07-04 | 2025-07-14 | `374ab28b` 2025-07-14 | `elnosh/gonuts` | `github.com/Origami74/gonuts-tollgate` | 0 | v0.6.0, v0.6.1 | 2 (v0.6.0 2025-07-10, v0.6.1 2025-07-14) | 0 | 2 | MIT | no | **no** | `ci.yml` |
| 2 | **`Amperstrand/gonuts-tollgate`** | User (`Amperstrand`) | 2026-05-04 | **2026-09-02** | `85f94da5` 2026-07-17 | `Origami74/gonuts-tollgate` | `github.com/Origami74/gonuts-tollgate` (**never renamed**) | 8 open (6 Dependabot PRs + 2 from `c03rad0r`) | v0.6.0, v0.6.1, v0.6.2, v0.7.0, v0.7.2, v0.8.0 | none | 0 | 2 | MIT | no | **no** | `ci.yml` |
| 3 | **`OpenTollGate/gonuts-tollgate`** ← **the module we pin** | **Organization** | 2026-06-03 | 2026-08-26 | `4b7d125d` 2026-08-25 | `Amperstrand/gonuts-tollgate` | `github.com/OpenTollGate/gonuts-tollgate` (from commit `ae6963f` / tag ≥ v0.10.0) | **1 open (PR #23)** | v0.6.0 … **v0.11.1** (13, see §4) | **NONE — tags only, 0 releases** | 0 | **1 (ours: `felixfelix-bot`)** | MIT | no | **no** | `ci.yml`, `spec-quote-drift.yml` |

**Chain, stated plainly:**

```
elnosh/gonuts                    root, fork:false, MIT, 39★   [DORMANT: 0 commits in 13 months]
  └── Origami74/gonuts-tollgate  fork, issues DISABLED         [frozen 2025-07-14]
        └── Amperstrand/gonuts-tollgate  fork, issues DISABLED, module path still Origami74
              └── OpenTollGate/gonuts-tollgate  fork, issues DISABLED  ← src/go.mod pins this
                    └── felixfelix-bot/gonuts-tollgate  fork (ours)  ← used by 50 branches via `replace`
```

Corrections to the facts in the card — both verified, both matter:

- The card's assumed chain ("elnosh → Origami74 → OpenTollGate") is **missing a
  hop**. OpenTollGate's `parent` is `Amperstrand/gonuts-tollgate`. That
  intermediate repo is the source of the NUT-02 V2-keyset work and is **still
  being pushed to** (2026-09-02, later than OpenTollGate's own last push).
- `github.com/Origami74/gonuts-tollgate` is not dead as a *module path*: it is
  the path declared by **both** Origami74 and Amperstrand today. That is why 327
  local branches carry `replace github.com/Origami74/gonuts-tollgate =>
  github.com/OpenTollGate/gonuts-tollgate vX.Y.Z` — the path change at
  `ae6963f` could not be consumed without it.

### 1a. CI platform coverage — the answer is "none of them cover us"

| Repo | Runner | Go | Jobs | musl? | arm? | OpenWrt? |
|---|---|---|---|---|---|---|
| `elnosh/gonuts` | `ubuntu-latest` | 1.23.7 | fmt/vet/unit + 2 fuzz targets, mint integration (LND, CLN), wallet integration | **no** | **no** | **no** |
| `Origami74/…` | `ubuntu-latest` | 1.23.7 | same shape | **no** | **no** | **no** |
| `Amperstrand/…` | `ubuntu-latest` | 1.23.7 | same shape + live-mint V2 keyset test | **no** | **no** | **no** |
| `OpenTollGate/…` | `ubuntu-latest` | 1.23.7 | same + `livemint` job (`TestGetKeysetKeys_V2\|V1\|TestGetMintActiveKeyset`), + `spec-quote-drift.yml` | **no** | **no** | **no** |

`gonuts` is pure Go (no cgo in the wallet path), so cross-compiling to musl/arm
is not actually hard — but per the T9/T10 metric, *"a dependency with no upstream
CI for our platform is grade B at best"*. That is true of **every** repo in the
chain, including the root.

---

## 2. Merge bases — two different "parents", two different deltas

Measured in `~/repos/gonuts-tollgate` after fetching `main` of each upstream.

| Pair | Merge base | Date | Commits the left adds | Meaning |
|---|---|---|---|---|
| `OpenTollGate/main` vs **`Amperstrand/main`** (GitHub parent) | `8e3b21b` | 2026-05-27 | **31** (26 non-merge + 5 merges) | the fork's delta over its *declared parent* |
| `OpenTollGate/main` vs **`Origami74/main`** | `374ab28` | 2025-07-14 | 36 (Origami74 adds **0**) | OpenTollGate descends directly from Origami74's tip |
| `OpenTollGate/main` vs **`elnosh/main`** (true root) | `4d3aebf` | 2025-04-05 | **40** (34 non-merge + 6 merges) | the fork's delta over the *real* upstream |
| reverse: `elnosh/main` vs `OpenTollGate/main` | `4d3aebf` | — | **6** (4 non-merge + 2 merges) | upstream work the fork has **not** adopted |
| `Amperstrand/main` vs `elnosh/main` | `4d3aebf` | — | 13 | — |
| `Amperstrand/main` vs `OpenTollGate/main` (reverse) | `8e3b21b` | — | **4** | Amperstrand work OpenTollGate has not adopted |

Repo-wide divergence between the fork tip and the root:
`git diff --stat refs/research/elnosh/main 4b7d125` → **77 files changed,
5,975 insertions(+), 745 deletions(−)**.

---

## 3. Fork delta, commit by commit

Because there are two candidate "parents", this table covers the delta against
the **true root** (`4d3aebf..4b7d125`, 34 non-merge commits) — that is the delta
that matters for un-forking — and marks which commits came from which generation.

**Categories:** (a) generic bug fix · (b) protocol/NUT feature or fix ·
(c) build/tooling/CI/tests · (d) TollGate-specific glue · (e) vendored/unrelated.

**Upstreamability verdicts:** *yes* = looks upstreamable to the root as-is;
*no* = inherently fork/org-local; *backport* = already flowing the other way;
*blocked* = upstreamable in principle but has no counterparty (§5).

| # | SHA | Date | Author | Cat | Subject | Files | Upstreamable to `elnosh/gonuts`? |
|---|---|---|---|---|---|---|---|
| — | **Origami74 generation (4 commits, 2025-07)** | | | | | | |
| 1 | `cf693d5` | 2025-07-04 | origami74 | **d** | `feat: implement offline payment functionality` | 14 (`wallet/offline.go`, `wallet/send_options.go`, `wallet/wallet.go`, docs, example) | **no** — this *is* the fork's reason to exist; changes `Send` semantics to permit overpayment. Upstream has shown no interest in 14 months. |
| 2 | `60e2e16` | 2025-07-04 | origami74 | **c** | `fix go mod` | 1 (`go.mod`) | no (fork identity) |
| 3 | `b7146ab` | 2025-07-10 | origami74 | **e** | `basic version` | **53** — monolithic re-import of the whole tree | **no** — not a reviewable change |
| 4 | `374ab28` | 2025-07-14 | origami74 | **d** | `attempt swap before overpaying` | 1 (`wallet/wallet.go`) | no (part of the offline feature) |
| — | **Amperstrand generation (8 non-merge + 1 merge, 2026-05…2026-06)** | | | | | | |
| 5 | `c1b2095` | 2026-05-04 | amperstand | **b** | `feat: add V2 keyset ID derivation (NUT-02)` | 5 (`crypto/keyset.go`, `wallet/keyset.go`, CI, tests) | **yes in principle** — adds `DeriveKeysetIdV2`/`IsKeysetIdV2`, which the root does **not** have. Blocked (§5); also collides with the root's *own* V2 direction (PR #146, merged 2025-08-03). |
| 6 | `59d7fc5` | 2026-05-04 | amperstand | **c** | `fix: use forked btc-docker-test with CLN createrune retry` | 2 (`go.mod`, `go.sum`) | no (test-dep pin) |
| 7 | `81a652a` | 2026-05-19 | amperstand | **c** | `test: add V1/V2 keyset proof test against live mints` | 1 (new test) | yes, but test-only |
| 8 | `c3b9f93` | 2026-05-24 | Amperstrand | **c** | `chore: bump dependencies to align with tollgate-module-basic-go` | 2 (`go.mod`, `go.sum`) | **no — and this is a coupling signal**: the *fork's* deps were aligned to the *router's*, i.e. the fork's build moves when we move. |
| 9 | `ce83461` | 2026-05-29 | amperstand | **a** | `fix: tolerate invalid bolt11 invoices from FakeWallet mints` | 1 (`wallet/wallet.go`) | yes (small, generic) |
| — | **OpenTollGate generation (22 non-merge + 5 merges, 2026-07…2026-08)** | | | | | | |
| 10 | `7256cbf` | 2026-07-21 | Amperstrand | **a** | `fix: normalize mint URL and cap response reads at 1MB (#2)` | 2 (`wallet/client`) | yes (DoS hardening, generic) |
| 11 | `fa8da51` | 2026-07-21 | Amperstrand | **a** | `fix: increment keyset counter before swap to prevent already-signed errors (#3)` | 1 (`wallet/wallet.go`) | yes (counter race) |
| 12 | `9fefcd0` | 2026-07-21 | Amperstrand | **a** | `fix: use proof.Id for DLEQ verification instead of active keyset (#4)` | 2 (`cashu/nuts/nut12`, `wallet/wallet.go`) | yes — **correctness of DLEQ verification** |
| 13 | `6f39913` | 2026-07-21 | Amperstrand | **a** | `fix: use %w for error wrapping in keyset.go (#5)` | 1 | yes |
| 14 | `1ef9ce1` | 2026-07-21 | Amperstrand | **b** | `fix(nut13): hash V2 keyset IDs (>8 bytes) in DeriveKeysetPath` | 1 (`cashu/nuts/nut13`) | yes in principle, but depends on V2 support existing upstream |
| 15 | `3ee5265` | 2026-07-24 | Amperstrand | **a+b** | `feat: HTTP 429 rate-limit + resolveShortKeysetIds (V4 fix) + unit tests (#7)` | 4 | partly — 429 retry is generic; `resolveShortKeysetIds` is a V4/NUT-02 fix. **Graded tag `v0.9.0`.** |
| 16 | `62313cc` | 2026-07-25 | Amperstrand | **b** | `fix: adopt upstream NUT-13 improvements (big.Int + collision detection) (#8)` | 2 (`cashu/nuts/nut13`) | **backport** — this is the *reverse* direction; it exists because the fork lagged the root. Verified: `nut13.go` still differs from root by only 6 insertions / 3 deletions. |
| 17 | `1356a6d` | 2026-07-25 | Amperstrand | **a** | `fix: handle error from selectProofsToSend in inactive keyset selection (#9)` | 1 | yes |
| 18 | `7dc430b` | 2026-07-25 | Amperstrand | **a** | `fix: delete old proofs only after new proofs are constructed in swap (#11)` | 1 | yes — **funds-safety (proof loss)** |
| 19 | `28b728c` | 2026-07-25 | Amperstrand | **a** | `fix: guard against empty slice panics in keyset, proof, and token handling (#12)` | 3 | yes |
| 20 | `0986b73` | 2026-07-25 | Amperstrand | **a** | `fix: use %w for all error wrapping in wallet.go (#13)` | 1 | yes |
| 21 | `4818608` | 2026-07-25 | Amperstrand | **a** | `feat: retry 5xx on GET requests, return ServerError on POST 5xx (#14)` | 2 (`wallet/client`) | yes |
| 22 | `ae6963f` | 2026-07-25 | Amperstrand | **c+d** | `chore: rename module from Origami74 to OpenTollGate (#15)` | **48**, incl. `go.mod` | **no** — fork identity. **This single commit is the largest recurring cost to us (§7).** |
| 23 | `f90cf2a` | 2026-07-26 | Amperstrand | **c** | `feat(spec): greatspectations spec-quote drift checker for gonuts-tollgate` | 19, incl. `.gitmodules`, `specquotes.toml` | **no** — vendors a git submodule and third-party annotation tooling |
| 24 | `94837ff` | 2026-07-27 | Amperstrand | **c** | `docs(audit): add spec-audit prompt index for gonuts-tollgate` | 1 (docs) | no |
| 25 | `4a59a19` | 2026-07-27 | Amperstrand | **c** | `feat(spec): instrument gonuts NUT-09 through NUT-14 + P2PK wallet` | 7 (`cashu/nuts`, `wallet/p2pk.go`) | no (spec-quote annotations) |
| 26 | `386aaf2` | 2026-07-27 | Amperstrand | **c** | `feat(spec): instrument gonuts NUT-15/17/20 (MPP, WebSocket, Quote Signatures)` | 3 | no (spec-quote annotations) |
| 27 | `3fb9af8` | 2026-07-27 | Amperstrand | **b** | `fix: add NUT-04 accounting fields to mint quote responses (Critical)` | 4 (`cashu/nuts`, `mint/server.go`, `mint/websocket.go`, CI) | **yes** — real NUT-04 compliance fix |
| 28 | `997bb03` | 2026-07-27 | Amperstrand | **b** | `fix: NUT-20 binary message format + NUT-11 duplicate tag rejection` | 3 (`cashu/nuts`) | **yes** — protocol correctness |
| 29 | `296c7bf` | 2026-07-28 | Amperstrand | **b** | `fix(#328): enforce signatures when pubkeys present but n_sigs omitted (#16)` | 1 (`cashu/nuts`) | **yes, and highest priority** — a NUT-14 **signature-enforcement (bypass) fix** |
| 30 | `9430da0` | 2026-07-28 | Amperstrand | **b** | `fix(#326): add cbor struct tags for V4 token decoding (#17)` | 1 (`cashu/cashu.go`) | **yes** — V4 token decoding |
| 31 | `2fe073c` | 2026-07-25 | Amperstrand | **a** | `fix: serialize Melt calls in MultiMintPayment to prevent race condition` | 1 | yes — **first attempt**, later superseded by #33 |
| 32 | `f66b9f5` | 2026-08-25 | **Felix (ours)** | **a** | `fix(keyset): use GET /v1/keys instead of GET /v1/keys/{id}` | 3 | yes |
| 33 | `1d232bf` | 2026-08-25 | **Felix (ours)** | **a** | `fix(multimint): restore concurrent Melt + fix proof selection race` | 1 | yes |
| 34 | `c7188c6` | 2026-08-26 | **Felix (ours)** | **a** | `fix(wallet): use GET /v1/keys single-call in getActiveKeyset for existing mints` | 1 | yes |

**Merge commits (6):** `4b7d125` (PR #22, ours), `49dc7ec` (PR #21, ours),
`03aaf96` (PR #19, ours), `a286e4e` (PR #20, ours), `8e3b21b` (Amperstrand
PR #1, `feature/v2-keyset-ids`), `2d17889` (`fix: V2 keyset ID derivation path`
— tagged `v0.7.6`).

### 3a. Category totals (34 non-merge commits vs the root)

| Category | Count | Share | Notes |
|---|---|---|---|
| (a) generic bug fix | **14** | 41 % | includes the funds-safety and race fixes |
| (b) protocol/NUT feature or fix | **8** | 24 % | V2 keyset IDs, NUT-04/11/13/14/20, V4 CBOR |
| (c) build/tooling/CI/tests | **9** | 26 % | incl. the 48-file module rename and the spec-quote instrumentation |
| (d) TollGate-specific glue | **2** | 6 % | the offline-send feature — the fork's *raison d'être* |
| (e) vendored/unrelated | **1** | 3 % | the 53-file "basic version" re-import |

### 3b. Authorship of the delta (who maintains the fork)

`git shortlog -sne 4d3aebf..4b7d125`:

| Author identity | Commits |
|---|---|
| `Amperstrand` (2 addresses) | 25 |
| `amperstand <atlas@oh-my-opencode.com>` | 4 |
| `origami74@gmail.com` | 4 |
| `c03rad0r` (ours, merge commits) | 4 |
| `Felix` / `felixfelix-bot` (ours, code) | 3 |

So **29 of 40 commits are Amperstrand / Origami74**, 7 are ours. Amperstrand also
opened PR #23, the only open PR on `OpenTollGate/gonuts-tollgate`.

> **UNVERIFIED:** whether `Amperstrand` and the `OpenTollGate` organisation are
> the same principal. `gh api orgs/OpenTollGate/members` returns an empty list
> (membership may be private — the API cannot distinguish "no members" from
> "private membership"). The commit authorship pattern strongly suggests one
> human, but the identity claim is not established here.

---

## 4. Unadopted upstream releases

### 4a. Against the true root (`elnosh/gonuts`)

Our pin has adopted **zero** of the root's last two releases.

| Release/tag | Date | Tagged commit on `main`? | Adopted by our pin? |
|---|---|---|---|
| `v0.4.0` | 2025-02-05 | yes (ancestor) | in the lineage (pre-dates the 2025-04-05 fork point) |
| **`v0.4.1`** | **2025-08-01** | **NO** (`79f97ee4` is not an ancestor of `main`) | **NO** |
| **`v0.4.2`** | **2025-08-16** | **NO** (`2ea4b48f` is not an ancestor of `main`) | **NO** |
| post-release commits on `main` | 2025-07-31 → 2025-08-13 | — | **NO** — `5ec5560` *verify new keyset_id with all other stored keyset_ids*, `4cc22c2` *add base64 check and big int conversion*, `04057b1` *stabilize integration test nutshell version*, `481cf37` *correct integer comparison* (+ merges `4fee858`, `c0aac85`) |

Two extra facts worth recording because they weaken the root even as a *pin target*:

- **`v0.4.2` is tagged on a branch commit, not on `main`** — the tag
  (`2ea4b48f`) is not reachable from `elnosh/gonuts@main`. Pinning `@v0.4.2` gets
  you a commit the maintainer's own default branch does not contain. That is a
  release-hygiene defect, not a supply-chain attack — but it is the kind of thing
  this programme is trying to stop depending on.
- The root's V2-keyset work (PR #146, merged 2025-08-03) and the fork's
  V2-keyset work (`c1b2095`, 2026-05-04) are **independent implementations that
  never converged**. Neither side adopted the other's.

### 4b. Against the module we pin (`OpenTollGate/gonuts-tollgate`)

Our pin `v0.11.1` **is** the newest tag; the tree is identical to `main`'s tip
(`git diff --stat v0.11.1 4b7d125` is empty). So there is nothing to adopt here —
but the *release process* is thin, and that is part of the cost:

- **13 real tags, 0 GitHub Releases.** Remote tags (16 refs incl. peeled):
  `v0.6.0, v0.6.1, v0.6.2, v0.7.0, v0.7.1, v0.7.3, v0.7.4, v0.7.5, v0.7.6,
  v0.8.0, v0.9.0, v0.10.0, v0.11.1`. No release notes, no changelog, no
  signatures, no published hashes.
- **Tag gaps / non-monotonic tags:** `v0.7.2` exists (`632f0c6`, 2026-06-19) only
  on **Amperstrand**, never pushed to OpenTollGate. `v0.11.0` and
  `v0.11.0-alpha1` exist **only locally** in our clone — they were never pushed
  to origin. Three of the last sixteen tags (`v0.7.3`, `v0.7.4`, `v0.8.0`) are
  **not ancestors of `main`** — they point at branch commits.
- **No issue tracker.** `has_issues: false` on all three fork generations. There
  is no public place to file a bug or see one tracked.

### 4c. Against `Amperstrand` (the intermediate, still-active fork)

`Amperstrand/main` (`85f94da5`) has **4 commits that OpenTollGate `main` lacks**,
including a dependency security bump:

| Commit | Date | Subject |
|---|---|---|
| `85f94da` | 2026-07-17 | `build(deps): bump golang.org/x/crypto from 0.35.0 to 0.52.0 (#3)` |
| `632f0c6` | 2026-06-19 | `Merge branch 'feature/v2-keyset-ids'` (tag `v0.7.2`) |
| `217ce81` | — | `debug: log swap request keyset IDs and response for V2 diagnosis` |
| `9b2b843` | — | `fix: tolerate invalid bolt11 invoices from FakeWallet mints` |

None of these changed our pin's behaviour: the router's own `go.mod` resolves
`golang.org/x/crypto` independently (it is at a *newer* version, v0.54.0 per
commit `8f115f3`), and `9b2b843`'s content reached OpenTollGate as `ce83461`.
Recorded so the next reader does not re-derive it.

---

## 5. Does upstream still accept patches?

**Answer: no, not usefully — at the root. Yes, at the fork — but the fork is
what we are trying to escape.**

### 5a. `elnosh/gonuts` (the true upstream) — the counterparty is gone

| Evidence | Value |
|---|---|
| Commits to `main` since 2025-09-13 | **0** (`gh api repos/elnosh/gonuts/commits?since=2025-09-13`) |
| Last commit to `main` | `c0aac85a`, **2025-08-13** — **13 months** before this study |
| Last merged PR | #147, merged 2025-08-13 |
| Oldest open PR | **#149** ("Gonuts multig improvements", `lescuer97`), opened **2025-09-25** — **12 months** open, `mergeable_state: blocked`, **0 comments, 0 review comments**, 2 commits, +197/−38 across 3 files; last touched 2026-03-27 |
| Other open PRs | #148 (draft, 2025-08-29), #143 (2025-03-31, by the maintainer), #137 (2025-03-20), #6 (draft, 2024-03-31) |
| Issues filed 2026-07-24 by `Amperstrand` (i.e. the fork's steward, upstream) | **#150** "Race condition in MultiMintPayment: goroutines access wallet without mutex" — **0 comments**, unresolved ~7 weeks; **#152** "Add wallet-level CheckProofState cleanup and Restore recovery (NUT-07, NUT-09)" — 1 comment, unresolved |
| `CONTRIBUTING.md` | **does not exist** (no such file; root listing is `.env.*`, `.github`, `Dockerfile`, `LICENSE`, `README.md`, `cashu`, `cmd`, `crypto`, `go.mod`, `go.sum`, `mint`, `testutils`, `wallet`) |
| `SECURITY.md` | **does not exist** |
| External contributors ever merged | 2 (`lescuer97`, 5 commits; `0ceanSlim`, 1) — the door was open, it is just no longer staffed |

Interpretation: the root **did** accept patches from outside contributors as
recently as 2025-08-13, so the process was never closed. It is **inactive**.
A 40-commit, 77-file, 5,975-insertion patch series landing into a repo whose
maintainer has not committed in 13 months, whose one substantive incoming PR has
sat blocked for 12 months, and which has no CONTRIBUTING process to follow, has
an expected merge time that is not a number. This is the decisive fact for
path (i).

### 5b. `OpenTollGate/gonuts-tollgate` (the fork we pin) — patches *are* accepted

| Evidence | Value |
|---|---|
| Merged PRs | 22 total. **#19, #20, #21, #22 were all authored by `felixfelix-bot` (ours)** and merged **2026-08-25** by `c03rad0r` |
| Merge latency for our patches | PR #19 opened 2026-08-15, merged 2026-08-25 (10 days); PRs #20/#21 opened 2026-08-25 and merged the same day |
| Open PR | **#23** `fix(wallet): reject empty-proof tokens in Receive` by `Amperstrand`, branch `fix/empty-proofs-guard`, opened **2026-08-26**, still open. Note the local clones `~/repos/gonuts-pr23-red` and `~/repos/gonuts-pr23-build-2` are checkouts of exactly this unmerged branch. |
| Closed/unmerged PRs | #1, #6, #10, #18 — the fork returns patches, it does not just accumulate them |
| Upstream bugs we filed at the root | #150, #152 — see above; **we routed these to the dead root rather than the live fork**, which is itself part of the diagnosis |

Interpretation: **the fork is the only live "upstream" and it works.** Our PRs
merge in days. But merging into the fork does not remove the fork; it *is* the
fork's maintenance, performed by us (§8).

---

## 6. What the router actually needs from the fork (concrete)

This is the number that decides path (ii). Measured in
`/home/c03rad0r/worktrees/wallet-research/src`.

### 6a. Blast radius of the coupling: **2 files**

- **Non-test Go files importing `github.com/OpenTollGate/gonuts-tollgate/*`:
  exactly 2** — `tollwallet/gonuts_wallet.go` and `tollwallet/tollwallet.go`.
- Test files importing it: 9 (`tollwallet/{bench,cross_vectors,spending_conditions,compatibility_matrix,tollwallet}_test.go`,
  `merchant/{quotes_wireformat,offline_wallet_integration,lightning_state,merchant_token_flow}_test.go`).
- Modules whose `go.mod` names it: **5** —
  `src/go.mod` (v0.11.1, `// indirect`), `src/tollwallet/go.mod` (v0.11.1),
  `src/merchant/go.mod` (v0.11.1), `src/cli/go.mod` (v0.11.1, `// indirect`),
  `scripts/token-recovery/go.mod` (**v0.10.0 — skewed one release behind the rest**).
- Total `go.mod` files in the repo: **18**. So a module-path change is *not* an
  18-module change today, but it is a 5-module change, and the `replace` block
  in `src/go.mod` already carries 13 sibling-module rewrites as precedent for how
  this kind of surgery goes.

The `WalletPort` seam (`src/tollwallet/port.go`) is real and already carries a
second implementation (`cdk_wallet.go` behind `//go:build cdk_wallet`), so the
coupling is genuinely contained. **The migration is not the hard part; the
fork's content is.**

### 6b. The fork-only API the router actually calls: **one thing**

Diffing `wallet.Wallet`'s exported surface, root vs fork tip, gives exactly four
fork-only methods — `SendOffline`, `SendWithOptions`, `getProofsForAmountWithOptions`,
`swapWithRetry` — and **no** root-only methods (the root's surface is a strict
subset in this respect).

Of those four, the router calls **one**:

| Fork-only element | Router call site | Product feature affected |
|---|---|---|
| `wallet.SendOptions` (struct: `IncludeFees`, `AllowOverpayment`, `MaxOverpaymentPercent`, `MaxOverpaymentAbsolute`), `wallet.SendResult`, `wallet.DefaultSendOptions` | `TollWallet.SendWithOverpayment` — `src/tollwallet/tollwallet.go:241-272` | **Reseller mode** — buying access from an upstream TollGate |
| `Wallet.SendWithOptions` | same, line 251 | as above |
| → consumers | `src/merchant/merchant.go:984` and `src/merchant/merchant_degraded.go:105` (`m.tollwallet.SendWithOverpayment(...)`; the degraded-mode interface at `merchant_degraded.go:19`) | reseller purchase, incl. degraded/offline mode |
| `Wallet.SendOffline` | **not called anywhere in `src/`** (`grep -rn SendOffline src/` → 0 hits) | none — the fork's original purpose is **unused by us** |
| `swapWithRetry` | internal to the fork's `Send`/swap path | implicit robustness only |

### 6c. But the *behavioural* fork-only content is much larger than the API diff

The API surface understates it. Independently verified as **fork-only** by
comparing the two trees:

- **V2 keyset IDs (NUT-02).** Root has **no** `DeriveKeysetIdV2` and **no**
  `IsKeysetIdV2`; the fork has both (`crypto/keyset.go:196`, `:230`). Plus
  `resolveShortKeysetIds` (`3ee5265`) and the NUT-13 V2 path hashing
  (`1ef9ce1`, `2d17889`). This is **not cosmetic**: it is whether the wallet can
  talk to mints and decode V4 tokens that use short (>8-byte-hashed) keyset IDs.
- **`swapWithRetry`** wrapping the swap path (present in the fork, absent in root
  — root's `swapProofs`/`swapToSend`/`swapToTrusted` exist in both).
- **The offline feature** (`wallet/offline.go`, `wallet/send_options.go`) — absent
  from the root's `wallet/` listing entirely.
- **20+ correctness/security fixes** landed after the fork point (§3 rows 10–34),
  of which several are not "nice to have": `7dc430b` (proof loss in swap),
  `296c7bf` (NUT-14 HTLC signature enforcement), `997bb03` (NUT-20 format),
  `9430da0` (V4 CBOR decode), `9fefcd0` (DLEQ verification using the wrong
  keyset), `fa8da51`/`2fe073c`/`1d232bf` (races), `4818608`/`3ee5265`
  (5xx/429 handling), `28b728c` (panics).

What is **not** different: `wallet.Config` is byte-identical between root and
fork (`WalletPath`, `CurrentMintURL`), and `LoadWallet` is identical apart from
`%v`→`%w`. So the on-disk store format and the load path were never forked —
which is good news for any migration (and relevant to T6), but it also means the
"offline" claim in `tollwallet.go`'s comment is about *sending*, not *loading*.

> Quoted from the code, because it is the module's own assessment of the fork:
> `src/tollwallet/tollwallet.go:51` — *"The issue arises because of our hacky fork
> of gonuts for the offline functionatlity we need. Long term fix is switching to
> CDK."* (typo in original). The follow-on TODO at lines 49–50 documents a live
> bug the fork did **not** fix: the wallet DB is not unlocked if there is no
> network connection at boot.

---

## 7. The three un-fork paths, costed

Effort figures are **estimates in engineer-days** by a single engineer familiar
with the code; they are not measurements. The evidence columns are measurements.

### Path (i) — upstream the fork's patches, then depend on the upstream module

This splits, because there are two candidate counterparts.

**(i-a) Contribute the 40-commit delta to `elnosh/gonuts`, then pin
`github.com/elnosh/gonuts`.**

| Item | Estimate | Evidence / blocker |
|---|---|---|
| Serialise 34 non-merge commits into reviewable PRs (rebase onto `elnosh/main`; strip the module rename `ae6963f`, the greatspectations submodule `f90cf2a`, the spec-instrumentation commits `4a59a19`/`386aaf2`, and the 53-file `b7146ab` re-import) | **5–10 d** | `wallet/wallet.go` alone diverges by 495 changed lines; `wallet/keyset.go` by 137 |
| Resolve the conflicting V2-keyset designs (root PR #146/Lescuer97 vs fork `c1b2095`) | **2–5 d** + design discussion | both sides implemented V2 independently; neither adopted the other |
| Get the offline-send feature accepted | **unbounded** | it changes `Send` semantics; upstream has ignored it for 14 months |
| **Await review** | **unbounded — expected value ≈ 0** | §5a: 0 commits in 13 months; PR #149 blocked 12 months; issues #150/#152 (filed by the fork's own steward) unanswered |
| Module-path rewrite in our repo (see (ii) below) | **2–4 d** | 2 non-test files, 9 test files, 5 `go.mod` files |
| **Total** | **≈ 9–19 d of our work, plus a wait with no counterparty** | |

**Blocker:** not technical — **there is no one at the other end.** Also note
(i-a) would *lose* the root's own 6 unadopted commits in the process unless they
are merged first.

**(i-b) Contribute the residual to `OpenTollGate/gonuts-tollgate` (the live
fork).** This is **already our practice**: PRs #19–#22 merged 2026-08-25 in
days; PR #23 is open (we hold local clones of its branch). Cost to finish the
outstanding work: **0–2 d**. But this does **not** un-fork anything — it is
maintenance *of* path (iii). *Recorded so it is not mistaken for a solution.*

### Path (ii) — drop the fork's patches, vendor/depend on a pinned upstream release

Target: `github.com/elnosh/gonuts@v0.4.2` (the root's newest release).

| Item | Estimate | What concretely breaks |
|---|---|---|
| Rewrite imports `github.com/OpenTollGate/gonuts-tollgate/*` → `github.com/elnosh/gonuts/*` | **2–3 d** | 2 non-test files (`tollwallet/gonuts_wallet.go`, `tollwallet/tollwallet.go`), 9 test files, 5 `go.mod`/`go.sum` pairs (incl. the v0.10.0 skew in `scripts/token-recovery`) |
| `TollWallet.SendWithOverpayment` (`tollwallet.go:241-272`) — delete or reimplement | **delete: 0.5 d / reimplement: 3–10 d** | **Reseller mode loses overpayment tolerance.** Upstream `Send(amount, mintURL, includeFees)` returns exact proofs and errors when the exact split is impossible — the whole reason the fork exists (`cf693d5`, `374ab28`). Callers: `merchant/merchant.go:984`, `merchant/merchant_degraded.go:105` |
| Restore V2/short keyset-ID support (`DeriveKeysetIdV2`, `IsKeysetIdV2`, `resolveShortKeysetIds`, NUT-13 V2 path hashing) | **5–15 d** | **Hard product breakage until done**: the root has no V2 keyset IDs at all, so any mint issuing short keyset IDs and any V4 token carrying them stops working. This is a compatibility cliff, not a nuance. |
| Re-apply the correctness/security fixes (rows 10–34) | **5–15 d** | If not re-applied, you re-ship: proof loss in swap (`7dc430b`), the HTLC signature-enforcement hole (`296c7bf`), wrong-keyset DLEQ verification (`9fefcd0`), the `MultiMintPayment` race (`2fe073c`/`1d232bf`), empty-slice panics (`28b728c`), NUT-20 wire-format error (`997bb03`), missing NUT-04 accounting fields (`3fb9af8`). Several are CVE-shaped. |
| **Net effect** | **≈ 4–8 weeks, and the end state is NOT grade A** | Every re-applied fix becomes **our** patch set on top of an upstream that no longer merges → **grade B forever**, with grade-B's permanent re-base cost. And you still do not get a *current* upstream: you get `v0.4.2`, which is not even an ancestor of the root's `main`. |

**Verdict: DISQUALIFIED.** It looks like the cheap option ("just pin upstream")
and is the most expensive one, because the fork contains a security fix, a
funds-safety fix, and the only V2-keyset support in existence. The honest name
for path (ii) is *"rewrite the wallet's Cashu layer from a dead root and take
back every bug the fork fixed."*

### Path (iii) — keep the fork (status quo)

| Cost | Measured value |
|---|---|
| Router commits touching the wallet-dependency surface, last 6 months | **22** (`src/tollwallet/`, `src/go.mod`, `src/go.sum`), by: Amperstrand 15, `c03rad0r` 3, Felix 3, Matt Van Horn 1 |
| …of which **pure pin/dep surgery** | **10** — including `1030db6` *deps: rename gonuts module Origami74 → OpenTollGate, bump v0.10.0*, `edf8832` *correct gonuts-tollgate v0.9.0 go.sum hashes*, `005493c` *bump v0.9.0*, `f57c487` *bump v0.8.0*, `3728875` *V2 keyset swap crash — bump v0.7.4 → v0.7.6*, `1ac26b0` *(ours) bump v0.7.1 → v0.7.4 for swap-counter race*, `3456dbf` *drop felixfelix-bot fork replace — build against OpenTollGate gonuts-tollgate v0.11.1*, `3275242` *align sub-module gonuts replace directives*, `a6a01cf` *sync all dependencies across 14 go.mod files + add drift checker* |
| …of which **gonuts-specific** | **8** |
| Our own commits into the fork (last 6 months) | **7** authored commits (4 merged PRs #19–#22 + 1 open branch) + 4 merge commits by `c03rad0r` |
| Module-path renames survived, in 14 months | **2** (`github.com/Origami74/gonuts-tollgate` at v0.6.x/v0.7.x; `github.com/OpenTollGate/gonuts-tollgate` from v0.10.0) |
| Branches in this repo carrying `replace github.com/Origami74/gonuts-tollgate => …` | **327** (across 1,075 branch refs inspected) |
| Branches pointing the dependency at **our own fork** (`=> github.com/felixfelix-bot/gonuts-tollgate v0.11.1`) | **50** — i.e. we *have already forked the fork*, and only dropped it from `main` in Aug 2026 (`3456dbf`, PR #361) |
| Branches pointing it at `github.com/Amperstrand/gonuts-tollgate` | **32** |
| Branches still on `github.com/elnosh/gonuts` directly | **4** (`replace github.com/elnosh/gonuts => github.com/Amperstrand/gonuts`) — one of which (`upstream/feat/testing-framework-validation`) still requires the pseudo-version `v0.3.1-0.20250123162555-7c0381a585e3`. **This is the same pattern the card observed in the separate `fips-exit-gate` repo: yes, a second project already depends on the root directly.** |
| Release surface we consume | 13 tags, **0 GitHub Releases**; `v0.11.0` never pushed; `v0.7.2` only on Amperstrand; `v0.7.3`/`v0.7.4`/`v0.8.0` tagged off-`main` |
| Issue-tracking surface | **none** (`has_issues: false` on all 3 fork generations) |
| Bus factor of the steward | **1** (`Amperstrand`, 29 of 40 commits; org has no publicly visible members) |
| **Observed burn rate** | **≈ 4 dependency commits/month**, ~2 of them pure pin surgery → **≈ 1–2 engineer-days/month baseline**, plus a spike whenever a mint breaks us (that spike has happened at least twice: `3728875` V2-keyset swap crash; `1ac26b0` swap-counter race) |
| **Standing risk** | the root stays dead permanently, and the one steward of the live fork stops. At that point the fork's 40 commits become **ours** with no upstream at all — i.e. the fork-ownership problem this programme exists to solve, with the escape hatch (upstream) already welded shut. |

**Verdict: least-bad today; not a resting place.**

### 7d. The option the card did not ask for, but which every path needs

Because the coupling is **2 non-test files in one package** (§6a), the whole
decision is reversible for a small fixed price. Whatever is chosen, the cheapest
insurance is to keep `github.com/OpenTollGate/gonuts-tollgate` out of every
package except `src/tollwallet/` (it already is, except in 9 test files), and to
re-point the tests at the `WalletPort` seam rather than the concrete library
(`merchant/offline_wallet_integration_test.go` is the largest such leak at ~650
lines and imports `gonuts-tollgate/wallet` and `nut04` directly). That is
**1–2 d** and it is what makes (i)/(ii)/(iii) a *choice* rather than a fate.

---

## 8. A/B/C grade with evidence

Metric (from `02-method/overlooked-metrics.md`, "Added 2026-09-13 —
fork-ownership risk"):

| Score | Meaning in the metric |
|---|---|
| A | upstream maintains it and we consume releases |
| B | upstream maintains it, but not our platform — we carry a platform patch set |
| C | we own a fork outright |

**Grade: `C`.**

**Evidence for C**

| # | Measurement | Value |
|---|---|---|
| 1 | **Our commits touching the dependency, last 6 months** | **22** in `src/tollwallet/` + `src/go.mod`/`go.sum` (15 Amperstrand, 3 `c03rad0r`, 3 Felix, 1 Matt Van Horn); **10** are directly pin/dep surgery, **8** of those gonuts-specific. Two of them were *unblocking* re-pins of the dependency to pick up a fix (`1ac26b0`: v0.7.1→v0.7.4 for the swap-counter race; `3728875`: v0.7.4→v0.7.6 for the V2-keyset swap crash). |
| 2 | **Upstream releases not adopted** | Root: **2 of 2** recent releases unadopted (`v0.4.1`, `v0.4.2`) plus 6 commits. The root's own `v0.4.2` tag is not an ancestor of the root's `main`. |
| 3 | **Upstream CI coverage of our platform** | **None.** All four repos in the chain run `ubuntu-latest` (amd64/glibc) only. No musl, no arm, no OpenWrt. Per the metric this alone caps the grade at **B**. |
| 4 | **We have already forked the fork** | `github.com/OpenTollGate/gonuts-tollgate => github.com/felixfelix-bot/gonuts-tollgate v0.11.1` appears on **50** of our branches; it was only removed from `main` on 2026-08-27 (`3456dbf`, PR #361). |
| 5 | **No release process to consume** | 13 tags, **0 GitHub Releases**, 0 release notes, 0 signed artifacts; `v0.11.0` never pushed; `v0.7.2` exists only on the intermediate fork; 3 tags sit off `main`. |
| 6 | **No issue tracker anywhere in the fork chain** | `has_issues: false` on Origami74, Amperstrand and OpenTollGate. There is no public place to report or track the bug we are about to hit. |
| 7 | **Root upstream is dead** | 0 commits in 13 months; oldest open PR 12 months, blocked, 0 comments; two issues filed 2026-07-24 by the fork's own steward still unanswered. So the fork cannot be retired by merging into the root on any timescale. |
| 8 | **Steward bus factor 1, no governance** | Amperstrand authored 29 of 40 delta commits; the org has no publicly visible members; the module path was renamed twice in 14 months, each time forcing repo-wide `replace` surgery (327 branches). |
| 9 | **Path (ii) is closed** | Dropping the fork's patches would revert a signature-enforcement fix, a proof-loss fix, all V2-keyset support, and the reseller overpayment path (§6, §7(ii)). A dependency you cannot leave without shipping known bugs is a dependency you own. |

**Honest counter-argument (kept on the record, per this branch's convention)**

An `A`-leaning reading is defensible and must not be hidden: `OpenTollGate/gonuts-tollgate`
is *actively maintained by someone other than us* (Amperstrand, 29/40 commits,
last push 2026-08-26), and we **do** consume its tags — our pin `v0.11.1` is the
newest tag and its tree is identical to `main`'s tip. Our four PRs merged in days.
On a strict reading of *"do we own a fork outright?"*, the answer is **no**: we
are a contributor and consumer, not the owner. On that reading the grade is `B`
(the fork is not tested for musl/arm — evidence #3).

**Why C wins anyway:** the metric's stated purpose is *not* to describe the
current maintainer but to measure the **standing tax of depending on a downstream
copy of someone else's code**, and to answer *"will we fork again?"*. On that
question the record since the metric was written is unambiguous: we have already
forked the fork (50 branches, evidence #4); we spend ~4 commits/month on the
dependency surface, half of it pin surgery (#1); the root can never absorb the
delta (#7); and there is no release or issue process to plan around (#5, #6). The
grade `C` here means **"the dependency is an unreleasable fork whose upkeep lands
on us whether or not our name is on it"** — which is the outcome the programme
exists to escape. The mitigating fact (an active external steward) is why the
*incremental* cost of staying put is lower than a naive `C` implies, and it is
why §7(iii) remains the recommendation *for now* rather than an emergency.

**Comparative note for T9/T13:** this is a `C` that is *live* rather than
abandoned — a materially different risk profile from `nucula`'s bus-factor-1 /
no-licence position, and from `CDK`'s self-declared ALPHA. All three must be
scored on the same table; this document supplies the gonuts row.

---

## 9. UNVERIFIED / open items

1. **Whether `elnosh` (the root maintainer) would accept any of the fork's
   patches if asked.** No maintainer statement exists. The `blocked` PR #149 and
   the unanswered self-described-bug issues (#150/#152) are behaviour, not a
   policy. Do not upgrade "no counterparty observed" into "refused".
2. **Whether `Amperstrand` and the `OpenTollGate` organisation are the same
   principal.** `orgs/OpenTollGate/members` returns empty, which the API cannot
   distinguish from private membership.
3. **Whether the fork's V2 keyset derivation (`DeriveKeysetIdV2`,
   `crypto/keyset.go:196`) is spec-correct against NUT-02 v2.** Not checked
   against the spec here; out of T10's scope. Relevant to T1b/E10.
4. **The actual mint-compatibility blast radius of losing V2/short keyset IDs.**
   §6c/§7(ii) assert the mechanism (root has neither `DeriveKeysetIdV2` nor
   `resolveShortKeysetIds`); *which* production mints the router talks to would
   break was not measured. That is a T1b/E10 measurement.
5. **Whether `~/repos/gonuts-pr23-red` and `~/repos/gonuts-pr23-build-2` are
   authoritative.** They are checkouts of the branch behind the **unmerged** PR
   #23 (`fix/empty-proofs-guard`, head `d5ef357`, authored by `Amperstrand`,
   opened 2026-08-26, still open). Treated as research clones only.
6. **Effort estimates in §7** are estimates by one engineer, not measurements.
7. **Whether `wallet/config.go`-equivalent on-disk formats are
   migration-compatible with the root.** `wallet.Config` and `LoadWallet` are
   identical between root and fork tip (§6c) and the store is BoltDB in both, but
   no migration was attempted. This belongs to T6.

---

## 10. Reproducing this document

```bash
# --- provenance (gh CLI authenticated) ---
for r in elnosh/gonuts Origami74/gonuts-tollgate \
         Amperstrand/gonuts-tollgate OpenTollGate/gonuts-tollgate; do
  gh api "repos/$r" --jq '{full_name, fork, parent: .parent.full_name,
    created_at, pushed_at, default_branch, open_issues_count,
    stars: .stargazers_count, license: .license.spdx_id,
    archived, has_issues}'
  gh api "repos/$r/tags?per_page=100" --jq '.[] | "\(.name) \(.commit.sha[0:8])"'
  gh api "repos/$r/releases?per_page=50" --jq length   # 0 for the fork
done
gh api "repos/elnosh/gonuts/commits?since=2025-09-13T00:00:00Z" --jq length   # 0
gh pr  list --repo elnosh/gonuts --state open --limit 50
gh pr  list --repo OpenTollGate/gonuts-tollgate --state all --limit 60
gh api repos/elnosh/gonuts/pulls/149 --jq '{mergeable_state, comments, review_comments}'

# --- merge bases and the delta (local clone, read-only fetches) ---
cd ~/repos/gonuts-tollgate
git fetch --no-tags https://github.com/Amperstrand/gonuts-tollgate.git  refs/heads/main:refs/research/amp/main
git fetch --no-tags https://github.com/elnosh/gonuts.git               refs/heads/main:refs/research/elnosh/main
git fetch --no-tags https://github.com/Origami74/gonuts-tollgate.git   refs/heads/main:refs/research/origami/main
git merge-base refs/research/amp/main HEAD          # 8e3b21b
git merge-base refs/research/elnosh/main HEAD       # 4d3aebf
git rev-list --no-merges --count 4d3aebf..4b7d125   # 34
git rev-list --merges    --count 4d3aebf..4b7d125   # 6
git log --oneline --reverse 4d3aebf..4b7d125        # the full delta
git log --oneline refs/research/elnosh/main ^4b7d125  # 6 unadopted upstream commits
git diff --stat refs/research/elnosh/main 4b7d125   # 77 files, +5975/-745
git diff refs/research/elnosh/main 4b7d125 -- wallet/wallet.go   # the API diff
git show 4b7d125:wallet/send_options.go             # the router's only fork-only dependency

# --- the router side ---
cd /home/c03rad0r/worktrees/wallet-research/src
grep -rl "OpenTollGate/gonuts-tollgate" --include=*.go . | grep -v _test.go   # 2 files
grep -Hn "gonuts-tollgate" ../scripts/token-recovery/go.mod src/cli/go.mod \
  src/go.mod src/merchant/go.mod src/tollwallet/go.mod
grep -rn "SendOffline" --include=*.go .                                       # 0 hits
sed -n '240,272p' tollwallet/tollwallet.go        # SendWithOverpayment
git -C /home/c03rad0r/worktrees/wallet-research log --since=2026-03-13 \
  --format='%h|%ad|%an|%s' --date=short -- src/tollwallet/ src/go.mod src/go.sum
```
