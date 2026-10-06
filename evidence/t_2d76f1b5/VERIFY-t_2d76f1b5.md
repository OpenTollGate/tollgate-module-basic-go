# LINUX-HOST-6 SHIM-NFT — evidence (t_2d76f1b5)

Card: `t_2d76f1b5` — "authed_v4 set + per-MAC counters + flock + KiB quantisation".
Board: `tollgate-module-basic-go`. Deliverable: `nft.go` (the nftables backend of
the `ndsctl` host-mode shim, LINUX-HOST-5's module `src/cmd/tollgate-ndsctl`).

## What was authored

New module `src/cmd/tollgate-ndsctl/`:

| file | role |
|---|---|
| `go.mod` | `module tollgate-ndsctl`, `go 1.25.0` (matches the sibling nested modules) |
| `nft.go` | the deliverable: `Auth`/`Deauth`/`CountersKiB`, nft JSON decode, EEXIST/ENOENT classification, flock seam |
| `lock.go` | `lockGate` — exclusive `flock(2)` on `/run/tollgate/ndsctl.lock` (build tag `!nft_noflock`) |
| `lock_noflock.go` | `lockGate` with the lock compiled out — the negative control (build tag `nft_noflock`) |
| `stall.go` | `TOLLGATE_NFT_TEST_STALL_MS` test hook that widens the read-do-write window |
| `nft_test.go` | 11 unit tests against a stateful in-memory nft model |
| `lock_test.go` | cross-process flock mutual-exclusion test (re-exec pattern) |
| `netns_test.go` | kernel evidence tier (`-tags netns`, root, `TOLLGATE_NETNS=1`) |

`main.go` / `contract.go` / `backend.go` are LINUX-HOST-5's deliverables and are
**not** touched here; `nft.go` only adds files to that module.

## Behaviour, line by line against the card

* **auth adds an element to `authed_v4`, EEXIST = SUCCESS.** `authLocked` adds the
  address to *every* `authed_v4` set that exists (LINUX-HOST-4 mirrors it into each
  integrated table), one script per set, tolerating `IsEEXIST`. A duplicate `add`
  can never abort the run.
* **deauth deletes the element and removes the per-MAC counter rules and resets the
  baselines.** `deauthLocked` deletes the element, deletes the up/dl rules by handle,
  then deletes the named counters. A deleted named counter reads back as zero, which
  is the kernel side of the module's in-memory `ClearDataBaseline` assumption.
* **per-client `up_<key>`/`dl_<key>` counters.** `CounterKey` strips the MAC
  separators so the name is a legal nft identifier; the rules are literally
  `iifname "$AP" ether saddr "<mac>" counter name up_<key>` and the mirror
  `oifname "$AP" ether daddr "<mac>" counter name dl_<key>`.
* **flock on `/run/tollgate/ndsctl.lock`.** `withLock` wraps every mutating verb; the
  CLI is a separate process, so an in-process mutex does not cover it.
* **json reads `nft -j list counters` and divides by 1024 TRUNCATING.**
  `CountersKiB` returns `bytes / 1024` (Go integer division truncates toward zero).

## Reproduced evidence

Unit tier — `go test -race -count=1 ./...` → **11 passed, 0 failed** (`unit-verbose.txt`).

Cross-process flock — `TestLockMutualExclusion` launches 3 copies of the test binary,
each taking the lock and holding it 150 ms. With the lock: peak concurrent holders = 1
(part of the 11 passing). With `-tags nft_noflock`: **FAIL**, peak = 3
(`lock-negative-control.txt`).

Kernel tier, real `nft` 1.1.5 in a throwaway netns
(`cli 10.66.0.2 MAC 02:11:22:33:44:55` → `br-tg` → host forward → server `192.168.9.2`):

```
leg1 ok: two auths, one element
leg2 ok: concurrent auths left exactly one up + one dl rule
leg3 ok: up=4103 KiB dl=0 KiB after 4 MiB
leg4 ok: deauth removed element, rules and counters; json reads 0/0
--- PASS: TestNetnsNftBackend (3.67s)
```
(`netns-positive.txt`)

Negative control, same kernel tier with `-tags "netns nft_noflock"` (flock compiled out):

```
counter rules for 02:11:22:33:44:55 = 4, want exactly 2 (up + dl): the flock is not serialising
--- FAIL: TestNetnsNftBackend (2.65s)
```
(`netns-negative-control.txt`)

Note on leg3: 4 MiB of UDP payload measured as 4103 KiB. The counter counts L2/L3
frame bytes including headers, so it is slightly above the 4096 KiB payload — the
point of the leg is `> 0` and non-truncated, which holds.

## Honest limits / not proven

* Module integration with LINUX-HOST-5's `main.go`/`backend.go` is **not** proven — that
  card is still running and its files do not exist yet. `nft.go` compiles and its tests
  run as `package main` without a `main` function.
* No device/hardware run: this is a network-namespace reproduction, not the router bench.
* `$AP`, the table (`inet tollgate`) and the chain (`forward_gate`) are defaults from
  LINUX-HOST-2's rendered shape; if NETUP-RENDER names them differently the `Config`
  fields must be set by SHIM-CLI.
* The real nft error strings are matched by regex (`File exists` / `No such file or
  directory`); libnftables wording was confirmed against the live 1.1.5 binary.
