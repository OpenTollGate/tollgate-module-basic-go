# Installer-contract lane (built package → pristine apk-era VM)

Asserts the **installer's** contracts against a live OpenWrt 25.12 (apk-era)
device, using a built `.apk` and the documented operator flow — including the
install-time credential environment (`TOLLGATE_ADMIN_PASSWORD`,
`TOLLGATE_PRIVATE_WIFI_PASSWORD`). It complements
[tests/happy-path/](../happy-path/) (which exercises the artifact bytes on the
lane host) by covering what only a real install can: apk dependency closure,
uci-defaults execution, credential lifecycle, listener/mapping convergence and
reboot persistence.

Origin: every check here was first verified by hand on the vlab lane on
2026-10-11 (OpenWrt 25.12.5 x86-64, `tollgate-wrt-0.6.0_rc1-r0.apk`), which is
how #888 was found. This script is that session, repeatable.

## Run it

```bash
bash tests/installer-contract/run.sh \
  --artifact ~/rel-artifacts/apk-x86-64/tollgate-wrt-…apk \
  --source-sha "$(git rev-parse HEAD)" \
  --host 127.0.0.1 --port 2222 --ssh-key ~/.ssh/tg_vlab_ed \
  --serial 127.0.0.1:4554 \
  --out ~/installer-contract.out
```

Prerequisites:

- a **pristine** OpenWrt 25.12 VM (the lane runs `firstboot -y` itself unless
  `--skip-pristine`) with **key-based root ssh** already working — or a serial
  console (`--serial`, qemu `-serial telnet:…`) so the lane can bootstrap the
  dhcp fix and its key itself after the reset;
- `openssl` on the lane host (credential comparison runs host-side — the
  device's busybox has no crypt tool for this);
- the built `.apk`, and the commit it was built from (`--source-sha`; without
  it, `provenance:source-sha` reports NOT_VERIFIED and the run fails).

Exit 0 = all contracts hold. Output idiom matches the happy-path suite:

```
ICCHECK <id> <PASS|FAIL|SKIP|NOT_VERIFIED> <detail...>
ICRESULT total=N pass=N fail=N skip=N not_verified=N
ICFAILED <ids...>
ICEXIT 0|1
```

**Fail-closed semantics.** `NOT_VERIFIED` is a first-class, failing status: a
required check whose input could not be obtained — no VM reachable, a phase
skipped without `--dev`, provenance not recorded — is never reported as PASS
or silently skipped. `--skip-pristine`/`--skip-reboot` are dev-only escapes;
without `--dev` they fail the run as NOT_VERIFIED. Every run also writes
`verdict.json` into the evidence dir: per-check statuses with details, the
artifact's sha256, the recorded source SHA, the image identity
(`/etc/openwrt_release`, kernel, apk version) and the lane host environment —
plus `refuse.log` / `install.log` (raw apk output) and `checks.tsv`.

## Prove the gate can fail, before spending a VM on it

```bash
bash tests/installer-contract/selftest.sh
```

Runs the exact check logic (`checks.sh`, shared with the live lane) against
planted fixtures, no VM and no network: the #899 tear (a listenerless
`uhttpd.main`), including the stealth variant where another instance claims
the ports so netstat liveness alone would read green; the #911 shape
(apk-tools completing the transaction — package registered, service started,
exit 1, guidance suppressed — despite the failing preinst); mapping
mismatches; and crash-loop log signatures. Healthy fixtures stay green, so
the controls are not tautologies. `SELFTESTRESULT total=18 ok=18 bad=0`,
exit 0.

## The rc3 contract (and the remaining known-red)

Since #902 + #907 (v0.6.0-rc3) the package **sets no credential at all**
and the **preinst refuses any install while root has no password**. The
lane asserts exactly that:

- `contract:preinst-refuses-empty-root` — a pristine install attempt must
  fail cleanly with the guidance message and register nothing;
- `flow:operator-sets-password` + `contract:package-sets-no-credential` —
  the operator sets the root password, the install succeeds, and root's
  hash is byte-identical across the install (nothing applied, replaced or
  generated); `contract:no-provisional-marker` pins the same on disk;
- persistence: the operator's credential and the mapping survive a reboot.

Earlier revisions of this lane asserted the #894 staged-secret channel;
that mechanism was absorbed by #897 and then removed by #902's stronger
design — the history lives in #888.

The remaining known-reds are kept asserted on purpose:

- **#888's** `contract:credential-applied` (apk-tools does not deliver the
  install-time environment to the uci-defaults script);
- **#890's** `listeners:8090` / `listeners:8443` and, reading the config
  itself rather than the wire, `listeners:uhttpd-sections` /
  `mapping:port-ownership` — the #899 tear (a listenerless `uhttpd.main`)
  still present on fresh-image installs;
- **#911's** `contract:preinst-refuses-empty-root` — apk-tools 25.12
  completes the transaction (package registered, service started, exit 1,
  guidance suppressed) despite the failing preinst. The check reports the
  observed shape verbatim and goes green only when apk actually refuses.

A fence that cannot fail is decoration; the reds are shipped on purpose.

## The pristine phase trusts nothing

On one 25.12.5 x86 image `firstboot -y && reboot` silently kept `/etc`
(dropbear keys, an installed tollgate-wrt, root's old hash) — a false
"pristine" that turns every credential assertion into a lie. The lane
therefore (a) forces `reboot -f`, and (b) **verifies** the clean state
(no tollgate-wrt registered, root credential empty) before installing.
For a snapshot-mode VM, the true reset is restarting the VM process
(qemu `snapshot=on` discards the volatile disk); labgrid release/acquire
is lock-only on this provider and does not restart it.

## vlab notes (lessons already paid for)

- The qemu user-net VM ships with `br-lan` static (`192.168.1.1`, no default
  route): `uci set network.lan.proto='dhcp' && uci commit network &&
  /etc/init.d/network restart` gives it `10.0.2.15` via slirp, and only then
  do hostfwd ssh and outbound feeds work.
- Install a lane key via the serial console (`echo <pubkey> >
  /etc/dropbear/authorized_keys`); `scp` needs `-O` or a `cat`-pipe — the
  guest's dropbear has no sftp-server.
- VM host keys churn with snapshot reprovisioning: the lane already passes
  `UserKnownHostsFile=/dev/null`.
- The x86 VM has **no radios and no br-private**: SSID derivation, private
  networking and band steering need the physical lane (PRTA journey on the
  x1860 seats).

## What is out of scope here (what a green run does NOT prove)

Radios/SSID, br-private flows, real captivity against NoDogSplash on hardware,
ipk/opkg-era installs (the 24.10 frozen lane), and the portal SPA behavior
(happy-path's ground). Beyond the physical-lane items:

- **No payment, wallet or fund-safety coverage** — nothing here mints, swaps
  or melts; the cloud-lab conformance lane owns that surface.
- **apk's scriptlet stderr is suppressed by apk-tools itself** — the gate
  observes the guidance text only if apk chooses to surface it; its absence is
  recorded as an observed fact, not assumed either way.
- **x86-64 VM only** — target-specific scriptlet or kernel behavior on
  mips/arm is not exercised by this lane.
- **One install + one reboot** — no upgrade path, no soak, no repeated
  install loops (the install-path lane on the PRTA bench owns upgrade/parity).

