#!/usr/bin/env bash
# shellcheck disable=SC2016
# Contract: the package's firewall sections must reference zones that exist
# on the router they are written to — resolved from the router's own config,
# not assumed from stock names (#755).
#
# Why: the sections referenced the stock zone by name (src='lan'). Rename the
# zone — segmentation doctrine does — and fw4 silently skips the section:
# exit 0, warnings on stderr only, never logread. Bench-verified on rc1:
# renaming firewall.@zone[0] alone removed Allow-TollGate-In from the ruleset
# with zero diagnostics, and the A/B leg (trusted client, same NDS state)
# went from a completed download to `Operation not permitted` — the zone name
# was the only variable:
# https://github.com/OpenTollGate/tollgate-module-basic-go/issues/755#issuecomment-6066062538
#
# The fix has two halves, both pinned here:
#   A. Behavioural — the real resolver and section writer, sourced out of
#      99-tollgate-setup (driver stripped), driven against a stub uci that
#      models the stock topology and the verdict's rename. The captive zone
#      is resolved gatewayinterface → network → zone and converged into
#      tollgate_in.src on every pass.
#   B. The fail-loudly half — a referenced zone that does not exist is named
#      in the setup log AND syslog (the one logread line the bench session
#      did not have), and the postinst surfaces fw4's own skip diagnostics
#      into syslog instead of discarding them with 2>/dev/null.

set -u
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SCRIPT="$ROOT/packaging/files/etc/uci-defaults/99-tollgate-setup"
MK="$ROOT/packaging/Makefile"

printf 'check-firewall-zone-refs: root %s\n' "$ROOT"

fail=0
ok()  { printf '  PASS: %s\n' "$1"; }
bad() { printf '  FAIL: %s\n' "$1"; fail=$((fail+1)); }

SANDBOX="$(mktemp -d)"
trap 'rm -rf "$SANDBOX"' EXIT
mkdir -p "$SANDBOX/bin"

# The stub uci: a flat key→value store at $UCI_STATE (the ssid harness's
# shape), extended to answer the `uci show` scrapes the resolver performs.
# `uci -q get A.B.C` reads state/<A.B.C>; `uci -q show X` cats state/show.X
# verbatim (the fixtures below model real `uci show` output); `uci set`
# records the write into state/ and a side log.
cat > "$SANDBOX/bin/uci" <<'EOF'
#!/bin/sh
STATE="${UCI_STATE:?}"
# Normalise: the code under test says `uci -q show X` / `uci -q get A.B.C`;
# strip the flag so the command words are stable below.
[ "$1" = "-q" ] && shift
case "$1 $2" in
    "show network") cat "$STATE/show.network" 2>/dev/null; exit 0 ;;
    "show firewall") cat "$STATE/show.firewall" 2>/dev/null; exit 0 ;;
esac
case "$1" in
    get)
        key=$(echo "$2" | tr '.' '_')
        [ -f "$STATE/$key" ] && cat "$STATE/$key" && exit 0
        exit 1
        ;;
    set)
        # BusyBox uci passes `set section.option=value` as ONE argument.
        echo "$2" >> "$STATE/uci-set.log"
        key=$(echo "${2%%=*}" | tr '.' '_')
        printf '%s' "${2#*=}" > "$STATE/$key"
        exit 0
        ;;
esac
exit 1
EOF
chmod +x "$SANDBOX/bin/uci"

# A stub logger so the fail-loudly half is observable.
cat > "$SANDBOX/bin/logger" <<'EOF'
#!/bin/sh
echo "logger:$*" >> "${UCI_STATE:?}/syslog.log"
EOF
chmod +x "$SANDBOX/bin/logger"

# Everything above the driver marker is library code (check-ssid-format's
# discipline): the resolver, the assert, and the section writer.
awk '/^# -- driver/{exit} {print}' "$SCRIPT" > "$SANDBOX/lib.sh"

seed_stock() {
    printf 'br-lan' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
    cat > "$STATE/show.network" <<'EOF'
network.lan=device
network.lan.device='br-lan'
network.wan=interface
network.wan.device='eth1'
EOF
    cat > "$STATE/show.firewall" <<'EOF'
firewall.@zone[0]=zone
firewall.@zone[0].name='lan'
firewall.@zone[0].network='lan'
firewall.@zone[1]=zone
firewall.@zone[1].name='wan'
firewall.@zone[1].network='wan'
EOF
}

seed_renamed() {
    # The verdict's shape: the captive bridge, its network and its zone all
    # renamed; stock 'lan' exists nowhere.
    printf 'br-portal' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
    cat > "$STATE/show.network" <<'EOF'
network.portal=device
network.portal.device='br-portal'
network.wan=interface
network.wan.device='eth1'
EOF
    cat > "$STATE/show.firewall" <<'EOF'
firewall.@zone[0]=zone
firewall.@zone[0].name='portal'
firewall.@zone[0].network='portal'
firewall.@zone[1]=zone
firewall.@zone[1].name='wan'
firewall.@zone[1].network='wan'
EOF
}

run_setup() {
    (
        set -u
        export UCI_STATE="$STATE"
        export PATH="$SANDBOX/bin:$PATH"
        export LOGFILE="$SANDBOX/setup.log"
        # shellcheck source=/dev/null
        . "$SANDBOX/lib.sh"
        # The library assigns its own LOGFILE at source time (its boot-time
        # default); re-point it at the harness AFTER the dot so the stubbed
        # run never appends to the host's real /tmp/tollgate-setup.log.
        LOGFILE="$SANDBOX/setup.log"
        setup_tollgate_firewall_rules
    ) > "$SANDBOX/harness.log" 2>&1
}

STATE="$SANDBOX/state-stock"; mkdir -p "$STATE"; seed_stock
run_setup
if grep -q "^firewall.tollgate_in.src=lan$" "$STATE/uci-set.log" 2>/dev/null; then
    ok "stock topology: tollgate_in.src converged to the stock 'lan'"
else
    bad "stock topology: src not converged to 'lan'"; sed -n 1,5p "$SANDBOX/harness.log"
fi

STATE="$SANDBOX/state-renamed"; mkdir -p "$STATE"; seed_renamed
run_setup
if grep -q "^firewall.tollgate_in.src=portal$" "$STATE/uci-set.log" 2>/dev/null; then
    ok "renamed topology: tollgate_in.src converged to 'portal' (the verdict's repro, resolved)"
else
    bad "renamed topology: src not resolved to 'portal'"; sed -n 1,5p "$SANDBOX/harness.log"
fi
if ! grep -q "^firewall.tollgate_in.src=lan$" "$STATE/uci-set.log" 2>/dev/null; then
    ok "the stock name is never written on a renamed router"
else
    bad "the stock 'lan' was written on the renamed router — fw4 would skip it"
fi

# Idempotence: a second pass writes the same src again (converged, not
# create-once) and nothing else drifts.
before="$(cat "$STATE/uci-set.log")"
run_setup
[ "$(cat "$STATE/uci-set.log")" = "$before$(grep 'tollgate_in.src' <<< "$before")" ] \
    || grep -q "firewall.tollgate_in.src=portal" <<< "$(tail -3 "$STATE/uci-set.log")"
grep -q "^firewall.tollgate_in.src=portal$" "$STATE/uci-set.log" \
    && ok "re-run converges to the same zone (idempotent)" \
    || bad "re-run lost the resolved zone"

# --- the fail-loudly half ----------------------------------------------------
STATE="$SANDBOX/state-missing-zone"; mkdir -p "$STATE"
# The zone that owns the captive bridge does not exist as a firewall zone:
# resolution falls back with a warning, and the assert must name the missing
# zone for BOTH sections' refs — in the setup log AND syslog.
printf 'br-island' > "$STATE/nodogsplash_@nodogsplash[0]_gatewayinterface"
cat > "$STATE/show.network" <<'EOF'
network.island=device
network.island.device='br-island'
EOF
printf '' > "$STATE/show.firewall"
run_setup
grep -q "WARNING: no firewall zone carries network 'island'" "$SANDBOX/setup.log" \
    && ok "unresolvable bridge warned in the setup log" \
    || bad "no warning for an unresolvable bridge"
grep -q "ERROR: firewall.tollgate_in references zone .lan., which does not exist" "$SANDBOX/setup.log" \
    && ok "missing zone named in the setup log (fail loudly)" \
    || bad "missing zone not named in the setup log"
grep -q "tollgate-setup ERROR: firewall.tollgate_in references missing zone .lan." "$STATE/syslog.log" \
    && ok "missing zone named in SYSLOG (the logread line the bench lacked)" \
    || bad "missing zone not surfaced to syslog"

# --- the postinst surfaces fw4's own skip diagnostics ------------------------
if grep -q "tollgate-fw4-reload.log" "$MK" && grep -q "specifies invalid value" "$MK"; then
    ok "postinst captures fw4 reload output and logs skip diagnostics"
else
    bad "postinst still discards fw4 reload diagnostics"
fi
if grep -q "firewall reload 2>/dev/null" "$MK"; then
    bad "a firewall reload still silences fw4 entirely"
else
    ok "no silenced firewall reload remains in the postinst"
fi

# --- structural: the resolver exists and the writer converges ----------------
grep -q "resolve_captive_zone()" "$SCRIPT" \
    && ok "resolver shipped in 99-tollgate-setup" || bad "resolver missing"
grep -q 'uci set firewall.tollgate_in.src="$TOLLGATE_CAPTIVE_ZONE"' "$SCRIPT" \
    && ok "tollgate_in.src is converged from the resolver, not create-once" \
    || bad "tollgate_in.src not converged"

printf 'check-firewall-zone-refs: %s\n' "$([ "$fail" = 0 ] && echo PASS || echo "FAIL ($fail)")"
exit $([ "$fail" = 0 ] && echo 0 || echo 1)
