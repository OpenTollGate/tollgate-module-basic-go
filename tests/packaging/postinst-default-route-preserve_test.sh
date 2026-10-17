#!/usr/bin/env bash
# Offline test for the default-route preservation across the postinst network
# restart.
#
# THE DEFECT THIS PINS
#
# The postinst runs `/etc/init.d/network restart` so uci-defaults take effect
# on a RUNNING router. A full netifd restart re-applies UCI-backed routes only:
# a default route added by hand — the statically-routed bench/CI seat pattern,
# no DHCP WAN and no uci gateway — is silently wiped, and the seat loses egress
# the moment the installer prints "Restarting network...". Reproduced live on
# the vlab seat (issue #962): runtime `default via 10.99.99.2 dev br-lan`,
# postinst runs, `ip route show default` comes back empty.
#
# WHAT IT PINS
#
#   1. both postinst recipes (the standalone packaging/postinst consumed by
#      local-build-ipk.sh, and the inline heredoc in packaging/Makefile that the
#      feed's recipe mirrors verbatim) snapshot the default routes BEFORE the
#      first restart and restore AFTER the last one;
#   2. the restore is idempotent — a route netifd re-installed identically is
#      not re-added;
#   3. the restore function itself actually restores: executed with a mocked
#      `ip` whose route table the "restart" wiped, the snapshot comes back;
#   4. the Makefile's SECOND restart (the recheck_private_subnet block, module
#      #926) is also covered — the restore call must come after it, not between
#      the two restarts.
#
# The historical instance: rc6 E2E on the COVR-X1860 bench (2026-10-16) lost
# `default via 192.168.105.2 dev br-private` in the installer and the seat went
# dark until the route was re-applied by hand.

set -u
REPO="$(cd "$(dirname "$0")/../.." && pwd)"
FAIL=0

fail() { echo "FAIL: $*" >&2; FAIL=1; }
ok()   { echo "ok: $*"; }

# ---- 1: both recipes carry the snapshot/restore -----------------------------

for recipe in packaging/postinst; do
    if grep -q '_pre_restart_defroutes=' "$REPO/$recipe" &&
       grep -q 'restore_default_routes "' "$REPO/$recipe" &&
       grep -q 'restore_default_routes()' "$REPO/$recipe"; then
        ok "$recipe snapshots and restores default routes"
    else
        fail "$recipe lost the default-route snapshot/restore contract"
    fi
done

if grep -Fq '_pre_restart_defroutes="$$(ip route show default' "$REPO/packaging/Makefile" &&
   grep -Fq 'restore_default_routes()' "$REPO/packaging/Makefile" &&
   grep -Fq 'restore_default_routes "$$_pre_restart_defroutes"' "$REPO/packaging/Makefile"; then
    ok "packaging/Makefile heredoc snapshots and restores default routes"
else
    fail "packaging/Makefile lost the default-route snapshot/restore contract"
fi

# ---- 2: in the Makefile, the restore runs AFTER the recheck block -----------

mkline_restore=$(grep -n 'restore_default_routes "\$$_pre_restart_defroutes"' "$REPO/packaging/Makefile" | cut -d: -f1)
mkline_recheck=$(grep -n 'recheck_private_subnet && /etc/init.d/network restart' "$REPO/packaging/Makefile" | cut -d: -f1)
if [ -n "$mkline_restore" ] && [ -n "$mkline_recheck" ] && [ "$mkline_restore" -gt "$mkline_recheck" ]; then
    ok "Makefile restore call sits after the second (recheck) restart"
else
    fail "Makefile restore call must come after the recheck_private_subnet restart"
fi

# The snapshot must happen BEFORE the first restart in both recipes.
for recipe in packaging/postinst; do
    snap=$(grep -n '_pre_restart_defroutes=' "$REPO/$recipe" | head -1 | cut -d: -f1)
    rst=$(grep -n '/etc/init.d/network restart' "$REPO/$recipe" | head -1 | cut -d: -f1)
    if [ -n "$snap" ] && [ -n "$rst" ] && [ "$snap" -lt "$rst" ]; then
        ok "$recipe snapshots before the restart"
    else
        fail "$recipe must snapshot before the network restart"
    fi
done
mk_snap=$(grep -nF '_pre_restart_defroutes="$$(ip route show default' "$REPO/packaging/Makefile" | head -1 | cut -d: -f1)
mk_rst=$(grep -n '/etc/init.d/network restart' "$REPO/packaging/Makefile" | head -1 | cut -d: -f1)
if [ -n "$mk_snap" ] && [ -n "$mk_rst" ] && [ "$mk_snap" -lt "$mk_rst" ]; then
    ok "Makefile heredoc snapshots before the restart"
else
    fail "Makefile must snapshot before the network restart"
fi

# ---- 3: functional run of the restore with a mocked ip ----------------------

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"

cat > "$WORK/bin/ip" <<'FAKE'
#!/bin/sh
# stateful fake: the "route table" is $WORK_STATE; `ip route add` appends, so
# the test can observe exactly what the restore re-added.
case "$1 $2" in
    "route show") cat "$WORK_STATE" 2>/dev/null; exit 0 ;;
    "route add")  shift 2; echo "$*" >> "$WORK_STATE"; exit 0 ;;
esac
exit 0
FAKE
chmod +x "$WORK/bin/ip"

extract_restore_fn() {
    # pull the restore_default_routes function definition out of packaging/postinst
    sed -n '/^restore_default_routes()/,/^}/p' "$REPO/packaging/postinst"
}

# 3a: wiped table -> snapshot restored verbatim
export WORK_STATE="$WORK/state.a"
printf 'default via 10.99.99.2 dev br-lan\n' > "$WORK_STATE"   # pre-restart table
snapshot='default via 10.99.99.2 dev br-lan'
: > "$WORK_STATE"                                              # restart wiped it
eval "$(extract_restore_fn)"
PATH="$WORK/bin:$PATH" restore_default_routes "$snapshot"
if [ "$(cat "$WORK_STATE")" = "default via 10.99.99.2 dev br-lan" ]; then
    ok "restore re-adds a wiped runtime default"
else
    fail "restore did not re-add the wiped default (table: $(cat "$WORK_STATE" 2>/dev/null))"
fi

# 3b: route netifd re-installed identically -> NOT duplicated
export WORK_STATE="$WORK/state.b"
printf 'default via 192.168.105.2 dev br-private\n' > "$WORK_STATE"
snapshot='default via 192.168.105.2 dev br-private'
PATH="$WORK/bin:$PATH" restore_default_routes "$snapshot"
if [ "$(wc -l < "$WORK_STATE")" -eq 1 ]; then
    ok "restore is idempotent when the route survived"
else
    fail "restore duplicated a surviving route: $(cat "$WORK_STATE")"
fi

# 3c: empty snapshot -> no-op, no crash
export WORK_STATE="$WORK/state.c"
: > "$WORK_STATE"
PATH="$WORK/bin:$PATH" restore_default_routes "" && [ ! -s "$WORK_STATE" ] && \
    ok "empty snapshot is a no-op" || fail "empty snapshot case misbehaved"

# 3d: uci-backed default (proto static marker) coexists with the runtime one
export WORK_STATE="$WORK/state.d"
printf 'default via 10.99.99.2 dev br-lan proto static\n' > "$WORK_STATE"
snapshot='default via 10.99.99.2 dev br-lan'
PATH="$WORK/bin:$PATH" restore_default_routes "$snapshot"
if grep -qxF 'default via 10.99.99.2 dev br-lan' "$WORK_STATE"; then
    ok "runtime default restored alongside the uci-backed one"
else
    fail "runtime default missing next to the uci-backed route: $(cat "$WORK_STATE")"
fi

# ---- verdict -----------------------------------------------------------------

if [ "$FAIL" -eq 0 ]; then
    echo "postinst-default-route-preserve: PASS"
    exit 0
fi
echo "postinst-default-route-preserve: FAIL" >&2
exit 1
