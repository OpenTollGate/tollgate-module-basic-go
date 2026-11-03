#!/bin/sh
# wallet-storage.sh — the ONE declaration of the wallet-storage overlay policy
# (docs/architecture/wallet-storage-mmap-incapable-overlay-decision.md).
#
# Sourced by BOTH consumers — the uci-defaults setup (99-tollgate-setup, at
# install/upgrade) and the procd init (tollgate-wrt, at every boot) — so the
# predicate and the bring-up can never drift between the two writers. The
# Go side (src/merchant walletDirFor) only CONSUMES the outcome: it prefers
# /etc/tollgate/wallet when that directory is a mountpoint.
#
# Why this exists: bbolt requires mmap(PROT_READ, MAP_SHARED) of wallet.db.
# jffs2 — the overlay of every squashfs NOR target — has no shared mappings,
# so wallet init fails EINVAL at creation (#583/#1035; wallets never held
# value there). The remedy is a small ext4 loop image: the loop device reads
# the backing file with read() (no mmap of the jffs2 inode), and ext4 inside
# the image mmaps normally, so the wallet stack works unmodified.
#
# Everything here is idempotent: safe at install, at upgrade and at boot.

WALLET_DIR="${WALLET_DIR:-/etc/tollgate}"
WALLET_MOUNT_SUBDIR="wallet"
WALLET_LOOP_IMAGE="${WALLET_LOOP_IMAGE:-$WALLET_DIR/wallet.ext4}"
WALLET_IMAGE_MB="${WALLET_IMAGE_MB:-8}"
# Tests point this at a fixture instead of /proc/mounts. Never set on a router.
WALLET_MOUNTS_FILE="${WALLET_MOUNTS_FILE:-/proc/mounts}"

# -- predicate ----------------------------------------------------------------

wallet_fs_type() {
    stat -f -c %T "$1" 2>/dev/null || echo unknown
}

# wallet_overlay_upper <path>: the upperdir= of the longest overlay mount
# covering <path>, or empty. The upperdir path lives in the merged namespace,
# so stat -f on it yields the UPPER filesystem's type.
wallet_overlay_upper() {
    _path=$(readlink -f "$1" 2>/dev/null || echo "$1")
    awk -v path="$_path" '
        $3 == "overlay" || $3 == "overlayfs" {
            mp = $2; opts = $4
            # A mount at "/" covers every path (the string test below cannot
            # see a prefix shorter than a directory separator).
            if (mp == "/" || path == mp || index(path "/", mp "/") == 1) {
                len = length(mp)
                if (len > bestlen) {
                    bestlen = len
                    n = split(opts, o, ",")
                    up = ""
                    for (i = 1; i <= n; i++)
                        if (o[i] ~ /^upperdir=/) { up = substr(o[i], 10); break }
                    best = up
                }
            }
        }
        END { print best }
    ' "$WALLET_MOUNTS_FILE" 2>/dev/null
}

# wallet_needs_loop_image: 0 when the wallet dir's filesystem (or the overlay
# upper backing it) cannot host a shared mapping — jffs2 today; anything else
# is assumed capable (ext4/f2fs/ubifs/tmpfs/btrfs/...).
wallet_needs_loop_image() {
    _fs=$(wallet_fs_type "$WALLET_DIR")
    if [ "$_fs" = "jffs2" ]; then
        return 0
    fi
    if [ "$_fs" = "overlayfs" ] || [ "$_fs" = "overlay" ]; then
        _upper=$(wallet_overlay_upper "$WALLET_DIR")
        [ -n "$_upper" ] || return 1
        if [ "$(wallet_fs_type "$_upper")" = "jffs2" ]; then
            return 0
        fi
    fi
    return 1
}

# -- bring-up -----------------------------------------------------------------

wallet_storage_mountpoint() {
    echo "$WALLET_DIR/$WALLET_MOUNT_SUBDIR"
}

wallet_storage_is_mounted() {
    _mp=$(wallet_storage_mountpoint)
    awk -v mp="$_mp" '$2 == mp { found = 1 } END { exit !found }' \
        "$WALLET_MOUNTS_FILE" 2>/dev/null
}

# ensure_wallet_storage: mount the wallet loop image when (and only when) the
# wallet dir sits on an mmap-incapable filesystem. Creates the image once;
# replays the ext4 journal (fsck -p) on every boot mount so an unclean power
# loss converges before bbolt ever opens the file — fund safety before wear.
ensure_wallet_storage() {
    if wallet_storage_is_mounted; then
        return 0
    fi
    if ! wallet_needs_loop_image; then
        return 0
    fi

    _mp=$(wallet_storage_mountpoint)
    mkdir -p "$WALLET_DIR" "$_mp"

    if ! grep -q " loop\$" /proc/modules 2>/dev/null && ! [ -e /dev/loop0 ] && ! [ -e /dev/loop-control ]; then
        modprobe loop 2>/dev/null || {
            echo "wallet-storage: kmod-loop unavailable and no loop device — wallet cannot run on this filesystem (ADR wallet-storage-mmap-incapable-overlay)" >&2
            return 1
        }
    fi

    if [ ! -f "$WALLET_LOOP_IMAGE" ]; then
        # Fail at install (ADR D3): refuse to spend flash we do not have.
        _free_kb=$(df -k "$WALLET_DIR" 2>/dev/null | awk 'NR==2 { print $4 }')
        _need_kb=$((WALLET_IMAGE_MB * 1024))
        if [ -z "$_free_kb" ] || [ "$_free_kb" -lt "$_need_kb" ]; then
            echo "wallet-storage: need ${WALLET_IMAGE_MB}MB free for the wallet image under $WALLET_DIR, have ${_free_kb:-?}KB — refusing (ADR D3 fail-at-install)" >&2
            return 1
        fi
        # Preserve the jffs2 husk, never delete: a wallet.db from a failed
        # pre-ADR init attempt contains a seed and nothing else, but the
        # evidence trail stays.
        if [ -f "$WALLET_DIR/wallet.db" ]; then
            if [ -e "$WALLET_DIR/wallet.db.jffs2-husk" ]; then
                mv "$WALLET_DIR/wallet.db" "$WALLET_DIR/wallet.db.jffs2-husk.$(date +%s)"
                echo "wallet-storage: moved stale jffs2 wallet.db aside (a previous husk was preserved)" >&2
            else
                mv "$WALLET_DIR/wallet.db" "$WALLET_DIR/wallet.db.jffs2-husk"
                echo "wallet-storage: moved stale jffs2 wallet.db aside as wallet.db.jffs2-husk" >&2
            fi
        fi
        truncate -s "${WALLET_IMAGE_MB}M" "$WALLET_LOOP_IMAGE" 2>/dev/null \
            || dd if=/dev/zero of="$WALLET_LOOP_IMAGE" bs=1M count="$WALLET_IMAGE_MB" status=none
        mkfs.ext4 -F -q -L tollgate-wallet "$WALLET_LOOP_IMAGE" || {
            echo "wallet-storage: mkfs.ext4 failed" >&2
            return 1
        }
    fi

    # Journal replay before every mount: an unclean shutdown leaves the
    # image dirty, and bbolt must never open a dirty fs.
    _fsck_rc=0
    fsck.ext4 -p "$WALLET_LOOP_IMAGE" >/dev/null 2>&1 || _fsck_rc=$?
    case "$_fsck_rc" in
        0|1) ;;
        2)
            echo "wallet-storage: fsck requests a reboot before mounting $WALLET_LOOP_IMAGE (rc=2) — refusing to hand a half-repaired filesystem to bbolt" >&2
            return 1 ;;
        *)
            echo "wallet-storage: fsck found uncorrectable errors in $WALLET_LOOP_IMAGE (rc=$_fsck_rc) — refusing to mount; recover the image before the wallet can run" >&2
            return 1 ;;
    esac

    _loopdev=$(losetup -f --show "$WALLET_LOOP_IMAGE" 2>/dev/null) || {
        echo "wallet-storage: losetup failed" >&2
        return 1
    }
    mount -t ext4 -o noatime "$_loopdev" "$_mp" || {
        echo "wallet-storage: mount $_loopdev on $_mp failed" >&2
        losetup -d "$_loopdev" 2>/dev/null
        return 1
    }
    echo "wallet-storage: $WALLET_LOOP_IMAGE on $_mp via $_loopdev (ext4 loop image — mmap-incapable overlay, ADR)" >&2
    return 0
}
