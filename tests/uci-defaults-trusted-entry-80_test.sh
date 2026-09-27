#!/usr/bin/env bash
# Offline tests for the plain-HTTP entry point on :80 that
# 99-tollgate-setup's setup_uhttpd_trusted_entry() installs — no router, no
# uhttpd, no network needed.
#
# THE DEFECT (bench MT3000, 2026-09-25, pre17, operator-validated): nodogsplash
# only DNATs :80 to its own splash port for a PRE-AUTH client; for a TRUSTED
# client (mark 0x20000) or an AUTHENTICATED one (0x30000) its nat chain returns
# before the DNAT, and nothing listened on :80 — so `http://<router>/`, the URL
# a tester types and the URL a paying customer types to get back to the portal,
# answered nothing. The client fell through to `https://<router>/` and landed on
# LuCI (the other half of this pair; see
# tests/uci-defaults-luci-ports-not-pre-auth_test.sh and
# tests/packaging/luci-not-guest-reachable_test.sh).
#
# WHAT THIS PINS
#   1. exactly one uhttpd section listens on :80 — `uhttpd.trusted` — and it is
#      NOT uhttpd.main (LuCI's docroot is /www: putting :80 there would answer
#      the customer with the router's admin login, reopening the defect above);
#      uhttpd.main keeps :8080 and uhttpd.portal keeps :2051.
#   2. that section's docroot holds a redirect stub to the portal SPA on :2051,
#      with a <noscript> fallback carrying a real host (the stub cannot use JS's
#      location.hostname in the no-JS case), i.e. the same destination the
#      pre-auth stub in 90-tollgate-captive-portal-symlink uses.
#   3. both setup paths re-assert it (a fresh install AND a reinstall whose
#      version marker is unchanged — the same-version path is the one that
#      repairs a section an operator or a partial install lost).
#   4. re-running converges instead of duplicating: one listen entry per family,
#      and the stub is only rewritten when its content differs.
#
# The harness reuses the repo's existing offline pattern: the shipped script is
# copied into a temp dir (absolute paths into the live system are redirected)
# and run with `/bin/sh` against a fake `uci` and a fake `apk`.
#
# Usage: bash tests/uci-defaults-trusted-entry-80_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

FAKE_VERSION="0.6.0_alpha4-r0"
export FAKE_APK_VERSION="$FAKE_VERSION"
cat > "$TMP/bin/apk" <<'SHIM'
#!/bin/sh
# fake apk — only `apk list --installed` is consulted by the setup script.
if [ "${1:-}" = "list" ]; then
    printf '%s\n' "tollgate-wrt-${FAKE_APK_VERSION} aarch64_cortex-a53 {tollgate-wrt} (GPL-3.0-only) [installed]"
fi
exit 0
SHIM
chmod +x "$TMP/bin/apk"

export UCI_STATE="$TMP/uci.state"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
# fake uci — one "key=value" line per option, list entries one per line.
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"; shift || true
values() { grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-; }
case "$cmd" in
    get)
        vals="$(values "$1")"
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
    add_list)
        printf '%s\n' "$1" >> "$state"
        ;;
    del_list)
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    add)
        printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state"
        ;;
    delete)
        grep -v -F -- "$1=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    show)
        [ "${1:-}" = "wireless" ] || exit 0
        while IFS= read -r line; do
            case "${line%%=*}" in
                wireless.*.*) printf "%s='%s'\n" "${line%%=*}" "${line#*=}" ;;
                wireless.*)   printf '%s\n' "$line" ;;
            esac
        done < "$state"
        ;;
    export|commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"
export PATH="$TMP/bin:$PATH"

# A stand-in for hexdump, which generates the SSID suffix and the private key:
# not present in every test environment, and the full-setup path (which this
# test also drives) needs it to get past setup_public_wifi.
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

FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
export LOGFILE
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
    -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
    -e "s|> */proc/sys/kernel/hostname|> $TMP/kernel-hostname|" \
    -e "s|/etc/profile|$TMP/profile|g" \
    "$ROOT/$SCRIPT" > "$SCRIPT_UNDER_TEST"
if grep -q "^SETUP_FLAG=\"$FLAG\"\$" "$SCRIPT_UNDER_TEST"; then
    ok "test harness redirected SETUP_FLAG in the copied script"
else
    bad "could not redirect SETUP_FLAG in the copied setup script"
fi
# The two host-touching writes are redirected the way the repo's older harness
# does it (rewrite the absolute path in the copy), and the rewrite is ASSERTED:
# an anchor that stops matching because the shipped line changed shape would
# otherwise silently leave this test writing the LIVE hostname and /etc/profile
# of whatever machine runs it. That is not hypothetical — this file exists
# because a run of the suite created /etc/tollgate/router-home on the host.
if grep -qF "> $TMP/kernel-hostname" "$SCRIPT_UNDER_TEST" &&
   grep -qF "$TMP/profile" "$SCRIPT_UNDER_TEST"; then
    ok "test harness redirected the kernel hostname and the profile hook in the copied script"
else
    bad "could not redirect /proc/sys/kernel/hostname and /etc/profile in the copied script — a run of this test would write the LIVE host"
fi

# Every absolute path the driver touches during a full run is pinned into the
# sandbox: the credential fixtures (the gate would otherwise read the live
# /etc/shadow and call `passwd root`) and the :80 docroot, which this change
# introduces — an un-pinned run writes into the LIVE
# /etc/tollgate/router-home of whatever host runs the suite.
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd.db"
printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
: > "$PASSWD_FILE"
export ROUTER_HOME_DIR="$TMP/router-home"

seed_stock() { # a stock-router seed: LAN address, radio bands, guest/private APs
    : > "$UCI_STATE"
    {
        printf '%s\n' 'network.lan=interface' 'network.lan.ipaddr=192.168.1.1' \
                      'network.lan.netmask=255.255.255.0'
        printf '%s\n' 'system.@system[0]=system' 'system.@system[0].hostname=OpenWrt'
        printf '%s\n' 'wireless.radio0=wifi-device' 'wireless.radio0.band=2g' \
                      'wireless.radio1=wifi-device' 'wireless.radio1.band=5g'
        printf '%s\n' 'wireless.default_radio0=wifi-iface' 'wireless.default_radio0.device=radio0' \
                      'wireless.default_radio0.mode=ap' 'wireless.default_radio0.ssid=OpenWrt'
        printf '%s\n' 'wireless.default_radio1=wifi-iface' 'wireless.default_radio1.device=radio1' \
                      'wireless.default_radio1.mode=ap' 'wireless.default_radio1.ssid=OpenWrt'
    } >> "$UCI_STATE"
}

run_driver() { # run_driver <marker-content|__ABSENT__>
    local marker="$1"
    if [ "$marker" = "__ABSENT__" ]; then rm -f "$FLAG"; else printf '%s\n' "$marker" > "$FLAG"; fi
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >/dev/null 2>"$TMP/run.err"
    return $?
}

# ------------------------------------------------------------- state readers
listen_lines()  { grep -F -- "uhttpd.$1.listen_http=" "$UCI_STATE" 2>/dev/null | cut -d= -f2-; }
# Whole-line match: `grep -F` alone would let "…:8080" satisfy a check for
# "…:80", i.e. the exact confusion this test exists to catch.
listen_count()  { grep -F -x -c -- "uhttpd.$1.listen_http=$2" "$UCI_STATE" 2>/dev/null | tr -d ' '; }
section_opt()   { grep -F -- "uhttpd.$1.$2=" "$UCI_STATE" 2>/dev/null | head -n1 | cut -d= -f2-; }
any_80()        { grep -F -- "listen_http=" "$UCI_STATE" 2>/dev/null | grep -c -E "listen_http=.*:(80|80)$" | tr -d ' '; }
STUB="$ROUTER_HOME_DIR/index.html"

assert_80_contract() { # <label>
    local label="$1" port n
    n=$(listen_count trusted '0.0.0.0:80')
    [ "$n" = 1 ] && ok "$label: uhttpd.trusted listens on 0.0.0.0:80 (exactly one entry)" \
                 || bad "$label: uhttpd.trusted has $n '0.0.0.0:80' listen entries (want 1)"
    n=$(listen_count trusted '[::]:80')
    [ "$n" = 1 ] && ok "$label: uhttpd.trusted listens on [::]:80 (exactly one entry)" \
                 || bad "$label: uhttpd.trusted has $n '[::]:80' listen entries (want 1)"
    [ "$(section_opt trusted home)" = "$ROUTER_HOME_DIR" ] \
        && ok "$label: uhttpd.trusted home is the package docroot ($ROUTER_HOME_DIR)" \
        || bad "$label: uhttpd.trusted home is '$(section_opt trusted home)' (want $ROUTER_HOME_DIR)"
    [ "$(section_opt trusted no_dirlists)" = 1 ] \
        && ok "$label: uhttpd.trusted has no_dirlists=1" \
        || bad "$label: uhttpd.trusted no_dirlists is '$(section_opt trusted no_dirlists)' (want 1)"
    [ "$(section_opt trusted rfc1918_filter)" = 0 ] \
        && ok "$label: uhttpd.trusted serves RFC1918 clients (rfc1918_filter=0)" \
        || bad "$label: uhttpd.trusted rfc1918_filter is '$(section_opt trusted rfc1918_filter)' (want 0)"

    # The trap: :80 must belong to that section and to no other. LuCI's
    # uhttpd.main has home=/www, so a :80 listen entry moved onto uhttpd.main
    # would answer the customer with the router's admin login — the defect this
    # pair of changes exists to close.
    for other in main portal; do
        n=$(listen_count "$other" '0.0.0.0:80')
        [ "$n" = 0 ] && ok "$label: uhttpd.$other does NOT listen on :80" \
                     || bad "$label: uhttpd.$other listens on :80 — the admin docroot would answer the customer's plain-HTTP URL"
    done
    for port in 2051; do
        n=$(listen_count portal "0.0.0.0:$port")
        [ "$n" = 1 ] && ok "$label: uhttpd.portal still listens on :$port" \
                     || bad "$label: uhttpd.portal lost its :$port listener ($n entries)"
    done
    n=$(listen_count main '0.0.0.0:8080')
    [ "$n" = 1 ] && ok "$label: uhttpd.main still listens on :8080 (LuCI stays configured)" \
                 || bad "$label: uhttpd.main lost its :8080 listener ($n entries)"
}

assert_stub_document() { # <label>
    local label="$1"
    if [ ! -f "$STUB" ]; then
        bad "$label: no stub document at $STUB — http://<router>/ has nothing to serve"
        return
    fi
    grep -q 'location\.replace' "$STUB" \
        && ok "$label: the :80 document redirects (location.replace present)" \
        || bad "$label: the :80 document has no location.replace — a browser would show a dead page"
    grep -q ':2051/splash\.html' "$STUB" \
        && ok "$label: the :80 document targets the portal SPA on :2051/splash.html" \
        || bad "$label: the :80 document does not target :2051/splash.html"
    grep -q 'noscript' "$STUB" \
        && ok "$label: the :80 document keeps a <noscript> fallback" \
        || bad "$label: the :80 document has no <noscript> fallback (a client without JS is stranded)"
    grep -q 'http://192\.168\.1\.1:2051/splash\.html' "$STUB" \
        && ok "$label: the <noscript> fallback carries the installed LAN address" \
        || bad "$label: the <noscript> fallback does not carry the LAN address (got '$(grep -o 'http://[^"]*' "$STUB" | tail -n1)')"
}

# ------------------------------------------------------------------ full setup
echo "== first boot (full setup) installs the :80 entry point"
seed_stock
run_driver __ABSENT__
rc=$?
[ "$rc" = 0 ] && ok "full setup exits 0" \
              || bad "full setup exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
assert_80_contract "full setup"
assert_stub_document "full setup"

echo "== re-running the installer converges instead of duplicating"
stub_before="$(cat "$STUB" 2>/dev/null)"
run_driver "$FAKE_VERSION"
rc=$?
[ "$rc" = 0 ] && ok "reinstall exits 0" \
              || bad "reinstall exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
assert_80_contract "reinstall"
if [ -n "$stub_before" ] && [ "$(cat "$STUB" 2>/dev/null)" = "$stub_before" ]; then
    ok "reinstall: the stub document was left byte-identical (no needless rewrite)"
else
    bad "reinstall: the stub document changed"
fi
if grep -q 'portal redirect stub already present' "$LOGFILE"; then
    ok "reinstall: the log reports the stub as already present"
else
    bad "reinstall: the log does not report an unchanged stub ($(grep -i 'stub' "$LOGFILE" | head -n2 | tr '\n' ' '))"
fi

echo "== the same-version path repairs a missing section and a deleted stub"
# The reinstall case that matters: an operator (or a partial install) lost the
# uhttpd.trusted section, or the docroot document. The verify/repair path must
# put both back — it is the path every reinstall of an unchanged version takes.
grep -v -F 'uhttpd.trusted.' "$UCI_STATE" > "$UCI_STATE.tmp" && mv "$UCI_STATE.tmp" "$UCI_STATE"
rm -f "$STUB"
run_driver "$FAKE_VERSION"
assert_80_contract "same-version repair"
assert_stub_document "same-version repair"
if grep -q "Setup branch VERIFY" "$LOGFILE"; then
    ok "same-version repair: the verify/repair branch was the path taken"
else
    bad "same-version repair: the driver did not take the verify/repair branch ($(head -n2 "$LOGFILE" | tr '\n' ' '))"
fi

echo "== the section is not a second writer of uhttpd.main or the portal"
# setup_uhttpd_trusted_entry must not touch the other instances' listeners:
# a repair that added :80 to uhttpd.main is exactly the "LuCI on the customer's
# URL" regression, and one that added it to uhttpd.portal would serve the SPA
# from a second origin.
after_before_main="$(listen_lines main)"
after_before_portal="$(listen_lines portal)"
run_driver "$FAKE_VERSION"
[ "$(listen_lines main)" = "$after_before_main" ] \
    && ok "uhttpd.main's listen list is unchanged by the :80 repair" \
    || bad "uhttpd.main's listen list changed: [$(listen_lines main)]"
[ "$(listen_lines portal)" = "$after_before_portal" ] \
    && ok "uhttpd.portal's listen list is unchanged by the :80 repair" \
    || bad "uhttpd.portal's listen list changed: [$(listen_lines portal)]"

# ------------------------------------------------------- static consistency
echo "== the :80 stub and the pre-auth stub point at the same portal origin"
NDS_STUB="$ROOT/packaging/files/etc/uci-defaults/90-tollgate-captive-portal-symlink"
if grep -q '2051/splash.html' "$NDS_STUB" && grep -q ':2051/splash\.html' "$SCRIPT"; then
    ok "both the pre-auth stub and the :80 stub target :2051/splash.html"
else
    bad "the pre-auth stub ($NDS_STUB) and the :80 stub do not agree on the portal origin"
fi
if grep -q "setup_uhttpd_trusted_entry" "$SCRIPT"; then
    ok "99-tollgate-setup defines/calls setup_uhttpd_trusted_entry"
else
    bad "setup_uhttpd_trusted_entry is gone from 99-tollgate-setup"
fi
calls=$(grep -c '^setup_uhttpd_trusted_entry$\|^    setup_uhttpd_trusted_entry$' "$SCRIPT")
[ "$calls" -ge 2 ] && ok "both setup paths call setup_uhttpd_trusted_entry ($calls call sites)" \
                   || bad "only $calls call site(s) of setup_uhttpd_trusted_entry — one setup path would skip the :80 entry point"

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
