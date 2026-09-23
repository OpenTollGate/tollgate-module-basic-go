#!/usr/bin/env bash
# Offline test for the invariant that the owner-facing admin board is not
# reachable from the captive/guest network. No router, no SDK, no network.
#
# The real defect this guards: uhttpd.admin serves the admin board on :8090
# over plain HTTP with `ubus_prefix=/ubus`, so the board's login form and
# rpcd's session endpoint share one cleartext origin, and the session that
# opens through it reaches ACL group `tollgate` — `file:["exec"]`,
# `system:["password_set"]`, `tollgate wallet_drain_cashu`, i.e. root command
# execution, a root password change, or the operator's money. The captive
# bridge that guests sit on is br-lan, and the board was reachable from it two
# ways at once: nodogsplash's pre-auth allow list carried `allow tcp port 8090`
# /`8443` (written by two different scripts), and nothing in the packet filter
# stood in front of the port for an *authenticated* guest either.
#
# The allow-list half is covered behaviourally by
# tests/uci-defaults-same-version-allowlist_test.sh and
# tests/uci-defaults-nodogsplash-443_test.sh, which run the shipped script
# against a fake `uci`. This test covers the other half — the packet-filter
# rule that does not depend on the allow list being in the intended state —
# and the packaging invariant that the rule actually ships (an nftables file
# under packaging/files/ that no recipe installs is not a control).
#
# It reuses the repo's existing packaging tier rather than inventing a harness:
# the recipe-coverage check follows
# tests/packaging/package-nodogsplash-dependency_test.sh (parse the recipe, not
# the comment describing it), and the artifact check builds a real package with
# packaging/build-ipk.sh and runs tests/packaging/assert-artifact-contents.sh
# against it, exactly as the build does.
#
# Usage: bash tests/packaging/admin-board-not-guest-reachable_test.sh

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

NFT_DIR="packaging/files/etc/nftables.d"
ADMIN_NFT="$NFT_DIR/31-admin-board-not-guest-reachable.nft"
ADMIN_PORTS="8090 8443"
# Ports the board must keep answering on, and which this rule must therefore
# leave alone: the captive portal the guest pays through, the backend API (its
# own rule), and LuCI.
KEPT_PORTS="2050 2051 2121 8080"

# ------------------------------------------------- 1. the packet-filter rule
echo "== the admin board is dropped on the captive bridge"
if [ ! -f "$ADMIN_NFT" ]; then
    bad "missing nftables rule: $ADMIN_NFT (nothing stops a captive client reaching the :8090 board)"
else
    ok "nftables rule present: $ADMIN_NFT"
    for proto in ipv4 ipv6; do
        for port in $ADMIN_PORTS; do
            if grep -qE "meta nfproto $proto iifname \"br-lan\" tcp dport \{ 8090, 8443 \}[[:space:]]+counter drop" "$ADMIN_NFT"; then
                ok "rule drops :$port on br-lan ($proto)"
                break
            fi
        done
    done
    # Both protocol families must be covered: an IPv6-only client on the guest
    # bridge would otherwise walk straight past an ipv4-only rule.
    for proto in ipv4 ipv6; do
        if grep -qE "meta nfproto $proto iifname \"br-lan\" tcp dport \{ 8090, 8443 \}[[:space:]]+counter drop" "$ADMIN_NFT"; then
            ok "rule covers $proto captive clients"
        else
            bad "rule does not cover $proto captive clients (an $proto-only guest still reaches :8090)"
        fi
    done
    # The two ports must be dropped together, in one rule per family: a rule
    # that names only :8090 leaves the board's TLS listener reachable.
    for port in $ADMIN_PORTS; do
        grep -qE "tcp dport \{ 8090, 8443 \}" "$ADMIN_NFT" \
            && ok "rule covers :$port" \
            || bad "rule does not name :$port"
    done
    # The customer journey must not be collateral damage.
    for port in $KEPT_PORTS; do
        if grep -E "dport.*$port.*drop" "$ADMIN_NFT" >/dev/null; then
            bad "rule drops :$port — that port is part of the customer journey or the operator's LuCI, not the admin board"
        else
            ok "rule leaves :$port alone"
        fi
    done
    # A chain name reused by another file in the same table makes `nft -f`
    # fail on reload, which would take the whole fw4 ruleset down with it.
    chain=$(sed -n 's/^chain \([a-z0-9_]*\) {.*/\1/p' "$ADMIN_NFT" | head -n1)
    if [ -z "$chain" ]; then
        bad "could not read a chain name out of $ADMIN_NFT"
    else
        dup=$(grep -rl "^chain $chain {" "$NFT_DIR" | wc -l | tr -d ' ')
        [ "$dup" = 1 ] && ok "chain '$chain' is declared once" \
                       || bad "chain '$chain' is declared in $dup files under $NFT_DIR (nft -f would fail)"
    fi
fi

# --------------------------------------------------- 2. the allow-list writer
# The other half, checked statically: no shipped file in this repo may ADD an
# admin-board port to nodogsplash's pre-auth list again. This is the drift the
# exposure came back from — the list is written by two scripts and repaired on
# every install, so a re-added line is a silent regression.
echo "== no shipped writer re-adds the admin ports to the pre-auth allow list"
readd=$(grep -rIn "add_list.*users_to_router.*port \(8090\|8443\)" \
    packaging scripts .github/workflows .ngit/act/workflows 2>/dev/null \
    | grep -v -E ':[0-9]+:[[:space:]]*#' || true)
if [ -n "$readd" ]; then
    while IFS= read -r line; do
        bad "pre-auth admin-board allowance re-introduced: $line"
    done <<EOF
$readd
EOF
else
    ok "no packaging/CI file adds :8090/:8443 to users_to_router"
fi

# And the writer must actively remove them, because an install that only omits
# the entries leaves every already-deployed router carrying them.
in_script=0
for port in $ADMIN_PORTS; do
    if grep -qE "del_list nodogsplash\.@nodogsplash\[0\]\.users_to_router='allow tcp port $port'" \
        packaging/files/etc/uci-defaults/99-tollgate-setup; then
        ok "99-tollgate-setup removes 'allow tcp port $port' from users_to_router"
    else
        in_script=1
        bad "99-tollgate-setup does not del_list 'allow tcp port $port' — a deployed router keeps the allowance"
    fi
done
if grep -q "assert_nodogsplash_allow_entries" packaging/files/etc/uci-defaults/99-tollgate-setup &&
   [ "$(grep -c 'assert_nodogsplash_allow_entries$' packaging/files/etc/uci-defaults/99-tollgate-setup)" -ge 1 ]; then
    ok "the removal lives in the shared allow-list writer, so both setup paths perform it"
fi

# --------------------------------------------- 3. the recipe installs the file
# A ruleset file no recipe installs is not a control: this is the exact defect
# assert-artifact-contents.sh documents for 30-backend-firewall.nft.
echo "== every source ruleset is installed by both packaging paths"
if [ ! -f "$ADMIN_NFT" ]; then
    bad "cannot check recipe coverage: $ADMIN_NFT does not exist"
else
    while IFS= read -r f; do
        base=$(basename "$f")
        if grep -qE "^[[:space:]]*install .*packaging/files/etc/nftables\.d/$base[[:space:]]" \
            packaging/local-build-ipk.sh; then
            ok "local-build-ipk.sh installs $base"
        else
            bad "local-build-ipk.sh does not install $base (the built .ipk would ship an incomplete ruleset)"
        fi
        if grep -q "files/etc/nftables\.d/\*.nft" packaging/Makefile; then
            ok "packaging/Makefile installs the $base rule via its *.nft glob"
        else
            bad "packaging/Makefile no longer installs *.nft from the source directory ($base unreachable)"
        fi
        if grep -q "/etc/nftables\.d/$base" packaging/Makefile; then
            ok "packaging/Makefile FILES_ lists /etc/nftables.d/$base"
        else
            bad "packaging/Makefile FILES_ does not list /etc/nftables.d/$base"
        fi
    done < <(find "$NFT_DIR" -maxdepth 1 -type f -name '*.nft' | sort)
fi

# ----------------------------------------------------- 4. it ships in an ipk
# Recipe text can be right while the artifact is wrong. Build a real .ipk from
# the recipe's own install lines and hand it to the repo's artifact checker,
# which asserts the packaged ruleset set equals the source set.
echo "== the rule is present in a package built from the recipe"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
export PAYLOAD="$WORK/payload"
mkdir -p "$PAYLOAD"
installed=0
while IFS= read -r line; do
    case "$line" in
        *nftables.d*) ;;
        *) continue ;;
    esac
    if sh -c "$line" >/dev/null 2>&1; then
        installed=$((installed + 1))
    else
        bad "recipe install line failed: $line"
    fi
done < <(grep -E "^[[:space:]]*install .*nftables\.d" packaging/local-build-ipk.sh)
if [ "$installed" -ge 1 ]; then
    ok "ran $installed nftables install line(s) from local-build-ipk.sh"
else
    bad "found no nftables install lines to run in packaging/local-build-ipk.sh"
fi

if [ ! -f "$PAYLOAD/etc/nftables.d/$(basename "$ADMIN_NFT")" ]; then
    bad "the recipe's own install lines do not put $(basename "$ADMIN_NFT") in the payload"
else
    ok "the recipe's install lines put $(basename "$ADMIN_NFT") in the payload"
    if env PKG_NAME="tollgate-wrt" PKG_VERSION="0.0.0-test" ARCH="aarch64_cortex-a53" \
           MAINTAINER="TollGate <tollgate@tollgate.me>" LICENSE="GPL-3.0-only" \
           sh packaging/build-ipk.sh "$PAYLOAD" "$WORK/tollgate-wrt.ipk" >/dev/null 2>&1 &&
       [ -f "$WORK/tollgate-wrt.ipk" ]; then
        ok "built a test .ipk from that payload"
        out=$(bash tests/packaging/assert-artifact-contents.sh "$WORK/tollgate-wrt.ipk" 2>&1)
        rc=$?
        if [ "$rc" = 0 ]; then
            ok "assert-artifact-contents.sh passes on the built .ipk"
        else
            bad "assert-artifact-contents.sh rejected the built .ipk: $(printf '%s' "$out" | grep -E 'MISSING|EMPTY|SIZE|FAIL' | head -n 3 | tr '\n' ' ')"
        fi
        if printf '%s' "$out" | grep -q "ok   etc/nftables.d/$(basename "$ADMIN_NFT")"; then
            ok "the artifact checker lists etc/nftables.d/$(basename "$ADMIN_NFT") as packaged intact"
        else
            bad "the artifact checker did not report etc/nftables.d/$(basename "$ADMIN_NFT") as packaged intact"
        fi
    else
        bad "could not build a test .ipk (needs packaging/build-ipk.sh, ar, tar, gzip)"
    fi
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
