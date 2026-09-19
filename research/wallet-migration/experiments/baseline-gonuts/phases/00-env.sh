#!/bin/sh
# phases/00-env.sh — reproducibility contract fact #1/#2 (hardware, OS image)
# and #6 (provenance). Emits OUT/env.txt plus raw/router-*.txt snapshots.
#
# Every number in 03-baseline/gonuts.md must be accompanied by this file.
. "$(dirname "$0")/../lib/common.sh"

ENVF="$OUT/env.txt"
: > "$ENVF"

say() { printf '%s\n' "$*" | tee -a "$ENVF"; }
sec() { say ""; say "## $*"; }

say "# env.txt — gonuts baseline (WALLET-BASELINE / T1a)"
say "# generated (UTC): $(utc)"
say "# host: $(hostname)"
say "# provenance: on-device measurements unless a line says emulated"

sec "HOST (build host, x86_64) — build cost / size / dependency facts only"
say "uname: $(uname -a)"
if [ -r /etc/os-release ]; then say "os-release: $(grep -E '^(PRETTY_NAME|VERSION_ID)=' /etc/os-release | tr '\n' ' ')"; fi
say "go: $(go version 2>&1)"
say "go env: GOMODCACHE=$(go env GOMODCACHE) GOCACHE=$(go env GOCACHE) GOFLAGS=$(go env GOFLAGS)"

sec "ROUTER — environment ID ENV-ROUTER-A64 (on-device)"
rssh 'echo "uname: $(uname -a)"; echo "board_name: $(cat /tmp/sysinfo/board_name 2>/dev/null)"; echo "openwrt_release:"; cat /etc/openwrt_release 2>/dev/null; echo "openwrt_version: $(cat /etc/openwrt_version 2>/dev/null)"; echo "cpu: $(grep -m1 "^model name" /proc/cpuinfo 2>/dev/null || grep -m1 "^CPU part" /proc/cpuinfo)"; echo "cpu_cores: $(grep -c ^processor /proc/cpuinfo)"; echo "machine: $(cat /proc/device-tree/model 2>/dev/null | tr -d "\0")"' >> "$ENVF" 2>&1

sec "ROUTER — memory (free -m)"
rssh 'free -m' >> "$ENVF" 2>&1

sec "ROUTER — storage (df -h) and MTD (empty == no raw NOR/NAND MTD on this device)"
rssh 'df -h; echo "--- /proc/mtd ---"; cat /proc/mtd; echo "--- /proc/partitions ---"; cat /proc/partitions' >> "$ENVF" 2>&1

sec "ROUTER — filesystem of the overlay (flash medium + write-accounting target)"
rssh 'cat /proc/mounts | grep -E "overlay|loop|mmcblk"' >> "$ENVF" 2>&1

sec "ROUTER — CPU accounting constant (needed to turn tick deltas into seconds)"
say "USER_HZ (clock ticks per second, CALIBRATED on-device — not assumed):"
rssh_script "$EXPERIMENT_DIR/lib/hzprobe.sh" | sed 's/^/  /' | tee -a "$ENVF"

sec "ROUTER — installed package + running version"
rssh 'apk list -I 2>/dev/null | grep -i tollgate; echo "--- /etc/init.d/tollgate-wrt ---"; /etc/init.d/tollgate-wrt status 2>&1 | head -2; echo "--- uptime ---"; uptime' >> "$ENVF" 2>&1

sec "ROUTER — deployed binary provenance (sha256 / size / Go build info)"
rssh 'ls -l /usr/bin/tollgate-wrt; sha256sum /usr/bin/tollgate-wrt; /usr/bin/tollgate version 2>&1 | head -5' >> "$ENVF" 2>&1

# --- raw snapshots kept verbatim for third parties -------------------------
rssh 'cat /etc/tollgate/config.json' > "$OUT/router-config.json" 2>&1 || true
rssh 'cat /proc/diskstats' > "$OUT/router-diskstats-before.txt" 2>&1 || true
rssh 'cat /proc/mounts' > "$OUT/router-mounts.txt" 2>&1 || true
rssh 'uname -a; cat /etc/openwrt_release; cat /tmp/sysinfo/board_name' > "$OUT/router-uname.txt" 2>&1 || true

# Pull the deployed binary and record its embedded module list (E1 provenance).
if [ "${FETCH_BINARY:-1}" = "1" ]; then
    rssh 'cat /usr/bin/tollgate-wrt' > "$OUT/tollgate-wrt.device.bin" 2>/dev/null || true
    if [ -s "$OUT/tollgate-wrt.device.bin" ]; then
        sha256sum "$OUT/tollgate-wrt.device.bin" > "$OUT/tollgate-wrt.device.bin.sha256"
        go version -m "$OUT/tollgate-wrt.device.bin" > "$OUT/tollgate-wrt.device.buildinfo.txt" 2>&1 || true
        readelf -d "$OUT/tollgate-wrt.device.bin" > "$OUT/tollgate-wrt.device.readelf-d.txt" 2>&1 || true
        file "$OUT/tollgate-wrt.device.bin" > "$OUT/tollgate-wrt.device.file.txt" 2>&1 || true
        size "$OUT/tollgate-wrt.device.bin" > "$OUT/tollgate-wrt.device.size.txt" 2>&1 || true
        say ""
        say "## deployed binary (pulled to $OUT/tollgate-wrt.device.bin)"
        cat "$OUT/tollgate-wrt.device.bin.sha256" | tee -a "$ENVF"
        cat "$OUT/tollgate-wrt.device.file.txt" | tee -a "$ENVF"
        cat "$OUT/tollgate-wrt.device.size.txt" | tee -a "$ENVF"
        grep -E "^\s*dep\s+github.com/OpenTollGate/gonuts-tollgate" "$OUT/tollgate-wrt.device.buildinfo.txt" | tee -a "$ENVF" || true
        grep -E "go1|^NEEDED|Shared library" "$OUT/tollgate-wrt.device.buildinfo.txt" "$OUT/tollgate-wrt.device.readelf-d.txt" 2>/dev/null | tee -a "$ENVF" || true
    fi
fi

echo "wrote $ENVF"
