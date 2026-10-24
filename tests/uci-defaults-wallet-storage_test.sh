#!/usr/bin/env bash
# Offline tests for the wallet-storage policy lib
# (packaging/files/usr/lib/tollgate/wallet-storage.sh) and its two consumers
# (99-tollgate-setup, init.d/tollgate-wrt).
#
# The contract (docs/architecture/
# wallet-storage-mmap-incapable-overlay-decision.md): on an mmap-incapable
# overlay — jffs2 directly, or overlayfs whose upperdir is jffs2 (the
# squashfs NOR DUT stack) — the wallet directory is moved into a small ext4
# loop image; on anything else, nothing happens. The failing syscall is
# pinned in evidence/jffs2-mmap (bbolt v1.4.0, mmap MAP_SHARED EINVAL).
#
# Everything that would touch a live router is faked: `stat -f` answers from
# a fixture table, /proc/mounts is redirected via WALLET_MOUNTS_FILE, and
# modprobe/losetup/mkfs/fsck/mount/dd are PATH stubs that record their
# calls. The planted-drift verdict at the end sabotages the predicate in a
# COPY of the lib and requires the jffs2 case to fail — a fence that cannot
# fail is decoration.
#
# Usage: bash tests/uci-defaults-wallet-storage_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }
eq()  { if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi; }

LIB="packaging/files/usr/lib/tollgate/wallet-storage.sh"
SETUP="packaging/files/etc/uci-defaults/99-tollgate-setup"
INIT="packaging/files/etc/init.d/tollgate-wrt"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/walletdir"

# ------------------------------------------------------------- fake stat -f
# STAT_MAP: "<path><TAB><fstype>" lines; anything unmapped answers "ext4".
STAT_MAP="$TMP/stat.map"
export STAT_MAP
cat > "$TMP/bin/stat" <<'SHIM'
#!/bin/sh
# emulate `stat -f -c %T <path>` from the fixture table (path = last arg,
# first match wins — the table may carry stale rows from earlier cases)
shift $(( $# - 1 ))
type=$(awk -v p="$1" '$1 == p { print $2; exit }' "$STAT_MAP")
echo "${type:-ext4}"
SHIM
chmod +x "$TMP/bin/stat"
tab=$(printf '\t')
printf '%s%sjffs2\n' "$TMP/walletdir" "$tab" > "$STAT_MAP"

# --------------------------------------------------------- recording stubs
export STUB_LOG="$TMP/stub.log"
: > "$STUB_LOG"
for cmd in modprobe losetup mkfs.ext4 fsck.ext4 mount dd truncate df logger; do
    cat > "$TMP/bin/$cmd" <<SHIM
#!/bin/sh
echo "$cmd \$*" >> "\$STUB_LOG"
case "$cmd" in
losetup) [ "\$1" = "-f" ] && [ "\$2" = "--show" ] && echo /dev/loop9 && exit 0 ;;
df) echo "Filesystem 1K-blocks Used Available Use% Mounted"; echo "/dev/ubi0_1 32768 8192 $((64 * 1024)) 20% /overlay" ;;
modprobe) exit 1 ;;   # the no-kmod-available negative path
esac
exit 0
SHIM
    chmod +x "$TMP/bin/$cmd"
done
export PATH="$TMP/bin:$PATH"

calls() { awk -v c="$1" '$1 == c { n++ } END { print n + 0 }' "$STUB_LOG"; }
reset_calls() { : > "$STUB_LOG"; }

mounts_fixture() { # mounts_fixture <content...> — written to the mounts file
    printf '%s\n' "$@" > "$TMP/mounts"
    export WALLET_MOUNTS_FILE="$TMP/mounts"
}

# ------------------------------------------------------------ lib sourcing
lib_load() { # lib_load — source the REAL lib with sandboxed paths
    (
        # shellcheck disable=SC1090
        . "$ROOT/$LIB" >/dev/null 2>&1
    )
}

run_lib() { # run_lib <function> [args...] — run in a subshell with env set
    (
        export WALLET_DIR="$TMP/walletdir"
        export WALLET_LOOP_IMAGE="$TMP/wallet.ext4"
        export WALLET_IMAGE_MB=8
        # shellcheck disable=SC1090
        . "$ROOT/$LIB"
        "$@"
    )
}

# == predicate ===============================================================

mounts_fixture "/dev/mmcblk0p2 / ext4 rw 0 0"

r=$(run_lib wallet_fs_type "$TMP/walletdir")
eq "fs type reads the fixture table" "$r" "jffs2"

r=$(run_lib wallet_fs_type "$TMP/unmapped-dir")
eq "unmapped path defaults to capable (ext4)" "$r" "ext4"

# overlay resolution: longest overlay mount covering the dir wins — a root
# overlay ("/") covers everything, a deeper mount on the wallet dir outranks it
mounts_fixture \
    "overlay / overlayfs rw,lowerdir=/rom,upperdir=/overlay/rootupper,workdir=/overlay/work 0 0" \
    "overlay $TMP overlayfs rw,lowerdir=/rom,upperdir=/overlay/upper,workdir=/overlay/work 0 0" \
    "overlay $TMP/walletdir overlayfs rw,lowerdir=/rom,upperdir=/overlay/upper2,workdir=/overlay/work2 0 0"
mkdir -p "$TMP/walletdir"
r=$(run_lib wallet_overlay_upper "$TMP/walletdir")
eq "overlay upper resolves to the LONGEST covering mount" "$r" "/overlay/upper2"

# overlay upper jffs2 → needs image; upper ext4 → not. The wallet dir is
# covered by the root overlay here (mp "/"), whose upperdir the table marks
mounts_fixture \
    "overlay / overlayfs rw,lowerdir=/rom,upperdir=/overlay/upper2,workdir=/overlay/work 0 0"
printf '%s%soverlayfs\n%s%sjffs2\n' "$TMP/walletdir" "$tab" "/overlay/upper2" "$tab" > "$STAT_MAP"
run_lib wallet_needs_loop_image
eq "overlayfs with jffs2 upper needs the loop image" "$?" "0"

printf '%s%soverlayfs\n%s%sext4\n' "$TMP/walletdir" "$tab" "/overlay/upper2" "$tab" > "$STAT_MAP"
run_lib wallet_needs_loop_image
eq "overlayfs with ext4 upper does not" "$?" "1"

printf '%s%sext4\n' "$TMP/walletdir" "$tab" > "$STAT_MAP"
run_lib wallet_needs_loop_image
eq "direct ext4 does not" "$?" "1"

printf '%s%sjffs2\n' "$TMP/walletdir" "$tab" > "$STAT_MAP"
run_lib wallet_needs_loop_image
eq "direct jffs2 does" "$?" "0"

# == bring-up ================================================================

mounts_fixture "/dev/mmcblk0p2 / ext4 rw 0 0"
printf '%s%sjffs2\n' "$TMP/walletdir" "$tab" > "$STAT_MAP"
echo "wallet.db-husk-marker" > "$TMP/walletdir/wallet.db"
# pre-create the loop-control marker so the modprobe stub's failure is moot
mounts_fixture "/dev/mmcblk0p2 / ext4 rw 0 0"

ensure_case() { # ensure_case — run ensure_wallet_storage with /dev spoofed
    (
        export WALLET_DIR="$TMP/walletdir"
        export WALLET_LOOP_IMAGE="$TMP/wallet.ext4"
        export WALLET_MOUNTS_FILE="$TMP/mounts"
        # The lib checks for /dev/loop0 or /dev/loop-control; spoof via a
        # sourced override is NOT allowed (we test the shipped code), so the
        # stub modprobe is flipped to succeed for this case.
        export STUB_MODPROBE_OK=1
        # shellcheck disable=SC1090
        . "$ROOT/$LIB"
        ensure_wallet_storage
    )
}

# flip modprobe to succeed (fresh stub with env switch)
cat > "$TMP/bin/modprobe" <<'SHIM'
#!/bin/sh
echo "modprobe $*" >> "$STUB_LOG"
[ "${STUB_MODPROBE_OK:-0}" = 1 ] && exit 0
exit 1
SHIM
chmod +x "$TMP/bin/modprobe"

out=$(ensure_case 2>&1); rc=$?
eq "ensure on jffs2 succeeds" "$rc" "0"
eq "mkfs.ext4 was called once" "$(calls mkfs.ext4)" "1"
eq "losetup attached the image" "$(calls losetup)" "1"
eq "mount mounted the loop device" "$(calls mount)" "1"
[ -f "$TMP/walletdir/wallet.db.jffs2-husk" ] && ok "jffs2 husk preserved, not deleted" || bad "jffs2 husk preserved, not deleted"

# already-mounted → no new calls
reset_calls
mounts_fixture \
    "/dev/mmcblk0p2 / ext4 rw 0 0" \
    "/dev/loop9 $TMP/walletdir/wallet ext4 rw,noatime 0 0"
out=$(ensure_case 2>&1); rc=$?
eq "already mounted: success without work" "$rc" "0"
eq "already mounted: no losetup" "$(calls losetup)" "0"
eq "already mounted: no mount" "$(calls mount)" "0"

# mmap-capable → no-op
reset_calls
mounts_fixture "/dev/mmcblk0p2 / ext4 rw 0 0"
printf '%s%sext4\n' "$TMP/walletdir" "$tab" > "$STAT_MAP"
out=$(ensure_case 2>&1); rc=$?
eq "ext4: success without work" "$rc" "0"
eq "ext4: no mkfs" "$(calls mkfs.ext4)" "0"

# free-space refusal (df stub flipped to report 1KB free)
cat > "$TMP/bin/df" <<'SHIM'
#!/bin/sh
echo "df $*" >> "$STUB_LOG"
echo "Filesystem 1K-blocks Used Available Use% Mounted"
echo "/dev/ubi0_1 32768 32700 1 99% /overlay"
SHIM
chmod +x "$TMP/bin/df"
rm -f "$TMP/wallet.ext4"
reset_calls
mounts_fixture "/dev/mmcblk0p2 / ext4 rw 0 0"
printf '%s%sjffs2\n' "$TMP/walletdir" "$tab" > "$STAT_MAP"
out=$(ensure_case 2>&1); rc=$?
eq "insufficient flash: refused (D3 fail-at-install)" "$rc" "1"
eq "insufficient flash: no mkfs" "$(calls mkfs.ext4)" "0"

# == consumer fence ==========================================================

# Both consumers source the ONE declaration and call the bring-up.
if grep -q 'WALLET_STORAGE_LIB=/usr/lib/tollgate/wallet-storage.sh' "$SETUP" &&
    grep -q 'ensure_wallet_storage' "$SETUP"; then
    ok "99-tollgate-setup sources the lib and calls ensure_wallet_storage"
else
    bad "99-tollgate-setup sources the lib and calls ensure_wallet_storage"
fi
if grep -q '/usr/lib/tollgate/wallet-storage.sh' "$INIT" &&
    grep -q 'ensure_wallet_storage' "$INIT"; then
    ok "init.d tollgate-wrt sources the lib and calls ensure_wallet_storage"
else
    bad "init.d tollgate-wrt sources the lib and calls ensure_wallet_storage"
fi

# == planted-drift verdict ===================================================
# Sabotage the predicate in a COPY (always "capable") and require the jffs2
# bring-up case to STOP creating the image — the fence bites.
SABOTAGED="$TMP/wallet-storage-sabotaged.sh"
sed 's/^wallet_needs_loop_image() {$/wallet_needs_loop_image() { return 1; } # SABOTAGED\nwallet_needs_loop_image_orig() {/' "$ROOT/$LIB" > "$SABOTAGED"
reset_calls
mounts_fixture "/dev/mmcblk0p2 / ext4 rw 0 0"
printf '%s%sjffs2\n' "$TMP/walletdir" "$tab" > "$STAT_MAP"
rm -f "$TMP/wallet.ext4"
(
    export WALLET_DIR="$TMP/walletdir"
    export WALLET_LOOP_IMAGE="$TMP/wallet.ext4"
    export WALLET_MOUNTS_FILE="$TMP/mounts"
    export STUB_MODPROBE_OK=1
    # shellcheck disable=SC1090
    . "$SABOTAGED"
    ensure_wallet_storage >/dev/null 2>&1
)
if [ "$(calls mkfs.ext4)" = "0" ]; then
    ok "planted drift: predicate sabotage stops bring-up (verdict bites)"
else
    bad "planted drift: bring-up ran under a sabotaged predicate — fence is decorative"
fi

echo
echo "pass=$PASS fail=$FAIL"
[ "$FAIL" = 0 ]
