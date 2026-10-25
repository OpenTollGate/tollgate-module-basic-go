#!/usr/bin/env bash
# Offline test for recheck_private_subnet in
# packaging/files/etc/uci-defaults/99-tollgate-setup.
#
# The private subnet is chosen at uci-defaults time, before a fresh DHCP WAN
# necessarily has a lease. If the uplink later lands on the same /24 as the
# derived private network (the observed 192.168.2.1 against a 192.168.2.0/24
# upstream), the router answers for the upstream gateway's own address and DNS
# breaks. recheck_private_subnet re-derives the private subnet once the uplink is
# up; the package postinst calls it after `/etc/init.d/network restart`.
#
# Everything runs against a fake `uci` and a fake `ip` — no router.
#
# Usage: bash tests/uci-defaults-private-recheck_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# fake uci — flat "key=value" state, the subset the setup script uses.
cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
set -uo pipefail
STATE="${UCI_STATE:?UCI_STATE not set}"
quiet=0
[ "${1:-}" = "-q" ] && { quiet=1; shift; }
cmd="${1:-}"; shift || true
state_get() { grep -F -- "$1=" "$STATE" | head -n1 | cut -d= -f2-; }
case "$cmd" in
    get)
        v=$(state_get "$1") || v=""
        [ -n "$v" ] || exit 1
        printf '%s\n' "$v"
        ;;
    set)
        key="${1%%=*}"; val="${1#*=}"
        [ "$key" = "$1" ] && exit 1
        if grep -qF -- "$key=" "$STATE"; then
            sed -i "s|^$(printf '%s' "$key" | sed 's/[.[\*^$]/\\&/g')=.*|$key=$val|" "$STATE"
        else
            printf '%s=%s\n' "$key" "$val" >> "$STATE"
        fi
        ;;
    commit) : ;;
    *) exit 0 ;;
esac
SHIM
chmod +x "$TMP/bin/uci"

# fake ip — reports no live addresses, so upstream_networks uses the UCI WAN.
cat > "$TMP/bin/ip" <<'SHIM'
#!/usr/bin/env bash
exit 0
SHIM
chmod +x "$TMP/bin/ip"

export PATH="$TMP/bin:$PATH"
export UCI_STATE="$TMP/uci.state"
export LOGFILE="$TMP/setup.log"

seed() { printf '%s\n' "$1" > "$UCI_STATE"; : > "$LOGFILE"; }
run_recheck() { ( TOLLGATE_SETUP_LIB_ONLY=1 . "$SCRIPT" >/dev/null 2>&1; recheck_private_subnet ); }
private_addr() { grep '^network.private.ipaddr=' "$UCI_STATE" | cut -d= -f2-; }

# 1. the private subnet collides with the now-visible upstream -> it moves.
seed 'network.lan.ipaddr=192.168.1.1
network.lan.netmask=255.255.255.0
network.private.ipaddr=192.168.2.1
network.private.netmask=255.255.255.0
network.wan.ipaddr=192.168.2.50
network.wan.netmask=255.255.255.0'
if run_recheck; then ok "collision: recheck reports it moved the private subnet (rc=0)"; else bad "collision: recheck should return 0"; fi
got=$(private_addr)
if [ "$got" != "192.168.2.1" ] && [ -n "$got" ]; then ok "collision: private moved off 192.168.2.1 (now $got)"; else bad "collision: private still '$got'"; fi

# 2. no upstream known -> a no-op.
seed 'network.lan.ipaddr=192.168.1.1
network.lan.netmask=255.255.255.0
network.private.ipaddr=192.168.2.1
network.private.netmask=255.255.255.0'
if run_recheck; then bad "no upstream: recheck must not move the private subnet"; else ok "no upstream: recheck is a no-op"; fi
[ "$(private_addr)" = "192.168.2.1" ] && ok "no upstream: private unchanged" || bad "no upstream: private changed"

# 3. non-colliding private -> a no-op.
seed 'network.lan.ipaddr=192.168.1.1
network.lan.netmask=255.255.255.0
network.private.ipaddr=10.3.4.1
network.private.netmask=255.255.255.0
network.wan.ipaddr=192.168.2.50
network.wan.netmask=255.255.255.0'
if run_recheck; then bad "non-colliding: recheck must not move the private subnet"; else ok "non-colliding: recheck is a no-op"; fi
[ "$(private_addr)" = "10.3.4.1" ] && ok "non-colliding: private unchanged" || bad "non-colliding: private changed"

# 4. no private network at all -> a no-op (a router without one).
seed 'network.lan.ipaddr=192.168.1.1
network.lan.netmask=255.255.255.0
network.wan.ipaddr=192.168.2.50
network.wan.netmask=255.255.255.0'
if run_recheck; then bad "no private: recheck must not act"; else ok "no private: recheck is a no-op"; fi

# 5. the postinst calls the recheck after the network restart.
if grep -q "recheck_private_subnet" packaging/Makefile; then
    ok "postinst: packaging/Makefile calls recheck_private_subnet"
else
    bad "postinst: packaging/Makefile does not call recheck_private_subnet"
fi

printf '\nPASS=%d FAIL=%d\n' "$PASS" "$FAIL"
[ "$FAIL" -eq 0 ]
