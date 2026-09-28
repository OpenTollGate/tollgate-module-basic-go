#!/usr/bin/env bash
# Offline tests for the radio band logic in the first-boot setup script
# (packaging/files/etc/uci-defaults/99-tollgate-setup): band detection,
# band-to-radio resolution and the adopt/rebind/skip/remove rules of
# setup_band_ap, all against a fake uci — no router needed.
#
# The real bug this guards (#452): OpenWrt numbers radio sections by
# detection order, not by band, so `radio0` is the 5 GHz radio on some
# hardware. Every case below therefore uses radios whose section names
# disagree with their bands.
#
# Usage: bash tests/uci-defaults-band_test.sh
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

# ---------------------------------------------------------------- fake uci
# Flat-file stand-in for /etc/config/wireless: one "key=value" line per
# option, "wireless.<section>=wifi-device|wifi-iface" lines for sections.
# `uci show` prints option values single-quoted and section types bare,
# matching real uci output closely enough for the parsers under test.
mkdir -p "$TMP/bin"
cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
# fake uci — state in $UCI_STATE; supports the subset the setup script uses.
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
    show)
        [ "${1:-}" = "wireless" ] || exit 0
        while IFS= read -r line; do
            key="${line%%=*}"
            val="${line#*=}"
            case "$key" in
                wireless.*) ;;
                *) continue ;;
            esac
            case "$key" in
                wireless.*.*) printf "%s='%s'\n" "$key" "$val" ;;
                *)            printf '%s=%s\n' "$key" "$val" ;;
            esac
        done < "$STATE"
        ;;
    set)
        key="${1%%=*}"
        val="${1#*=}"
        [ "$key" = "$1" ] && exit 1      # not key=value
        if grep -qF -- "$key=" "$STATE"; then
            sed -i "s|^$(printf '%s' "$key" | sed 's/[.[\*^$]/\\&/g')=.*|$key=$val|" "$STATE"
        else
            printf '%s=%s\n' "$key" "$val" >> "$STATE"
        fi
        ;;
    delete)
        key="${1%%.*}"                   # wireless.radio0.opt or wireless.radio0
        sed -i "/^$(printf '%s' "$1" | sed 's/[.[\*^$]/\\&/g')\(\.\..*\)\?=/d" "$STATE" 2>/dev/null
        sed -i "/^$(printf '%s' "$1" | sed 's/[.[\*^$]/\\&/g')=/d" "$STATE"
        ;;
    commit) : ;;
    *) exit 0 ;;
esac
SHIM
chmod +x "$TMP/bin/uci"
export PATH="$TMP/bin:$PATH"
export UCI_STATE="$TMP/wireless.state"
export LOGFILE="$TMP/setup.log"
: > "$LOGFILE"

# Source every function from the real script without running the setup.
GATEWAY_NAME="TollGate-TEST"    # configure_radio_ap reads it for the SSID
export GATEWAY_NAME
TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" 2>/dev/null
if ! command -v radio_band >/dev/null 2>&1; then
    echo "FATAL: could not source the setup script's functions" >&2
    exit 1
fi
ok "setup script sources in lib-only mode"

radios_state() { # one "section:option=value" per arg, in uci show order
    : > "$UCI_STATE"
    local entry key val
    for entry in "$@"; do
        key="${entry%%=*}"; val="${entry#*=}"
        printf '%s=%s\n' "$key" "$val" >> "$UCI_STATE"
    done
}

# ------------------------------------------------------------- radio_band
echo "== radio_band"
radios_state "wireless.r0=wifi-device" "wireless.r0.band=2g"
[ "$(radio_band r0)" = "2g" ] && ok "band option is read" || bad "band option is read"
radios_state "wireless.r0=wifi-device" "wireless.r0.band=5g"
[ "$(radio_band r0)" = "5g" ] && ok "5g band" || bad "5g band"
radios_state "wireless.r0=wifi-device" "wireless.r0.hwmode=11a"
[ "$(radio_band r0)" = "5g" ] && ok "legacy hwmode 11a -> 5g" || bad "legacy hwmode 11a -> 5g"
radios_state "wireless.r0=wifi-device" "wireless.r0.hwmode=11ng"
[ "$(radio_band r0)" = "2g" ] && ok "legacy hwmode 11ng -> 2g" || bad "legacy hwmode 11ng -> 2g"
radios_state "wireless.r0=wifi-device" "wireless.r0.hwmode=11na"
[ "$(radio_band r0)" = "5g" ] && ok "legacy hwmode 11na -> 5g (parity with Go)" \
    || bad "legacy hwmode 11na -> 5g (parity with Go)"
radios_state "wireless.r0=wifi-device" "wireless.r0.channel=36"
[ "$(radio_band r0)" = "5g" ] && ok "channel 36 -> 5g" || bad "channel 36 -> 5g"
radios_state "wireless.r0=wifi-device" "wireless.r0.channel=6"
[ "$(radio_band r0)" = "2g" ] && ok "channel 6 -> 2g" || bad "channel 6 -> 2g"
radios_state "wireless.r0=wifi-device" "wireless.r0.channel=auto"
radio_band r0 2>/dev/null && bad "channel auto is unknown (rc 0)" || ok "channel auto is unknown (rc 1)"

# ------------------------------------------------- find_radio_by_band / map
echo "== find_radio_by_band"
# The #452 hardware: radio0 is the 5 GHz radio, radio1 the 2.4 GHz one.
radios_state \
    "wireless.radio0=wifi-device" "wireless.radio0.band=5g" \
    "wireless.radio1=wifi-device" "wireless.radio1.band=2g"
[ "$(find_radio_by_band 5g)" = "radio0" ] && ok "5g resolves to radio0 on swapped hardware" \
    || bad "5g resolves to radio0 on swapped hardware"
[ "$(find_radio_by_band 2g)" = "radio1" ] && ok "2g resolves to radio1 on swapped hardware" \
    || bad "2g resolves to radio1 on swapped hardware"
find_radio_by_band 6g >/dev/null 2>&1 && bad "unknown band returns nothing" || ok "unknown band returns nothing"

echo "== detect_band_radios"
detect_band_radios
[ "$R2G" = "radio1" ] && [ "$R5G" = "radio0" ] \
    && ok "R2G/R5G are band-correct on swapped hardware" \
    || bad "R2G/R5G on swapped hardware (got R2G=$R2G R5G=$R5G)"
radios_state "wireless.radio0=wifi-device" "wireless.radio0.band=2g"
detect_band_radios
[ "$R2G" = "radio0" ] && [ -z "$R5G" ] \
    && ok "single-band router: R5G empty" \
    || bad "single-band router (got R2G=$R2G R5G=$R5G)"
radios_state "wireless.radio0=wifi-device" "wireless.radio1=wifi-device"
detect_band_radios
[ "$R2G" = "radio0" ] && [ "$R5G" = "radio1" ] \
    && ok "no band info anywhere: radio0/radio1 fallback (legacy behavior)" \
    || bad "no-band-info fallback (got R2G=$R2G R5G=$R5G)"

# -------------------------------------------------------- first_ap_iface_on
echo "== first_ap_iface_on"
radios_state \
    "wireless.radio0=wifi-device" "wireless.radio0.band=2g" \
    "wireless.wif1=wifi-iface" "wireless.wif1.device=radio0" "wireless.wif1.mode=sta" \
    "wireless.wif2=wifi-iface" "wireless.wif2.device=radio0" "wireless.wif2.mode=ap"
[ "$(first_ap_iface_on radio0)" = "wif2" ] \
    && ok "STA sections are skipped when adopting an AP" \
    || bad "STA sections are skipped when adopting an AP"

# ------------------------------------------------------------ setup_band_ap
echo "== setup_band_ap"
# 1. Existing section by name on the WRONG radio is adopted and rebound.
radios_state \
    "wireless.radio0=wifi-device" "wireless.radio0.band=2g" \
    "wireless.radio1=wifi-device" "wireless.radio1.band=5g" \
    "wireless.tollgate_5g_open=wifi-iface" "wireless.tollgate_5g_open.device=radio0" \
    "wireless.tollgate_5g_open.mode=ap"
setup_band_ap "$R5G" tollgate_5g_open
uci -q get wireless.tollgate_5g_open.device 2>/dev/null | grep -qx radio1 \
    && ok "stale 5g section is rebound to the band's radio" \
    || bad "stale 5g section rebind (device=$(uci -q get wireless.tollgate_5g_open.device 2>/dev/null))"

# 2. No named section: the radio's stock AP section is adopted.
radios_state \
    "wireless.radio0=wifi-device" "wireless.radio0.band=2g" \
    "wireless.default_radio0=wifi-iface" "wireless.default_radio0.device=radio0" \
    "wireless.default_radio0.mode=ap"
R2G=radio0; R5G=""
setup_band_ap radio0 tollgate_2g_open
uci -q get wireless.default_radio0.name 2>/dev/null | grep -qx tollgate_2g_open \
    && ok "stock AP section is adopted and labelled" \
    || bad "stock AP adoption (name=$(uci -q get wireless.default_radio0.name 2>/dev/null))"

# 3. Nothing to adopt: a section is created on the band's radio.
radios_state \
    "wireless.radio5=wifi-device" "wireless.radio5.band=5g"
setup_band_ap radio5 tollgate_5g_open
[ "$(uci -q get wireless.tollgate_5g_open.device 2>/dev/null)" = "radio5" ] \
    && ok "missing section is created on the band's radio" \
    || bad "section creation (device=$(uci -q get wireless.tollgate_5g_open.device 2>/dev/null))"

# 4. No radio for the band: a stale section is REMOVED, not left on a phantom.
radios_state \
    "wireless.radio0=wifi-device" "wireless.radio0.band=2g" \
    "wireless.tollgate_5g_open=wifi-iface" "wireless.tollgate_5g_open.device=radio1" \
    "wireless.tollgate_5g_open.mode=ap"
setup_band_ap "" tollgate_5g_open
uci -q get wireless.tollgate_5g_open >/dev/null 2>&1 \
    && bad "stale 5g section survives without a 5g radio" \
    || ok "stale 5g section is removed without a 5g radio"

# 5. A section in STA mode is never touched (it may hold the upstream link).
radios_state \
    "wireless.radio0=wifi-device" "wireless.radio0.band=2g" \
    "wireless.tollgate_2g_open=wifi-iface" "wireless.tollgate_2g_open.device=radio0" \
    "wireless.tollgate_2g_open.mode=sta"
setup_band_ap radio0 tollgate_2g_open
[ "$(uci -q get wireless.tollgate_2g_open.mode 2>/dev/null)" = "sta" ] \
    && ok "STA section is preserved" \
    || bad "STA section is preserved"

# --------------------------------------------------------- enable_all_radios
echo "== enable_all_radios"
radios_state \
    "wireless.radio0=wifi-device" "wireless.radio0.band=2g" "wireless.radio0.disabled=1" \
    "wireless.radio1=wifi-device" "wireless.radio1.band=5g" "wireless.radio1.disabled=1"
enable_all_radios
[ "$(uci -q get wireless.radio0.disabled 2>/dev/null)" = "0" ] && \
[ "$(uci -q get wireless.radio1.disabled 2>/dev/null)" = "0" ] \
    && ok "every radio is enabled" || bad "every radio is enabled"

# ------------------------------------------------------------------ verdict
echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
