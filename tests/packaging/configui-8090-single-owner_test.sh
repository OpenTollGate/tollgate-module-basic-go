#!/usr/bin/env bash
# Offline test for the :8090 OWNERSHIP contract of the shipped setup script.
#
# The contract (docs/architecture/default-ui-and-entry-port-decision.md, D4):
#
#   * exactly ONE uhttpd section listens on :8090, and it is the portal-staged
#     admin board — uhttpd.admin, home=/www/tollgate, written by
#     999-tollgate-admin-setup (staged by packaging/portal-build.sh from the
#     pinned portal tree, installed by packaging/Makefile). A second section
#     claiming :8090 is a bind fight in which one of the two admin UIs
#     disappears;
#   * LuCI is ALONE on :8080 — uhttpd.main, home=/www — and nothing else
#     listens there;
#   * this package writes NO :8090 listener of its own. It used to: a legacy
#     whitelabel configUI writer created a DEDICATED second `uhttpd.<brand>`
#     section on :8090 (home=/www/<brand>) whenever a brand file and a branded
#     docroot were present. That writer is gone, and a router upgrading from a
#     build that carried it must CONVERGE: the stale section is DELETED (not
#     merely port-stripped, which the portal-staged 92 already does), so a
#     reinstall-over-legacy ends with exactly one :8090 owner.
#
# Why the deletion has to live here: 92 strips :8090/:8443 from every other
# section but leaves the SECTION standing, so a router that once ran the legacy
# writer keeps a stale branded uhttpd instance whose docroot is a webroot this
# build does not serve. Only the module's own script runs on every install,
# upgrade and same-version re-run, so only it can converge that state.
#
# Offline: no router, no SDK, no network. The shipped driver runs against a fake
# uci/apk/passwd and a sandboxed /etc, on BOTH setup paths — the same seam
# tests/packaging/admin-board-requires-credential_test.sh and
# tests/uci-defaults-trusted-entry-80_test.sh use.
#
# The legacy section name is never spelled out in this repo (see
# tests/packaging/rebrand-literal-gutter_test.sh): the fixture builds it from
# two halves, and the contract is asserted on the SHAPE (a non-owner section
# claiming :8090 / a foreign section left behind), never on the literal.
#
# Usage: bash tests/packaging/configui-8090-single-owner_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# --------------------------------------------------------------- fake apk
# The shipped script carries the __TOLLGATE_VERSION__ placeholder, so a source
# run resolves the setup version from the package manager. The marker scenarios
# below are keyed off this value.
FAKE_VERSION="0.6.0_alpha4-r0"
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
# section. `delete <section>` must drop the section AND its options — a shim
# that only dropped the "<section>=uhttpd" line would make the deletion
# assertions below pass against a script that deletes nothing (the vacuous-shim
# trap).
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
            # An anonymous section reference (`<cfg>.@<type>[n]`, e.g.
            # nodogsplash.@nodogsplash[0]) has no line of its own: answer from
            # its options, or `uci -q get ... || uci add ...` guards re-create
            # the section on every run and the idempotence check below reports a
            # writer that is in fact converged.
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
        grep -v -F -e "$1=" -e "$1." "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    show|export)
        cat "$state"
        ;;
    commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"

# ------------------------------------------------------------ fake passwd
# BusyBox `passwd root` stand-in: reads the new password twice from stdin and
# writes a hash. The admin credential gate is not what this test is about, but
# it runs on both setup paths and it DROPS the :8090 listeners when no
# credential can be established — which would make every ownership assertion
# below fail for the wrong reason.
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
# Deterministic stand-in for hexdump (not present in every environment), which
# the device-code mint and the private-key generator both use. Without it the
# full-setup path cannot get past setup_public_wifi.
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

export ORIGINAL_PATH="$PATH"
export PATH="$TMP/bin:$PATH"

# --------------------------------------------------- the script under test
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
export LOGFILE
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
    -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
    -e "s|> */proc/sys/kernel/hostname|> $TMP/kernel-hostname|" \
    -e "s|/etc/profile|$TMP/profile|g" \
    -e "s|/etc/tollgate/brand|$TMP/etc/tollgate/brand|g" \
    "$ROOT/$SCRIPT" > "$SCRIPT_UNDER_TEST"
if grep -q "^SETUP_FLAG=\"$FLAG\"\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^LOGFILE=$LOGFILE\$" "$SCRIPT_UNDER_TEST"; then
    ok "test harness redirected SETUP_FLAG and LOGFILE in the copied script"
else
    bad "could not redirect SETUP_FLAG/LOGFILE in the copied setup script"
fi
# The brand FILE is redirected too, and asserted: "no brand file" below must
# mean exactly that, whatever the machine running the suite carries in its own
# /etc/tollgate/brand.
if grep -qF "$TMP/etc/tollgate/brand" "$SCRIPT_UNDER_TEST"; then
    ok "test harness redirected the brand file in the copied script"
else
    bad "could not redirect /etc/tollgate/brand in the copied script"
fi
# The two host-touching writes of the FULL path are redirected in the copy and
# the rewrite is ASSERTED: an anchor that stopped matching would silently leave
# this test writing the LIVE hostname and /etc/profile of the machine that runs
# it.
if grep -qF "> $TMP/kernel-hostname" "$SCRIPT_UNDER_TEST" &&
   grep -qF "$TMP/profile" "$SCRIPT_UNDER_TEST"; then
    ok "test harness redirected the kernel hostname and the profile hook in the copied script"
else
    bad "could not redirect /proc/sys/kernel/hostname and /etc/profile in the copied script — a run of this test would write the LIVE host"
fi

# Every other absolute path the driver touches is pinned into the sandbox by
# env: the credential fixtures, the :80 docroot and the device-code store.
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd.db"
export ROUTER_HOME_DIR="$TMP/router-home"
export DEVICE_CODE_STORE="$TMP/config/tollgate"
BRAND_FILE="$TMP/etc/tollgate/brand"
mkdir -p "$TMP/config" "$TMP/etc/tollgate"
# A credential EXISTS: the gate then leaves the board standing (its own
# fail-closed behaviour is pinned by tests/packaging/admin-board-requires-credential_test.sh).
printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
: > "$PASSWD_FILE"
rm -f "$BRAND_FILE"

# The legacy second writer's section name, assembled so this file never carries
# the literal the gutter test forbids.
LEGACY="net4"; LEGACY="${LEGACY}sats"
OWNER_DOCROOT='/www/tollgate'

# ------------------------------------------------------------- state fixtures
state()      { printf '%s\n' "$@" >> "$UCI_STATE"; }
section_opt() { grep -F -- "uhttpd.$1.$2=" "$UCI_STATE" 2>/dev/null | head -n1 | cut -d= -f2-; }
listen_count() { grep -F -x -c -- "uhttpd.$1.listen_http=$2" "$UCI_STATE" 2>/dev/null | tr -d ' '; }
section_lines() { grep -E -c -- "^uhttpd\.$1(\.|=)" "$UCI_STATE" 2>/dev/null || true; }

# The :8090 owner, exactly as the portal-staged 92 writes it (ADMIN_HOME default
# /www/tollgate; :8443 present when a cert/key pair exists).
seed_owner() {
    state 'uhttpd.admin=uhttpd' \
          'uhttpd.admin.listen_http=0.0.0.0:8090' \
          'uhttpd.admin.listen_http=[::]:8090' \
          'uhttpd.admin.listen_https=0.0.0.0:8443' \
          'uhttpd.admin.listen_https=[::]:8443' \
          'uhttpd.admin.home=/www/tollgate' \
          'uhttpd.admin.ubus_prefix=/ubus' \
          'uhttpd.admin.error_page=/index.html'
}

# LuCI's instance, mid-life: :8080 on both families.
seed_luci() {
    state 'uhttpd.main=uhttpd' \
          'uhttpd.main.listen_http=0.0.0.0:8080' \
          'uhttpd.main.listen_http=[::]:8080' \
          'uhttpd.main.home=/www'
}

# The legacy writer's section. `stripped` is the state a previous build's own
# fail-closed gate left behind: the section survives with NO :8090 listener, so
# a purge that only looked at the port claim would never converge it.
seed_legacy() { # seed_legacy <stripped|live>
    state "uhttpd.$LEGACY=uhttpd" "uhttpd.$LEGACY.home=/www/$LEGACY"
    if [ "$1" = live ]; then
        state "uhttpd.$LEGACY.listen_http=0.0.0.0:8090" \
              "uhttpd.$LEGACY.listen_http=[::]:8090"
    fi
}

# A stock-ish box minus uhttpd: LAN, radios, a system section, so the FULL path
# has somewhere to write.
seed_host() {
    state 'network.lan=interface' 'network.lan.ipaddr=192.168.1.1' 'network.lan.netmask=255.255.255.0' \
          'system.@system[0]=system' 'system.@system[0].hostname=OpenWrt' \
          'wireless.radio0=wifi-device' 'wireless.radio0.band=2g' \
          'wireless.radio1=wifi-device' 'wireless.radio1.band=5g' \
          'wireless.default_radio0=wifi-iface' 'wireless.default_radio0.device=radio0' \
          'wireless.default_radio0.mode=ap' 'wireless.default_radio0.ssid=OpenWrt' \
          'wireless.default_radio1=wifi-iface' 'wireless.default_radio1.device=radio1' \
          'wireless.default_radio1.mode=ap' 'wireless.default_radio1.ssid=OpenWrt'
}

# ----------------------------------------------------- the contract, as predicates
# Sections claiming :8090 — on http or https, ipv4 or ipv6. Sorted, one per line.
claimants_8090() {
    grep -E -- '^uhttpd\.[^.]+\.listen_https?=.*:8090$' "$UCI_STATE" 2>/dev/null |
        sed -e 's/^uhttpd\.//' -e 's/\..*//' | sort -u
}
claimant_count() { claimants_8090 | grep -c . 2>/dev/null || true; }
sole_claimant()  { claimants_8090 | head -n1; }

# Sections claiming :8080 (LuCI's port).
claimants_8080() {
    grep -E -- '^uhttpd\.[^.]+\.listen_https?=.*:8080$' "$UCI_STATE" 2>/dev/null |
        sed -e 's/^uhttpd\.//' -e 's/\..*//' | sort -u
}

sole_8090_owner_ok() {
    [ "$(claimant_count)" = 1 ] && [ "$(sole_claimant)" = admin ]
}
luci_alone_8080_ok() {
    [ "$(claimants_8080 | grep -c .)" = 1 ] && [ "$(claimants_8080 | head -n1)" = main ]
}
legacy_section_gone_ok() { [ "$(section_lines "$LEGACY")" = 0 ]; }

assert_contract() { # assert_contract <label>
    local label="$1" home
    if sole_8090_owner_ok; then
        ok "$label: exactly one section claims :8090 and it is uhttpd.admin"
    else
        bad "$label: :8090 claimants are '$(claimants_8090 | tr '\n' ' ')'(want only admin)"
    fi
    home="$(section_opt admin home)"
    [ "$home" = "$OWNER_DOCROOT" ] \
        && ok "$label: the :8090 owner's home is $OWNER_DOCROOT" \
        || bad "$label: the :8090 owner's home is '${home:-<unset>}' (want $OWNER_DOCROOT)"
    [ "$(listen_count admin '0.0.0.0:8090')" = 1 ] && [ "$(listen_count admin '[::]:8090')" = 1 ] \
        && ok "$label: the owner listens on :8090 on both families, once each" \
        || bad "$label: the owner's :8090 listeners are $(listen_count admin '0.0.0.0:8090')/$(listen_count admin '[::]:8090') (want 1/1)"
    if legacy_section_gone_ok; then
        ok "$label: no foreign uhttpd section was left behind"
    else
        bad "$label: a foreign uhttpd section survived ($(section_lines "$LEGACY") config lines)"
    fi
    if luci_alone_8080_ok; then
        ok "$label: LuCI is alone on :8080"
    else
        bad "$label: :8080 claimants are '$(claimants_8080 | tr '\n' ' ')(want only main)"
    fi
    [ "$(section_opt main home)" = /www ] \
        && ok "$label: LuCI's home is still /www" \
        || bad "$label: LuCI's home is '$(section_opt main home)' (want /www)"
}

# ------------------------------------------- controls on the detectors themselves
echo "== negative controls: the detectors must be able to see the defect"
: > "$UCI_STATE"
seed_host
seed_luci
seed_owner
if sole_8090_owner_ok; then
    ok "positive control: the seeded single-owner state satisfies the :8090 predicate"
else
    bad "positive control: the seeded single-owner state fails the :8090 predicate (claimants: $(claimants_8090 | tr '\n' ' '))"
fi
state "uhttpd.$LEGACY=uhttpd" "uhttpd.$LEGACY.listen_http=0.0.0.0:8090"
if sole_8090_owner_ok; then
    bad "negative control: a second section claiming :8090 still satisfies the :8090 predicate — the assertion carries no information"
else
    ok "negative control: a second section claiming :8090 is rejected (claimants: $(claimants_8090 | tr '\n' ' '))"
fi
if legacy_section_gone_ok; then
    bad "negative control: a seeded foreign section reads as absent — the deletion assertion would pass vacuously"
else
    ok "negative control: a seeded foreign section is detected ($(section_lines "$LEGACY") config lines)"
fi
: > "$UCI_STATE"
seed_host
seed_luci
seed_owner
state 'uhttpd.portal=uhttpd' 'uhttpd.portal.listen_http=0.0.0.0:8080'
if luci_alone_8080_ok; then
    bad "negative control: a second section claiming :8080 still satisfies the LuCI predicate"
else
    ok "negative control: a second section claiming :8080 is rejected (claimants: $(claimants_8080 | tr '\n' ' '))"
fi

# ------------------------------------------------------------------ the driver
run_driver() { # run_driver <marker-content|__ABSENT__>
    local marker="$1"
    if [ "$marker" = "__ABSENT__" ]; then rm -f "$FLAG"; else printf '%s\n' "$marker" > "$FLAG"; fi
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" > "$TMP/run.out" 2> "$TMP/run.err"
    return $?
}
branch_is() { grep -qE "Setup branch $1([[:space:]]|$)" "$LOGFILE" 2>/dev/null; }

check_branch() { # check_branch <label> <expected-verdict>
    if branch_is "$2"; then
        ok "$1: the driver took the $2 path"
    else
        bad "$1: the driver did not take the $2 path (log: $(grep -m1 'Setup branch' "$LOGFILE" 2>/dev/null))"
    fi
}

# ------------------------------------------------------ 1. fresh package install
# No marker: the first boot / first install. 92 has already run (the install
# and the boot path both apply 90, 92, 99 in numeric order), so the board exists
# before 99 is called; the module must leave it as the single owner.
echo
echo "== scenario 1: fresh install (no marker, no brand file)"
: > "$UCI_STATE"
seed_host
seed_owner
rm -f "$BRAND_FILE"
run_driver __ABSENT__
check_branch "fresh install" FULL
assert_contract "fresh install"

# --------------------------------------------------------- 2. upgrade over a marker
# The upgrade case: an older marker forces the FULL path, and the box carries the
# legacy second writer (live), a branded docroot, a stray :8090 on uhttpd.main
# from an old repair, and LuCI.
echo
echo "== scenario 2: upgrade from a marker carrying the legacy writer"
: > "$UCI_STATE"
seed_host
seed_luci
seed_owner
seed_legacy live
state 'uhttpd.main.listen_http=0.0.0.0:8090' 'uhttpd.main.listen_http=[::]:8090'
run_driver '0.6.0_alpha3-r0'
check_branch "upgrade" FULL
assert_contract "upgrade"
if grep -q -F -x -- 'uhttpd.main.listen_http=0.0.0.0:8090' "$UCI_STATE"; then
    bad "upgrade: a stray :8090 on uhttpd.main survived (it serves LuCI's docroot on the board's port)"
else
    ok "upgrade: the stray :8090 on uhttpd.main was stripped"
fi

# ------------------------------------------------- 3. reinstall at the same version
# The marker equals the shipped version, so the driver takes the verify/repair
# path — the one every reinstall takes. It must converge the same way.
echo
echo "== scenario 3: reinstall-over-marker (verify/repair path)"
: > "$UCI_STATE"
seed_host
seed_luci
seed_owner
seed_legacy live
run_driver "$FAKE_VERSION"
check_branch "reinstall" VERIFY
assert_contract "reinstall"
# Idempotence: the repair path run twice must leave the uhttpd contract
# unchanged the second time. Scoped to the uhttpd config — the config this test
# is about — and compared as a SET of lines (sorted), because the fake uci
# appends a re-set option at the end of its flat file rather than updating it in
# place.
uhttpd_lines() { grep -E '^uhttpd\.' "$1" 2>/dev/null | sort; }
cp "$UCI_STATE" "$TMP/state.after-first"
run_driver "$FAKE_VERSION"
if diff <(uhttpd_lines "$TMP/state.after-first") <(uhttpd_lines "$UCI_STATE") > "$TMP/idempotence.diff" 2>&1; then
    ok "reinstall: the second run leaves the uhttpd config unchanged (idempotent)"
else
    bad "reinstall: the second run changed the uhttpd config — $(head -n 6 "$TMP/idempotence.diff" | tr '\n' ' ')"
fi

# ------------------------------------------- 4. legacy section left behind, stripped
# A box whose legacy section had its :8090 listeners removed by an earlier
# build's fail-closed gate: nothing claims the port, but the stale branded
# uhttpd instance is still there. "Deleted, not just port-stripped" is the
# requirement, and this is the state that proves it.
echo
echo "== scenario 4: legacy section left behind with no :8090 listener"
: > "$UCI_STATE"
seed_host
seed_luci
seed_owner
seed_legacy stripped
if legacy_section_gone_ok; then
    bad "scenario 4 setup: the stripped legacy section was not seeded"
else
    ok "scenario 4 setup: the stripped legacy section is present with no :8090 claim"
fi
if [ "$(claimant_count)" = 1 ]; then
    ok "scenario 4 setup: nothing else claims :8090, so only the section deletion can converge this box"
else
    bad "scenario 4 setup: the seed already has $(claimant_count) :8090 claimants"
fi
run_driver "$FAKE_VERSION"
check_branch "legacy-left-behind" VERIFY
assert_contract "legacy-left-behind"

# ---------------------------------------------- the module is not a :8090 writer
# The static half: no writer in the shipped script may ADD a :8090 listener
# (removing one, or deleting a foreign section, is the contract).
echo
echo "== the shipped script adds no :8090 listener of its own"
added="$(grep -nE 'add_list[[:space:]]+uhttpd\.[^.]+\.listen_(http|https)=.*:8090' "$ROOT/$SCRIPT" || true)"
if [ -z "$added" ]; then
    ok "no add_list of a :8090 listener anywhere in $SCRIPT"
else
    bad "the shipped script still ADDS a :8090 listener: $(printf '%s' "$added" | head -n 2 | tr '\n' ' ')"
fi

echo
printf 'tests: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
