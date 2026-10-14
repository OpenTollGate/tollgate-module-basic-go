#!/usr/bin/env bash
# Offline tests for the PORTAL BANNER (nodogsplash.gatewayname) on BOTH setup
# paths of packaging/files/etc/uci-defaults/99-tollgate-setup.
#
# The defect this guards — found by the cold, execution-backed review of PR #605
# (verdict artifact: reports/reviews/tollgate-module-basic-go-t_eb753cb5-PR605-
# deepseek-flash.md, findings F1 and F2):
#
#   * F2 — the verify/repair path converged gatewayname on a SECOND value,
#     "$GATEWAY_NAME", while the full setup (setup_nodogsplash) writes
#     "${GATEWAY_NAME} Portal". Two writers, two spellings, and the banner flapped
#     with whichever path ran last. The comment next to the repair-path write
#     claimed the full setup "writes it from the same value" — it did not.
#   * F1 — that repair-path write was never committed on a SETTLED router: its
#     block runs BEFORE the `nds_before=$(uci export nodogsplash)` snapshot the
#     conditional commit is taken against, so a gatewayname-only change can never
#     be the reason that commit fires. The write stayed an uncommitted /tmp/.uci
#     session delta: /etc/config/nodogsplash kept the OLD banner, the convergence
#     was lost at the next reboot, and any LATER `uci commit nodogsplash` (the
#     stale gatewaydomainname delete, or an allow-list repair) re-applied the
#     stale delta on top of the file.
#
# Why the existing suites could not see either one: they use a flat-file fake
# `uci` whose `export` is a no-op (`show|export|commit|revert) : ;;`), so
# `[ -z "$nds_before" ]` is always true and the harness commits unconditionally.
# That is a fidelity gap, not a weak assertion — and mutation M8 of the review
# (strip " Portal" from the FULL path) left the whole suite at 71 passed / 0
# failed, i.e. nothing pinned the two writers to one value.
#
# So this suite drives the SHIPPED driver end to end — both branches, the real
# version-marker logic, the real writers — against a DELTA-AWARE fake `uci`: a
# COMMITTED state file plus a pending-delta file, with `commit` merging the one
# into the other and `export` showing the merged view, exactly the two-state
# model real uci has under /tmp/.uci. That makes "was it committed?" an
# observable fact instead of an assumption.
#
# Usage: bash tests/uci-defaults-gatewayname-banner_test.sh
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
SHIPPED="v0.6.0-alpha5"
OLDER="v0.6.0-alpha4"
WANT="TollGate-OQ3Q Portal"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# ---------------------------------------------------------------- fake uci
# Delta-aware stand-in for uci. Two files per sandbox:
#   <state>        the COMMITTED configuration (one "key=value" line per option;
#                  a section line is "pkg.sec=pkg", as the other suites seed it)
#   <state>.delta  the PENDING session delta: "<op><TAB><key><TAB><value>" lines
# `get`/`export` show committed+delta (what a live uci shows), `commit <pkg>`
# merges the package's delta into the state file and drops it from the delta —
# which is what makes an uncommitted `uci set` visible as a leftover delta.
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
delta="$state.delta"
eff="$state.eff"
[ -f "$state" ] || : > "$state"
[ -f "$delta" ] || : > "$delta"
TAB=$(printf '\t')

q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true

# effective () — print the values of one key: committed first, then the pending
# delta applied in order (set replaces, add_list appends, del_list removes,
# delete clears).  Prefix matching is exact (awk index), not a regex, because
# the keys carry '.', '@' and '[]'.
effective() {
    awk -v k="$1" 'index($0, k "=") == 1 { print substr($0, length(k) + 2) }' "$state" > "$eff"
    while IFS="$TAB" read -r op key val; do
        [ "$key" = "$1" ] || continue
        case "$op" in
            set)      printf '%s\n' "$val" > "$eff" ;;
            add_list) printf '%s\n' "$val" >> "$eff" ;;
            del_list) grep -v -F -x -- "$val" "$eff" > "$eff.2" 2>/dev/null; mv "$eff.2" "$eff" ;;
            delete)   : > "$eff" ;;
        esac
    done < "$delta"
    cat "$eff"
}

case "$cmd" in
    get)
        out="$(effective "${1:-}")"
        if [ -z "$out" ]; then
            [ "$q" = 1 ] || echo "uci: Entry not found" >&2
            exit 1
        fi
        printf '%s\n' "$out"
        ;;
    set)
        key="${1%%=*}"; val="${1#*=}"
        printf '%s\t%s\t%s\n' set "$key" "$val" >> "$delta"
        ;;
    add_list)
        key="${1%%=*}"; val="${1#*=}"
        printf '%s\t%s\t%s\n' add_list "$key" "$val" >> "$delta"
        ;;
    del_list)
        key="${1%%=*}"; val="${1#*=}"
        printf '%s\t%s\t%s\n' del_list "$key" "$val" >> "$delta"
        ;;
    delete)
        printf '%s\t%s\t%s\n' delete "${1:-}" "" >> "$delta"
        ;;
    add)
        printf '%s\t%s\t%s\n' set "${1:-}.${2:-${1:-}}" "${2:-${1:-}}" >> "$delta"
        ;;
    commit)
        pkg="${1:-}"
        : > "$delta.keep"
        while IFS="$TAB" read -r op key val; do
            keep=0
            if [ -z "$pkg" ]; then
                keep=1
            elif [ "$key" = "$pkg" ]; then
                keep=1
            else
                case "$key" in "$pkg".*) keep=1 ;; esac
            fi
            if [ "$keep" = 0 ]; then
                printf '%s\t%s\t%s\n' "$op" "$key" "$val" >> "$delta.keep"
                continue
            fi
            case "$op" in
                set)
                    grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
                    mv "$state.tmp" "$state"
                    printf '%s=%s\n' "$key" "$val" >> "$state"
                    ;;
                add_list)
                    printf '%s=%s\n' "$key" "$val" >> "$state"
                    ;;
                del_list)
                    grep -v -F -x -- "$key=$val" "$state" > "$state.tmp" 2>/dev/null
                    mv "$state.tmp" "$state"
                    ;;
                delete)
                    grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
                    mv "$state.tmp" "$state"
                    ;;
            esac
        done < "$delta"
        mv "$delta.keep" "$delta"
        ;;
    export|show)
        pkg="${1:-}"
        {
            awk -F= '{ print $1 }' "$state" 2>/dev/null
            cut -f2 "$delta" 2>/dev/null
        } | sort -u | while IFS= read -r k; do
            [ -n "$k" ] || continue
            if [ -n "$pkg" ]; then
                [ "$k" = "$pkg" ] || case "$k" in "$pkg".*) ;; *) continue ;; esac
            fi
            effective "$k" | while IFS= read -r v; do
                printf "%s='%s'\n" "$k" "$v"
            done
        done
        ;;
    revert|rename|reorder|move|batch) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"
export PATH="$TMP/bin:$PATH"

# ------------------------------------------- absolute paths into the sandbox
# Only the paths the driver reads/writes outside a config file are redirected in
# a copy of the script; the body — both branches, every writer — is shipped code.
export DEVICE_CODE_STORE="$TMP/tollgate.conf"
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd"
export ROUTER_HOME_DIR="$TMP/router-home"
printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
: > "$PASSWD_FILE"
: > "$TMP/kernel-hostname"
: > "$TMP/profile"

SRC="$TMP/99-tollgate-setup"
sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$TMP/tollgate-setup-done\"|" \
    -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$TMP/setup.log|" \
    -e "s|^SETUP_VERSION=\"__TOLLGATE_VERSION__\"\$|SETUP_VERSION=\"$SHIPPED\"|" \
    -e "s|^NDS_INIT=\"/etc/init.d/nodogsplash\"\$|NDS_INIT=\"$TMP/no-such-init\"|" \
    -e "s|^UHTTPD_INIT=\"/etc/init.d/uhttpd\"\$|UHTTPD_INIT=\"$TMP/no-such-init\"|" \
    -e "s|^TOLLGATE_CLI=\"/usr/bin/tollgate\"\$|TOLLGATE_CLI=\"$TMP/no-such-tollgate\"|" \
    -e "s|/etc/profile|$TMP/profile|g" \
    -e "s|/proc/sys/kernel/hostname|$TMP/kernel-hostname|" \
    -e "s|/etc/config/nodogsplash|$TMP/nodogsplash.config|g" \
    "$ROOT/$SCRIPT" > "$SRC"
if grep -q "SETUP_VERSION=\"$SHIPPED\"" "$SRC" && grep -q "SETUP_FLAG=\"$TMP/" "$SRC"; then
    ok "the sandbox redirects SETUP_FLAG / LOGFILE / SETUP_VERSION in its copy"
else
    bad "could not redirect the driver's absolute paths — the sandbox is not isolated"
fi

# ------------------------------------------------------------- fixtures
# The bench box as the review modelled it: deployed under a code, on the name the
# INSTALLER wrote (hostname), with a captive SSID a later deploy had re-minted and
# a banner carrying the pre-convergence name. No store: every already-deployed
# router is in exactly this state.
seed_deployed() { # <state-file> ; a complete allow list unless $2 is "drift"
    local extra
    : > "$1"
    {
        printf '%s\n' \
            'network.lan=interface' \
            'network.lan.ipaddr=192.168.1.1' \
            'system.@system[0]=system' \
            'system.@system[0].hostname=tollgate-OQ3Q' \
            'wireless.radio0=wifi-device' \
            'wireless.radio0.band=2g' \
            'wireless.tollgate_2g_open=wifi-iface' \
            'wireless.tollgate_2g_open.device=radio0' \
            'wireless.tollgate_2g_open.mode=ap' \
            'wireless.tollgate_2g_open.ssid=tollgate-0GLK' \
            'wireless.private_radio0=wifi-iface' \
            'wireless.private_radio0.device=radio0' \
            'wireless.private_radio0.mode=ap' \
            'wireless.private_radio0.ssid=c08r4d0r-AB12' \
            'wireless.private_radio0.key=Alpha-Bravo-Charlie-11' \
            'uhttpd.main=uhttpd' \
            'uhttpd.main.redirect_https=1' \
            'nodogsplash.@nodogsplash[0]=nodogsplash' \
            'nodogsplash.@nodogsplash[0].gatewayname=tollgate-0GLK Portal' \
            'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2121' \
            'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2050'
        # The :2051 journey rule is deliberately missing on the "drift" fixture:
        # it makes the allow-list block below take its commit path, which is how
        # the uncommitted banner delta used to get flushed with the WRONG value.
        [ "${2:-}" = "drift" ] || printf '%s\n' \
            'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2051'
    } >> "$1"
    : > "$1.delta"
}

state_opt() { # state_opt <state> <key>
    awk -v k="$2" 'index($0, k "=") == 1 { v = substr($0, length(k) + 2) } END { print v }' "$1"
}
pending_gw() { # pending_gw <state> — pending-delta ops touching gatewayname
    awk -F'\t' '$2 ~ /gatewayname/ { n++ } END { print n + 0 }' "$1.delta"
}
branch_of() { # branch_of <setup log>
    sed -n 's/.*Setup branch \([A-Z_]*\).*/\1/p' "$1" | tail -n1
}

run_case() { # run_case <case-name> <flag-version-or-empty> <drift|>
    local d="$TMP/$1" flag="$2" mode="${3:-}"
    mkdir -p "$d"
    seed_deployed "$d/uci.state" "$mode"
    if [ -n "$flag" ]; then
        printf '%s\n' "$flag" > "$TMP/tollgate-setup-done"
    else
        rm -f "$TMP/tollgate-setup-done"
    fi
    : > "$TMP/setup.log"
    UCI_STATE="$d/uci.state" DEVICE_CODE_STORE="$d/config-tollgate" \
        sh "$SRC" >"$d/run.out" 2>"$d/run.err"
    return $?
}

seed_deployed "$TMP/ctrl.state"
fixture_gw="$(state_opt "$TMP/ctrl.state" 'nodogsplash.@nodogsplash[0].gatewayname')"

# ------------------------------------------- the value the writers must share
echo "== ONE banner value: the full path and the repair path are pinned together"
# Both set-sites must carry the identical value expression — this is the
# assertion mutation M8 (strip " Portal" from the FULL path) has to fail, and it
# is checked statically as well as through the two live runs below.
gw_vals="$(grep -o 'nodogsplash\[0\]\.gatewayname="[^"]*"' "$ROOT/$SCRIPT" | sort -u)"
gw_count="$(printf '%s\n' "$gw_vals" | grep -c .)"
eq "exactly one gatewayname value across every writer in the script" "$gw_count" "1"
eq "  ...and it is the banner with its ' Portal' suffix" \
   "$gw_vals" 'nodogsplash[0].gatewayname="${GATEWAY_NAME} Portal"'
if grep -A2 -F 'uci -q set nodogsplash.@nodogsplash[0].gatewayname="${GATEWAY_NAME} Portal"' "$ROOT/$SCRIPT" \
     | grep -q 'uci commit nodogsplash'; then
    ok "the repair path COMMITS the gatewayname it writes (no /tmp/.uci delta)"
else
    bad "the repair path writes gatewayname without committing it in the same block"
fi

echo "== control: the fixture starts on a DIFFERENT banner, so the assertions have content"
eq "the seeded fixture banner is the pre-convergence one" "$fixture_gw" "tollgate-0GLK Portal"
[ "$fixture_gw" != "$WANT" ] && ok "the fixture banner differs from the value under test" \
                             || bad "the fixture already holds the expected value — the assertion is vacuous"

# ---------------------------------------------------------- FULL path
if run_case full "$OLDER"; then ok "full-setup run exits 0"; else bad "full-setup run exited $?"; fi
eq "the full path took the FULL branch" "$(branch_of "$TMP/setup.log")" "FULL"
full_gw="$(state_opt "$TMP/full/uci.state" 'nodogsplash.@nodogsplash[0].gatewayname')"
eq "the FULL path commits the banner '${WANT}' (mutation M8 fails here)" "$full_gw" "$WANT"
eq "the FULL path leaves no pending gatewayname delta" "$(pending_gw "$TMP/full/uci.state")" "0"

# ------------------------------------------------ REPAIR path, settled router
# The normal case: the router has already been set up under this version, so the
# allow list is complete and ONLY the banner needs to converge. This is the case
# the old code left as a delta.
if run_case repair "$SHIPPED"; then ok "reinstall (same version) run exits 0"; else bad "reinstall run exited $?"; fi
eq "the same-version reinstall took the VERIFY branch" "$(branch_of "$TMP/setup.log")" "VERIFY"
repair_gw="$(state_opt "$TMP/repair/uci.state" 'nodogsplash.@nodogsplash[0].gatewayname')"
eq "the repair path COMMITS the banner '${WANT}'" "$repair_gw" "$WANT"
eq "the repair path's banner is the SAME value the full path writes" "$repair_gw" "$full_gw"
eq "no gatewayname op is left pending in the /tmp/.uci-style delta" \
   "$(pending_gw "$TMP/repair/uci.state")" "0"
eq "the repair path's captive SSID converged on the same code" \
   "$(state_opt "$TMP/repair/uci.state" 'wireless.tollgate_2g_open.ssid')" "TollGate-OQ3Q"
eq "the operator's PSK is untouched" \
   "$(state_opt "$TMP/repair/uci.state" 'wireless.private_radio0.key')" "Alpha-Bravo-Charlie-11"

# ------------------------------------------ REPAIR path with allow-list drift
# With drift the allow-list block DOES commit nodogsplash — which is how the
# uncommitted banner delta described by the review got re-applied with the wrong
# value ('TollGate-OQ3Q'). The banner must be right in this case too.
if run_case repair-drift "$SHIPPED" drift; then ok "drifting reinstall run exits 0"; else bad "drifting run exited $?"; fi
drift_gw="$(state_opt "$TMP/repair-drift/uci.state" 'nodogsplash.@nodogsplash[0].gatewayname')"
eq "the drifting repair path commits the SAME banner" "$drift_gw" "$WANT"
eq "  ...and the drifted allow-list rule was repaired (the commit did fire)" \
   "$(awk -F= '$1 == "nodogsplash.@nodogsplash[0].users_to_router" { print $2 }' \
      "$TMP/repair-drift/uci.state" | grep -c '^allow tcp port 2051$')" "1"
eq "  ...with no gatewayname op left pending" "$(pending_gw "$TMP/repair-drift/uci.state")" "0"

printf '\n%s passed, %s failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
