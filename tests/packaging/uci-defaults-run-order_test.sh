#!/usr/bin/env bash
# Offline test for the ORDER the uci-defaults run in on the INSTALL path, and
# for the ORDER the BOOT path applies them in.
#
# THE DEFECT THIS PINS
#
# The admin board's setup script is FAIL CLOSED: while root's /etc/shadow hash is
# empty it DELETES uhttpd.admin's listeners, on the stated principle that the
# board must be UNREACHABLE, not merely unhelpful. The credential that makes the
# board authenticatable is CREATED by 99-tollgate-setup. Numeric order
# (90, 92, 99) therefore ran the gate BEFORE the credential existed: on a fresh
# hand-install the gate killed the admin board and nothing restored it. The field
# log is the gate's "admin board ... is NOT being served ... root has no usable
# password (state: empty)" immediately followed by 99's "generated one for the
# :8090 admin board".
#
# The gate must run LAST, so the credential exists before the gate looks for it.
# The feed's recipe already ran it last (90, 99, 92); the module's ran numeric
# (90, 92, 99). This test pins the unified, gate-last contract.
#
# WHAT IT PINS
#
#   1. every in-repo recipe runs the uci-defaults in ASCENDING numeric order, so
#      an install converges to the state the next boot produces (the boot path
#      applies /etc/uci-defaults/* in one collated glob);
#   2. the CREDENTIAL script (99-tollgate-setup) runs BEFORE the admin GATE
#      (*-tollgate-admin-setup), and the gate runs LAST;
#   3. the gate's INSTALLED FILENAME sorts AFTER the credential script's, so the
#      boot path is gate-last too - reordering the recipe alone is not the fix,
#      because a boot does not read the recipe;
#   4. the file the recipes run is the file the packaging stages (portal-build.sh)
#      and installs (packaging/Makefile), so the run order and the shipped file
#      cannot drift apart.
#
# The historical instance, for the record: the module's postinst used to run the
# same scripts as 90, 99, 92, so the LAST writer of uhttpd.main differed between
# the install pass and the boot pass. #593 moved it onto the boot order
# (90, 92, 99) to converge the two - which is what put the fail-closed gate in
# front of the credential and produced this defect. Converging on the boot order
# is right; the boot order must be gate-last, which is why the fix is a rename
# (the numeric prefix IS the boot order) rather than only a recipe edit.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

CREDENTIAL="99-tollgate-setup"
GATE_GLOB='*-tollgate-admin-setup'

# Every in-repo recipe that runs the uci-defaults itself, on install.
RECIPES=(
    "packaging/Makefile"
    "packaging/postinst"
)

# The script list of the first `for script in ... ; do` loop that mentions
# uci-defaults, in the order the loop runs them.
script_order() { # script_order <file>
    awk '/for script in .*uci-defaults/ { f = 1 } f { print } f && /;[[:space:]]*do/ { exit }' "$1" 2>/dev/null |
        grep -o 'uci-defaults/[0-9A-Za-z._-]*' | sed 's|uci-defaults/||'
}

# order_ok <name...>: the contract above, as one predicate, so the recipes and
# the controls below are judged by exactly the same rule.
#
#   - every prefix parses and the list ascends by numeric prefix;
#   - the credential script is present and BEFORE the gate;
#   - the gate is present and is the LAST entry;
#   - collating the names (what the boot glob does) still leaves the gate last.
order_ok() {
    local prev="" name num i=0 cred_i="" gate_i="" cred_n="" gate_n="" bad="0"
    [ "$#" -gt 0 ] || return 1
    for name in "$@"; do
        i=$((i + 1))
        num="${name%%-*}"
        case "$num" in ''|*[!0-9]*) return 1 ;; esac
        if [ -n "$prev" ] && [ "$((10#$num))" -lt "$((10#$prev))" ]; then
            bad="1"
        fi
        prev="$num"
        case "$name" in
            "$CREDENTIAL")            cred_i="$i"; cred_n="$name" ;;
            $GATE_GLOB)               gate_i="$i"; gate_n="$name" ;;
        esac
    done
    [ "$bad" = 0 ] || return 1
    [ -n "$cred_i" ] && [ -n "$gate_i" ] || return 1
    [ "$cred_i" -lt "$gate_i" ] || return 1
    [ "$gate_i" = "$i" ] || return 1
    # The boot path collates the filenames; the gate must still be last there.
    [ "$(printf '%s\n' "$@" | LC_ALL=C sort | tail -n 1)" = "$gate_n" ] || return 1
    return 0
}

echo "== every recipe runs the credential BEFORE the fail-closed admin gate"
GATE_NAME=""
for recipe in "${RECIPES[@]}"; do
    if [ ! -f "$recipe" ]; then
        bad "recipe missing: $recipe"
        continue
    fi
    mapfile -t order < <(script_order "$recipe")
    if [ "${#order[@]}" -eq 0 ]; then
        bad "$recipe: no uci-defaults run loop found (the extractor stopped matching)"
        continue
    fi
    for name in "${order[@]}"; do
        case "$name" in $GATE_GLOB) GATE_NAME="$name" ;; esac
    done
    if order_ok "${order[@]}"; then
        ok "$recipe: ${order[*]}"
    else
        bad "$recipe: ${order[*]} — the credential must sort/run before the gate, and the gate must run last"
    fi
done

echo
echo "== the gate's filename sorts after the credential's, so BOOT is gate-last too"
if [ -z "$GATE_NAME" ]; then
    bad "no *-tollgate-admin-setup entry in any recipe — cannot check the boot order"
else
    first="$(printf '%s\n%s\n' "$CREDENTIAL" "$GATE_NAME" | LC_ALL=C sort | head -n 1)"
    if [ "$first" = "$CREDENTIAL" ]; then
        ok "the boot glob applies $CREDENTIAL before $GATE_NAME"
    else
        bad "the boot glob applies $GATE_NAME before $CREDENTIAL — a boot would still gate before the credential exists"
    fi
fi

echo
echo "== the file the recipes run is the file the packaging stages and installs"
if [ -n "$GATE_NAME" ]; then
    if grep -q "uci-defaults/$GATE_NAME" packaging/portal-build.sh; then
        ok "portal-build.sh stages the gate as $GATE_NAME"
    else
        bad "portal-build.sh does not stage the gate as $GATE_NAME — the recipe runs a file the build never produces"
    fi
    if grep -q "uci-defaults/$GATE_NAME" packaging/Makefile; then
        ok "packaging/Makefile installs the gate as $GATE_NAME"
    else
        bad "packaging/Makefile does not install the gate as $GATE_NAME"
    fi
fi

echo
echo "== negative control: the order this repository shipped must fail the check"
# The order the module shipped before the fix (the fail-closed gate before the
# credential). If the predicate accepts it, the assertions carry no information.
if order_ok \
   90-tollgate-captive-portal-symlink 92-tollgate-admin-setup 99-tollgate-setup; then
    bad "negative control: the check accepted 90, 92, 99 — it does not detect the gate-before-credential defect"
else
    ok "negative control: 90, 92, 99 is rejected (the gate ran before the credential existed)"
fi
# A rename that only the recipe follows is not a fix either: same names, recipe
# order swapped. The boot glob would still gate first.
if order_ok \
   90-tollgate-captive-portal-symlink 99-tollgate-setup 92-tollgate-admin-setup; then
    bad "negative control: the check accepted 90, 99, 92 with the OLD gate filename — the boot path is not fixed"
else
    ok "negative control: 90, 99, 92 with an untouched gate filename is rejected (boot would still gate first)"
fi

echo
echo "== positive control: the gate-last order must pass the same check"
if order_ok \
   90-tollgate-captive-portal-symlink 99-tollgate-setup 999-tollgate-admin-setup; then
    ok "positive control: 90, 99, 999 passes the check"
else
    bad "the gate-last order 90, 99, 999 does not pass the check"
fi

echo
printf 'tests: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
