# Wallet Storage on mmap-incapable Overlays (jffs2): ext4-loop Now, mmap-free Engine Later — Architecture Decision

> **Status: Proposed (2026-10-24).** Nothing in this record is in force yet.
> Acceptance is a maintainer action — the drafting account may not accept its
> own proposal, and no PR here claims it has been accepted.

## Context

On every squashfs + jffs2-overlay target class (NOR-flash ipq40xx/ath79/ramips
small-flash lanes — including the `mips_24kc`/`mipsle` release lanes), the
wallet cannot initialize at all (#583, #1035): bbolt requires a shared `mmap`
of its database file, and jffs2 — log-structured, compressed, erase-block
aligned — has never provided page-cache-coherent shared mappings. Until
#1078 the failure also lied about its cause ("no reachable mints" while every
mint answered), which cost days of triage; the underlying storage half of the
storage-degraded family is what this record decides.

**The failing syscall is now pinned, not suspected.** Reproduced 2026-10-24 on
ai-legion (kernel 7.0, `modprobe nandsim` + `mkfs.jffs2` + `mtdblock`; bbolt
v1.4.0 — the version the gonuts fork pins — opened exactly as
`storage.InitBolt` opens it):

```
# direct jffs2:
mmap(NULL, 32768, PROT_READ, MAP_SHARED, 3, 0) = -1 EINVAL (Invalid argument)
bolt.Open → "invalid argument"          # first failure, BEFORE any flock

# overlayfs with upper=jffs2 (the DUT stack, WS-AP3915i @ OpenWrt 25.12.5):
mmap(NULL, 32768, PROT_READ, MAP_SHARED, 3, 0) = -1 EINVAL (Invalid argument)
bolt.Open → "invalid argument"          # byte-identical to #1035's log line

# controls: ext4 → same binary succeeds (bolt.Update nil);
#           jffs2 MAP_PRIVATE → mapping succeeds (see A2 for why it is useless)
```

So: NOT the file lock, NOT an armv7 quirk, NOT 25.12-specific — the shared
read mapping itself, the same signature as etcd-io/bbolt#258 and
boltdb/bolt#592/#272. Upstream considers jffs2 out of scope ("the mmap is
pretty core to Bolt's implementation"). Physical-DUT confirmation under the
real NOR stack remains desirable but is infra-blocked (bench seat dark since
2026-10-20; PoE switch wedged — see the tracker row); the overlayfs-upper
reproduction above closes the interpretive gap.

**One simplifying fact:** wallets on jffs2 never initialized, ever — the
16 KiB db is created and init fails immediately. No wallet ever held value on
a jffs2 overlay, so the migration in D1 moves aside an empty husk. There is
no balance-migration risk in this family.

Constraints that shape the decision: the binary stays `CGO_ENABLED=0` for
mips/mipsel/arm/arm64/x86; `src/tollwallet/port.go` (WalletPort) is the
declared seam for alternative backends; the CDK sidecar is the declared
long-term wallet direction; kmod availability is per-target SDK feed truth
(`packaging/build-inputs.json`).

## Decision

**D1 — near term (device-level): the wallet directory lives on a small ext4
loop image when the overlay is mmap-incapable.** `99-tollgate-setup` (or a
packaging uci-default, whichever the packaging review prefers) detects the
condition — `stat -f -c %T` on the wallet dir answering `jffs2`, or the
overlay's upper layer resolving to a jffs2 device via `/proc/mounts` — and
then, once per device:

1. creates `/etc/tollgate/wallet.ext4` (default 8 MiB; growth policy in Q2),
   `mkfs.ext4 -j` **with journal** (fund safety outranks wear; see Q1),
2. `losetup -f` it (requires `kmod-loop` in the affected lanes' package
   sets — feed truth, discovered, not enumerated),
3. mount persists across boots via fstab/procd,
4. the wallet path the backend uses becomes the mountpoint
   (`tollwallet.New(walletPath=...)` receives a directory today; no code
   change in the wallet stack itself),
5. any husk `wallet.db` from failed pre-ADR init attempts is moved to
   `wallet.db.jffs2-husk` beside the mount, never deleted.

The loop device reads the backing file with `read()` — no mmap of the jffs2
inode is involved — and ext4 inside the image mmaps normally. bbolt works
unmodified.

**D2 — long term: the mmap-free engine arrives with the CDK sidecar
migration, not as a bbolt fork.** SQLite (CDK's persistence) does not require
mmap (`mmap_size` defaults off); a pure-Go mmap-free KV in the fork was
considered and deferred — it re-implements an allocator for one filesystem
class. This record does not choose the engine; it removes the blocker now
and leaves the engine choice to the sidecar-migration ADR. D1 must not grow
roots: when the sidecar lands, the loop image is the thing to migrate OFF of.

**D3 — guardrail: fail at install, not at first payment.** Setup refuses to
leave a wallet path on an mmap-incapable filesystem without the loop image
(the #1078 honest-reason work keeps the runtime surface truthful meanwhile).
A packaging test asserts the detection predicate against both spellings of
the condition (direct jffs2; overlayfs with jffs2 upper) — mutation-checked
like every fence in this repo.

## Alternatives considered (and refused)

**A1 — do nothing; rely on the honest error.** Leaves the NOR fleet — real
deployment lanes in the release matrix — permanently wallet-less. Rejected:
availability is the product.

**A2 — bbolt fork with `MAP_PRIVATE`.** The mapping succeeds on jffs2, and it
is a trap: private pages are COW and diverge from the file after bbolt's
`pwrite` writes — silent stale reads, then corruption. Refused on fund-safety
grounds (boltdb/bolt#592 maintainer's own assessment).

**A3 — tmpfs wallet + periodic sync to flash.** A power-loss window in which
proofs exist only in RAM is a fund-loss window. Refused.

**A4 — convert the overlay to ubifs/f2fs.** Firmware-image scope on
third-party target kernels; out of this repo's control. Refused here, noted
for the conwrt side.

## Consequences

- Affected lanes gain wallets with no wallet-stack code change; the cost is a
  packaging/setup change plus two runtime dependencies (`kmod-loop`,
  `e2fsprogs`) on exactly the mmap-incapable lanes.
- Open measured questions (each needs a bench answer before rc):
  - **Q1 — NOR wear:** ext4+journal inside one image file vs today's direct
    (failing) writes; journal-on is the safe default, wear soak is a PRTA
    24h-class campaign.
  - **Q2 — image growth:** 8 MiB default, headroom policy for proof-heavy
    wallets (alert, not auto-grow, in v1).
- Evidence and repro method: `ai-legion:/tmp/opencode/tg-storage/evidence/
  jffs2-mmap/` (traces, the probe program, both controls).
