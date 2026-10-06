#!/usr/bin/env bash
# Offline test for the invariant that the backend API (:2121) answers the
# OWNER network, not only the captive one. No router, no SDK, no network.
#
# The real defect this guards (measured 2026-10-04 on a freshly flashed
# GL-MT3000 carrying alpha4-pre21): uhttpd.admin serves the owner-facing board
# on :8090, and that board is deliberately NOT reachable from the captive
# bridge (31-admin-board-not-guest-reachable.nft), so br-private is the one
# network the board is administered from. The board is a thin shell:
# payment-api.ts reads pricing, whoami, session balance and ln-invoice from
# http://<router>:2121/. 30-backend-firewall.nft exempted only br-lan and lo,
# so on the owner network the board rendered while every one of its data calls
# died — the browser console showed "error fetching tollgate data: TypeError:
# NetworkError when attempting to fetch resource" and "lightning capability
# probe failed", once per retry, forever. The packet filter dropped the API on
# the only network where the UI is reachable.
#
# The sibling of tests/packaging/luci-not-guest-reachable_test.sh and
# admin-board-not-guest-reachable_test.sh (same tier: parse the shipped recipe,
# not the comment describing it). An nftables file under packaging/files/ that
# no recipe installs is not a control — checked at the bottom.
#
# Usage: bash tests/packaging/backend-api-owner-network_test.sh

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT" || exit 1

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

NFT_DIR="packaging/files/etc/nftables.d"
API_NFT="$NFT_DIR/30-backend-firewall.nft"
# Every interface the API must answer on. br-lan is the captive bridge (guests
# pay through the portal, whose SPA calls :2121); br-private is the operator's
# bridge, where the admin board lives; lo is the router itself.
OWNER_IFACES="br-lan br-private lo"
# The pre-fix set. It must be gone: leaving br-private out is the defect.
STALE_SET='iifname != { "br-lan", "lo" }'
# Interfaces that are NOT a TollGate bridge. An exemption naming one of these
# is an accident, not a policy — and the whole point of the rule is that
# everything except the two LAN bridges is dropped.
FOREIGN_IFACES="eth0 eth1 wan wwan br-guest br-wan wlan0"
API_PORT="2121"

# Normalise an interface list to a sorted, space-joined, quote-free string.
norm() {
    printf '%s' "$1" \
        | tr ',' '\n' \
        | sed 's/^[[:space:]]*//; s/[[:space:]]*$//' \
        | grep -v '^$' \
        | sort -u \
        | tr '\n' ' ' \
        | sed 's/ $//'
}

# ------------------------------------------------- 1. the packet-filter rule
echo "== the backend API is dropped on everything except the two LAN bridges"
if [ ! -f "$API_NFT" ]; then
    bad "missing nftables rule: $API_NFT (nothing scopes the :$API_PORT API)"
else
    ok "nftables rule present: $API_NFT"

    # The exemption set, per rule. Counted so an ipv4-only rule cannot pass:
    # an IPv6-only owner client would walk straight past it.
    sets=0
    while IFS= read -r raw; do
        sets=$((sets + 1))
        got="$(norm "$raw")"
        if [ "$got" = "$OWNER_IFACES" ]; then
            ok "exemption set #$sets is '$got'"
        else
            bad "exemption set #$sets is '$got' (want '$OWNER_IFACES') — a client on an unlisted bridge cannot reach the API"
        fi
    done < <(grep -o 'iifname != {[^}]*}' "$API_NFT" | sed 's/^iifname != { *//; s/ *}$//; s/"//g')

    if [ "$sets" = 2 ]; then
        ok "both protocol families are covered (ipv4 + ipv6)"
    else
        bad "$sets exemption rule(s) found, want 2 (ipv4 + ipv6) — a missing family leaves that stack's clients unguarded"
    fi

    # The rule must still be a drop. An "exemption" that became an accept would
    # open the API to the WAN side, which is the reason the rule exists (#226).
    drops=$(grep -cE "tcp dport $API_PORT .*counter drop" "$API_NFT")
    if [ "$drops" = 2 ]; then
        ok "both rules still end in 'counter drop'"
    else
        bad "$drops rule(s) end in 'counter drop', want 2 — the API guard was removed or loosened"
    fi
    if grep -E "dport.*$API_PORT" "$API_NFT" | grep -q 'accept'; then
        bad "the fragment ACCEPTS :$API_PORT somewhere — that opens the API beyond the LAN bridges"
    else
        ok "no accept rule for :$API_PORT in the fragment"
    fi

    # The exact pre-fix literal must be gone, so a later edit cannot silently
    # restore the exemption set that dropped the owner network.
    if grep -qF -- "$STALE_SET" "$API_NFT"; then
        bad "the pre-fix exemption set is still present ($STALE_SET) — br-private cannot reach the API the admin board reads from"
    else
        ok "the pre-fix exemption set is gone"
    fi

    for iface in $FOREIGN_IFACES; do
        if grep -qE "iifname != \{[^}]*\"$iface\"" "$API_NFT"; then
            bad "the exemption names '$iface', which is not a TollGate LAN bridge — the API would answer a non-LAN client"
        fi
    done
    ok "no foreign interface is exempted"

    # One port, one fragment: :8090/:8443 belong to 31-, :8080/:443 to 32-.
    frag_ports=$(grep -oE "tcp dport [0-9]+" "$API_NFT" | awk '{print $3}' | sort -u | tr '\n' ' ' | sed 's/ $//')
    if [ "$frag_ports" = "$API_PORT" ]; then
        ok "the fragment guards exactly :$API_PORT"
    else
        bad "the fragment guards '$frag_ports' (want '$API_PORT') — one port, one fragment"
    fi

    # Same shape as the sibling guards: an include fragment inside fw4's table,
    # hook input priority -1 so the drop is authoritative (no later accept can
    # dig a client out), and a chain name unique in the directory (a reused
    # name makes `nft -f` fail on reload and takes the whole ruleset down).
    if grep -qE '^table ' "$API_NFT"; then
        bad "$API_NFT opens its own table — fragments are includes inside fw4's table inet"
    else
        ok "$API_NFT is an include fragment (no own table declaration)"
    fi
    if grep -qE 'type filter hook input priority -1' "$API_NFT"; then
        ok "the chain hooks input at priority -1 (the drop is authoritative)"
    else
        bad "$API_NFT does not hook input at priority -1 — a later accept could undo the drop"
    fi
    chain=$(sed -n 's/^chain \([a-z0-9_]*\) {.*/\1/p' "$API_NFT" | head -n1)
    if [ -z "$chain" ]; then
        bad "could not read a chain name out of $API_NFT"
    else
        dup=$(grep -rl "^chain $chain {" "$NFT_DIR" | wc -l | tr -d ' ')
        [ "$dup" = 1 ] && ok "chain '$chain' is declared once" \
                       || bad "chain '$chain' is declared in $dup files under $NFT_DIR (nft -f would fail)"
    fi
fi

# ------------------------- 2. the sibling guards still scope the captive bridge
# This invariant only makes sense next to its siblings: the admin board and LuCI
# are kept OFF br-lan, which is exactly why the owner network has to be able to
# reach the API the board reads. If one of those guards later widens or moves,
# the premise here changed and this test should be revisited — not silently
# downgraded to a comment.
echo "== the admin-board and LuCI guards are still captive-bridge-scoped"
for frag in 31-admin-board-not-guest-reachable 32-luci-not-guest-reachable; do
    if grep -qF -- 'iifname "br-lan"' "$NFT_DIR/$frag.nft" 2>/dev/null; then
        ok "$frag.nft is still iifname \"br-lan\"-literal"
    else
        bad "$frag.nft lost its iifname \"br-lan\" scope — re-check whether br-private is still the owner network (this test's premise)"
    fi
done

# --------------------------------------------- 3. a recipe installs the file
# A ruleset file no recipe installs is not a control: this is the defect
# assert-artifact-contents.sh documents for 30-backend-firewall.nft.
echo "== the recipe installs the rule"
if [ ! -f "$API_NFT" ]; then
    bad "cannot check recipe coverage: $API_NFT does not exist"
else
    base=$(basename "$API_NFT")
    if grep -qE "^[[:space:]]*install .*packaging/files/etc/nftables\.d/${base}[[:space:]]" packaging/local-build-ipk.sh; then
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

# ------------------------------------------------- 4. it compiles as nft sees it
# The static checks above read the file; this one hands it to the real parser,
# wrapped in fw4's table the way the include contract requires. `nft -c` is
# check-only — it never touches the running ruleset. Skipped where nft or the
# privilege to run it is absent (it needs CAP_NET_ADMIN), because a skipped
# parser check is honest while a failed one would be indistinguishable from a
# missing binary.
echo "== the fragment parses"
if [ ! -f "$API_NFT" ]; then
    : # already reported above
elif command -v nft >/dev/null 2>&1; then
    work=$(mktemp)
    { printf 'table inet fw4 {\n'; grep -v '^#' "$API_NFT"; printf '}\n'; } > "$work"
    if nft -c -f "$work" >/dev/null 2>&1; then
        ok "nft -c accepts the fragment inside 'table inet fw4'"
    elif [ "$(id -u)" != 0 ]; then
        printf 'SKIP nft -c needs root (CAP_NET_ADMIN) on this host\n'
    else
        bad "nft -c rejects the fragment — 'nft -f' would fail the whole fw4 reload"
    fi
    rm -f "$work"
else
    printf 'SKIP nft not installed on this host\n'
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
