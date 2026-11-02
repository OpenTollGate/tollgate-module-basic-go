#!/usr/bin/env bash
# Offline SEQUENCE test for the two uhttpd writers on a fresh install (#849).
#
# THE DEFECT THIS PINS
#
# The install pass runs the uci-defaults in the recipe order 90, 99, 999 (the
# gate-last contract of tests/packaging/uci-defaults-run-order_test.sh): the
# module's 99-tollgate-setup writes uhttpd.main, and the portal-staged
# 999-tollgate-admin-setup — the mode-aware admin writer, staged from the
# pinned portal tree by packaging/portal-build.sh — writes uhttpd.admin and
# STRIPS its own pair from every other section. Between them sits the D4
# mapping gate: 99 honoured entry_ui=board only when the marker file said so,
# and the marker's only writer is 999 itself, at the END of its run. On a
# fresh install 99 therefore ran first, found no marker, repaired board to the
# legacy luci mapping and put uhttpd.main on the ENTRY pair (:8080/:443); 999
# then applied board mode, pointed uhttpd.admin at the entry pair and stripped
# :8080/:443 from every other section — leaving uhttpd.main with NO listeners
# at all. On the router that is LuCI unbound and procd crash-looping the
# default uhttpd instance ("No sockets bound, unable to continue"): issue
# #849, observed on a stock 25.12.5 x86-64 VM.
#
# WHAT IT PINS — the combined end state of the REAL 99 followed by the REAL
# pinned 999, in the recipe order, not either script in isolation:
#
#   * no shipped uhttpd section ends the sequence with zero listeners (the
#     admin board's DELIBERATE fail-closed refusal — root has no usable
#     password — is the one sanctioned exception, and is asserted as such);
#   * exactly ONE section claims each of the four admin ports 8080/443/8090/
#     8443, on http or https, IPv4 or IPv6 (invariant 3: one binder per port);
#   * BOTH mappings converge: board mode leaves main on :8090/:8443 and admin
#     on :8080/:443; luci mode the reverse;
#   * a SECOND pass of the same sequence leaves the uhttpd config unchanged
#     (the boot sweep and every reinstall re-run both scripts);
#   * the entry-ui marker records the mapping the box actually applied.
#
# THE 999 UNDER TEST IS THE PINNED PORTAL'S, never a committed copy (the
# drift doctrine: one source per fact). It is resolved the way
# tests/packaging/assert-portal-bundle-contract.sh resolves every check: a
# local clone that already has the pin (PORTAL_DIR, default the
# portal-build.sh checkout at /tmp/tollgate-captive-portal-site), else one
# shallow fetch of the pinned SHA from packaging/build-inputs.json. Without
# either (an offline machine that never built the portal), the suite SKIPs
# loudly: the module repo carries no 92 to fall back on.
#
# Offline: no router, no SDK, no service starts. The init.d restarts the 999
# performs are redirected to `:` in the copy; uci/apk/passwd/hexdump are
# fakes; every absolute live-system path is pinned into a sandbox by env (the
# same seam tests/packaging/configui-8090-single-owner_test.sh and
# tests/uci-defaults-entry-ui-mapping_test.sh use). What this harness CANNOT
# prove, offline, is the router-visible half: the uci-defaults boot glob
# collation, apk's install-time consumption of the scripts, procd respawn and
# uhttpd's zero-listener exit — those live on the bench QEMU lane.
#
# Usage: bash tests/packaging/entry-ui-install-sequence_test.sh [PORTAL_DIR]
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/etc/tollgate" "$TMP/etc/uci-defaults" "$TMP/init.d" \
         "$TMP/config" "$TMP/etc/tollgate/ssl" "$TMP/router-home"

# --------------------------------------------- resolve the pinned 999 script
# Same resolution order as assert-portal-bundle-contract.sh: a local clone
# that has the pin, else one shallow fetch. A committed copy would drift.
PORTAL_DIR="${1:-${PORTAL_DIR:-}}"
if [ -z "$PORTAL_DIR" ] && [ -d /tmp/tollgate-captive-portal-site/.git ]; then
    PORTAL_DIR=/tmp/tollgate-captive-portal-site
fi
PIN=""
if command -v jq >/dev/null 2>&1 && [ -r packaging/build-inputs.json ]; then
    PIN="$(jq -r '.portal.commit // empty' packaging/build-inputs.json)"
    REPO="$(jq -r '.portal.repo // empty' packaging/build-inputs.json)"
fi
fetch_dir=""
fetch_92() { # prints the pinned 92 script's bytes; empty on failure
    local content=""
    if [ -n "$PORTAL_DIR" ] && git -C "$PORTAL_DIR" rev-parse --git-dir >/dev/null 2>&1; then
        if git -C "$PORTAL_DIR" cat-file -e "${PIN}^{commit}" 2>/dev/null; then
            content="$(git -C "$PORTAL_DIR" show "${PIN}:packaging/files/etc/uci-defaults/92-tollgate-admin-setup" 2>/dev/null)"
        fi
    fi
    if [ -z "$content" ] && [ -n "$REPO" ] && command -v git >/dev/null 2>&1; then
        if [ -z "$fetch_dir" ]; then
            fetch_dir="$(mktemp -d)"
            git -C "$fetch_dir" init -q >/dev/null 2>&1
            git -C "$fetch_dir" remote add origin "$REPO" >/dev/null 2>&1
            git -C "$fetch_dir" fetch -q --depth 1 origin "$PIN" >/dev/null 2>&1 || true
        fi
        if git -C "$fetch_dir" cat-file -e "${PIN}^{commit}" 2>/dev/null; then
            content="$(git -C "$fetch_dir" show "${PIN}:packaging/files/etc/uci-defaults/92-tollgate-admin-setup" 2>/dev/null)"
        fi
    fi
    printf '%s' "$content"
}

SKIP_REASON=""
if [ -z "$PIN" ]; then
    SKIP_REASON="no readable .portal.commit in packaging/build-inputs.json"
else
    NINETYNINE_RAW="$(fetch_92)"
    if [ -z "$NINETYNINE_RAW" ]; then
        SKIP_REASON="the pinned portal tree is unreachable (no clone with ${PIN:0:12}, and the shallow fetch failed) — run bash packaging/portal-build.sh first, or pass PORTAL_DIR"
    fi
fi
if [ -n "$SKIP_REASON" ]; then
    echo "SKIP: $SKIP_REASON"
    echo "     the sequence contract cannot be asserted without the pinned 999."
    exit 0
fi

# --------------------------------------------------------------- fake apk
FAKE_VERSION="0.6.0_seqtest-r0"
export FAKE_APK_VERSION="$FAKE_VERSION"
cat > "$TMP/bin/apk" <<'SHIM'
#!/bin/sh
if [ "${1:-}" = "list" ]; then
    printf '%s\n' "tollgate-wrt-${FAKE_APK_VERSION} aarch64_cortex-a53 {tollgate-wrt} (GPL-3.0-only) [installed]"
fi
exit 0
SHIM
chmod +x "$TMP/bin/apk"

# --------------------------------------------------------------- fake uci
# Flat-file stand-in: one "key=value" line per option, "<section>=<type>" per
# section; `delete <section>` drops the section AND its options (the
# vacuous-shim trap). Same shim as configui-8090-single-owner_test.sh.
export UCI_STATE="$TMP/uci.state"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
case "$cmd" in
    get)
        vals="$(grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-)"
        if [ -z "$vals" ]; then
            vals="$(grep -F -- "$1." "$state" 2>/dev/null | head -n1 | cut -d= -f2-)"
        fi
        if [ -z "$vals" ]; then
            [ "$q" = 1 ] || echo "uci: Entry not found" >&2
            exit 1
        fi
        printf '%s\n' "$vals"
        ;;
    set)
        key="${1%%=*}"
        [ "$key" = "$1" ] && exit 1
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        printf '%s\n' "$1" >> "$state"
        ;;
    add_list) printf '%s\n' "$1" >> "$state" ;;
    del_list)
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    add) printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state" ;;
    delete)
        grep -v -F -e "$1=" -e "$1." "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    show|export) cat "$state" ;;
    commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"

# --------------------------------------------------------------- fake jq
# The one query both scripts use (`jq -r '.entry_ui // empty' FILE`). A missing
# file, a missing key and a non-JSON body all print nothing — the shim cannot
# flatter the fail-closed matrix. Same shim as
# tests/uci-defaults-entry-ui-mapping_test.sh.
cat > "$TMP/bin/jq" <<'SHIM'
#!/bin/sh
[ "${1:-}" = "-r" ] && shift
expr="${1:-}"; shift || true
file="${1:-}"
case "$expr" in
    ".entry_ui // empty")
        sed -n 's/.*"entry_ui"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file" 2>/dev/null | head -n1
        ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/jq"

# ------------------------------------------------------------ fake passwd
export PASSWD_LOG="$TMP/passwd.log"
export PASSWD_SEEN="$TMP/passwd.seen"
export PASSWD_FAILS=0
cat > "$TMP/bin/passwd" <<'SHIM'
#!/bin/sh
printf '%s\n' "$*" >> "$PASSWD_LOG"
IFS= read -r p1 || p1=""
IFS= read -r p2 || p2=""
[ "${PASSWD_FAILS:-0}" = "1" ] && exit 1
[ -n "$p1" ] || exit 1
printf '%s\n' "$p1" > "$PASSWD_SEEN"
awk -F: -v OFS=: -v h='$1$stub$abcdefghijklmnopqrstuv' \
    '$1 == "root" { $2 = h } { print }' "$SHADOW_FILE" > "$SHADOW_FILE.tmp" 2>/dev/null
mv "$SHADOW_FILE.tmp" "$SHADOW_FILE"
exit 0
SHIM
chmod +x "$TMP/bin/passwd"

# ------------------------------------------------------------ fake hexdump
export HEXDUMP_SEQ_FILE="$TMP/hexdump.seq"
printf '0\n' > "$HEXDUMP_SEQ_FILE"
cat > "$TMP/bin/hexdump" <<'SHIM'
#!/bin/sh
seq="${HEXDUMP_SEQ_FILE:?fake hexdump: HEXDUMP_SEQ_FILE is not set}"
n=$(cat "$seq" 2>/dev/null || echo 0)
n=$((n + 1))
printf '%s\n' "$n" > "$seq"
printf '%04X\n' "$n"
SHIM
chmod +x "$TMP/bin/hexdump"

export PATH="$TMP/bin:$PATH"

# ------------------------------------------------------ sandboxed live paths
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
CONFIG_JSON="$TMP/etc/tollgate/config.json"
ENTRY_MARKER="$TMP/etc/tollgate/entry-ui-mapping"
# The probe target is the SANDBOX install location of the staged admin script:
# the same file is both what 99 consults and what the sequence executes, exactly
# as /etc/uci-defaults/999-tollgate-admin-setup is on the router.
ADMIN_SETUP_INSTALLED="$TMP/etc/uci-defaults/999-tollgate-admin-setup"
UHTTPD_INIT="$TMP/init.d/uhttpd"
NDS_INIT="$TMP/init.d/nodogsplash"
TOLLGATE_CLI="$TMP/bin/tollgate-cli"          # deliberately NOT created
PROVISIONED_CERT="$TMP/etc/tollgate/ssl/server.crt"
PROVISIONED_KEY="$TMP/etc/tollgate/ssl/server.key"
IMAGE_CERT="$TMP/etc/uhttpd.crt"
IMAGE_KEY="$TMP/etc/uhttpd.key"
BRAND_FILE="$TMP/etc/tollgate/brand"

export TOLLGATE_ENTRY_UI_CONFIG="$CONFIG_JSON"
export TOLLGATE_CONFIG_JSON="$CONFIG_JSON"
export TOLLGATE_ENTRY_UI_MARKER="$ENTRY_MARKER"
export TOLLGATE_ENTRY_UI_ADMIN_SETUP="$ADMIN_SETUP_INSTALLED"
# The credential markers (#913's fixed-default path writes them on an empty
# root) default to absolute /etc paths; keep them in the sandbox so a source
# run cannot touch the build host's /etc/tollgate — the same discipline the
# mapping test pins (measured: without these, scenario 4 wrote empty markers
# to the HOST's live /etc/tollgate).
export ADMIN_PROVISIONAL_MARKER="$TMP/etc/tollgate/admin-credential-provisional"
export PRIVATE_WIFI_PROVISIONAL_MARKER="$TMP/etc/tollgate/private-wifi-credential-provisional"
export ENTRY_UI_JQ_BIN="$TMP/bin/jq"
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd"
export ROUTER_HOME_DIR="$TMP/router-home"
export DEVICE_CODE_STORE="$TMP/config/tollgate"
export TOLLGATE_SHADOW_FILE="$TMP/shadow"
export TOLLGATE_SSL_CERT="$PROVISIONED_CERT"
export TOLLGATE_SSL_KEY="$PROVISIONED_KEY"
export UHTTPD_IMAGE_CERT="$IMAGE_CERT"
export UHTTPD_IMAGE_KEY="$IMAGE_KEY"
export TOLLGATE_CLI

# ---------------------------------------------- the two scripts under test
# 99: the shipped script with the absolute live-system paths redirected (the
# configui-8090-single-owner_test.sh redirect set, plus the TLS/init vars the
# mapping harness pins), and the version placeholder pinned for determinism.
SCRIPT99="$TMP/99-tollgate-setup"
sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
    -e "s|^LOGFILE=.*tollgate-setup\.log.*$|LOGFILE=$LOGFILE|" \
    -e "s|^UHTTPD_INIT=\"/etc/init\.d/uhttpd\"\$|UHTTPD_INIT=\"$UHTTPD_INIT\"|" \
    -e "s|^NDS_INIT=\"/etc/init\.d/nodogsplash\"\$|NDS_INIT=\"$NDS_INIT\"|" \
    -e "s|^TOLLGATE_CLI=\"/usr/bin/tollgate\"\$|TOLLGATE_CLI=\"$TOLLGATE_CLI\"|" \
    -e "s|^PROVISIONED_CERT=\"/etc/tollgate/ssl/server\.crt\"\$|PROVISIONED_CERT=\"$PROVISIONED_CERT\"|" \
    -e "s|^PROVISIONED_KEY=\"/etc/tollgate/ssl/server\.key\"\$|PROVISIONED_KEY=\"$PROVISIONED_KEY\"|" \
    -e "s|^UHTTPD_IMAGE_CERT=\"/etc/uhttpd\.crt\"\$|UHTTPD_IMAGE_CERT=\"$IMAGE_CERT\"|" \
    -e "s|^UHTTPD_IMAGE_KEY=\"/etc/uhttpd\.key\"\$|UHTTPD_IMAGE_KEY=\"$IMAGE_KEY\"|" \
    -e "s|> */proc/sys/kernel/hostname|> $TMP/kernel-hostname|" \
    -e "s|/etc/profile|$TMP/profile|g" \
    -e "s|/etc/config/nodogsplash|$TMP/config/nodogsplash|g" \
    -e "s|/etc/tollgate/brand|$TMP/etc/tollgate/brand|g" \
    packaging/files/etc/uci-defaults/99-tollgate-setup > "$SCRIPT99"
sed -i "s|^SETUP_VERSION=\"__TOLLGATE_VERSION__\"\$|SETUP_VERSION=\"$FAKE_VERSION\"|" "$SCRIPT99"
if grep -q "^SETUP_FLAG=\"$FLAG\"\$" "$SCRIPT99" &&
   grep -q "^SETUP_VERSION=\"$FAKE_VERSION\"\$" "$SCRIPT99" &&
   grep -q "^UHTTPD_INIT=\"$UHTTPD_INIT\"\$" "$SCRIPT99"; then
    ok "harness redirected the live paths in the copied 99"
else
    bad "could not redirect the live paths in the copied 99 — the run would touch the host"
fi

# 999: the pinned portal script with ONLY its service restarts neutralised (no
# set -e in the script; on the router they are the point, here they are
# impossible). Everything else — the mapping resolution, the credential gate,
# the listener writes, the strip loop, the marker protocol — runs verbatim.
printf '%s\n' "$NINETYNINE_RAW" | \
    sed -e 's|/etc/init\.d/rpcd restart|:|g' \
        -e 's|/etc/init\.d/uhttpd restart|:|g' > "$ADMIN_SETUP_INSTALLED"
if grep -q '^: *$' "$ADMIN_SETUP_INSTALLED" && grep -q 'TOLLGATE_ENTRY_UI_MARKER' "$ADMIN_SETUP_INSTALLED"; then
    ok "harness neutralised the service restarts in the pinned 999 copy"
else
    bad "could not neutralise the service restarts in the pinned 999 copy"
fi
NINETYNINE="$ADMIN_SETUP_INSTALLED"

if sh -n "$SCRIPT99" && sh -n "$NINETYNINE"; then
    ok "both scripts under test are valid /bin/sh"
else
    bad "a script under test does not parse"
fi

# ------------------------------------------------------------- fixtures
state()      { printf '%s\n' "$@" >> "$UCI_STATE"; }
listen_count() { grep -F -x -c -- "uhttpd.$1.listen_$2=$3" "$UCI_STATE" 2>/dev/null | tr -d ' '; }

seed_host() {
    state 'network.lan=interface' 'network.lan.ipaddr=192.168.1.1' 'network.lan.netmask=255.255.255.0' \
          'system.@system[0]=system' 'system.@system[0].hostname=OpenWrt' \
          'wireless.radio0=wifi-device' 'wireless.radio0.band=2g' \
          'wireless.radio1=wifi-device' 'wireless.radio1.band=5g' \
          'wireless.default_radio0=wifi-iface' 'wireless.default_radio0.device=radio0' \
          'wireless.default_radio0.mode=ap' 'wireless.default_radio0.ssid=OpenWrt' \
          'wireless.default_radio1=wifi-iface' 'wireless.default_radio1.device=radio1' \
          'wireless.default_radio1.mode=ap' 'wireless.default_radio1.ssid=OpenWrt' \
          'nodogsplash.@nodogsplash[0]=nodogsplash' \
          'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2121'
}

# A fresh stock OpenWrt: LuCI's default uhttpd.main answers :80. No admin
# section, no marker, no config.json — exactly the issue #849 fresh install.
seed_stock() {
    : > "$UCI_STATE"
    rm -f "$CONFIG_JSON" "$ENTRY_MARKER" "$FLAG" "$BRAND_FILE" \
        "$ADMIN_PROVISIONAL_MARKER" "$PRIVATE_WIFI_PROVISIONAL_MARKER"
    : > "$PASSWD_LOG"
    : > "$PASSWD_FILE"
    : > "$TMP/profile"
    printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
    printf 'IMAGE-PLACEHOLDER-CERT (CN=OpenWrt, SAN DNS:OpenWrt)\n' > "$IMAGE_CERT"
    printf 'IMAGE-PLACEHOLDER-KEY\n' > "$IMAGE_KEY"
    printf 'PROVISIONED-IDENTITY-CERT\n' > "$PROVISIONED_CERT"
    printf 'PROVISIONED-IDENTITY-KEY\n' > "$PROVISIONED_KEY"
    seed_host
    state 'uhttpd.main=uhttpd' \
          'uhttpd.main.listen_http=0.0.0.0:80' \
          'uhttpd.main.listen_http=[::]:80' \
          'uhttpd.main.home=/www'
}

# --------------------------------------------- the contract, as predicates
# Sections claiming <port> — on http or https, IPv4 or IPv6. Sorted, one/line.
claimants() {
    grep -E -- "^uhttpd\.[^.]+\.listen_https?=.*:${1}\$" "$UCI_STATE" 2>/dev/null |
        sed -e 's/^uhttpd\.//' -e 's/\..*//' | sort -u
}
listener_lines() { grep -c -E -- "^uhttpd\.$1\.listen_(http|https)=" "$UCI_STATE" 2>/dev/null || true; }

one_owner() { # one_owner <port> <want-section>
    local got
    got="$(claimants "$1" | tr '\n' ' ')"
    [ "$(claimants "$1" | grep -c .)" = 1 ] && [ "${got% }" = "$2" ]
}

section_alive() { [ "$(listener_lines "$1")" -ge 1 ]; }

# The full fence, shared by every scenario: one binder per admin port, and no
# owned section listenerless. `refused <board|luci>` asserts the ONE sanctioned
# exception — the credential gate's deliberate refusal — instead of the admin
# listener, and requires the refusal to be visible in 999's output: the refused
# board's OWN pair is then expected to be unbound, while the OTHER pair still
# has exactly one owner (LuCI's).
assert_fence() { # assert_fence <label> [refused <board|luci>]
    local label="$1" refused="${2:-}" mode="${3:-}" p
    for p in 8080 443 8090 8443; do
        if [ "$(claimants "$p" | grep -c .)" = 1 ] && [ -n "$(claimants "$p")" ]; then
            ok "$label: exactly one section binds :$p ($(claimants "$p"))"
        elif [ "$refused" = refused ]; then
            local board_http=8080 board_https=443
            [ "$mode" = luci ] && { board_http=8090; board_https=8443; }
            if { [ "$p" = "$board_http" ] || [ "$p" = "$board_https" ]; } &&
               [ -z "$(claimants "$p")" ]; then
                ok "$label: :$p is unbound — the refused board's own pair"
            else
                bad "$label: :$p is claimed by '$(claimants "$p" | tr '\n' ' ')' (want exactly one owner)"
            fi
        else
            bad "$label: :$p is claimed by '$(claimants "$p" | tr '\n' ' ')' (want exactly one owner)"
        fi
    done
    for p in main portal trusted; do
        if section_alive "$p"; then
            ok "$label: uhttpd.$p keeps at least one listener"
        else
            bad "$label: uhttpd.$p ended the sequence with NO listeners (the #849 crash-loop state)"
        fi
    done
    if [ "$refused" = refused ]; then
        if [ "$(listener_lines admin)" = 0 ] &&
           { grep -q "is NOT being served" "$TMP/999.out" 2>/dev/null ||
             grep -q "is NOT being served" "$TMP/999.err" 2>/dev/null; }; then
            ok "$label: the admin board is deliberately refused (fail-closed gate), not torn"
        else
            bad "$label: admin listenerless without the refusal being stated (listeners=$(listener_lines admin))"
        fi
    else
        if section_alive admin; then
            ok "$label: uhttpd.admin keeps at least one listener"
        else
            bad "$label: uhttpd.admin ended the sequence with NO listeners"
        fi
    fi
}

assert_mapping() { # assert_mapping <label> <board|luci> [refused]
    local label="$1" main_http main_https admin_http admin_https
    if [ "$2" = board ]; then
        main_http=8090; main_https=8443; admin_http=8080; admin_https=443
    else
        main_http=8080; main_https=443; admin_http=8090; admin_https=8443
    fi
    [ "$(listen_count main http "0.0.0.0:$main_http")" = 1 ] && [ "$(listen_count main http "[::]:$main_http")" = 1 ] \
        && ok "$label: LuCI (uhttpd.main) answers :$main_http on both families" \
        || bad "$label: uhttpd.main :$main_http listeners are $(listen_count main http "0.0.0.0:$main_http")/$(listen_count main http "[::]:$main_http") (want 1/1)"
    [ "$(listen_count main https "0.0.0.0:$main_https")" = 1 ] && [ "$(listen_count main https "[::]:$main_https")" = 1 ] \
        && ok "$label: LuCI (uhttpd.main) answers :$main_https on both families" \
        || bad "$label: uhttpd.main :$main_https listeners are $(listen_count main https "0.0.0.0:$main_https")/$(listen_count main https "[::]:$main_https") (want 1/1)"
    if [ "${3:-}" = refused ]; then
        # The credential gate refuses the BOARD; which pair it would answer is
        # deliberately not bound. LuCI's pair above is the reachable half.
        ok "$label: the board is refused — its pair is intentionally unbound"
        return 0
    fi
    [ "$(listen_count admin http "0.0.0.0:$admin_http")" = 1 ] && [ "$(listen_count admin http "[::]:$admin_http")" = 1 ] \
        && ok "$label: the board (uhttpd.admin) answers :$admin_http on both families" \
        || bad "$label: uhttpd.admin :$admin_http listeners are $(listen_count admin http "0.0.0.0:$admin_http")/$(listen_count admin http "[::]:$admin_http") (want 1/1)"
    [ "$(listen_count admin https "0.0.0.0:$admin_https")" = 1 ] && [ "$(listen_count admin https "[::]:$admin_https")" = 1 ] \
        && ok "$label: the board (uhttpd.admin) answers :$admin_https on both families" \
        || bad "$label: uhttpd.admin :$admin_https listeners are $(listen_count admin https "0.0.0.0:$admin_https")/$(listen_count admin https "[::]:$admin_https") (want 1/1)"
}

uhttpd_lines() { grep -E '^uhttpd\.' "$UCI_STATE" 2>/dev/null | sort; }

run_99()  { : > "$LOGFILE"; sh "$SCRIPT99" > "$TMP/99.out" 2> "$TMP/99.err"; }
run_999() { sh "$NINETYNINE" > "$TMP/999.out" 2> "$TMP/999.err"; }

# ------------------------------------------- controls: the fence can see it
echo "== controls: the detectors must be able to see the defect"
seed_stock
state 'uhttpd.main.listen_http=0.0.0.0:8090' 'uhttpd.main.listen_http=[::]:8090' \
      'uhttpd.main.listen_https=0.0.0.0:8443' 'uhttpd.main.listen_https=[::]:8443' \
      'uhttpd.admin=uhttpd' \
      'uhttpd.admin.listen_http=0.0.0.0:8080' 'uhttpd.admin.listen_http=[::]:8080' \
      'uhttpd.admin.listen_https=0.0.0.0:443' 'uhttpd.admin.listen_https=[::]:443' \
      'uhttpd.admin.home=/www/tollgate'
if one_owner 8090 main && one_owner 8443 main && one_owner 8080 admin && one_owner 443 admin; then
    ok "positive control: a converged board state satisfies the one-owner predicates"
else
    bad "positive control: the converged board state fails the one-owner predicates"
fi
# The torn state of issue #849: 999 stripped the entry pair from a main that
# 99 had put there — main binds NOTHING.
state 'uhttpd.admin=uhttpd' 'uhttpd.admin.listen_http=0.0.0.0:8080' >/dev/null
sed -i -e '/^uhttpd\.main\.listen_http=/d' -e '/^uhttpd\.main\.listen_https=/d' "$UCI_STATE"
if section_alive main; then
    bad "negative control: a listenerless uhttpd.main reads as alive — the fence is vacuous"
else
    ok "negative control: a listenerless uhttpd.main (the #849 end state) is detected"
fi
# A double claim on :8090 — the bind fight the fence also guards.
state 'uhttpd.stray=uhttpd' 'uhttpd.stray.listen_http=0.0.0.0:8090' \
      'uhttpd.main.listen_http=0.0.0.0:8090' >/dev/null
if one_owner 8090 main; then
    bad "negative control: two sections claiming :8090 satisfy the one-owner predicate"
else
    ok "negative control: two sections claiming :8090 are rejected"
fi

# ------------------------------------------- 1. fresh install, board default
# No config.json (fail-closed board), no marker (its only writer has not run),
# the staged 999 present at its install location, a usable root credential.
# The recipe order is 90, 99, 999.
echo
echo "== scenario 1: fresh install (99 then 999, board default, credential set)"
seed_stock
run_99; rc99=$?
run_999; rc999=$?
[ "$rc99" = 0 ] || bad "scenario 1: 99 exited $rc99 (stderr: $(head -n 3 "$TMP/99.err" | tr '\n' ' '))"
[ "$rc999" = 0 ] || bad "scenario 1: 999 exited $rc999 (stderr: $(head -n 3 "$TMP/999.err" | tr '\n' ' '))"
grep -q "Setup branch FULL" "$LOGFILE" \
    && ok "scenario 1: 99 took the FULL path (fresh install)" \
    || bad "scenario 1: 99 did not take the FULL path ($(grep -m1 'Setup branch' "$LOGFILE" 2>/dev/null))"
assert_fence  "scenario 1"
assert_mapping "scenario 1" board
if grep -q "Repairing to the legacy luci mapping" "$LOGFILE"; then
    bad "scenario 1: 99 repaired board to luci although both halves are installed (the order-induced tear)"
else
    ok "scenario 1: 99 honoured board with both halves installed (no D4 repair)"
fi
if [ "$(cat "$ENTRY_MARKER" 2>/dev/null)" = board ]; then
    ok "scenario 1: the marker records the applied board mapping"
else
    bad "scenario 1: the marker is '$(cat "$ENTRY_MARKER" 2>/dev/null)' (want board)"
fi

# ------------------------------------------- 2. second pass converges (idem)
echo
echo "== scenario 2: a second pass of the same sequence changes nothing"
cp "$UCI_STATE" "$TMP/state.after-first"
run_99
run_999
if diff <(uhttpd_lines "$TMP/state.after-first") <(uhttpd_lines "$UCI_STATE") > "$TMP/idempotence.diff" 2>&1; then
    ok "scenario 2: the second 99+999 pass leaves the uhttpd config unchanged"
else
    bad "scenario 2: the second pass changed the uhttpd config — $(head -n 6 "$TMP/idempotence.diff" | tr '\n' ' ')"
fi
assert_fence "scenario 2"
assert_mapping "scenario 2" board

# ------------------------------------------- 3. luci mapping, same sequence
echo
echo "== scenario 3: entry_ui=luci — the reverse mapping converges too"
seed_stock
printf '{"entry_ui":"luci"}\n' > "$CONFIG_JSON"
printf 'luci\n' > "$ENTRY_MARKER"
run_99
run_999
assert_fence  "scenario 3"
assert_mapping "scenario 3" luci
[ "$(cat "$ENTRY_MARKER" 2>/dev/null)" = luci ] \
    && ok "scenario 3: the marker still records luci" \
    || bad "scenario 3: the marker is '$(cat "$ENTRY_MARKER" 2>/dev/null)' (want luci)"

# ---------------- 4. empty root — the #913 fixed-default contract converges
# No usable root credential and no operator-chosen password: the sequence now
# CONVERGES instead of refusing. 99 installs the fixed default (post-#913: a
# router nobody can log into is worse than a documented default the operator
# must change; see the #911 reconciliation), 999's gate then sees a non-empty
# hash, and the board is SERVED while LuCI stays on its mapped pair. The #838
# fail-closed refusal survives as a 999-level invariant, pinned by 4b with 99
# absent — the only state in which an empty hash still reaches the gate.
echo
echo "== scenario 4: empty root — the fixed-default credential converges the board"
seed_stock
printf 'root::0:0:99999:7:::\n' > "$SHADOW_FILE"   # empty hash: the fresh-flash state
run_99
run_999; rc999=$?
[ "$rc999" = 0 ] || bad "scenario 4: 999 exited $rc999"
[ -s "$PASSWD_LOG" ] \
    && ok "scenario 4: 99 installed a credential on the empty root (the #913 default path)" \
    || bad "scenario 4: 99 never invoked passwd on an empty root"
[ -n "$(awk -F: '$1=="root" && $2!="" && $2!="x" {print "set"}' "$SHADOW_FILE")" ] \
    && ok "scenario 4: root's shadow hash is set after the sequence" \
    || bad "scenario 4: root's shadow hash is still empty after the sequence"
[ -f "$ADMIN_PROVISIONAL_MARKER" ] \
    && ok "scenario 4: the provisional-credential marker is written (change forced at first login)" \
    || bad "scenario 4: the provisional-credential marker is absent"
assert_fence  "scenario 4"
assert_mapping "scenario 4" board

# ------ 4b. an emptied root hash on a converged box still refuses (the #838 gate)
# Post-#913 an empty hash never reaches 999 through the fresh sequence (99
# installs the default first), so the invariant is staged the way it presents
# in the field: the box has ALREADY converged (scenario 4's state), root's
# hash then goes empty (the deployed-router case the 92 script's own refusal
# comment describes), and a re-run of 999 must drop the board's listeners
# while LuCI keeps its pair and nothing else is torn.
echo
echo "== scenario 4b: converged box, root hash emptied — 999 re-refuses the board"
printf 'root::0:0:99999:7:::\n' > "$SHADOW_FILE"
run_999; rc999=$?
[ "$rc999" = 0 ] || bad "scenario 4b: 999 exited $rc999 (the refusal is a WARNING, not a failure)"
assert_fence "scenario 4b" refused board
assert_mapping "scenario 4b" board refused

echo
printf 'tests: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
