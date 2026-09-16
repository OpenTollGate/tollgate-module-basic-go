# Host setup — running mipsel (and aarch64) OpenWrt binaries on the x86_64 host

Recorded 2026-09-16 so the mipsel run is reproducible. Host: Ubuntu, kernel 6.17,
`docker` available, passwordless `sudo`.

## Goal

Execute an OpenWrt-musl **mipsel** binary on the build host (used for Spike N:
`experiments/nucula-port/output-link-mipsel.txt`) without a physical mipsel
router.

## 1. Install the qemu user-mode interpreters

```bash
docker pull multiarch/qemu-user-static
mkdir -p /tmp/qemu && cd /tmp/qemu
docker run --rm --entrypoint /bin/sh multiarch/qemu-user-static -c \
  'cd /usr/bin && tar cf - qemu-mipsel-static qemu-mipsn32el-static qemu-mips-static' > qemu.tar
tar xf qemu.tar -C /tmp/qemu
sudo cp /tmp/qemu/qemu-*-static /usr/bin/ && sudo chmod +x /usr/bin/qemu-*-static
```

(`docker cp` fails on the image's symlinks — use `tar`.)

## 2. Register binfmt (session-scoped; lost on reboot)

```bash
sudo modprobe binfmt_misc
[ -d /proc/sys/fs/binfmt_misc ] || sudo mount -t binfmt_misc binfmt_misc /proc/sys/fs/binfmt_misc
docker run --rm --privileged multiarch/qemu-user-static --reset -p yes
ls /proc/sys/fs/binfmt_misc | grep mips   # qemu-mipsel, qemu-mips, ...
```

Caveat: for a **dynamically linked** musl mipsel ELF, direct execution via
binfmt still fails on the loader (`/lib/ld-musl-mipsel-sf.so.1`) — and the
kernel may match the broader `qemu-mipsn32el` handler first. **Always invoke the
interpreter explicitly with a sysroot.**

## 3. Build a mipsel musl sysroot from the OpenWrt SDK

```bash
mkdir -p ~/nucula/sysroot-mipsel/lib
docker run --rm --entrypoint /bin/sh openwrt/sdk:ramips-mt7621-v25.12.5 -c \
  'cd /builder/staging_dir/toolchain-mipsel_24kc_gcc-14.3.0_musl/lib && \
   tar cf - ld-musl-mipsel-sf.so.1 libc.so libgcc_s.so.1' > /tmp/mipsel-libs.tar
tar xf /tmp/mipsel-libs.tar -C ~/nucula/sysroot-mipsel/lib
```

## 4. Run

```bash
qemu-mipsel-static -L ~/nucula/sysroot-mipsel \
  ~/nucula/deps/out-mipsel/nucula_core_harness
```

Expected tail: `SELFTEST_RESULT suites=4 failures=0`.

The default aarch64 build is unaffected; `run-cross-link.sh` links aarch64 with
`-static-libstdc++` (dynamic libgcc_s + musl) and mipsel the same way.

## 5. Scratch checkout hygiene (CDK)

`~/r2-work/cdk-upstream` is a scratch upstream clone holding our experimental
crates and build dirs. Keep them out of `git status` **without** committing
upstream:

```bash
git -C ~/r2-work/cdk-upstream/info 2>/dev/null # (info/ lives in .git)
cat >> ~/r2-work/cdk-upstream/.git/info/exclude <<'EOF'
crates/cdk-probe/
crates/cdk-walletd/
target-daemon/
target-mintd/
target-mintd-aarch64/
target-walletd-aarch64/
target-walletd-mipsel/
target-mipsel/
EOF
```

The crate sources are archived in this branch under
`experiments/cdk-footprint/probe/` and `experiments/cdk-walletd/`.
