#!/usr/bin/env bash
# Offline tests for the ONE DEVICE CODE of
# packaging/files/etc/uci-defaults/99-tollgate-setup.
#
# The defect this guards (measured on the bench MT3000, 2026-09-26): the router
# had three names and no two of them agreed. THIS script re-minted a suffix on
# every full setup (RANDOM_SUFFIX, "${BRAND_HOSTNAME}-${RANDOM_SUFFIX}"), the
# installer minted its own on every deploy while writing only the hostname and
# the captive SSID — it skips private_radio* on purpose — and the private SSID
# took a suffix from a THIRD path ("c08r4d0r-${RANDOM_SUFFIX}"). Nothing was
# ever stored, so nothing could be reused: hostname=tollgate-OQ3Q, a captive
# SSID re-minted to tollgate-0GLK by a later deploy.
#
# The contract now, shared with the installer (OpenTollGate/tollgate-installer,
# branding_test.go — the same case table, because the two writers must agree;
# two exceptions are documented, not claimed away: the nym CHARSET, which is
# [A-Za-z0-9_-] here and weaker there, and the installer's suffix-less
# nodogsplash banner — docs/architecture/one-device-code.md):
#
#   store         /etc/config/tollgate, `config device 'device'`
#                 option code '<4 x [A-Z0-9]>'
#                 option nym  '<operator nym>'
#   adoption      1. the store  2. a machine-shaped hostname
#                 3. a machine-shaped captive SSID  4. mint (only here)
#   identifiers   hostname      tollgate-<code>
#                 captive SSID  TollGate-<code>   (brand prefix as shipped)
#                 private SSID  <nym>-<code>      (unless the operator renamed it)
#
# Every function below is the SHIPPED one, sourced from the real script with
# TOLLGATE_SETUP_LIB_ONLY=1 (the hook the band tests use), run against a fake
# `uci`. The two absolute paths the code would touch on a live router — the
# store and the log — are redirected into the sandbox.
#
# Usage: bash tests/uci-defaults-device-code_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }
eq()  { # eq <label> <got> <want>
    if [ "$2" = "$3" ]; then ok "$1"; else bad "$1 (got '$2', want '$3')"; fi
}

SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# ---------------------------------------------------------------- fake uci
# Flat-file stand-in: one "key=value" line per option, section lines stored as
# "pkg.section=type". `get` prints the first match (real uci prints all values
# of a list, which none of the functions under test depends on); `commit` is
# recorded so a test can assert the store is committed, not just written.
export UCI_STATE="$TMP/uci.state"
export UCI_LOG="$TMP/uci.log"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"; shift || true
values() { grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-; }
case "$cmd" in
    get)
        v="$(values "$1")"
        if [ -z "$v" ]; then
            [ "$q" = 1 ] || echo "uci: Entry not found" >&2
            exit 1
        fi
        printf '%s\n' "$v" | head -n1
        ;;
    set)
        key="${1%%=*}"; val="${1#*=}"
        if [ "$key" = "$1" ]; then          # section creation: pkg.sec=type
            key="$(printf '%s' "$1" | cut -d= -f1)"
            val="$(printf '%s' "$1" | cut -d= -f2)"
        fi
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        printf '%s=%s\n' "$key" "$val" >> "$state"
        ;;
    commit) printf 'commit %s\n' "${1:-}" >> "${UCI_LOG:-/dev/null}" ;;
    show|export|add|add_list|del_list|delete|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"
export PATH="$TMP/bin:$PATH"

# --------------------------------------------------- the script under test
# DEVICE_CODE_STORE is the seam the script exposes for exactly this (same shape
# as ROUTER_HOME_DIR): redirected here so the test never touches the host's
# /etc/config. LOGFILE is re-pointed after sourcing because the script assigns
# it in its own preamble.
DEVICE_CODE_STORE="$TMP/tollgate.conf"
export DEVICE_CODE_STORE
TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" 2>/dev/null || {
    echo "FATAL: could not source $SCRIPT" >&2
    exit 1
}
LOGFILE="$TMP/setup.log"
export LOGFILE

for fn in setup_device_identity normalize_device_code code_from_name \
          is_minted_suffix safe_nym mint_device_code resolve_nym private_ssid_for_code \
          captive_ssid_for_code; do
    command -v "$fn" >/dev/null 2>&1 || { echo "FATAL: $fn not defined by the script" >&2; exit 1; }
done
ok "setup script sources in lib-only mode and defines the device-identity functions"

BRAND_HOSTNAME="TollGate"
# The default brand, pinned explicitly so the recognition assertions below do
# not depend on whether the MACHINE running this suite happens to carry an
# /etc/tollgate/brand file: with BRAND set, brand_token() does not read it.
BRAND="tollgate"

seed() { # seed "pkg.sec=type" "pkg.sec.opt=value" ...
    : > "$UCI_STATE"
    local entry
    for entry in "$@"; do printf '%s\n' "$entry" >> "$UCI_STATE"; done
}
state_get() { grep -F -- "$1=" "$UCI_STATE" 2>/dev/null | head -n1 | cut -d= -f2-; }
log_has() { grep -q -- "$1" "$LOGFILE" 2>/dev/null; }
reset_log() { : > "$LOGFILE"; }

# ------------------------------------------------------- pure helpers
echo "== normalize_device_code (the mint alphabet, from one place)"
eq "lowercase is uppercased"      "$(normalize_device_code oq3q)" "OQ3Q"
eq "four [A-Z0-9] is kept"        "$(normalize_device_code 0GLK)" "0GLK"
eq "surrounding whitespace is stripped" "$(normalize_device_code ' OQ3Q ')" "OQ3Q"
eq "three chars is not a code"    "$(normalize_device_code OQ3)"  ""
eq "five chars is not a code"     "$(normalize_device_code OQ3QQ)" ""
eq "punctuation is not a code"    "$(normalize_device_code 'OQ-3')" ""
eq "empty stays empty"            "$(normalize_device_code '')"   ""

echo "== code_from_name (adoption reads machine-shaped names only)"
eq "installer hostname"           "$(code_from_name tollgate-OQ3Q)" "OQ3Q"
eq "module SSID, brand case"      "$(code_from_name TollGate-0GLK)" "0GLK"
# A whitelabel build's name is recognized because the BRAND FILE says so, never
# because a brand is listed in the script: the same name is adopted under that
# build's brand token and ignored under any other.
eq "whitelabel brand token"       "$(BRAND=otherbrand code_from_name Otherbrand-AB12)" "AB12"
eq "a brand that is not configured" "$(code_from_name Otherbrand-AB12)" ""
eq "custom hostname yields none"  "$(code_from_name myrouter)"      ""
eq "short suffix yields none"     "$(code_from_name tollgate-abc)"  ""
eq "empty yields none"            "$(code_from_name '')"            ""

echo "== is_minted_suffix (a machine's suffix converges, an operator's does not)"
is_minted_suffix 0GLK   && ok "four chars is machine-shaped"   || bad "four chars is machine-shaped"
is_minted_suffix 123456 && ok "digits-only (legacy numeric) is machine-shaped" || bad "digits-only (legacy numeric) is machine-shaped"
is_minted_suffix Office && bad "a word is machine-shaped"      || ok "a word is not machine-shaped"
is_minted_suffix ''     && bad "empty is machine-shaped"       || ok "empty is not machine-shaped"

echo "== safe_nym (the nym travels into a shell-quoted command on the installer side)"
eq "an operator nym is kept"          "$(safe_nym amperstrand)" "amperstrand"
eq "a hyphen and an underscore pass"  "$(safe_nym a-b_c)"       "a-b_c"
eq "a quote is refused"               "$(safe_nym "x';reboot")" ""
eq "a space is refused"               "$(safe_nym 'my nym')"    ""
eq "empty is refused"                 "$(safe_nym '')"          ""
# The name reads like a predicate, so it must behave like one: a refusal exits
# non-zero, or a future `if safe_nym "$x"` would read an unsafe value as safe.
safe_nym amperstrand >/dev/null 2>&1 && ok "a nym exits zero" \
                                     || bad "a nym exits zero"
safe_nym "x'y" >/dev/null 2>&1 && bad "a refused value must exit non-zero" \
                               || ok "a refused value exits non-zero (safe as a predicate)"

echo "== mint_device_code (four characters of [A-Z0-9], never a bare prefix)"
minted="$(mint_device_code)"
case "$minted" in
    [A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9]) ok "mint has the shape [A-Z0-9]{4}: $minted" ;;
    *) bad "mint has the shape [A-Z0-9]{4} (got '$minted')" ;;
esac
second="$(mint_device_code)"
[ "$minted" != "$second" ] && ok "two mints differ ($minted/$second)" \
                           || bad "two mints are identical ($minted) — no entropy"

# --------------------------------------------- setup_device_identity: adoption
echo "== first boot: nothing stored, nothing machine-shaped — mint once and store"
seed "system.@system[0]=system" "system.@system[0].hostname=OpenWrt"
reset_log
setup_device_identity
case "$CODE" in
    [A-Z0-9][A-Z0-9][A-Z0-9][A-Z0-9]) ok "a code was minted on first boot: $CODE" ;;
    *) bad "a code was minted on first boot (got '$CODE')" ;;
esac
eq "the minted code is stored"           "$(state_get tollgate.device.code)" "$CODE"
eq "the nym is stored"                   "$(state_get tollgate.device.nym)"  "c08r4d0r"
eq "hostname carries the code"           "$DEVICE_HOSTNAME"                  "tollgate-$CODE"
eq "captive SSID carries the code"       "$DEVICE_SSID"                      "TollGate-$CODE"
log_has "Minted device code"             && ok "the mint is logged as a mint" || bad "the mint is logged as a mint"
grep -q '^commit tollgate$' "$UCI_LOG"   && ok "the store is committed (not left as a session delta)" \
                                         || bad "the store is committed"

echo "== the store is authoritative: nothing may re-mint over it"
seed "system.@system[0]=system" "system.@system[0].hostname=tollgate-ZZZZ" \
     "wireless.tollgate_2g_open=wifi-iface" "wireless.tollgate_2g_open.ssid=TollGate-1111" \
     "tollgate.device=device" "tollgate.device.code=OQ3Q" "tollgate.device.nym=c08r4d0r"
reset_log
setup_device_identity
eq "the stored code wins over hostname and SSID" "$CODE" "OQ3Q"
eq "the hostname is derived from the stored code" "$DEVICE_HOSTNAME" "tollgate-OQ3Q"
eq "the captive SSID is derived from the stored code" "$DEVICE_SSID" "TollGate-OQ3Q"
log_has "Minted device code" && bad "the store was ignored and a code was re-minted" \
                             || ok "no mint happened while a code was stored"

echo "== an already-deployed router adopts the code it is already known by"
# The bench box: hostname written by the installer, captive SSID re-minted
# afterwards by a later deploy, and NO store (every deployed router).
seed "system.@system[0]=system" "system.@system[0].hostname=tollgate-OQ3Q" \
     "wireless.tollgate_2g_open=wifi-iface" "wireless.tollgate_2g_open.ssid=tollgate-0GLK"
reset_log
setup_device_identity
eq "hostname wins over the stale SSID"   "$CODE" "OQ3Q"
eq "the adopted code is stored"          "$(state_get tollgate.device.code)" "OQ3Q"
eq "the captive SSID converges on it"    "$DEVICE_SSID" "TollGate-OQ3Q"
log_has "adopted from hostname"          && ok "the adoption is logged with its source" \
                                         || bad "the adoption is logged with its source"
log_has "Minted device code"             && bad "a code was minted instead of adopted" \
                                         || ok "nothing was minted on an already-deployed router"

echo "== an operator's custom hostname is not a code source (#444)"
seed "system.@system[0]=system" "system.@system[0].hostname=myrouter" \
     "wireless.tollgate_2g_open=wifi-iface" "wireless.tollgate_2g_open.ssid=TollGate-9K2M"
reset_log
setup_device_identity
eq "the captive SSID is adopted instead" "$CODE" "9K2M"
eq "the custom hostname is untouched"    "$(state_get system.@system[0].hostname)" "myrouter"
eq "the derived hostname still carries the code" "$DEVICE_HOSTNAME" "tollgate-9K2M"

echo "== a junk value in the store is re-derived, never trusted"
seed "system.@system[0]=system" "system.@system[0].hostname=tollgate-7Q7Q" \
     "tollgate.device=device" "tollgate.device.code=nope!"
reset_log
setup_device_identity
eq "a non-code is rejected and adoption runs" "$CODE" "7Q7Q"
eq "the store is repaired"                    "$(state_get tollgate.device.code)" "7Q7Q"

# ------------------------------------------------------- the private SSID
echo "== the private SSID shares the code, and the rename escape hatch holds"
CODE="OQ3Q"; NYM="c08r4d0r"
eq "a missing SSID is built from nym+code"     "$(private_ssid_for_code '')" "c08r4d0r-OQ3Q"
eq "a machine SSID converges (legacy hex)"     "$(private_ssid_for_code 'c08r4d0r-0GLK')" "c08r4d0r-OQ3Q"
eq "a machine SSID converges (digits)"         "$(private_ssid_for_code 'c08r4d0r-123456')" "c08r4d0r-OQ3Q"
eq "renamed SSID (no nym) is preserved"        "$(private_ssid_for_code 'MyNewNetwork')" "MyNewNetwork"
eq "renamed SSID (other nym) is preserved"     "$(private_ssid_for_code 'office-lan')" "office-lan"
eq "renamed SSID (nym + word) is preserved"    "$(private_ssid_for_code 'c08r4d0r-Office')" "c08r4d0r-Office"

echo "== the captive SSID on the repair path keeps an operator's own name"
DEVICE_SSID="TollGate-OQ3Q"
eq "a machine-shaped SSID converges"       "$(captive_ssid_for_code 'tollgate-0GLK')" "TollGate-OQ3Q"
eq "the brand case converges too"          "$(captive_ssid_for_code 'TollGate-0GLK')" "TollGate-OQ3Q"
eq "a whitelabel brand converges too"     "$(BRAND=otherbrand captive_ssid_for_code 'Otherbrand-AB12')" "TollGate-OQ3Q"
eq "an operator's own name survives"       "$(captive_ssid_for_code 'CafeWiFi')" "CafeWiFi"
eq "a brand word without a code survives"  "$(captive_ssid_for_code 'TollGate-CafeNet')" "TollGate-CafeNet"
eq "a missing SSID is built from the code" "$(captive_ssid_for_code '')" "TollGate-OQ3Q"

echo "== the nym is adopted from an existing machine-shaped private SSID"
seed "tollgate.device=device" "tollgate.device.code=OQ3Q" \
     "wireless.private_radio0=wifi-iface" "wireless.private_radio0.ssid=amperstrand-11AA"
resolve_nym
eq "the operator's own nym is kept" "$NYM" "amperstrand"
seed "tollgate.device=device" "tollgate.device.code=OQ3Q" \
     "wireless.private_radio0=wifi-iface" "wireless.private_radio0.ssid=MyNewNetwork"
resolve_nym
eq "a renamed SSID does not donate a nym" "$NYM" "c08r4d0r"
seed "tollgate.device=device" "tollgate.device.code=OQ3Q" "tollgate.device.nym=stored-nym"
resolve_nym
eq "a stored nym is authoritative" "$NYM" "stored-nym"
seed "tollgate.device=device" "tollgate.device.code=OQ3Q" \
     "wireless.private_radio0=wifi-iface" "wireless.private_radio0.ssid=x';reboot #-OQ3Q"
resolve_nym
eq "a quote-bearing adopted prefix is refused" "$NYM" "c08r4d0r"

# ----------------------------------------------------------- static guards
echo "== one mint path only (a second writer is how the drift started)"
mints=$(grep -c 'hexdump -n 3 -e' "$ROOT/$SCRIPT")
[ "$mints" = 1 ] && ok "exactly one 4-char mint in the shipped script" \
                 || bad "found $mints 4-char mint sites — the code must be minted in exactly one place"
grep -q 'RANDOM_SUFFIX' "$ROOT/$SCRIPT" \
    && bad "RANDOM_SUFFIX is back: a second, unshared suffix source" \
    || ok "no RANDOM_SUFFIX mint remains"
grep -q 'private_ssid="c08r4d0r-' "$ROOT/$SCRIPT" \
    && bad "the private SSID is hardcoded again instead of derived from the code" \
    || ok "the private SSID is derived from the code"

echo "== the store is read on BOTH setup paths"
grep -q '^    setup_device_identity$' "$ROOT/$SCRIPT" \
    && ok "the verify/repair path resolves and stores the code" \
    || bad "the verify/repair path does not resolve the code (the read-back is back)"
grep -q 'GATEWAY_NAME=$(captive_ssid_for_code' "$ROOT/$SCRIPT" \
    && ok "the repair path preserves an operator-named captive SSID" \
    || bad "the repair path forces the brand SSID on every router (a reinstall would rename the operator's open network)"
grep -q -F 'uci -q set nodogsplash.@nodogsplash[0].gatewayname="${GATEWAY_NAME} Portal"' "$ROOT/$SCRIPT" \
    && ok "the repair path converges nodogsplash's gatewayname with the SSID" \
    || bad "the repair path leaves nodogsplash's gatewayname on the pre-convergence name (the banner disagrees with the SSID)"
# ...and it must COMMIT it in the same block: the block runs before the
# nodogsplash export-diff snapshot, so a gatewayname-only change can never be the
# reason that conditional commit fires (the uncommitted /tmp/.uci delta, PR #605
# review finding F1). tests/uci-defaults-gatewayname-banner_test.sh drives both
# paths against a delta-aware uci and pins the committed value; this is the
# static half, so a reviewer reading only this suite still sees the contract.
if grep -A2 -F 'uci -q set nodogsplash.@nodogsplash[0].gatewayname="${GATEWAY_NAME} Portal"' "$ROOT/$SCRIPT" \
     | grep -q 'uci commit nodogsplash'; then
    ok "the repair path commits that gatewayname (not left as a session delta)"
else
    bad "the repair path writes gatewayname without committing it — the convergence is lost at reboot"
fi
grep -q '^setup_device_identity ' "$ROOT/$SCRIPT" \
    && ok "the full-setup path resolves the code" \
    || bad "the full-setup path does not resolve the code"

printf '\n%s passed, %s failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
