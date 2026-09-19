#!/bin/sh
# throwaway self-test for the experiment's router helpers (not part of the kit).
cd "$(dirname "$0")/.."
. ./lib/common.sh
echo "== hzprobe (raw) =="
rssh_script "$EXPERIMENT_DIR/lib/hzprobe.sh"
echo "== ruser_hz =="
ruser_hz
echo "== rcpu =="
rcpu
echo "== rssh_t (5s bound on a 10s sleep; expect rc=124, ~5s) =="
s=$(date +%s); rssh_t 5 'sleep 10; echo NEVER'; echo "rc=$? elapsed=$(( $(date +%s) - s ))s"
echo "== rdisk =="
rdisk
