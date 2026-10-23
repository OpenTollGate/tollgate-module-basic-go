#!/usr/bin/env bash
# shellcheck disable=SC2016  # SSID patterns below are literal text, not expansions
# Contract: the open AP this package ships is named `!<brand>-<device code>` —
# a leading '!' sort decoration, then four characters of [A-Z0-9] minted once
# and stored per #605 (one device code: docs/architecture/one-device-code.md) —
# and nothing else: `^!TollGate-[A-Z0-9]{4}$` for the default brand.
#
# Why the leading '!': it is 0x21, so it sorts before digits and letters in an
# alphabetically ordered WiFi list, putting the guest network first. It is
# PRESENTATION, not part of the discovery contract — the bare `TollGate-<code>`
# form is what already-deployed routers and third-party clients carry, and every
# reader (Go `hasTollGateSSID`, the shell `code_from_name`/`captive_ssid_for_code`
# via `strip_ssid_decoration`) accepts a leading '!' as optional decoration.
# The PRIVATE SSID (`<nym>-<code>`) never carries the '!': guests never see it.
#
# Why the exact shape is pinned: the SSID is the gateway's public name. The Go
# side keys on the brand prefixes (src/wireless_gateway_manager/brands.go
# `hasTollGateSSID`, vendor_element_manager.go `calculateScore`
# — "TollGate SSID format: 'TollGate-' + random chars"), the operator reads the
# code to tell two routers apart, and every scanner in range sees whatever
# this script writes into the beacon. A band suffix ("TollGate-A1B2-2.4GHz") or
# any other decoration is drift: it makes the name a lie on one radio, breaks
# the "both radios broadcast one network" property, and multiplies the names a
# client has to match.
#
# Two layers, because neither is sufficient alone:
#
#   A. Behavioural — the real functions are sourced out of 99-tollgate-setup
#      (driver stripped) and run against a stub `uci` that records what they
#      would have written, in the driver's own order: load_brand →
#      setup_device_identity (mints/adopts the code, derives DEVICE_SSID) →
#      detect_band_radios → setup_public_wifi. So the SSID asserted on is the
#      one the script actually generates, not a grep over its source.
#   B. Structural — the assignment sites are checked for the shapes that would
#      break the contract in branches a checkout cannot execute: the
#      same-version reinstall path converges the SSID from the stored code
#      (captive_ssid_for_code), and the whitelabel branch reads
#      /etc/tollgate/brand, which a CI checkout has no way to provide.
#
# Exit 0 when the contract holds, 1 otherwise.

set -uo pipefail

cd "$(dirname "$0")/../.." || exit 1
ROOT="$(pwd)"
SCRIPT="$ROOT/packaging/files/etc/uci-defaults/99-tollgate-setup"
CONTRACT_DEFAULT_RE='^!TollGate-[A-Z0-9]{4}$'
# The 4-character device code, as minted by mint_device_code() / shared with
# the installer (#605). The acceptance domain is the full [A-Z0-9] alphabet:
# this side's mint happens to emit hex, the installer's emits all of [A-Z0-9],
# and the contract is the shared alphabet.
SUFFIX_RE='^[A-Z0-9]{4}$'

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
        # Real `uci show` prints option values single-quoted but section
        # types BARE (`wireless.radio0=wifi-device`, `wireless.radio0.band='2g'`).
        # The parsers under test match the bare form (wifi_device_sections,
        # first_ap_iface_on), so a stub that quotes section types silently
        # disables band detection and AP adoption — the exact fidelity gap the
        # review flagged; tests/uci-defaults-band_test.sh's shim documents the
        # same rule. Option keys are config.section.option (two dots);
        # section-type keys are config.section (one dot).
        for f in "$state"/*; do
            [ -f "$f" ] || continue
            key="${f##*/}"
            val="$(cat "$f")"
            case "$key" in
                *.*.*) printf "%s='%s'\n" "$key" "$val" ;;
                *)     printf '%s=%s\n' "$key" "$val" ;;
            esac
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

# Runs the real load_brand() → setup_device_identity() → detect_band_radios()
# → setup_public_wifi() chain (the driver's order) against the stub uci and
# prints the SSID of every open AP that was written, one per line. With no
# argument the state is wiped first (a fresh device: the code is minted);
# `keep` reuses the previous run's state and store (what an upgrade or
# reinstall sees: the stored code must be adopted, never re-minted).
run_setup_public_wifi() {
    local state="$SANDBOX/state" f found=0
    if [ "${1:-}" != "keep" ]; then
        rm -rf "$state"
        mkdir -p "$state"
        # A first-boot wireless config as uci-defaults sees it: two AP radios,
        # both enabled, neither in STA mode, each declaring its band (the
        # band-aware setup resolves radios by band before assigning APs). No
        # SSID options, no stored code — nothing to adopt, so the mint path
        # runs, exactly as on a first boot.
        printf '%s' wifi-device > "$state/wireless.radio0"
        printf '%s' 2g          > "$state/wireless.radio0.band"
        printf '%s' 1           > "$state/wireless.radio0.disabled"
        printf '%s' wifi-device > "$state/wireless.radio1"
        printf '%s' 5g          > "$state/wireless.radio1.band"
        printf '%s' 1           > "$state/wireless.radio1.disabled"
        printf '%s' wifi-iface  > "$state/wireless.default_radio0"
        printf '%s' ap          > "$state/wireless.default_radio0.mode"
        printf '%s' radio0      > "$state/wireless.default_radio0.device"
        printf '%s' wifi-iface  > "$state/wireless.default_radio1"
        printf '%s' ap          > "$state/wireless.default_radio1.mode"
        printf '%s' radio1      > "$state/wireless.default_radio1.device"
    fi

    (
        set -u
        export UCI_STATE="$state"
        export PATH="$SANDBOX/bin:$PATH"
        # The device-code store is redirected into the per-run state via the
        # env seam the script exposes (DEVICE_CODE_STORE, same shape as the
        # device-code tests use): the harness never touches the host's
        # /etc/config, a wiped state is a fresh device, and `keep` sees the
        # previous run's store.
        export DEVICE_CODE_STORE="$state/tollgate.conf"
        # shellcheck source=/dev/null
        . "$SANDBOX/lib.sh"
        # shellcheck disable=SC2034  # read by the sourced log()
        LOGFILE="$SANDBOX/setup.log"   # never append to the router's real log
        # The driver's order (99-tollgate-setup full path): the identity is
        # resolved and stored FIRST (setup_device_identity exports
        # CODE/NYM/DEVICE_HOSTNAME/DEVICE_SSID), then the radios are mapped,
        # then the public APs are named from DEVICE_SSID.
        # A host without /etc/tollgate/brand makes load_brand's read fail
        # noisily into harness.log and take the default-brand branch — that is
        # the real first-boot default-brand behaviour, not an error.
        load_brand
        setup_device_identity
        detect_band_radios
        setup_public_wifi
    ) > "$SANDBOX/harness.log" 2>&1
    # The exit status is deliberately not asserted: the last statement of
    # setup_public_wifi() is a `cmd && cmd` guard, so a missing radio section
    # (or a BusyBox-ash quirk) can leave it non-zero after the SSID was
    # written. What the contract cares about is whether an SSID landed, and
    # that is read back from the stub's state below.

    # Read back whichever iface sections the APs actually landed on: with band
    # detection working (bare section types above) setup_band_ap ADOPTS the
    # stock default_radioN sections; the create path would write
    # tollgate_*_open. Keying on either fixed set would test the stub, not the
    # script — so every wireless SSID the run wrote is reported.
    for f in "$state"/wireless.*.ssid; do
        [ -f "$f" ] || continue
        found=1
        printf '%s\n' "$(cat "$f")"
    done
    [ "$found" -eq 1 ] || printf '<unset>\n'
}

# The brand the script will select on this host: load_brand() reads
# /etc/tollgate/brand and defaults to TollGate, so the expected prefix follows
# that file when a machine happens to have one. Without the file (a CI host,
# most dev boxes) the sourced load_brand() takes the same default-brand
# first-boot branch a router takes.
brand_prefix="TollGate"
if [ -r /etc/tollgate/brand ]; then
    brand="$(cat /etc/tollgate/brand 2>/dev/null || true)"
    case "$brand" in
        net4sats) brand_prefix="Net4sats" ;;
        *) brand_prefix="TollGate" ;;
    esac
    info "host has /etc/tollgate/brand='${brand}' -> expecting prefix '!${brand_prefix}-'"
else
    info "host has no /etc/tollgate/brand -> expecting default prefix '!TollGate-' (first-boot default-brand path)"
fi

# --- A. behavioural ---------------------------------------------------------
printf '\n--- A. generated SSID (real functions, stub uci) ---\n'
out="$(run_setup_public_wifi)"
ssid0="$(printf '%s\n' "$out" | sed -n 1p)"
n_apis="$(printf '%s\n' "$out" | grep -c . || true)"
if [ "$ssid0" = "<unset>" ]; then
    fail "the sourced setup did not write an SSID — see the harness output below"
    sed 's/^/        /' "$SANDBOX/harness.log" >&2
fi
# Report every AP section the run actually wrote, by its real section name —
# adoption lands the SSIDs on the stock default_radioN sections, a create
# path on tollgate_*_open; the label must not lie about which one ran.
for f in "$SANDBOX/state"/wireless.*.ssid; do
    [ -f "$f" ] || continue
    sec="${f##*/wireless.}"; sec="${sec%.ssid}"
    info "open AP section ${sec}: $(cat "$f")"
done
if [ "$n_apis" -eq 2 ]; then
    pass "both radios' APs were written (2 SSID writes)"
else
    fail "${n_apis} AP SSID write(s), expected one per radio (2)"
fi

expected_re="^!${brand_prefix}-[A-Z0-9]{4}\$"
idx=0
while IFS= read -r ssid; do
    idx=$((idx + 1))
    if printf '%s' "$ssid" | grep -qE "$expected_re"; then
        pass "open AP #${idx} SSID '${ssid}' matches ${expected_re}"
    else
        fail "open AP #${idx} SSID '${ssid:-<none>}' does not match ${expected_re}"
    fi
done < <(printf '%s\n' "$out")

if [ "$brand_prefix" = "TollGate" ]; then
    if printf '%s' "$ssid0" | grep -qE "$CONTRACT_DEFAULT_RE"; then
        pass "default-brand SSID matches the literal contract ${CONTRACT_DEFAULT_RE}"
    else
        fail "default-brand SSID '${ssid0:-<none>}' does not match ${CONTRACT_DEFAULT_RE}"
    fi
fi

distinct_ssids="$(printf '%s\n' "$out" | sort -u | wc -l)"
if [ "$distinct_ssids" -eq 1 ] && [ "$n_apis" -ge 2 ]; then
    pass "both open radios broadcast one SSID ('$ssid0') — no per-band naming"
else
    fail "open radios disagree on the SSID (${n_apis} writes, ${distinct_ssids} distinct)"
fi

# The code is per-device, not a constant: N fresh devices (empty stores) must
# mint N different codes, and each must still be 4 characters of [A-Z0-9].
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
        fail "fresh device ${distinct} produced '${v:-<none>}', which is not ${expected_re}"
    fi
done
if [ "$distinct" -ge "$((generations - 1))" ]; then
    pass "${generations} fresh devices minted ${distinct} distinct codes (the mint is per-device)"
else
    fail "${generations} fresh devices minted only ${distinct} distinct codes; the mint is not random"
fi

# The code is minted exactly ONCE per device and then reused (#605): a second
# run against the same state and store — what an upgrade or reinstall sees —
# must adopt the stored code, never re-mint. A version bump re-randomising the
# SSID is the measured drift the store exists to stop.
reuse_a="$(run_setup_public_wifi | sed -n 1p)"
reuse_b="$(run_setup_public_wifi keep | sed -n 1p)"
stored_code="$(cat "$SANDBOX/state/tollgate.device.code" 2>/dev/null || printf '')"
if [ -n "$reuse_a" ] && [ "$reuse_a" = "$reuse_b" ]; then
    pass "a same-store rerun reuses the code ('$reuse_b' both times — never re-minted)"
else
    fail "a same-store rerun re-minted: '${reuse_a:-<none>}' then '${reuse_b:-<none>}'"
fi
derived="$(printf '%s' "$reuse_b" | sed -E "s/^!?${brand_prefix}-//")"
if printf '%s' "$stored_code" | grep -qE "$SUFFIX_RE" && [ "$stored_code" = "$derived" ]; then
    pass "the store carries the code ('${stored_code}') and the SSID is derived from it"
else
    fail "store/SSID disagreement: stored='${stored_code}', SSID derives '${derived}'"
fi

suffix="$(printf '%s' "$ssid0" | sed -E "s/^!?${brand_prefix}-//")"
if printf '%s' "$suffix" | grep -qE "$SUFFIX_RE"; then
    pass "device code '${suffix}' is exactly 4 characters from [A-Z0-9]"
else
    fail "device code '${suffix}' is not 4 characters from [A-Z0-9]"
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

# B2 — the gateway name is the DEVICE-CODE SSID built by
#      setup_device_identity (load_brand's prefix + the stored/minted code),
#      so a whitelabel install renames the AP too and the code — not a
#      literal — is the suffix. Anything appended after it is the
#      band-suffix drift.
checked=0
while IFS= read -r hit; do
    [ -n "$hit" ] || continue
    checked=$((checked + 1))
    value="$(printf '%s' "$hit" | sed -nE 's/.*GATEWAY_NAME="(.*)".*/\1/p')"
    case "$value" in
        '$DEVICE_SSID')
            pass "gateway name is the device-code SSID \$DEVICE_SSID (line ${hit%%:*})" ;;
        *)
            fail "GATEWAY_NAME is not the device-code derivation (line ${hit%%:*}): ${value:-<unparsed>}" ;;
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
printf 'Shipped AP SSIDs match the contract (!-prefixed brand + 4-character [A-Z0-9] device code, one name for both radios).\n'
