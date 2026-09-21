#!/usr/bin/env bash
# shellcheck disable=SC2016  # SSID patterns below are literal text, not expansions
# Contract: the open AP this package ships is named `<brand>-<4 hex chars>` and
# nothing else — `^TollGate-[0-9A-F]{4}$` for the default brand.
#
# Why the exact shape is pinned: the SSID is the gateway's public name. The Go
# side keys on the `TollGate-` prefix (src/wireless_gateway_manager/
# discovery_log.go `hasTollGateSSID`, vendor_element_manager.go `calculateScore`
# — "TollGate SSID format: 'TollGate-' + random chars"), the operator reads the
# suffix to tell two routers apart, and every scanner in range sees whatever
# this script writes into the beacon. A band suffix ("TollGate-A1B2-2.4GHz") or
# any other decoration is drift: it makes the name a lie on one radio, breaks
# the "both radios broadcast one network" property, and multiplies the names a
# client has to match.
#
# Two layers, because neither is sufficient alone:
#
#   A. Behavioural — the real functions are sourced out of 99-tollgate-setup
#      (driver stripped) and run against a stub `uci` that records what they
#      would have written. So the SSID asserted on is the one the script
#      actually generates, not a grep over its source.
#   B. Structural — the assignment sites are checked for the shapes that would
#      break the contract in branches a checkout cannot execute: the
#      same-version reinstall path recovers its SSID from live config, and the
#      whitelabel branch reads /etc/tollgate/brand, which a CI checkout has no
#      way to provide.
#
# Exit 0 when the contract holds, 1 otherwise.

set -uo pipefail

cd "$(dirname "$0")/../.." || exit 1
ROOT="$(pwd)"
SCRIPT="$ROOT/packaging/files/etc/uci-defaults/99-tollgate-setup"
CONTRACT_DEFAULT_RE='^TollGate-[0-9A-F]{4}$'
# The 4-character device suffix, as produced by the script's generator.
SUFFIX_RE='^[0-9A-F]{4}$'

fails=0
pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1" >&2; fails=$((fails + 1)); }
info() { printf '  info  %s\n' "$1"; }

printf 'check-ssid-format: root %s\n' "$ROOT"

if [ ! -f "$SCRIPT" ]; then
    fail "$SCRIPT is missing; the SSID contract cannot be checked"
    printf '\n1 SSID-format check FAILED.\n' >&2
    exit 1
fi

SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/ssid-format.XXXXXX")"
trap 'rm -rf "$SANDBOX"' EXIT
mkdir -p "$SANDBOX/bin" "$SANDBOX/state"

# --- the uci stand-in -------------------------------------------------------
# A flat key -> value store under $UCI_STATE: the script under test believes it
# configured a router and the test reads back what it wrote. Subcommands the
# SSID paths do not use are accepted and ignored so the harness never depends
# on the exact set of uci calls made today.
cat > "$SANDBOX/bin/uci" <<'STUB'
#!/bin/sh
quiet=0
[ "${1:-}" = "-q" ] && { quiet=1; shift; }
cmd="${1:-}"
[ $# -gt 0 ] && shift
state="${UCI_STATE:?UCI_STATE must point at the harness state directory}"
case "$cmd" in
    get)
        if [ -f "$state/$1" ]; then cat "$state/$1"; exit 0; fi
        [ "$quiet" = 1 ] || printf 'uci: Entry not found\n' >&2
        exit 1 ;;
    set)
        key="${1%%=*}"; printf '%s' "${1#*=}" > "$state/$key" ;;
    add_list)
        key="${1%%=*}"; printf '%s\n' "${1#*=}" >> "$state/$key" ;;
    delete)
        rm -f "$state/$1" ;;
    show)
        for f in "$state"/*; do
            [ -f "$f" ] || continue
            printf "%s='%s'\n" "${f##*/}" "$(cat "$f")"
        done ;;
    add|commit|revert|export|import|rename) : ;;
    *) : ;;
esac
exit 0
STUB
chmod +x "$SANDBOX/bin/uci"

# Everything above the driver marker is library code. The driver itself reads
# and writes /etc, /proc and the setup-flag file, so a checkout cannot execute
# it; the functions that produce the SSID can, and those are what run here.
awk '/^# -- driver/{exit} {print}' "$SCRIPT" > "$SANDBOX/lib.sh"

# Runs the real load_brand() + setup_public_wifi() against the stub uci and
# prints the SSID each open radio ended up with, one per line. Non-zero when
# the sourced code did not complete.
run_setup_public_wifi() {
    local state="$SANDBOX/state" iface
    rm -rf "$state"
    mkdir -p "$state"
    # A first-boot wireless config as uci-defaults sees it: two AP radios, both
    # enabled, neither in STA mode.
    printf '%s' wifi-iface > "$state/wireless.default_radio0"
    printf '%s' ap         > "$state/wireless.default_radio0.mode"
    printf '%s' wifi-iface > "$state/wireless.default_radio1"
    printf '%s' ap         > "$state/wireless.default_radio1.mode"
    printf '%s' 1          > "$state/wireless.radio0"
    printf '%s' 1          > "$state/wireless.radio0.disabled"
    printf '%s' 1          > "$state/wireless.radio1"
    printf '%s' 1          > "$state/wireless.radio1.disabled"

    (
        set -u
        export UCI_STATE="$state"
        export PATH="$SANDBOX/bin:$PATH"
        # shellcheck source=/dev/null
        . "$SANDBOX/lib.sh"
        # shellcheck disable=SC2034  # read by the sourced log()
        LOGFILE="$SANDBOX/setup.log"   # never append to the router's real log
        load_brand
        setup_public_wifi
    ) > "$SANDBOX/harness.log" 2>&1
    # The exit status is deliberately not asserted: the last statement of
    # setup_public_wifi() is a `cmd && cmd` guard, so a missing radio section
    # (or a BusyBox-ash quirk) can leave it non-zero after the SSID was
    # written. What the contract cares about is whether an SSID landed, and
    # that is read back from the stub's state below.

    for iface in default_radio0 default_radio1; do
        if [ -f "$state/wireless.$iface.ssid" ]; then
            cat "$state/wireless.$iface.ssid"
        else
            printf '<unset>'
        fi
        printf '\n'
    done
}

# The brand the script will select on this host: load_brand() reads
# /etc/tollgate/brand and defaults to TollGate, so the expected prefix follows
# that file when a machine happens to have one.
brand_prefix="TollGate"
if [ -r /etc/tollgate/brand ]; then
    brand="$(cat /etc/tollgate/brand 2>/dev/null || true)"
    case "$brand" in
        net4sats) brand_prefix="Net4sats" ;;
        *) brand_prefix="TollGate" ;;
    esac
    info "host has /etc/tollgate/brand='${brand}' -> expecting prefix '${brand_prefix}-'"
fi

# --- A. behavioural ---------------------------------------------------------
printf '\n--- A. generated SSID (real functions, stub uci) ---\n'
out="$(run_setup_public_wifi)"
ssid0="$(printf '%s\n' "$out" | sed -n 1p)"
ssid1="$(printf '%s\n' "$out" | sed -n 2p)"
if [ "$ssid0" = "<unset>" ] || [ "$ssid1" = "<unset>" ]; then
    fail "the sourced setup did not write an SSID — see the harness output below"
    sed 's/^/        /' "$SANDBOX/harness.log" >&2
fi
info "radio0 SSID: ${ssid0:-<none>}"
info "radio1 SSID: ${ssid1:-<none>}"

expected_re="^${brand_prefix}-[0-9A-F]{4}\$"
for iface in default_radio0 default_radio1; do
    case "$iface" in
        default_radio0) ssid="$ssid0" ;;
        *)              ssid="$ssid1" ;;
    esac
    if printf '%s' "$ssid" | grep -qE "$expected_re"; then
        pass "$iface SSID '$ssid' matches ${expected_re}"
    else
        fail "$iface SSID '${ssid:-<none>}' does not match ${expected_re}"
    fi
done

if [ "$brand_prefix" = "TollGate" ]; then
    if printf '%s' "$ssid0" | grep -qE "$CONTRACT_DEFAULT_RE"; then
        pass "default-brand SSID matches the literal contract ${CONTRACT_DEFAULT_RE}"
    else
        fail "default-brand SSID '${ssid0:-<none>}' does not match ${CONTRACT_DEFAULT_RE}"
    fi
fi

if [ -n "$ssid0" ] && [ "$ssid0" = "$ssid1" ]; then
    pass "both open radios broadcast one SSID ('$ssid0') — no per-band naming"
else
    fail "open radios disagree on the SSID ('${ssid0:-<none>}' vs '${ssid1:-<none>}')"
fi

# The suffix is per-device, not a constant: N generations must move, and each
# one must still be the same 4 hex characters.
generations=6
distinct=0
values=""
for _ in $(seq 1 "$generations"); do
    v="$(run_setup_public_wifi | sed -n 1p)"
    case "$values" in
        *"|$v|"*) : ;;
        *) values="${values}|${v}|"; distinct=$((distinct + 1)) ;;
    esac
    if ! printf '%s' "$v" | grep -qE "$expected_re"; then
        fail "generation ${distinct} produced '${v:-<none>}', which is not ${expected_re}"
    fi
done
if [ "$distinct" -ge "$((generations - 1))" ]; then
    pass "${generations} generations produced ${distinct} distinct SSIDs (device-unique suffix)"
else
    fail "${generations} generations produced only ${distinct} distinct SSIDs; the suffix is not random"
fi
suffix="$(printf '%s' "$ssid0" | sed -E "s/^${brand_prefix}-//")"
if printf '%s' "$suffix" | grep -qE "$SUFFIX_RE"; then
    pass "device suffix '${suffix}' is exactly 4 characters from [0-9A-F]"
else
    fail "device suffix '${suffix}' is not 4 characters from [0-9A-F]"
fi

# --- B. structural ----------------------------------------------------------
printf '\n--- B. assignment sites (branches a checkout cannot run) ---\n'

# B1 — every SSID written into the wireless config comes from a variable. A
#      literal is where a fixed value or a band suffix gets in.
checked=0
while IFS= read -r hit; do
    [ -n "$hit" ] || continue
    checked=$((checked + 1))
    value="$(printf '%s' "$hit" | sed -nE 's/.*\.ssid="([^"]*)".*/\1/p')"
    case "$value" in
        '$GATEWAY_NAME'|'$private_ssid')
            pass "ssid write is a variable reference (line ${hit%%:*}: \$${value#\$})" ;;
        *)
            fail "ssid write is not a variable reference (line ${hit%%:*}): $(printf '%s' "$hit" | sed -E 's/^[0-9]+://')" ;;
    esac
done < <(grep -nE '\.ssid="' "$SCRIPT")
if [ "$checked" -eq 0 ]; then
    fail "no '.ssid=\"…\"' write found — this check is observing nothing"
fi

# B2 — the gateway name is built as <brand>-<suffix>, with the brand coming
#      from load_brand() so a whitelabel install renames the AP too, and with
#      the suffix last: anything appended after it is the band-suffix drift.
checked=0
while IFS= read -r hit; do
    [ -n "$hit" ] || continue
    checked=$((checked + 1))
    value="$(printf '%s' "$hit" | sed -nE 's/.*GATEWAY_NAME="(.*)".*/\1/p')"
    case "$value" in
        '${BRAND_HOSTNAME}-${RANDOM_SUFFIX}')
            pass "gateway name is \${BRAND_HOSTNAME}-\${RANDOM_SUFFIX} (line ${hit%%:*})" ;;
        '${BRAND_HOSTNAME}-$(hexdump'*')')
            pass "recovery fallback rebuilds the name from the brand + a fresh 4-hex suffix (line ${hit%%:*})" ;;
        *)
            fail "GATEWAY_NAME is not <brand>-<4-hex suffix> (line ${hit%%:*}): ${value:-<unparsed>}" ;;
    esac
done < <(grep -n 'GATEWAY_NAME="' "$SCRIPT")
if [ "$checked" -eq 0 ]; then
    fail "no 'GATEWAY_NAME=\"…\"' assignment found — this check is observing nothing"
fi

# B3 — no SSID-bearing literal carries a band token. This is the drift the
#      card exists for, and it is caught here whatever the branch is: the
#      reinstall path and the whitelabel path both write SSIDs.
checked=0
while IFS= read -r hit; do
    [ -n "$hit" ] || continue
    checked=$((checked + 1))
    value="$(printf '%s' "$hit" | sed -nE 's/.*(\.ssid|GATEWAY_NAME|private_ssid)="([^"]*)".*/\2/p')"
    if printf '%s' "$value" | grep -qE 'GHz|2\.4|5\.0|[[:space:]_-][25][Gg]([^[:alnum:]]|$)'; then
        fail "band token in an SSID value (line ${hit%%:*}): $value"
    else
        pass "no band token in SSID value (line ${hit%%:*}): $value"
    fi
done < <(grep -nE '(\.ssid|GATEWAY_NAME|private_ssid)="' "$SCRIPT")
if [ "$checked" -eq 0 ]; then
    fail "no SSID literal found — this check is observing nothing"
fi

# B4 — the nodogsplash gateway name is derived from the same value, so it
#      cannot acquire a band suffix of its own.
checked=0
while IFS= read -r hit; do
    [ -n "$hit" ] || continue
    checked=$((checked + 1))
    value="$(printf '%s' "$hit" | sed -nE 's/.*gatewayname="([^"]*)".*/\1/p')"
    case "$value" in
        *'${GATEWAY_NAME}'*)
            pass "nodogsplash gateway name derives from \${GATEWAY_NAME} (line ${hit%%:*})" ;;
        *)
            fail "nodogsplash gatewayname is not derived from \${GATEWAY_NAME} (line ${hit%%:*}): $value" ;;
    esac
done < <(grep -n 'gatewayname="' "$SCRIPT")
if [ "$checked" -eq 0 ]; then
    fail "no 'gatewayname=\"…\"' assignment found — this check is observing nothing"
fi

printf '\n'
if [ "$fails" -ne 0 ]; then
    printf '%d SSID-format check(s) FAILED.\n' "$fails" >&2
    exit 1
fi
printf 'Shipped AP SSIDs match the contract (brand + 4-hex device suffix, one name for both radios).\n'
