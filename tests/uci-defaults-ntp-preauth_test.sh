#!/usr/bin/env bash
# Offline tests for the pre-auth NTP server half of issue #627
# (packaging/files/etc/uci-defaults/99-tollgate-setup: setup_ntp_server).
#
# The allowlist half (udp/123 joining users_to_router) is covered by
# uci-defaults-nodogsplash-convergence_test.sh, whose CORE_ENTRIES now
# carries it through the full script run. This file covers the other half:
# busybox sysntpd must be told to LISTEN (system.ntp.enable_server='1'),
# idempotently, with the section created when the image shipped none — and
# a RUNNING router gets exactly one cheap restart of a stateless UDP
# responder, never a fresh boot (procd starts sysntpd after uci-defaults
# with this config already committed).
#
# No router, SDK or network needed: the function is extracted from the
# setup script (function-boundary extraction, the same style as the band
# tests), its absolute /etc/init.d/sysntpd path is rewritten by sed, and it
# runs against a fake uci and pidof.
#
# Usage: bash tests/uci-defaults-ntp-preauth_test.sh
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

# ---- extract setup_ntp_server by function boundary, rewriting the init path
awk '/^setup_ntp_server\(\) \{/,/^\}/' "$SCRIPT" \
    | sed "s|/etc/init.d/sysntpd|$TMP/init.d/sysntpd|g" \
    > "$TMP/ntp_fragment.sh"
grep -q 'enable_server' "$TMP/ntp_fragment.sh" \
    || { bad "extraction failed — setup_ntp_server not found in $SCRIPT"; exit 1; }

mkdir -p "$TMP/bin" "$TMP/init.d"

# ---- fake uci: flat-file state, the subset the function uses
cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
set -uo pipefail
STATE="${UCI_STATE:?UCI_STATE not set}"
quiet=0
[ "${1:-}" = "-q" ] && { quiet=1; shift; }
cmd="${1:-}"; shift || true
case "$cmd" in
    get)
        v="$(grep -F -- "$1=" "$STATE" | head -n1 | cut -d= -f2- || true)"
        [ -n "$v" ] || exit 1
        printf '%s\n' "$v"
        ;;
    set)
        key="${1%%=*}"; val="${1#*=}"
        if grep -qF -- "$key=" "$STATE"; then
            sed -i "s|^$(printf '%s' "$key" | sed 's/[.[\*^$]/\\&/g')=.*|$key=$val|" "$STATE"
        else
            printf '%s=%s\n' "$key" "$val" >> "$STATE"
        fi
        ;;
esac
SHIM
chmod +x "$TMP/bin/uci"

# ---- fake pidof: controlled by PIDOF_SYSNTPD (empty = not running)
cat > "$TMP/bin/pidof" <<'SHIM'
#!/usr/bin/env bash
[ "$1" = "sysntpd" ] || exit 1
[ -n "${PIDOF_SYSNTPD:-}" ] || exit 1
printf '%s\n' "$PIDOF_SYSNTPD"
SHIM
chmod +x "$TMP/bin/pidof"

# ---- fake init script: records restarts
cat > "$TMP/init.d/sysntpd" <<'SHIM'
#!/usr/bin/env bash
printf 'restart\n' >> "${RESTART_LOG:?}" 2>/dev/null || true
SHIM
chmod +x "$TMP/init.d/sysntpd"

run_ntp_setup() {
    # The fragment path travels by environment, not by sh -c positionals:
    # "$2" inside a -c string is unset (the earlier form silently sourced
    # nothing and every state assertion failed for the wrong reason).
    PATH="$TMP/bin:$PATH" UCI_STATE="$1" NTP_FRAGMENT="$TMP/ntp_fragment.sh" \
        sh -c '. "$NTP_FRAGMENT" >/dev/null 2>&1; setup_ntp_server'
}

# ---- case 1: fresh image, no system.ntp section, sysntpd not running
S1="$TMP/state1"; : > "$S1"
run_ntp_setup "$S1"
if grep -q '^system.ntp=timeserver$' "$S1"; then
    ok "the timeserver section is created when the image shipped none"
else
    bad "system.ntp=timeserver missing after setup: $(cat "$S1")"
fi
if grep -q '^system.ntp.enable_server=1$' "$S1"; then
    ok "enable_server=1 is set on a fresh image"
else
    bad "enable_server not set: $(cat "$S1")"
fi

# ---- case 2: idempotency — a second run changes nothing
cp "$S1" "$S1.before"
run_ntp_setup "$S1"
if diff -q "$S1.before" "$S1" >/dev/null; then
    ok "a second run is a no-op (idempotent)"
else
    bad "second run mutated state: $(diff "$S1.before" "$S1")"
fi

# ---- case 3: a router whose operator disabled the server is RE-ENABLED
S3="$TMP/state3"
printf 'system.ntp=timeserver\nsystem.ntp.enable_server=0\n' > "$S3"
run_ntp_setup "$S3"
if grep -q '^system.ntp.enable_server=1$' "$S3"; then
    ok "an explicitly disabled server is re-enabled (policy, not preference)"
else
    bad "enable_server=0 survived: $(cat "$S3")"
fi

# ---- case 4: a RUNNING sysntpd is restarted exactly once
S4="$TMP/state4"; : > "$S4"
RESTART_LOG="$TMP/restarts" PIDOF_SYSNTPD=1234 run_ntp_setup "$S4"
if [ "$(wc -l < "$TMP/restarts" 2>/dev/null || echo 0)" = "1" ]; then
    ok "a running sysntpd is restarted exactly once"
else
    bad "restart count = $(wc -l < "$TMP/restarts" 2>/dev/null || echo 0), want 1"
fi

# ---- case 5: a fresh boot (sysntpd not yet started) never touches the init
S5="$TMP/state5"; : > "$S5"
rm -f "$TMP/restarts"
run_ntp_setup "$S5"
if [ ! -e "$TMP/restarts" ]; then
    ok "a fresh boot leaves the init script untouched (procd starts it later)"
else
    bad "fresh boot restarted sysntpd: $(cat "$TMP/restarts")"
fi

printf 'ntp-preauth: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ]
