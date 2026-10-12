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

The remaining known-red is **#890's**: `listeners:8090` / `listeners:8443`
(LuCI's pair in board mode did not converge after a true fresh-image
install). Keep them asserted; they go green when the flip's LuCI half
converges on the fresh-install path.

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

## What is out of scope here

Radios/SSID, br-private flows, real captivity against NoDogSplash on hardware,
ipk/opkg-era installs (the 24.10 frozen lane), and the portal SPA behavior
(happy-path's ground).
