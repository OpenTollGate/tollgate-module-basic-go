# v0.6.0-rc1 gate verdict — software legs GREEN, hardware leg pending (commissioning session, 2026-10-08)

## Artifact matrix

12-row two-era matrix (apk on 25.12.0 SDK, ipk on 24.10.8 SDK). Ten rows verified as
built bytes with sha256 in the build tree:

| arch | apk (25.12) | ipk (24.10.8) |
|---|---|---|
| x86-64 | `d1b1ba5e…` | `a29a9877…` |
| mediatek-filogic (aarch64_cortex-a53) | `2eb9580f…` | `4bc3edb9…` |
| bcm27xx-bcm2711 (aarch64_cortex-a72) | `51f31f26…` | `02ed1ea1…` |
| ramips-mt7621 (mipsel_24kc) | `7a98f2f9…` | `5437f45a…` |
| ath79-generic (mips_24kc) | `292e4167…` | `17ffa8ad…` |
| bcm27xx-bcm2709 (arm_cortex-a7_neon-vfpv4) | reported complete by operator | reported complete by operator |

All ten rows above verified as built bytes in this workspace; full sha256s in the run
evidence (`/tmp/tollgate-build-artifacts`, campaign log dir on ai-legion).
bcm2709: the commissioning session reports both formats complete; the bytes are not
present in this workspace, and the one-line `EXPECTED_ARCH` fix (#717) is still open —
the owner should confirm the bcm2709 rows from that session's evidence before the tag.

## QEMU VM campaign (ai-legion, pristine 25.12.0 x86-64, snapshot=on)

**GREEN — 13 PASS, 0 product FAIL, 1 SKIP.** Pristine OpenWrt 25.12.0 x86-64 boot
(snapshot=on), one-transaction dependency-closure install (8 apks, `APK_RC=0`), then:
version `v0.6.0-rc1 commit 659de8a7` · service running · API `:2121` listening · portal
files present · CLI socket live at `/var/run/tollgate.sock` · `config.json` +
`/etc/tollgate/wallet.db` written · NDS up on `:2050` · NTP pre-auth nft rule present ·
same-version reinstall idempotent (service survives). Artifact bytes verified as the
release-baseline matrix apk (`d1b1ba5e…`). Two recorded FAILs were campaign-harness
defects (console-prompt echo polluting one assert; a stale `/etc` socket path) — both
superseded by marker-based re-checks, noted in the evidence file.

Evidence: `~/tollgate-vm/campaign-logs/rc1-result.txt` + full console transcript
`rc1-console.log` on ai-legion (boot → one-transaction closure install → check table).
Reboot-persistence remains skipped-by-design on this lane (snapshot=on boots are
pristine; it needs the persistent-disk variant of the place).

## Other gates already on record (this and the prior session)

- Go battery / release-check on the pinned baseline: READY FOR HARDWARE: YES.
- Conformance lane 5/5 GREEN (first fully-green run).
- Crash-window lane (#719): reproduced, triaged FALSE NEGATIVE — owed-grant recovers in
  ~5 s, post-recovery payment HTTP 200; assertion-vocabulary fix on branch
  `fix/crash-lane-assertion-vocabulary` (ready to merge).
- Conformance drift (#725): both sides fixed on branches (`fix/conformance-fast-subset-ids`
  here + PRTA matrix `subset: fast` markers on `feat/labgrid-vlab-venue`).
- rc1 on a real OpenWrt 24.10 VM: install/config/mint-probe/payment-RECEIVED verified;
  gate-open leg blocked by pre-existing base-image rot (0-byte stubs + kernel/kmod
  mismatch) — a base rebuild, not an rc1 defect.

## What the owner's word triggers

The software legs are green on the pinned baseline. The owner's go-ahead triggers:
1. merge the three ready branches (crash-lane assertion, conformance ids, #728 ARG fix)
   and re-run the release lane gates they touch (minutes);
2. confirm the bcm2709 matrix rows (or accept them from the commissioning session's
   evidence);
3. tag `v0.6.0` from the release lane's pinned commit — CI cross-compiles, publishes
   kind-1063 artifact events per arch/format on the Nostr mirrors, and the changelog
   `[Unreleased]` block finalizes into release notes;
4. the hardware bench acceptance (x1860/MT3000) stays the post-tag, pre-announce leg —
   it is the one gate this program could not run unattended (routers unreachable from
   ai-legion at commissioning time).

No action in this verdict tags or publishes anything by itself.
