#!/usr/bin/env bash
# Offline test for the scope of the wired management bridge (br-mgmt): the
# nftables half of the wired-LAN bridge decision. No router, no SDK, no network.
#
# What this pins. The wired LAN ports are moved onto a bridge of their own by
# `setup_mgmt_bridge` (99-tollgate-setup). That bridge is NOT a captive network:
# one nodogsplash gates one bridge, so br-mgmt has no gate, and a bridge this
# module cannot gate must not be able to buy — a br-mgmt client resolves a MAC,
# can be quoted and can pay, and the grant then fails in the single instance's
# `ndsctl` with `Client <mac> not found.` (money taken, nothing delivered). The
# reachability policy is therefore an ALLOW LIST with a catch-all drop:
#
#   allow:  DHCP/DNS, SSH, the four administration surfaces (:443, :8080,
#           :8090, :8443), and nothing else
#   drop:   everything else, which is what makes "br-mgmt cannot transact"
#           structural rather than incidental
#
# ...and the two administration guards keep their scope: 31-*.nft and 32-*.nft
# match the CAPTIVE bridge (br-lan) and must never be extended to br-mgmt, or a
# cable would be treated as the stranger those guards exist for.
#
# The repo's packaging tier is reused rather than reinvented: parse the recipe
# instead of the comment describing it, and build a real package from the
# recipe's own install lines — an nftables file under packaging/files/ that no
# recipe installs is not a control (the defect assert-artifact-contents.sh was
# written for).
#
# Usage: bash tests/packaging/mgmt-bridge-scope_test.sh

set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$ROOT" || exit 1

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

NFT_DIR="packaging/files/etc/nftables.d"
FRAG="$NFT_DIR/33-mgmt-bridge-scope.nft"
SETUP_SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
# The four administration surfaces br-mgmt may reach before any payment.
ADMIN_PORTS="443 8080 8090 8443"
# The customer/payment surfaces it must NOT reach while one gate gates one
# bridge: the captive portal (:2050), the portal SPA (:2051) and the payment API
# (:2121, the rule in 30-backend-firewall.nft is what marks a selling network).
BLOCKED_PORTS="2050 2051 2121"

# ------------------------------------------------------- 1. the packet filter
# Kept in a function over $1 so the negative control at the bottom can run the
# exact same checks against a fragment with the catch-all drop removed.
#
# Every rule-level assertion reads a COMMENT-STRIPPED copy of the fragment: the
# header explains at length what the rule deliberately does not do (`:2050`,
# `:2051`, `:2121` are named in it, and so is the captive bridge's own rule), and
# a grep over the raw file would read those explanations as configuration.
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
export PAYLOAD="$WORK/payload"
mkdir -p "$PAYLOAD"

check_fragment() { # <fragment path>
    local frag="$1"
    if [ ! -f "$frag" ]; then
        bad "missing nftables rule: $frag (nothing scopes the wired bridge to the admin surfaces)"
        return
    fi
    local rules="$WORK/rules.$$.$RANDOM.nft"
    grep -v '^[[:space:]]*#' "$frag" > "$rules" 2>/dev/null || : > "$rules"
    # Each administration port, for ipv4: a rule that names only some of them
    # leaves the rest reachable only by accident.
    local port
    for port in $ADMIN_PORTS; do
        if grep -qE "meta nfproto ipv4 iifname \"br-mgmt\" tcp dport \{[^}]*[[:space:],]${port}[[:space:],}][^}]*\}[[:space:]]+counter accept" "$rules"; then
            ok "the fragment accepts :$port on br-mgmt"
        else
            bad "the fragment does not accept :$port on br-mgmt — the administration surface is unreachable from the cable"
        fi
    done
    # The accept rule must name exactly the four administration surfaces: a
    # wider set is a capability this bridge must not have (see the header), and
    # is what the customer/payment assertions below would catch.
    local frag_ports
    frag_ports=$(grep -oE "tcp dport \{[^}]*\}" "$rules" | head -n1 | grep -oE '[0-9]+' | sort -n | uniq | tr '\n' ' ' | sed 's/ $//')
    [ "$frag_ports" = "443 8080 8090 8443" ] \
        && ok "the administration allow rule names exactly :443/:8080/:8090/:8443" \
        || bad "the administration allow rule names '$frag_ports' (want '443 8080 8090 8443')"
    # What a client needs to exist on the bridge at all.
    grep -qE 'meta nfproto ipv4 iifname "br-mgmt" udp dport \{ 53, 67, 68 \} counter accept' "$rules" \
        && ok "the fragment accepts DHCP/DNS (udp 53/67/68) on br-mgmt" \
        || bad "the fragment does not accept DHCP on br-mgmt — a client would get no lease and no DNS"
    grep -qE 'meta nfproto ipv4 iifname "br-mgmt" tcp dport 53 counter accept' "$rules" \
        && ok "the fragment accepts DNS over TCP on br-mgmt" \
        || bad "the fragment does not accept DNS over TCP on br-mgmt"
    grep -qE 'meta nfproto ipv4 iifname "br-mgmt" tcp dport 22 counter accept' "$rules" \
        && ok "the fragment accepts SSH on br-mgmt" \
        || bad "the fragment does not accept SSH on br-mgmt — the recovery path is gone"
    # The catch-all drop, last, per family: this is the half that keeps the
    # payment surfaces and every other service out of reach.
    for proto in ipv4 ipv6; do
        if grep -qE "meta nfproto $proto iifname \"br-mgmt\" counter drop" "$rules"; then
            ok "the fragment ends with a $proto catch-all drop for br-mgmt"
        else
            bad "the fragment has no $proto catch-all drop for br-mgmt — the allow list is decorative"
        fi
    done
    # ...and it must be the LAST rule of the chain: a drop above the accepts
    # would kill the administration surfaces the fragment exists to allow.
    local last_rule
    last_rule=$(grep -nE '^[[:space:]]*(meta|tcp|udp|ct|iifname|oifname|counter|jump|return|accept|drop|reject)' "$rules" | tail -n1 | cut -d: -f2-)
    if printf '%s' "$last_rule" | grep -qE 'iifname "br-mgmt" counter drop'; then
        ok "the catch-all drop is the fragment's last rule (nothing after it is reachable)"
    else
        bad "the fragment's last rule is '$(printf '%s' "$last_rule" | sed -e 's/^[[:space:]]*//')' — the catch-all drop must be last"
    fi
    # The blocked ports must not be accepted anywhere in the fragment.
    for port in $BLOCKED_PORTS; do
        if grep -E "dport.*[[:space:]{,]${port}[[:space:],}]" "$rules" | grep -q "accept"; then
            bad "the fragment ACCEPTS :$port on br-mgmt — a network this module cannot gate must not take money it cannot deliver"
        else
            ok "the fragment never accepts :$port on br-mgmt"
        fi
    done
    # Scope: this fragment is about the wired bridge only. A rule matching the
    # captive bridge here would extend an allow list onto the guest network.
    if grep -qE '(iifname|oifname) "br-lan"' "$rules"; then
        bad "the fragment matches br-lan in a rule — it must scope the wired bridge only"
    else
        ok "the fragment scopes br-mgmt only (no br-lan in a rule)"
    fi
    # One port, one owner: the administration ports are guarded on the captive
    # bridge by 31/32 and allowed here, and nowhere twice.
    local chain dup
    chain=$(sed -n 's/^chain \([a-z0-9_]*\) {.*/\1/p' "$rules" | head -n1)
    if [ -z "$chain" ]; then
        bad "could not read a chain name out of $frag"
    else
        dup=$(grep -rl "^chain $chain {" "$NFT_DIR" | wc -l | tr -d ' ')
        [ "$dup" = 1 ] && ok "chain '$chain' is declared once" \
                       || bad "chain '$chain' is declared in $dup files under $NFT_DIR (nft -f would fail)"
    fi
    if grep -qE '^table ' "$rules"; then
        bad "$frag opens its own table — fragments are includes inside fw4's table inet"
    else
        ok "$frag is an include fragment (no own table declaration)"
    fi
}

echo "== the wired bridge is scoped to the administration surfaces"
check_fragment "$FRAG"

# ------------------------------------------------- 2. the guards keep br-lan
# The inverse of the card that this change answers: the two administration
# guards and the enforcement fragment match the CAPTIVE bridge by name, and a
# br-mgmt client is not a guest. Extending either direction re-opens what the
# other closes.
echo "== the guest guards stay pinned to the captive bridge"
for guard in 20-nds-enforce.nft 30-backend-firewall.nft \
             31-admin-board-not-guest-reachable.nft 32-luci-not-guest-reachable.nft; do
    if [ ! -f "$NFT_DIR/$guard" ]; then
        bad "missing guard fragment: $NFT_DIR/$guard"
        continue
    fi
    if grep -q 'br-mgmt' "$NFT_DIR/$guard"; then
        bad "$guard mentions br-mgmt — a guest guard must not be extended to the operator's cable"
    else
        ok "$guard is pinned to the captive bridge (no br-mgmt)"
    fi
done
# The :2121 ACL is the marker of a network that sells. 30-backend-firewall.nft
# allows the payment API only from the captive bridge and loopback, so a br-mgmt
# client cannot pay even if this fragment were missing — assert that shape.
if grep -qE 'iifname != \{ "br-lan", "lo" \} tcp dport 2121 counter drop' "$NFT_DIR/30-backend-firewall.nft"; then
    ok "30-backend-firewall.nft still drops :2121 from every network but br-lan/loopback"
else
    bad "30-backend-firewall.nft no longer restricts :2121 to br-lan/loopback — the payment API would be reachable from the wired bridge"
fi

# -------------------------------------- 3. the two layers name the same ports
# The bridge the writer creates and the ports this fragment scopes must agree,
# and the union of the two guest guards' drop sets must equal the fragment's
# allow set: a fifth administration listener has to be added in both places in
# the same change, or one of them silently guards something else.
echo "== the allow set equals the administration surfaces the guards drop"
guard_ports=$(cat "$NFT_DIR/31-admin-board-not-guest-reachable.nft" "$NFT_DIR/32-luci-not-guest-reachable.nft" 2>/dev/null |
              grep -oE 'tcp dport \{[^}]*\}' | grep -oE '[0-9]+' | sort -n | uniq | tr '\n' ' ' | sed 's/ $//')
[ "$guard_ports" = "443 8080 8090 8443" ] \
    && ok "the guest guards drop exactly :443/:8080/:8090/:8443 (got '$guard_ports')" \
    || bad "the guest guards drop '$guard_ports' (want '443 8080 8090 8443') — the allow list and the guards have drifted apart"
# And the writer really creates the bridge this fragment scopes, on both paths.
grep -q "setup_mgmt_bridge" "$SETUP_SCRIPT" \
    && ok "99-tollgate-setup still provides setup_mgmt_bridge" \
    || bad "99-tollgate-setup no longer provides setup_mgmt_bridge — the fragment would scope a bridge that is never created"
grep -qE "network\.mgmt_bridge\.name='br-mgmt'" "$SETUP_SCRIPT" \
    && ok "the writer names the bridge br-mgmt" \
    || bad "the writer does not name the bridge br-mgmt (fragment and writer disagree)"

# --------------------------------------------- 4. the recipe installs the file
echo "== the recipe installs the rule"
if [ ! -f "$FRAG" ]; then
    bad "cannot check recipe coverage: $FRAG does not exist"
else
    base=$(basename "$FRAG")
    if grep -qE "^[[:space:]]*install .*packaging/files/etc/nftables.d/${base}[[:space:]]" \
        packaging/local-build-ipk.sh; then
        ok "local-build-ipk.sh installs $base"
    else
        bad "local-build-ipk.sh does not install $base (the built .ipk would ship a scope-less bridge)"
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
echo "== the rule is present in a package built from the recipe"
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

base=$(basename "$FRAG")
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
        if [ "$?" = 0 ]; then
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

# ------------------------------------------- 6. the fragments parse as a set
# Every fragment is loaded inside fw4's `table inet`, so validation has to use
# the same wrapper — `nft -c -f <fragment>` alone fails on the pre-existing
# files too, and a check that fails on the baseline has no signal. Needs
# CAP_NET_ADMIN; skipped, loudly, where it is unavailable (CI containers).
echo "== the fragment set parses the way fw4 loads it"
if ! command -v nft >/dev/null 2>&1; then
    echo "skip nft -c: no nft binary on this host"
elif ! sudo -n true >/dev/null 2>&1; then
    echo "skip nft -c: no passwordless sudo (CAP_NET_ADMIN) on this host"
else
    WRAP="$WORK/fw4_wrap.nft"
    { echo 'table inet fw4 {'; cat "$NFT_DIR"/*.nft; echo '}'; } > "$WRAP"
    if sudo nft -c -f "$WRAP" >/dev/null 2>&1; then
        ok "nft -c accepts every fragment inside one table inet fw4 (no chain collision)"
    else
        bad "nft -c rejected the fragment set: $(sudo nft -c -f "$WRAP" 2>&1 | head -n 2 | tr '\n' ' ')"
    fi
    BROKEN="$WORK/broken.nft"
    { cat "$WRAP"; echo 'meta nfproto ipv4 iifname "br-mgmt" counter bogus drop'; } > "$BROKEN"
    if sudo nft -c -f "$BROKEN" >/dev/null 2>&1; then
        bad "the nft -c check is vacuous: a deliberately malformed rule was accepted"
    else
        ok "negative control: nft -c rejects a malformed rule (the check is not vacuous)"
    fi
fi

# ------------------------------------------------------------- 7. RED control
# A guard that has never failed is decoration: strip the catch-all drop from a
# copy of the fragment and require the same checks to fail against it.
echo "== RED control: the catch-all drop is what makes the allow list real"
MUTANT="$WORK/33-mgmt-bridge-scope.mutant.nft"
grep -v -E 'meta nfproto (ipv4|ipv6) iifname "br-mgmt" counter drop' "$FRAG" > "$MUTANT"
if grep -qE 'meta nfproto ipv4 iifname "br-mgmt" counter drop' "$MUTANT"; then
    bad "RED control: could not strip the catch-all drop — the control is vacuous"
else
    before_fail=$FAIL
    red_fails=$( ( check_fragment "$MUTANT" >/dev/null 2>&1; echo "$((FAIL - before_fail))" ) )
    if [ "${red_fails:-0}" -gt 0 ]; then
        ok "RED: the guard fails without the catch-all drop (${red_fails} assertions red)"
    else
        bad "RED: the guard passed against a fragment with no drop — it does not test what it names"
    fi
fi
# ...and the same for an allow rule widened to include a payment surface, which
# is the shape a future contributor would reach for.
WIDE="$WORK/33-mgmt-bridge-scope.wide.nft"
sed 's/tcp dport { 443, 8080, 8090, 8443 }/tcp dport { 443, 8080, 8090, 8443, 2121 }/' "$FRAG" > "$WIDE"
if cmp -s "$WIDE" "$FRAG"; then
    bad "RED control: could not widen the allow rule — the control is vacuous"
else
    before_fail=$FAIL
    red_fails=$( ( check_fragment "$WIDE" >/dev/null 2>&1; echo "$((FAIL - before_fail))" ) )
    if [ "${red_fails:-0}" -gt 0 ]; then
        ok "RED: the guard fails when :2121 is added to the allow list (${red_fails} assertions red)"
    else
        bad "RED: the guard passed against a fragment that allows the payment API"
    fi
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
