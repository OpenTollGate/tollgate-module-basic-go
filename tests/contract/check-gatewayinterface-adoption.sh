#!/usr/bin/env bash
# shellcheck disable=SC2016
# Contract: the NDS gatewayinterface converges by ADOPTION, never clobber
# (#788) — a full setup pass (every package upgrade) must not revert a
# correctly-renamed portal bridge.
#
# Evidence: the #757 re-verify's rel-cluster leg — a router renamed to
# br-portal (bridge, network and gatewayinterface coherent, everything
# working) went through a package upgrade's full setup pass and came out
# `BEFORE=br-portal AFTER=br-lan`: NDS died on a bridge that no longer named
# the portal, and the next defs render flipped every nft guard to follow the
# reversion. The fix is the #785/#786 adoption pattern applied to
# gatewayinterface: an installed value that a network in uci owns is KEPT;
# the stock br-lan is written only for a fresh install, a garbage value, or
# an interface no network owns — and every non-stock decision is logged.
#
# The convergence must also run BEFORE the two renderers in the same pass
# (they read gatewayinterface); that ordering is asserted structurally
# against the setup script.

set -u
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT="$ROOT/packaging/files/etc/uci-defaults/99-tollgate-setup"

printf 'check-gatewayinterface-adoption: root %s\n' "$ROOT"

fail=0
ok()  { printf '  PASS: %s\n' "$1"; }
bad() { printf '  FAIL: %s\n' "$1"; fail=$((fail+1)); }

SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT
mkdir -p "$SANDBOX/bin" "$SANDBOX/state"

cat > "$SANDBOX/bin/uci" <<'EOF'
#!/bin/sh
STATE="${UCI_STATE:?}"
[ "$1" = "-q" ] && shift
case "$1 $2" in
    "show network") cat "$STATE/show.network" 2>/dev/null; exit 0 ;;
esac
case "$1" in
    get)
        key=$(echo "$2" | tr '.' '_')
        [ -f "$STATE/$key" ] && cat "$STATE/$key" && exit 0
        exit 1
        ;;
    set)
        echo "$2" >> "$STATE/uci-set.log"
        key=$(echo "${2%%=*}" | tr '.' '_')
        printf '%s' "${2#*=}" > "$STATE/$key"
        exit 0
        ;;
esac
exit 1
EOF
chmod +x "$SANDBOX/bin/uci"

cat > "$SANDBOX/bin/logger" <<'EOF'
#!/bin/sh
echo "logger:$*" >> "${UCI_STATE:?}/syslog.log"
EOF
chmod +x "$SANDBOX/bin/logger"

awk '/^# -- driver/{exit} {print}' "$SCRIPT" > "$SANDBOX/lib.sh"

seed_network() {
    cat > "$STATE/show.network" <<'EOF'
network.portal=device
network.portal.device='br-portal'
network.lan=device
network.lan.device='br-lan'
EOF
}

run_converge() {
    (
        set -u
        export UCI_STATE="$STATE"
        export PATH="$SANDBOX/bin:$PATH"
        export LOGFILE="$SANDBOX/setup.log"
        # shellcheck source=/dev/null
        . "$SANDBOX/lib.sh"
        LOGFILE="$SANDBOX/setup.log"
        converge_gatewayinterface
    ) > "$SANDBOX/harness.log" 2>&1
    rm -rf "$SANDBOX/last-state"; cp -r "$STATE" "$SANDBOX/last-state"
    cat "$SANDBOX/harness.log" >> "$SANDBOX/all-harness.log"
}

# --- 1. the clobber case: renamed-and-owned is KEPT --------------------------
STATE="$SANDBOX/renamed"; mkdir -p "$STATE"; seed_network
printf 'br-portal' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
run_converge
if grep -q "gatewayinterface" "$STATE/uci-set.log" 2>/dev/null; then
    bad "a correctly-renamed gatewayinterface was rewritten (the #788 clobber)"
else
    ok "installed-and-owned gatewayinterface kept (no clobber)"
fi
grep -q "adopting the installed gatewayinterface 'br-portal'" "$SANDBOX/setup.log" \
    && ok "the adoption is logged" || { bad "adoption not logged"; sed -n 1,3p "$SANDBOX/harness.log"; }

# --- 2. fresh install: the stock literal --------------------------------------
STATE="$SANDBOX/fresh"; mkdir -p "$STATE"; seed_network
run_converge
grep -qF "nodogsplash.@nodogsplash[0].gatewayinterface=br-lan" "$STATE/uci-set.log" 2>/dev/null \
    && ok "fresh install writes the stock br-lan" || bad "fresh install did not write br-lan"

# --- 3. garbage value: reset, loudly ------------------------------------------
STATE="$SANDBOX/garbage"; mkdir -p "$STATE"; seed_network
printf 'not!an!ifname' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
run_converge
grep -qF "nodogsplash.@nodogsplash[0].gatewayinterface=br-lan" "$STATE/uci-set.log" 2>/dev/null \
    && ok "garbage gatewayinterface reset to br-lan" || bad "garbage value not reset"
grep -q "not a usable interface name" "$SANDBOX/setup.log" \
    && ok "the garbage reset is warned in the setup log" || bad "garbage reset silent"

# --- 4. unowned interface: reset, loudly, on both surfaces --------------------
STATE="$SANDBOX/unowned"; mkdir -p "$STATE"; seed_network
printf 'br-ghost' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
run_converge
grep -qF "nodogsplash.@nodogsplash[0].gatewayinterface=br-lan" "$STATE/uci-set.log" 2>/dev/null \
    && ok "interface no network owns reset to br-lan" || bad "unowned interface kept"
grep -q "owned by no network" "$SANDBOX/setup.log" \
    && ok "the unowned reset is warned" || bad "unowned reset silent"
grep -q "tollgate-setup WARNING.*owned by no network" "$STATE/syslog.log" 2>/dev/null \
    && ok "the unowned reset reaches syslog" || bad "unowned reset not in syslog"

# --- 5. stock value: idempotent and silent ------------------------------------
STATE="$SANDBOX/stock"; mkdir -p "$STATE"; seed_network
printf 'br-lan' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
: > "$SANDBOX/setup.log"
run_converge
if grep -q "gatewayinterface" "$STATE/uci-set.log" 2>/dev/null; then
    bad "stock gatewayinterface rewritten needlessly"
else
    ok "stock gatewayinterface kept (idempotent)"
fi
grep -q "adopting" "$SANDBOX/setup.log" && bad "stock value logged as an adoption (noise)" || ok "stock value silent"

# --- 6. structural: the convergence precedes the renderers --------------------
conv_line=$(grep -n "converge_gatewayinterface$" "$SCRIPT" | head -1 | cut -d: -f1)
preauth_line=$(grep -n "/usr/local/bin/tollgate-nds-preauth-render$" "$SCRIPT" | head -1 | cut -d: -f1)
defs_line=$(grep -n "/usr/local/bin/tollgate-nft-interfaces-render$" "$SCRIPT" | head -1 | cut -d: -f1)
if [ -n "$conv_line" ] && [ -n "$defs_line" ] && [ "$conv_line" -lt "$defs_line" ]; then
    ok "converge_gatewayinterface runs before the defs renderer (the renderers read it)"
else
    bad "convergence not before the renderers (conv=$conv_line defs=$defs_line)"
fi
[ -z "$preauth_line" ] || [ "$conv_line" -lt "$preauth_line" ] \
    && ok "convergence before the preauth renderer too" \
    || bad "convergence after the preauth renderer (conv=$conv_line preauth=$preauth_line)"
grep -q "^converge_gatewayinterface()" "$SCRIPT" \
    && ok "the convergence ships in 99-tollgate-setup" || bad "converge_gatewayinterface missing"

printf 'check-gatewayinterface-adoption: %s\n' "$([ "$fail" = 0 ] && echo PASS || echo "FAIL ($fail)")"
exit $([ "$fail" = 0 ] && echo 0 || echo 1)
