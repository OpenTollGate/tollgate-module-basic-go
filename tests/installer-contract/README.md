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
  --host 127.0.0.1 --port 2222 --ssh-key ~/.ssh/tg_vlab_ed \
  --out ~/installer-contract.out
```

Prerequisites:

- a **pristine** OpenWrt 25.12 VM (the lane runs `firstboot -y` itself unless
  `--skip-pristine`) with **key-based root ssh** already working;
- `openssl` on the lane host (credential comparison runs host-side — the
  device's busybox has no crypt tool for this);
- the built `.apk`.

Exit 0 = all contracts hold. Output idiom matches the happy-path suite:

```
ICCHECK <id> <PASS|FAIL|SKIP> <detail...>
ICRESULT total=N pass=N fail=N skip=N
ICFAILED <ids...>
ICEXIT 0|1
```

## The known-red checks

1. `contract:credential-applied` — in the default `--credentials-mode file`
   (the #894 channel) this must PASS: the staged secret becomes root's hash
   (sha256crypt comparison host-side) and `contract:credentials-consumed`
   asserts both files were unlinked. Running with `--credentials-mode env`
   instead demonstrates **#888**: apk-tools on 25.12 drops the installer's
   environment, so the env channel cannot apply the credential — that mode
   stays red until apk-tools behavior changes, by design.
   `contract:provisional-lifecycle` pins the safe degraded path whenever the
   chosen credential did NOT apply (random provisional credential, never
   printed, marker present, FIRST-RUN page offered, board still fail-closed).
2. `listeners:8090` / `listeners:8443` — **the acceptance criterion of #890**:
   LuCI's pair in board mode did not converge after a true fresh-image
   install (the `main` uhttpd section ends up with no listeners), while the
   same package converged them on an already-booted device. Keep them
   asserted; they go green when the flip's LuCI half converges on the
   fresh-install path.

A fence that cannot fail is decoration; these are shipped red on purpose.

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

## What is out of scope here

Radios/SSID, br-private flows, real captivity against NoDogSplash on hardware,
ipk/opkg-era installs (the 24.10 frozen lane), and the portal SPA behavior
(happy-path's ground).
