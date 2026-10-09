#!/usr/bin/env bash
# Offline tests for the GLOBAL IPv6 disable in 99-tollgate-setup
# (setup_disable_ipv6 — issues #148 and #783).
#
# #148's LAN half (dhcp.lan.ra/dhcpv6 disabled, network.lan.ip6assign=0)
# shipped long ago and is regression-pinned here. #783 bench-verified on
# rc1 that a pre-auth client still reaches the internet over IPv6 when the
# WAN offers it — so the disable is now global: network.lan.ipv6=0,
# network.wan.ipv6=0 (when a wan section exists), and the stock wan6
# section disabled (when present). A missing wan/wan6 section is adoption,
# not an error.
#
# No router, SDK or network needed: the policy runs from its ONE
# declaration (packaging/files/usr/local/bin/tollgate-ipv6-disable) and
# from both carriers' wiring, against a fake uci.
#
# Usage: bash tests/uci-defaults-ipv6-global_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# ---- THE declaration: packaging/files/usr/local/bin/tollgate-ipv6-disable
# Both carriers (setup wrapper + postinst block) consume this one file;
# the semantic cases below run it directly, the wiring cases prove the
# carriers route through it, and the fence case makes a second copy of
# any axis a build failure.
DECL="packaging/files/usr/local/bin/tollgate-ipv6-disable"
grep -q "network.lan.ipv6='0'" "$DECL" \
    || { bad "declaration missing or pre-#783 shape: $DECL"; exit 1; }
cp "$DECL" "$TMP/v6_fragment.sh"
# The setup-path wrapper, extracted by function boundary with a stubbed
# log — its run below proves the driver's call applies the declaration.
# Both carriers invoke the declaration by its shipped ABSOLUTE path
# (/usr/local/bin/...) — correct on the router, absent on a test host.
# The extraction rewrites it to the checkout copy (the harness's standard
# path-stub, so the wiring is exercised against the real declaration).
awk '/^setup_disable_ipv6\(\) \{/,/^\}/' "$SCRIPT" \
    | sed "s|/usr/local/bin/tollgate-ipv6-disable|$ROOT/$DECL|g" > "$TMP/setup_wrapper.sh"
grep -q "tollgate-ipv6-disable" "$TMP/setup_wrapper.sh" \
    || { bad "extraction failed — setup_disable_ipv6 not found or no longer routes through the declaration in $SCRIPT"; exit 1; }
printf '\nlog() { echo "log: $*"; }\nsetup_disable_ipv6\n' >> "$TMP/setup_wrapper.sh"

mkdir -p "$TMP/bin"

# ---- fake uci: flat-file state, the subset the function uses
cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
set -uo pipefail
STATE="${UCI_STATE:?UCI_STATE not set}"
quiet=0
[ "${1:-}" = "-q" ] && { quiet=1; shift; }
cmd="${1:-}"; shift || true
case "$cmd" in
    get)
        key="${1:-}"
        if [ -f "$STATE/$key" ]; then cat "$STATE/$key"; exit 0; fi
        for f in "$STATE/$key".*; do [ -e "$f" ] && exit 0; done
        exit 1
        ;;
    set)
        arg="${1:-}"
        key="${arg%%=*}"
        val="${arg#*=}"
        mkdir -p "$STATE/$(dirname "$key")"
        printf '%s' "$val" > "$STATE/$key"
        ;;
    commit)
        # The postinst block commits dhcp/network; flat-file state needs no
        # flush, but the command must succeed so `|| true` paths stay honest.
        exit 0
        ;;
    *)
        echo "uci-shim: unsupported command: $cmd" >&2
        exit 99
        ;;
esac
SHIM
chmod +x "$TMP/bin/uci"

run_fragment() {  # run_fragment <state-dir>: runs the extracted function
    UCI_STATE="$1" PATH="$TMP/bin:$PATH" sh "$TMP/v6_fragment.sh" 2>"$1/stderr.log"
}

state_has() { [ -f "$1/$2" ] && [ "$(cat "$1/$2")" = "$3" ]; }

# ---- case 1: stock topology (lan + wan + wan6 all present)
S="$TMP/stock"; mkdir -p "$S"
for k in network.lan.ip6assign network.wan.proto network.wan6.proto dhcp.lan.interface; do
    mkdir -p "$S/$(dirname "$k")"; echo "'placeholder'" > "$S/$k"
done
run_fragment "$S"
state_has "$S" dhcp.lan.ra disabled           && ok "stock: dhcp.lan.ra=disabled (#148 half kept)" || bad "stock: dhcp.lan.ra not disabled"
state_has "$S" dhcp.lan.dhcpv6 disabled        && ok "stock: dhcp.lan.dhcpv6=disabled (#148 half kept)" || bad "stock: dhcp.lan.dhcpv6 not disabled"
state_has "$S" network.lan.ip6assign 0         && ok "stock: network.lan.ip6assign=0 (#148 half kept)" || bad "stock: ip6assign not 0"
state_has "$S" network.lan.ipv6 0              && ok "stock: network.lan.ipv6=0 (global, #783)" || bad "stock: network.lan.ipv6 not 0"
state_has "$S" network.wan.ipv6 0              && ok "stock: network.wan.ipv6=0 (global, #783)" || bad "stock: network.wan.ipv6 not 0"
state_has "$S" network.wan6.disabled 1         && ok "stock: network.wan6.disabled=1 (#783)" || bad "stock: wan6 not disabled"

# ---- case 2: idempotence — a second run changes nothing (state already converged)
before="$(find "$S" -type f ! -name stderr.log | sort | xargs md5sum | md5sum)"
run_fragment "$S"
after="$(find "$S" -type f ! -name stderr.log | sort | xargs md5sum | md5sum)"
[ "$before" = "$after" ] && ok "idempotent: second run is a no-op" || bad "idempotent: second run changed state"

# ---- case 3: wan6-less topology (some images/modem setups) — adoption, no error
S3="$TMP/no-wan6"; mkdir -p "$S3"
for k in network.lan.ip6assign network.wan.proto; do
    mkdir -p "$S3/$(dirname "$k")"; echo "'placeholder'" > "$S3/$k"
done
run_fragment "$S3"; rc=$?
[ $rc -eq 0 ] && ok "no-wan6: function exits 0 (missing section is adoption)" || bad "no-wan6: function exited $rc"
[ ! -f "$S3/network.wan6.disabled" ] && ok "no-wan6: no wan6 section invented" || bad "no-wan6: wan6.disabled written for a section that does not exist"
state_has "$S3" network.wan.ipv6 0 && ok "no-wan6: wan.ipv6=0 still set" || bad "no-wan6: wan.ipv6 not set"

# ---- case 4: previously-ENABLED v6 (upgrade case) — the policy re-asserts
S4="$TMP/pre-enabled"; mkdir -p "$S4"
for k in network.lan.ipv6 network.wan.ipv6; do
    mkdir -p "$S4/$(dirname "$k")"; echo "1" > "$S4/$k"
done
mkdir -p "$S4/dhcp.lan"; echo "'enabled'" > "$S4/dhcp.lan.ra"
run_fragment "$S4"
state_has "$S4" network.lan.ipv6 0 && state_has "$S4" network.wan.ipv6 0 && state_has "$S4" dhcp.lan.ra disabled \
    && ok "upgrade: enabled v6 is re-disabled (policy, not adoption)" || bad "upgrade: pre-enabled v6 survived"

# ---- case 5: BOTH carriers wire to the declaration --------------------------
# The setup wrapper runs the declaration (driver path) and the postinst
# block runs it before the network restart (upgrade path). Each leg plants
# stock state, runs the carrier with the declaration on PATH, and asserts
# the five axes landed — proving the wiring, not just the policy.
POSTINST="packaging/postinst"
awk '/^# IPv6 is disabled at INSTALL time/,/^fi$/' "$POSTINST" \
    | sed "s|/usr/local/bin/tollgate-ipv6-disable|$ROOT/$DECL|g" > "$TMP/postinst_fragment.sh"
grep -q "tollgate-ipv6-disable" "$TMP/postinst_fragment.sh" \
    || { bad "extraction failed — install-time IPv6 block not found or no longer routes through the declaration in $POSTINST"; }

SW="$TMP/wiring-setup"; mkdir -p "$SW"
for k in network.lan.ip6assign network.wan.proto network.wan6.proto dhcp.lan.interface; do
    mkdir -p "$SW/$(dirname "$k")"; echo "'placeholder'" > "$SW/$k"
done
UCI_STATE="$SW" PATH="$TMP/bin:$ROOT/packaging/files/usr/local/bin:$PATH" \
    sh "$TMP/setup_wrapper.sh" >/dev/null 2>"$SW/stderr.log"; rc=$?
[ $rc -eq 0 ] && ok "wiring: setup wrapper routes through the declaration (exit 0)" || bad "wiring: setup wrapper exited $rc"
state_has "$SW" network.lan.ipv6 0 && state_has "$SW" network.wan.ipv6 0 && state_has "$SW" network.wan6.disabled 1 \
    && ok "wiring: setup path applies all global axes via the declaration" || bad "wiring: setup path did not apply the policy"

S5="$TMP/postinst-stock"; mkdir -p "$S5"
for k in network.lan.ip6assign network.wan.proto network.wan6.proto dhcp.lan.interface; do
    mkdir -p "$S5/$(dirname "$k")"; echo "'placeholder'" > "$S5/$k"
done
UCI_STATE="$S5" PATH="$TMP/bin:$ROOT/packaging/files/usr/local/bin:$PATH" sh "$TMP/postinst_fragment.sh" >/dev/null 2>"$S5/stderr.log"; rc=$?
[ $rc -eq 0 ] && ok "postinst: install-time block exits 0" || bad "postinst: install-time block exited $rc"
state_has "$S5" dhcp.lan.ra disabled      && ok "postinst: dhcp.lan.ra=disabled" || bad "postinst: dhcp.lan.ra not disabled"
state_has "$S5" dhcp.lan.dhcpv6 disabled   && ok "postinst: dhcp.lan.dhcpv6=disabled" || bad "postinst: dhcp.lan.dhcpv6 not disabled"
state_has "$S5" network.lan.ip6assign 0    && ok "postinst: network.lan.ip6assign=0" || bad "postinst: ip6assign not 0"
state_has "$S5" network.lan.ipv6 0         && ok "postinst: network.lan.ipv6=0" || bad "postinst: network.lan.ipv6 not 0"
state_has "$S5" network.wan.ipv6 0         && ok "postinst: network.wan.ipv6=0" || bad "postinst: network.wan.ipv6 not 0"
state_has "$S5" network.wan6.disabled 1    && ok "postinst: network.wan6.disabled=1" || bad "postinst: wan6 not disabled"

# ---- case 6: ONE declaration — a second copy of any axis fails the build ----
# The #791/#796 one-declaration discipline, rooted: the five axis literals
# may exist in exactly one file under packaging/ (the declaration). A copy
# pasted back into 99-setup or postinst — the drift this rule exists to
# prevent — fails here by name.
AXES="dhcp.lan.ra='disabled' dhcp.lan.dhcpv6='disabled' network.lan.ip6assign='0' network.lan.ipv6='0'"
declare -A axis_expect=( [dhcp.lan.ra='disabled']="$DECL" [dhcp.lan.dhcpv6='disabled']="$DECL"
    [network.lan.ip6assign='0']="$DECL" [network.lan.ipv6='0']="$DECL" )
for axis in "${!axis_expect[@]}"; do
    holders="$(grep -rlF "$axis" packaging/ --include='*' 2>/dev/null | grep -v '^packaging/files/usr/local/bin/tollgate-ipv6-disable$' | tr '\n' ' ')"
    [ -z "$holders" ] \
        && ok "one-declaration: $axis lives only in the declaration" \
        || bad "one-declaration: $axis also copied into: $holders"
done
# wan/wan6 axes are existence-guarded; assert their carriers too
for axis in "network.wan.ipv6='0'" "network.wan6.disabled='1'"; do
    holders="$(grep -rlF "$axis" packaging/ 2>/dev/null | grep -v '^packaging/files/usr/local/bin/tollgate-ipv6-disable$' | tr '\n' ' ')"
    [ -z "$holders" ] \
        && ok "one-declaration: $axis lives only in the declaration" \
        || bad "one-declaration: $axis also copied into: $holders"
done

# ---- case 7: negative controls — the assertions must bite --------------------
# Two sabotaged copies of the declaration: a flipped value and a dropped
# axis. Each runs against the same planted stock state; if the case-1
# assertions could NOT tell sabotage from policy, these checks fail.
NEG1="$TMP/neg-flipped"; mkdir -p "$NEG1"
for k in network.lan.ip6assign network.wan.proto network.wan6.proto dhcp.lan.interface; do
    mkdir -p "$NEG1/$(dirname "$k")"; echo "'placeholder'" > "$NEG1/$k"
done
sed "s|network.lan.ipv6='0'|network.lan.ipv6='1'|" "$DECL" > "$TMP/neg1.sh"
UCI_STATE="$NEG1" PATH="$TMP/bin:$PATH" sh "$TMP/neg1.sh" >/dev/null 2>&1
state_has "$NEG1" network.lan.ipv6 0 \
    && bad "negative control: a flipped axis value PASSED the stock assertion — the pin is blind" \
    || ok "negative control: flipped value is caught (assertion bites)"
state_has "$NEG1" network.wan.ipv6 0 \
    && ok "negative control: sabotage is surgical (other axes still applied)" \
    || bad "negative control: sabotage leaked beyond the flipped axis"

NEG2="$TMP/neg-dropped"; mkdir -p "$NEG2"
for k in network.lan.ip6assign network.wan.proto network.wan6.proto dhcp.lan.interface; do
    mkdir -p "$NEG2/$(dirname "$k")"; echo "'placeholder'" > "$NEG2/$k"
done
grep -v "dhcp.lan.ra='disabled'" "$DECL" > "$TMP/neg2.sh"
UCI_STATE="$NEG2" PATH="$TMP/bin:$PATH" sh "$TMP/neg2.sh" >/dev/null 2>&1
[ ! -f "$NEG2/dhcp.lan.ra" ] \
    && ok "negative control: dropped #148 axis leaves the pin's key absent (assertion would fail)" \
    || bad "negative control: dropped axis still present — pin cannot catch a drop"

# ---- summary
echo
echo "passed $PASS, failed $FAIL"
[ $FAIL -eq 0 ]
