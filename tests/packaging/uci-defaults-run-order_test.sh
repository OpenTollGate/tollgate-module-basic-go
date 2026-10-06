#!/usr/bin/env bash
# Offline test for the ORDER the postinst runs the uci-defaults scripts in.
#
# At boot, /etc/init.d/boot applies /etc/uci-defaults/* in NUMERIC order —
# 90-tollgate-captive-portal-symlink, 92-tollgate-admin-setup,
# 99-tollgate-setup. The postinst a package install triggers used to run the
# same scripts as 90, 99, 92 instead, so the LAST writer of uhttpd.main differed
# between the install pass and the boot pass: an install converged to whatever
# the earlier order produced until the next reboot re-ran them numerically. That
# is a divergence nobody sees until the reboot, and both of those scripts derive
# uhttpd.main.redirect_https — this module's 99 from certificate COVERAGE
# (`tollgate ssl covers`), the pinned portal's 92 from the same coverage rule
# since the module's portal pin advanced to 4158030 (guarded by
# tests/packaging/assert-portal-bundle-contract.sh, CHECK F).
#
# An install must converge to the state the boot path produces, whatever either
# script decides. That is what this test pins: the in-repo recipes run the list
# in ascending numeric order and 99-tollgate-setup — the script that re-derives
# the whole uhttpd contract from coverage — is the last writer.
#
# The historical instance, for the record: with the pre-#593 guard (a readable,
# non-empty cert+key pair and a configured listen_https) 92 armed the
# :8080 -> https:// hop for the OpenWrt image's placeholder certificate
# (CN=OpenWrt, SAN DNS:OpenWrt, 561 bytes, bench MT3000 2026-09-26), which covers
# neither the router's hostname nor its LAN IP, while 99 derived 0 from coverage.
# With 92 last, that install ended in a hard certificate error on every admin
# login until the next boot. 92 no longer carries that premise in the portal pin,
# but the feed repository's vendored copy does (net/tollgate-wrt/files/uci-defaults/
# 92-tollgate-admin-setup, existence-only as of 2026-09-27) and its recipe still
# runs 92 last — so the order is worth holding regardless of which copy a router
# gets, and the feed needs the same two changes in its own repository.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

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

# order_is_boot_order <name...>: the list must be in ascending numeric order (the
# order the boot path applies them in) and must contain 99-tollgate-setup, the
# writer that derives the uhttpd contract from coverage.
order_is_boot_order() {
    local prev="" name num bad="0"
    [ "$#" -gt 0 ] || return 1
    for name in "$@"; do
        num="${name%%-*}"
        case "$num" in ''|*[!0-9]*) return 1 ;; esac
        if [ -n "$prev" ] && [ "$((10#$num))" -lt "$((10#$prev))" ]; then
            bad="1"
        fi
        prev="$num"
    done
    [ "$bad" = 0 ] || return 1
    case " $* " in *" 99-tollgate-setup "*) ;; *) return 1 ;; esac
    return 0
}

echo "== the postinst runs the uci-defaults in the boot path's numeric order"
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
    if order_is_boot_order "${order[@]}"; then
        ok "$recipe: ${order[*]}"
    else
        bad "$recipe: ${order[*]} — not ascending by numeric prefix, so install order differs from boot order"
    fi
done

echo
echo "== 99-tollgate-setup is the last writer of the uhttpd contract on the install path"
last_writer() { # last_writer <file> -> the last uci-default in the loop
    local order
    mapfile -t order < <(script_order "$1")
    printf '%s' "${order[@]: -1}"
}
for recipe in "${RECIPES[@]}"; do
    [ -f "$recipe" ] || continue
    got="$(last_writer "$recipe")"
    if [ "$got" = "99-tollgate-setup" ]; then
        ok "$recipe: last uci-default is $got"
    else
        bad "$recipe: last uci-default is ${got:-none}, want 99-tollgate-setup (it is the one that re-derives the contract from coverage)"
    fi
done

echo
echo "== negative control: the order this repository shipped must fail the check"
# The order the module shipped before the fix. If the check above cannot reject
# it, the assertions carry no information.
if order_is_boot_order \
   90-tollgate-captive-portal-symlink 99-tollgate-setup 92-tollgate-admin-setup; then
    bad "negative control: the check accepted 90, 99, 92 — it does not detect the divergence"
else
    ok "negative control: 90, 99, 92 is rejected (92 was the last writer at install time, 99 at boot)"
fi
if order_is_boot_order \
   90-tollgate-captive-portal-symlink 92-tollgate-admin-setup 99-tollgate-setup; then
    ok "positive control: the boot order 90, 92, 99 passes the same check"
else
    bad "the boot order 90, 92, 99 does not pass the check"
fi

echo
printf 'tests: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
