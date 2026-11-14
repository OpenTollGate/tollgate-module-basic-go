#!/usr/bin/env bash
# Offline test: the INSTALL path commits pending uci sessions after running
# the uci-defaults scripts (#1087).
#
# THE DEFECT THIS PINS
#
# OpenWrt's boot harness (uci_apply_defaults in /etc/init.d/boot) commits
# after each uci-defaults script. The postinst path ran the same scripts
# with NO outer commit, so an interrupted script's /tmp/.uci delta had
# nothing to rescue it until the next boot (next-boot self-heal, not
# install-path correctness).
#
# WHAT IT PINS
#
#   1. packaging/postinst runs `uci commit` AFTER the uci-defaults loop
#      and BEFORE any later uci set of its own;
#   2. the Makefile twin (the from-source install path) carries the same
#      commit after the same loop;
#   3. the commit is a BARE `uci commit` (flushes every pending delta,
#      matching the boot harness), not a config-specific one.
set -euo pipefail
cd "$(dirname "$0")/../.."

fail() { echo "FAIL: $*" >&2; exit 1; }

# --- 1. postinst: bare commit after the loop, before the next uci set ---
loop_end=$(grep -n '^done$' packaging/postinst | head -1 | cut -d: -f1)
[ -n "$loop_end" ] || fail "uci-defaults loop terminator not found in packaging/postinst"

commit_line=$(awk -v s="$loop_end" 'NR > s && /^uci commit( |$)/ {print NR; exit}' packaging/postinst)
[ -n "$commit_line" ] || fail "no 'uci commit' after the uci-defaults loop in packaging/postinst"

next_set=$(awk -v s="$loop_end" 'NR > s && /^uci set / {print NR; exit}' packaging/postinst)
if [ -n "$next_set" ]; then
    [ "$commit_line" -lt "$next_set" ] \
        || fail "postinst's uci commit comes AFTER its own later uci sets — an interrupted set could still strand a delta"
fi
grep -q '^uci commit 2>/dev/null || true$' packaging/postinst \
    || fail "postinst's commit must be the bare, error-tolerant form"

# --- 2. Makefile twin: same commit after the same loop ---
mk_loop_end=$(grep -n '^done$' packaging/Makefile | head -1 | cut -d: -f1)
[ -n "$mk_loop_end" ] || fail "uci-defaults loop terminator not found in packaging/Makefile"
mk_commit=$(awk -v s="$mk_loop_end" 'NR > s && NR < s+8 && /^uci commit( |$)/ {print NR; exit}' packaging/Makefile)
[ -n "$mk_commit" ] || fail "no 'uci commit' within the lines following the loop in packaging/Makefile (twin drift)"

echo "PASS: install path commits pending uci sessions (postinst + Makefile twins agree)"
