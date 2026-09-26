#!/usr/bin/env bash
# Offline test for the invariant that LuCI — the router's administration UI —
# is not reachable from the captive/guest network. No router, no SDK, no network.
#
# The real defect this guards (bench MT3000, 2026-09-25, pre17, measured from a
# MAC the box had never seen): uhttpd.main serves LuCI on :8080 (and on :443
# whenever a cert/key pair exists) with `home=/www`, and /www/index.html
# meta-refreshes into /cgi-bin/luci, i.e. the router's administration login. A
# paying guest who typed `http://<router>/` got nothing on :80, fell through to
# `https://<router>/` and landed on that login — which the operator reasonably
# read as "the install is broken". The captive bridge guests sit on is br-lan,
# and LuCI was reachable from it two ways at once: nodogsplash's pre-auth allow
# list carried `allow tcp port 8080`/`443`, and nothing in the packet filter
# stood in front of either port for an *authenticated* guest either.
#
# This is the sibling of tests/packaging/admin-board-not-guest-reachable_test.sh
# (:8090/:8443, the same class of surface) and reuses the repo's packaging tier:
# parse the recipe rather than the comment describing it, and build a real
# package for the artifact-level half — an nftables file under packaging/files/
# that no recipe installs is not a control.
#
# Usage: bash tests/packaging/luci-not-guest-reachable_test.sh

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT" || exit 1

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

NFT_DIR="packaging/files/etc/nftables.d"
LUCI_NFT="$NFT_DIR/32-luci-not-guest-reachable.nft"
SETUP_SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
LUCI_PORTS="8080 443"
# Ports a captive client must keep reaching, and which this fragment must
# therefore leave alone: the portal the guest pays through on :2050 (nodogsplash
# gateway) and :2051 (the SPA), the backend API :2121 (its own rule), and the
# plain-HTTP entry point this same change adds on :80 (setup_uhttpd_trusted_entry
# — dropping it here would kill the fix it ships with, for exactly the trusted
# clients that need it).
KEPT_PORTS="2050 2051 2121 80"

# ------------------------------------------------- 1. the packet-filter rule
echo "== LuCI is dropped on the captive bridge"
if [ ! -f "$LUCI_NFT" ]; then
    bad "missing nftables rule: $LUCI_NFT (nothing stops a captive client reaching the :8080 LuCI login)"
else
    ok "nftables rule present: $LUCI_NFT"
    # Each port, not each rule: a rule that names only :8080 leaves LuCI's TLS
    # listener reachable, so the port is checked for membership in the rule's own
    # port set rather than only for the rule's presence.
    for port in $LUCI_PORTS; do
        for proto in ipv4 ipv6; do
            if grep -qE "meta nfproto $proto iifname \"br-lan\" tcp dport \{[^}]*[[:space:],]${port}[[:space:],}][^}]*\}[[:space:]]+counter drop" "$LUCI_NFT"; then
                ok "rule drops :$port on br-lan ($proto)"
                break
            fi
        done
    done
    # Both protocol families must be covered: an IPv6-only client on the guest
    # bridge would otherwise walk straight past an ipv4-only rule.
    for proto in ipv4 ipv6; do
        if grep -qE "meta nfproto $proto iifname \"br-lan\" tcp dport \{ 8080, 443 \}[[:space:]]+counter drop" "$LUCI_NFT"; then
            ok "rule covers $proto captive clients"
        else
            bad "rule does not cover $proto captive clients (an $proto-only guest still reaches the LuCI login)"
        fi
    done
    # The two ports must be dropped together, in one rule per family: a rule that
    # names only :8080 leaves the TLS listener reachable.
    for port in $LUCI_PORTS; do
        grep -qE "tcp dport \{ 8080, 443 \}" "$LUCI_NFT" \
            && ok "rule covers :$port" \
            || bad "rule does not name :$port"
    done
    # The customer journey must not be collateral damage — :80 in particular,
    # because the plain-HTTP entry point for trusted clients is shipped by this
    # same change and a drop here would make it unreachable for the guests it
    # exists for.
    for port in $KEPT_PORTS; do
        if grep -E "dport.*[[:space:]{,]$port[[:space:],}]" "$LUCI_NFT" | grep -q "drop"; then
            bad "rule drops :$port — that port is part of the customer journey or the :80 entry point, not LuCI"
        else
            ok "rule leaves :$port alone"
        fi
    done
    # A chain name reused by another file in the same table makes `nft -f` fail
    # on reload, which would take the whole fw4 ruleset down with it.
    chain=$(sed -n 's/^chain \([a-z0-9_]*\) {.*/\1/p' "$LUCI_NFT" | head -n1)
    if [ -z "$chain" ]; then
        bad "could not read a chain name out of $LUCI_NFT"
    else
        dup=$(grep -rl "^chain $chain {" "$NFT_DIR" | wc -l | tr -d ' ')
        [ "$dup" = 1 ] && ok "chain '$chain' is declared once" \
                       || bad "chain '$chain' is declared in $dup files under $NFT_DIR (nft -f would fail)"
    fi
    # Every fragment is loaded inside fw4's `table inet`; a file that opens its
    # own table would break the include contract.
    if grep -qE "^table " "$LUCI_NFT"; then
        bad "$LUCI_NFT opens its own table — fragments are includes inside fw4's table inet"
    else
        ok "$LUCI_NFT is an include fragment (no own table declaration)"
    fi
fi

# --------------------------------------------------- 2. the allow-list writer
# The other half, checked statically: no shipped packaging/CI file may ADD a
# LuCI port to nodogsplash's pre-auth list again. This is the drift the exposure
# came back from — the list is written by two scripts and repaired on every
# install, so a re-added line is a silent regression.
echo "== no shipped writer re-adds the LuCI ports to the pre-auth allow list"
readd=$(grep -rIn "add_list.*users_to_router.*port \(8080\|443\)" \
    packaging scripts .github/workflows .ngit/act/workflows 2>/dev/null \
    | grep -v -E ':[0-9]+:[[:space:]]*#' || true)
if [ -n "$readd" ]; then
    while IFS= read -r line; do
        bad "pre-auth LuCI allowance re-introduced: $line"
    done <<EOF
$readd
EOF
else
    ok "no packaging/CI file adds :8080/:443 to users_to_router"
fi

# `tollgate-cli ssl enable` writes the :443 allowance
# (src/cmd/tollgate-cli/ssl.go). That is now redundant — the customer path no
# longer uses TLS at all — but it is harmless while it lasts: the packet filter
# above drops :443 on br-lan whatever the allow list says, and the next install
# removes the entry again. Pin that this is the ONLY remaining writer so a second
# one cannot appear unnoticed.
echo "== the CLI's :443 allowance is the single known, covered exception"
cli_add=$(grep -rIn 'add_list.*users_to_router=allow tcp port 443' src 2>/dev/null | wc -l | tr -d ' ')
[ "$cli_add" = 1 ] && ok "exactly one src/ writer of the :443 allowance (tollgate-cli ssl enable)" \
                   || bad "$cli_add src/ writers of the :443 allowance — more than the known CLI one"

# And the writer must actively remove them, because an install that only omits
# the entries leaves every already-deployed router carrying them.
for port in $LUCI_PORTS; do
    if grep -qE "del_list nodogsplash\.@nodogsplash\[0\]\.users_to_router='allow tcp port $port'" \
        "$SETUP_SCRIPT"; then
        ok "99-tollgate-setup removes 'allow tcp port $port' from users_to_router"
    else
        bad "99-tollgate-setup does not del_list 'allow tcp port $port' — a deployed router keeps the allowance"
    fi
done
if grep -q 'detected by removing them' "$SETUP_SCRIPT" ||
   [ "$(grep -c "del_list nodogsplash.@nodogsplash\[0\].users_to_router='allow tcp port 8080'" "$SETUP_SCRIPT")" -ge 1 ]; then
    ok "the removal lives in the shared allow-list writer, so both setup paths perform it"
else
    bad "the :8080 removal is not in the shared allow-list writer (one setup path would skip it)"
fi

# ------------------------------------- 3. the two layers agree on the ports
# The allow-list half and the packet-filter half must name the same ports, or
# one of them is silently guarding a different surface (the :8090 board is a
# separate file and must not be duplicated here).
echo "== the allow-list removal and the drop rule name the same ports"
frag_ports=$(grep -oE "tcp dport \{[^}]*\}" "$LUCI_NFT" 2>/dev/null | head -n1 |
             grep -oE '[0-9]+' | sort -n | uniq | tr '\n' ' ' | sed 's/ $//')
[ "$frag_ports" = "443 8080" ] && ok "the fragment drops exactly :443 and :8080 (got '$frag_ports')" \
                              || bad "the fragment drops '$frag_ports' (want '443 8080')"
for port in 8090 8443; do
    removed=$(grep -c "users_to_router='allow tcp port $port'" \
        packaging/files/etc/nftables.d/32-luci-not-guest-reachable.nft 2>/dev/null)
    [ "$removed" = 0 ] && ok "the :$port board port is not mixed into the LuCI fragment" \
                       || bad "the LuCI fragment also claims :$port — one port, one fragment"
done

# --------------------------------------------- 4. the recipe installs the file
# A ruleset file no recipe installs is not a control: this is the exact defect
# assert-artifact-contents.sh documents for 30-backend-firewall.nft.
echo "== the recipe installs the rule"
if [ ! -f "$LUCI_NFT" ]; then
    bad "cannot check recipe coverage: $LUCI_NFT does not exist"
else
    base=$(basename "$LUCI_NFT")
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
fi

# ----------------------------------------------------- 5. it ships in an ipk
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

base=$(basename "$LUCI_NFT")
if [ ! -f "$PAYLOAD/etc/nftables.d/$base" ]; then
    bad "the recipe's own install lines do not put $base in the payload"
else
    ok "the recipe's install lines put $base in the payload"
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
        if printf '%s' "$out" | grep -q "ok   etc/nftables.d/$base"; then
            ok "the artifact checker lists etc/nftables.d/$base as packaged intact"
        else
            bad "the artifact checker did not report etc/nftables.d/$base as packaged intact"
        fi
    else
        bad "could not build a test .ipk (needs packaging/build-ipk.sh, ar, tar, gzip)"
    fi
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
