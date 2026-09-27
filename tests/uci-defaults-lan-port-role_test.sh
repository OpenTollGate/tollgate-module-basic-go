#!/usr/bin/env bash
# Offline test for the wired LAN port ROLE — `tollgate.lan_ports.role` — in
# packaging/files/etc/uci-defaults/99-tollgate-setup. No router, no network.
#
# What this pins. The wired LAN ports used to sit on the CAPTIVE bridge; PR #601
# moved them onto a bridge of their own, br-mgmt, so the operator's cable reaches
# the administration surfaces before paying and stops sharing a layer-2 domain
# with the strangers on the open SSID. That placement was hard-coded, and the
# operator's ask ("the connected devices can access luci ... but they still need
# to pay") has a second half — a cable plugged into a port is not always an
# administration port. This suite pins the field that decides it, and the three
# placements it can choose:
#
#   role=mgmt     br-mgmt. Administration reachable, no internet, no payment
#                 surface. The default, and what an unset/empty field means.
#   role=private  br-private. The operator's own trusted network: full internet
#                 (private_zone forwards to the wan) and the admin surfaces
#                 answer (both guards are captive-bridge-scoped).
#   role=guest    the CAPTIVE bridge. A customer port: must pay, admin dropped,
#                 and back in the guests' layer-2 domain.
#
# Four properties are load-bearing and each has assertions here:
#   1. the placement is a MOVE, whichever way the role changes: the bridges that
#      are not the target are CLEARED, so a port is never on two bridges (and so
#      never on two layer-2 domains);
#   2. it is CONVERGENT and IDEMPOTENT: the union of what the candidate bridges
#      hold is what moves, so a role switch from any previous placement lands
#      correctly, a reset router (port back on the captive bridge, `sysupgrade
#      -n`) is repaired, and a second run changes nothing and duplicates nothing;
#   3. it FAILS LOUDLY AND CHANGES NOTHING on every input it cannot honour — an
#      unknown role, a role whose target bridge does not exist, or a router with
#      no wired port listed anywhere. A writer that guessed a bridge on a typo
#      would move the operator's only cable access to a network he did not ask
#      for;
#   4. NOTHING ELSE MOVES WITH IT, for any role: exactly one nodogsplash section
#      pinned to the captive bridge, the two administration guards and the
#      enforcement/payment fragments still `br-lan`-literal, and no fragment made
#      role-conditional. `mgmt` and `private` are networks this stack cannot
#      gate, and a network it cannot gate must not sell; `guest` is the one
#      network that has the gate. That is a property of the stack, not of this
#      option (see docs/architecture/lan-port-role-decision.md), and the tests
#      refuse both drift directions.
#
# How it checks: the shipped driver is copied into a temp dir (only its
# SETUP_FLAG and LOGFILE absolute paths are rewritten) and RUN against a fake
# `uci`, `apk`, `ip` and /etc/shadow — the harness
# tests/uci-defaults-mgmt-bridge_test.sh uses — so the assertions are about the
# config the shipped code actually generates, not about a grep of the source.
# Its fake `uci` models SECTION deletion properly (`delete network.mgmt` removes
# the section's options too), because this writer deletes sections: a shim that
# only removed the `network.mgmt=` line would let a half-deleted management
# bridge pass as removed. The last section mutates the writer out of a copy of
# the script and requires the assertions to FAIL against it, so every guard is
# proven to bite.
#
# Usage: bash tests/uci-defaults-lan-port-role_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT" || exit 1
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"
FIELD="packaging/files/etc/config/tollgate"
NFT_DIR="packaging/files/etc/nftables.d"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

# ---------------------------------------------------------------- fake apk
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

# ---------------------------------------------------------------- fake uci
# Flat-file stand-in: one "key=value" line per option, so a list option is one
# line per entry (which is how `uci get` renders a list).
#
# `delete` is SECTION-AWARE on purpose: real uci removes a section and every
# option in it, and this writer deletes whole sections (network.mgmt,
# dhcp.mgmt, ...). A shim whose delete only dropped the exact `<key>=` line
# would leave `network.mgmt.ipaddr=…` behind and every "the scaffolding is
# gone" assertion would read a half-deleted section as deleted — the exact class
# of vacuous pass this repo has been bitten by before (`del_list` in
# tests/uci-defaults-mgmt-bridge_test.sh).
export UCI_STATE="$TMP/uci.state"
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
values() { grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-; }
drop_key() { # remove <key>= and any <key>.*= — i.e. the section and its options
    awk -v k="$1" 'index($0, k "=") != 1 && index($0, k ".") != 1' "$state" > "$state.tmp" 2>/dev/null
    mv "$state.tmp" "$state"
}
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
        key="${key%%[[:space:]]*}"
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        printf '%s\n' "$1" >> "$state"
        ;;
    add_list)
        printf '%s\n' "$1" >> "$state"
        ;;
    add)
        printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state"
        ;;
    delete)
        drop_key "$1"
        ;;
    del_list)
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    show|export|commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"

# ----------------------------------------------------------------- fake ip
# `upstream_networks` reads the kernel for addresses that are not the LAN's, so
# the host's own interfaces must never be consulted (the test would otherwise
# depend on, and leak, the machine it runs on).
export FAKE_IP_UPSTREAM="$TMP/ip.upstream"
: > "$FAKE_IP_UPSTREAM"
cat > "$TMP/bin/ip" <<'SHIM'
#!/bin/sh
lan_ip="$(grep -F -- 'network.lan.ipaddr=' "$UCI_STATE" 2>/dev/null | cut -d= -f2- | tail -n1)"
[ -n "$lan_ip" ] || lan_ip="192.168.1.1"
case "$lan_ip" in
    */*) : ;;
    *) lan_ip="$lan_ip/24" ;;
esac
printf '1: lo    inet 127.0.0.1/8 scope host lo\n'
printf '2: br-lan    inet %s brd 192.168.1.255 scope global br-lan\n' "$lan_ip"
[ -s "$FAKE_IP_UPSTREAM" ] && cat "$FAKE_IP_UPSTREAM"
exit 0
SHIM
chmod +x "$TMP/bin/ip"

# ------------------------------------------------------------- fake hexdump
# The random-fallback address path draws octets with hexdump; the queue makes a
# draw deterministic.
export HEXDUMP_QUEUE_FILE="$TMP/hexdump.queue"
: > "$HEXDUMP_QUEUE_FILE"
cat > "$TMP/bin/hexdump" <<'SHIM'
#!/bin/sh
f="${HEXDUMP_QUEUE_FILE:-}"
if [ -n "$f" ] && [ -s "$f" ]; then
    val="$(head -n1 "$f")"
    tail -n +2 "$f" > "$f.tmp" 2>/dev/null && mv "$f.tmp" "$f"
    printf '%s\n' "$val"
    exit 0
fi
printf '%s\n' "${HEXDUMP_FIXED:-5a}"
SHIM
chmod +x "$TMP/bin/hexdump"

export PATH="$TMP/bin:$PATH"

# --------------------------------------------------- the script under test
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
    -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
    "$ROOT/$SCRIPT" > "$SCRIPT_UNDER_TEST"
if grep -q "^SETUP_FLAG=\"$FLAG\"\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^LOGFILE=$LOGFILE\$" "$SCRIPT_UNDER_TEST"; then
    ok "harness redirected SETUP_FLAG and LOGFILE in the copied script"
else
    bad "could not redirect SETUP_FLAG/LOGFILE in the copied setup script"
fi

# The driver also runs the admin-credential gate (reads the live /etc/shadow and
# would invoke `passwd root` on an empty hash) on the verify/repair path — pin it
# and the router home to fixtures so the test never touches the machine it runs on.
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd.db"
printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
: > "$PASSWD_FILE"
export ROUTER_HOME_DIR="$TMP/router-home"

# ------------------------------------------------------------- the fixtures
# A stock router: two radios, the stock guest APs, the captive bridge with its
# IMAGE-OWNED port list, one nodogsplash section pinned to the captive bridge,
# and the operator's private network (the /24 next to the LAN's, so the
# management bridge's address derivation has a neighbour to skip).
#   seed_router <ports> [<private-ipaddr>]
seed_router() {
    local ports="$1" private_ip="${2:-192.168.2.1}" p
    : > "$UCI_STATE"
    {
        printf '%s\n' 'wireless.radio0=wifi-device' 'wireless.radio0.band=2g'
        printf '%s\n' 'wireless.radio0.channel=1' 'wireless.radio0.disabled=0'
        printf '%s\n' 'wireless.radio0=wifi-device'
        printf '%s\n' 'wireless.radio1=wifi-device' 'wireless.radio1.band=5g'
        printf '%s\n' 'wireless.radio1.channel=36' 'wireless.radio1.disabled=0'
        printf '%s\n' 'wireless.default_radio0=wifi-iface' 'wireless.default_radio0.device=radio0'
        printf '%s\n' 'wireless.default_radio0.mode=ap' 'wireless.default_radio0.network=lan'
        printf '%s\n' 'wireless.default_radio1=wifi-iface' 'wireless.default_radio1.device=radio1'
        printf '%s\n' 'wireless.default_radio1.mode=ap' 'wireless.default_radio1.network=lan'
        printf '%s\n' 'network.lan=interface' 'network.lan.device=br-lan'
        printf '%s\n' 'network.lan.proto=static' 'network.lan.ipaddr=192.168.1.1/24'
        printf '%s\n' 'network.lan.netmask=255.255.255.0'
        printf '%s\n' 'network.@device[0]=device' 'network.@device[0].name=br-lan'
        printf '%s\n' 'network.@device[0].type=bridge'
        for p in $ports; do
            printf 'network.@device[0].ports=%s\n' "$p"
        done
        printf '%s\n' 'network.wan=interface' 'network.wan.device=eth0' 'network.wan.proto=dhcp'
        printf '%s\n' 'system.@system[0]=system' 'system.@system[0].hostname=TollGate'
        printf '%s\n' 'dhcp.@dnsmasq[0]=dnsmasq'
        # The one captive gate: the role field must never touch it.
        printf '%s\n' 'nodogsplash.@nodogsplash[0]=nodogsplash'
        printf '%s\n' "nodogsplash.@nodogsplash[0].gatewayinterface=br-lan"
        printf '%s\n' "nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 443"
        if [ -n "$private_ip" ]; then
            printf '%s\n' 'network.private=interface' 'network.private.proto=static'
            printf '%s\n' 'network.private.device=br-private'
            printf 'network.private.ipaddr=%s\n' "$private_ip"
            printf '%s\n' 'network.private.netmask=255.255.255.0'
            printf '%s\n' 'network.private_bridge=device' 'network.private_bridge.type=bridge'
            printf '%s\n' 'network.private_bridge.name=br-private'
            printf '%s\n' 'dhcp.private=dhcp' 'dhcp.private.interface=private'
            printf '%s\n' 'wireless.private_radio0=wifi-iface'
            printf '%s\n' 'wireless.private_radio0.network=private'
            printf '%s\n' 'firewall.private_zone=zone' 'firewall.private_zone.name=private'
            printf '%s\n' 'firewall.private_zone.network=private'
            printf '%s\n' 'firewall.private_zone.input=ACCEPT'
            printf '%s\n' 'firewall.private_zone.output=ACCEPT'
            printf '%s\n' 'firewall.private_zone.forward=ACCEPT'
            printf '%s\n' 'firewall.private_forwarding=forwarding'
            printf '%s\n' 'firewall.private_forwarding.src=private'
            printf '%s\n' 'firewall.private_forwarding.dest=wan'
        fi
    } >> "$UCI_STATE"
}

# The private network without its bridge device section: the one shape
# role=private must refuse rather than guess.
seed_no_private_bridge() {
    seed_router 'eth1'
    grep -v -F -- 'network.private_bridge' "$UCI_STATE" > "$UCI_STATE.tmp"
    mv "$UCI_STATE.tmp" "$UCI_STATE"
}

# A router whose captive bridge declares NO ports and which has no other bridge:
# the one case every role must refuse (a target with no ports is unreachable).
seed_portless() {
    : > "$UCI_STATE"
    {
        printf '%s\n' 'network.lan=interface' 'network.lan.device=br-lan'
        printf '%s\n' 'network.lan.proto=static' 'network.lan.ipaddr=192.168.1.1/24'
        printf '%s\n' 'network.lan.netmask=255.255.255.0'
        printf '%s\n' 'network.@device[0]=device' 'network.@device[0].name=br-lan'
        printf '%s\n' 'network.@device[0].type=bridge'
        printf '%s\n' 'network.private=interface' 'network.private.proto=static'
        printf '%s\n' 'network.private.device=br-private'
        printf '%s\n' 'network.private.ipaddr=192.168.2.1'
        printf '%s\n' 'network.private_bridge=device' 'network.private_bridge.name=br-private'
    } >> "$UCI_STATE"
}

# A router with no device section for the captive bridge at all (a hand-built
# config): role=guest must refuse.
seed_no_captive_section() {
    seed_router 'eth1'
    grep -v -F -- 'network.@device[0]' "$UCI_STATE" > "$UCI_STATE.tmp"
    mv "$UCI_STATE.tmp" "$UCI_STATE"
}

# The operator's field.
role_set() { printf 'tollgate.lan_ports.role=%s\n' "$1" >> "$UCI_STATE"; }
role_unset() { grep -v -F -- 'tollgate.' "$UCI_STATE" > "$UCI_STATE.tmp" 2>/dev/null; mv "$UCI_STATE.tmp" "$UCI_STATE"; }

count() { grep -F -c "$1" "$UCI_STATE" 2>/dev/null | tr -d ' '; }
has()   { grep -F -q "$1" "$UCI_STATE"; }
line()  { grep -F -x "$1" "$UCI_STATE" 2>/dev/null; }
no_upstream() { : > "$FAKE_IP_UPSTREAM"; }
draw() { printf '%s\n' "$@" > "$HEXDUMP_QUEUE_FILE"; }

# Run ONLY the writer, through the repo's LIB_ONLY seam: sources the shipped
# script (which returns before its own branch logic) and calls the entry point.
run_writer() {
    : > "$LOGFILE"
    # shellcheck disable=SC1090  # the path is built from $ROOT on purpose
    TOLLGATE_SETUP_LIB_ONLY=1 . "$SCRIPT_UNDER_TEST" >/dev/null 2>&1
    setup_mgmt_bridge >/dev/null 2>&1
    return $?
}

# The whole shipped script on the reinstall/upgrade (same-version) path — the
# path an already-installed router takes, and the one where a reset router's
# image-owned port list has to be repaired.
run_same_version() {
    printf '%s\n' "$FAKE_VERSION" > "$FLAG"
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >/dev/null 2>"$TMP/run.err"
    return $?
}

# --------------------------------------------------------------- assertions
ports_on() { # <section> <port> [<port> ...]
    local sec="$1" port want=0 n
    shift
    for port in "$@"; do
        want=$((want + 1))
        n=$(count "$sec.ports=$port")
        [ "$n" = 1 ] && ok "$sec lists $port exactly once" \
                     || bad "$sec lists $port $n time(s) (want 1)"
    done
}

no_ports_on() { # <section>...
    local sec
    for sec in "$@"; do
        if grep -qE "^$(printf '%s' "$sec" | sed 's/[][\.*^$]/\\&/g')\.ports=" "$UCI_STATE"; then
            bad "$sec still lists ports ('$(grep -E "^$(printf '%s' "$sec" | sed 's/[][\.*^$]/\\&/g')\.ports=" "$UCI_STATE" | tr '\n' ' ')') — the move must CLEAR every bridge that is not the target"
        else
            ok "$sec lists no ports"
        fi
    done
}

mgmt_scaffolding_absent() { # <label>
    local label="$1" stray
    stray=$(grep -E '^(network\.mgmt|network\.mgmt_bridge|dhcp\.mgmt|firewall\.mgmt_zone)(=|\.)' "$UCI_STATE" 2>/dev/null || true)
    if [ -z "$stray" ]; then
        ok "$label: the management bridge's scaffolding is gone (no portless br-mgmt is left behind)"
    else
        bad "$label: the management bridge was left behind: $(printf '%s ' $stray)"
    fi
}

mgmt_scaffolding_present() { # <label>
    local label="$1" missing="" key
    for key in 'network.mgmt=interface' 'network.mgmt.device=br-mgmt' \
               'network.mgmt_bridge=device' 'network.mgmt_bridge.name=br-mgmt' \
               'dhcp.mgmt.interface=mgmt' 'firewall.mgmt_zone.name=mgmt' \
               'firewall.mgmt_zone.forward=REJECT'; do
        has "$key" || missing="$missing $key"
    done
    if [ -z "$missing" ]; then
        ok "$label: the management bridge is configured in full"
    else
        bad "$label: missing$missing"
    fi
}

nothing_written_for_roles() { # <label> — no bridge config was invented
    local label="$1" stray
    stray=$(grep -E '^(network\.mgmt|network\.mgmt_bridge|dhcp\.mgmt|firewall\.mgmt_zone|network\.private_bridge\.ports)(=|\.)' "$UCI_STATE" 2>/dev/null || true)
    if [ -z "$stray" ]; then
        ok "$label: nothing was written"
    else
        bad "$label: the refused run still wrote: $(printf '%s ' $stray)"
    fi
}

# ===========================================================================
# 1. the field itself — where it lives, and what it ships as
# ===========================================================================
echo "== the role field is a shipped, operator-editable uci option"
if [ -f "$FIELD" ]; then
    ok "the uci namespace $(basename "$(dirname "$FIELD")")/$(basename "$FIELD") is in the tree"
else
    bad "$FIELD is missing — there is no field for 99-tollgate-setup to read"
fi
if [ -f "$FIELD" ] && grep -qE "^config lan_ports 'lan_ports'\$" "$FIELD"; then
    ok "the field declares the named section 'lan_ports' (so uci get tollgate.lan_ports.role addresses it)"
else
    bad "the field has no 'config lan_ports' section"
fi
if [ -f "$FIELD" ] && grep -qE "^[[:space:]]*option role 'mgmt'\$" "$FIELD"; then
    ok "the shipped default is role 'mgmt' (the br-mgmt placement, i.e. PR #601's)"
else
    bad "the field's shipped default is not 'mgmt'"
fi
# The default in the file and the default in the writer must be the same string:
# two defaults that disagreed would make the field's own documentation wrong.
file_default=$(sed -n "s/^[[:space:]]*option role '\(.*\)'\$/\1/p" "$FIELD" 2>/dev/null | head -n1)
code_default=$(sed -n "s/^LAN_PORTS_ROLE_DEFAULT='\(.*\)'\$/\1/p" "$ROOT/$SCRIPT" | head -n1)
[ -n "$file_default" ] && [ "$file_default" = "$code_default" ] \
    && ok "the shipped default ('$file_default') is the writer's default too" \
    || bad "the field's default ('$file_default') and the writer's ('$code_default') disagree"
# Every documented value must be one the writer accepts, and vice versa.
for v in $(sed -n "s/^[[:space:]]*option role '\(.*\)'\$/\1/p" "$FIELD"); do
    case "$v" in
        mgmt|private|guest) ok "the shipped default '$v' is a role the writer implements" ;;
        *) bad "'$v' is documented in the field but is not a role" ;;
    esac
done

echo "== both packaging recipes install it"
if grep -qE '^[[:space:]]*\$\(INSTALL_CONF\)[[:space:]]+.*files/etc/config/tollgate[[:space:]]+\$\(1\)/etc/config/tollgate' packaging/Makefile; then
    ok "packaging/Makefile installs etc/config/tollgate (INSTALL_CONF)"
else
    bad "packaging/Makefile does not install the field — a file under packaging/files/ that no recipe installs is not a control"
fi
if grep -qE '^install -D -m [0-7]+ packaging/files/etc/config/tollgate[[:space:]]+"\$PAYLOAD/etc/config/tollgate"' packaging/local-build-ipk.sh; then
    ok "packaging/local-build-ipk.sh stages the field into the payload"
else
    bad "packaging/local-build-ipk.sh does not stage the field"
fi
if grep -qE '^[[:space:]]*install -D -m [0-7]+ packaging/files/etc/config/tollgate[[:space:]]+"\$PAYLOAD/etc/config/tollgate"' .github/workflows/build-package.yml; then
    ok "the release lane (build-package.yml) stages the field into its payload"
else
    bad "the release lane does not stage the field — a release-built .apk would ship no /etc/config/tollgate"
fi
if grep -q '^	/etc/config/tollgate \\$' packaging/Makefile; then
    ok "the field is in the package's file manifest (FILES_$(basename "$ROOT"))"
else
    bad "the field is missing from the package's FILES_ manifest"
fi
# The setting is the operator's: an upgrade must not silently reset the cable to
# another bridge, so the file is in the sysupgrade keep list.
if grep -q '^/etc/config/tollgate$' packaging/files/lib/upgrade/keep.d/tollgate; then
    ok "the field survives sysupgrade (listed in lib/upgrade/keep.d/tollgate)"
else
    bad "the field is not in the sysupgrade keep list — an upgrade would reset the role"
fi

echo "== the writer reads the field in exactly one place"
n_read=$(grep -c 'lan_ports_role' "$SCRIPT")
[ "$n_read" = 2 ] && ok "lan_ports_role is defined once and called once (the dispatcher)" \
                  || bad "lan_ports_role appears $n_read time(s) (want 2: one definition, one call site)"
if grep -qE 'uci (get|-q get) tollgate\.lan_ports\.role' "$SCRIPT"; then
    ok "the option read is tollgate.lan_ports.role"
else
    bad "the writer does not read tollgate.lan_ports.role"
fi

# ===========================================================================
# 2. role=mgmt (and the default) — the placement PR #601 built
# ===========================================================================
echo "== default: with no tollgate config at all, the ports land on br-mgmt"
seed_router 'eth1'
role_unset
no_upstream
draw 5a 5a
run_writer
rc=$?
[ "$rc" = 0 ] && ok "the default run exits 0" || bad "the default run exited $rc"
ports_on network.mgmt_bridge eth1
no_ports_on 'network.@device[0]' network.private_bridge
mgmt_scaffolding_present "default role"
grep -q 'Wired LAN ports moved from br-lan to br-mgmt' "$LOGFILE" \
    && ok "the default placement is recorded as a move from the captive bridge" \
    || bad "the default placement is not recorded"

echo "== an EMPTY value is the default, not an error (an option added later must not break an installed router)"
seed_router 'eth1'
role_set ''
no_upstream
draw 5a 5a
run_writer
rc=$?
[ "$rc" = 0 ] && ok "an empty role exits 0" || bad "an empty role exited $rc"
ports_on network.mgmt_bridge eth1
mgmt_scaffolding_present "empty role"

echo "== role=mgmt says so explicitly, and is idempotent"
seed_router 'eth1'
role_set mgmt
no_upstream
draw 5a 5a
run_writer
ports_on network.mgmt_bridge eth1
no_ports_on 'network.@device[0]'
: > "$LOGFILE"
run_writer
ports_on network.mgmt_bridge eth1
[ "$(count 'network.mgmt_bridge=device')" = 1 ] \
    && ok "idempotent: network.mgmt_bridge declared once" \
    || bad "idempotent: network.mgmt_bridge declared $(count 'network.mgmt_bridge=device') time(s)"
grep -q 'Wired LAN ports already on br-mgmt' "$LOGFILE" \
    && ok "the converged mgmt run says so instead of moving anything again" \
    || bad "the converged mgmt run did not report the already-placed state"

# ===========================================================================
# 3. role=private — the cable joins the operator's trusted network
# ===========================================================================
echo "== role=private: the wired ports move onto br-private, free and trusted"
seed_router 'eth1'
role_set private
no_upstream
draw 5a 5a
run_writer
rc=$?
[ "$rc" = 0 ] && ok "the private run exits 0" || bad "the private run exited $rc"
ports_on network.private_bridge eth1
no_ports_on 'network.@device[0]' network.mgmt_bridge
mgmt_scaffolding_absent "private role"
grep -q 'Wired LAN ports moved from br-lan to br-private' "$LOGFILE" \
    && ok "the private placement is recorded as a move from the captive bridge" \
    || bad "the private placement is not recorded"
# The private network is REUSED, not rebuilt: its own zone keeps its forwarding
# to the wan (that is what makes the role "trusted, free") and its address.
for key in 'firewall.private_zone.network=private' \
           'firewall.private_zone.forward=ACCEPT' \
           'firewall.private_forwarding.dest=wan' \
           'network.private.ipaddr=192.168.2.1' \
           'dhcp.private.interface=private'; do
    has "$key" && ok "the private role leaves '$key' alone" \
                || bad "the private role changed or dropped '$key'"
done

echo "== role=private is idempotent"
: > "$LOGFILE"
run_writer
ports_on network.private_bridge eth1
grep -q 'Wired LAN ports already on br-private' "$LOGFILE" \
    && ok "the converged private run says so" \
    || bad "the converged private run did not report the already-placed state"

echo "== role=private with a multi-port device moves every port"
seed_router 'lan1 lan2 lan3'
role_set private
run_writer
ports_on network.private_bridge lan1 lan2 lan3
no_ports_on 'network.@device[0]'

echo "== role=private refuses when there is no private network to attach to"
seed_no_private_bridge
role_set private
run_writer
rc=$?
[ "$rc" != 0 ] && ok "a router with no private bridge returns non-zero" \
               || bad "role=private returned 0 with no private network"
grep -q "ERROR: skipping the wired-LAN bridge move — role='private'" "$LOGFILE" \
    && ok "the refusal is logged as an ERROR" \
    || bad "the refusal is not logged (an operator would see nothing)"
nothing_written_for_roles "private role with no private bridge"
line 'network.@device[0].ports=eth1' >/dev/null \
    && ok "the refused run left the ports on the captive bridge (the cable still works)" \
    || bad "the refused run moved ports it could not place"

# ===========================================================================
# 4. role=guest — the cable is a customer port again
# ===========================================================================
echo "== role=guest: the wired ports stay on / go back to the CAPTIVE bridge"
seed_router 'eth1'
role_set guest
no_upstream
run_writer
rc=$?
[ "$rc" = 0 ] && ok "the guest run exits 0" || bad "the guest run exited $rc"
ports_on 'network.@device[0]' eth1
no_ports_on network.mgmt_bridge network.private_bridge
mgmt_scaffolding_absent "guest role"
grep -q 'Wired LAN ports already on br-lan' "$LOGFILE" \
    && ok "an already-captive router is reported as converged, not moved" \
    || bad "the guest run on a captive router did not report the converged state"

echo "== role=guest moves the ports back from where another role left them"
seed_router 'eth1'
role_set private
run_writer
role_unset
role_set guest
: > "$LOGFILE"
run_writer
ports_on 'network.@device[0]' eth1
no_ports_on network.private_bridge network.mgmt_bridge
grep -q 'Wired LAN ports moved from br-private to br-lan' "$LOGFILE" \
    && ok "the move back to the captive bridge names br-private as the source" \
    || bad "the move back to the captive bridge is not recorded correctly"

echo "== role=guest refuses when the config has no device section for the captive bridge"
seed_no_captive_section
role_set guest
run_writer
rc=$?
[ "$rc" != 0 ] && ok "a missing captive device section returns non-zero" \
               || bad "role=guest returned 0 with no device section for the captive bridge"
grep -q "ERROR: skipping the wired-LAN bridge move — role='guest'" "$LOGFILE" \
    && ok "the refusal is logged as an ERROR" \
    || bad "the guest refusal is not logged"
nothing_written_for_roles "guest role with no captive device section"

# ===========================================================================
# 5. the transitions — a role change carries the ports, and only the ports
# ===========================================================================
echo "== mgmt -> private: the ports leave br-mgmt and br-mgmt is cleaned up"
seed_router 'eth1'
role_set mgmt
no_upstream
draw 5a 5a
run_writer
ports_on network.mgmt_bridge eth1
mgmt_scaffolding_present "mgmt step"
role_unset
role_set private
: > "$LOGFILE"
run_writer
ports_on network.private_bridge eth1
no_ports_on network.mgmt_bridge 'network.@device[0]'
mgmt_scaffolding_absent "mgmt -> private"
grep -q 'Wired LAN ports moved from br-mgmt to br-private' "$LOGFILE" \
    && ok "the move is recorded as leaving br-mgmt" \
    || bad "the mgmt -> private move is not recorded correctly"

echo "== private -> mgmt: the ports come back, and the bridge is rebuilt"
seed_router 'eth1'
role_set private
run_writer
ports_on network.private_bridge eth1
role_unset
role_set mgmt
no_upstream
draw 5a 5a
: > "$LOGFILE"
run_writer
rc=$?
[ "$rc" = 0 ] && ok "the private -> mgmt run exits 0" \
              || bad "the private -> mgmt run exited $rc — the ports on br-private must be found, not refused"
ports_on network.mgmt_bridge eth1
no_ports_on network.private_bridge 'network.@device[0]'
mgmt_scaffolding_present "private -> mgmt"
grep -q 'Wired LAN ports moved from br-private to br-mgmt' "$LOGFILE" \
    && ok "the move back names br-private as the source" \
    || bad "the private -> mgmt move is not recorded correctly"

echo "== a factory reset / sysupgrade -n is repaired for a non-default role too"
seed_router 'eth1'
role_set private
run_writer
# The base image puts the port back on the captive bridge on the next boot.
printf 'network.@device[0].ports=eth1\n' >> "$UCI_STATE"
: > "$LOGFILE"
run_writer
ports_on network.private_bridge eth1
no_ports_on 'network.@device[0]'
grep -q 'Wired LAN ports moved from br-lan to br-private' "$LOGFILE" \
    && ok "the reset port is moved onto the role's bridge again" \
    || bad "the reset port was not re-placed"
[ "$(count 'network.private_bridge.ports=eth1')" = 1 ] \
    && ok "the repair did not duplicate the port" \
    || bad "the repair duplicated the port ($(count 'network.private_bridge.ports=eth1') entries)"

# ===========================================================================
# 6. the refusals — an input this writer cannot honour moves nothing
# ===========================================================================
echo "== an unrecognised role is refused, loudly, and nothing moves"
for bad_role in br-lan mgmt,private MGMT ''; do
    [ -n "$bad_role" ] || continue
    seed_router 'eth1'
    role_set "$bad_role"
    run_writer
    rc=$?
    [ "$rc" != 0 ] && ok "role='$bad_role' returns non-zero" \
                   || bad "role='$bad_role' returned 0 — this writer never guesses a bridge"
    grep -q "ERROR: skipping the wired-LAN bridge move — tollgate.lan_ports.role='$bad_role'" "$LOGFILE" \
        && ok "role='$bad_role' is refused with an ERROR naming the value" \
        || bad "role='$bad_role' is refused without naming the value in the log"
    nothing_written_for_roles "role='$bad_role'"
    line 'network.@device[0].ports=eth1' >/dev/null \
        && ok "role='$bad_role' left the ports on the captive bridge" \
        || bad "role='$bad_role' moved the ports anyway"
done

echo "== a router with no wired port at all is refused, whatever the role asks for"
for r in mgmt private guest; do
    seed_portless
    role_set "$r"
    run_writer
    rc=$?
    [ "$rc" != 0 ] && ok "role=$r on a portless router returns non-zero" \
                   || bad "role=$r on a portless router returned 0"
    grep -q 'ERROR: skipping the wired-LAN' "$LOGFILE" \
        && ok "role=$r on a portless router logs an ERROR" \
        || bad "role=$r on a portless router is silent"
    nothing_written_for_roles "portless router, role=$r"
done

# ===========================================================================
# 7. what must NOT move with the ports — one gate, and br-lan-literal guards
# ===========================================================================
echo "== one gate, and it stays the captive bridge, whatever the role says"
n_add=$(grep -c "uci add nodogsplash nodogsplash" "$SCRIPT")
[ "$n_add" = 1 ] && ok "one writer creates the nodogsplash section" \
                 || bad "$n_add writers create a nodogsplash section (want 1 — a second instance breaks the first)"
n_gw=$(grep -c "uci set nodogsplash.@nodogsplash\[0\].gatewayinterface='br-lan'" "$SCRIPT")
[ "$n_gw" = 1 ] && ok "the gateway interface is written once, as br-lan" \
                || bad "$n_gw writers of gatewayinterface='br-lan' (want 1)"
stray_gw=$(grep -nE "uci set nodogsplash\.[^ ]*gatewayinterface" "$SCRIPT" |
           grep -v "nodogsplash\.@nodogsplash\[0\]\.gatewayinterface='br-lan'" || true)
if [ -z "$stray_gw" ]; then
    ok "no other section is ever given a gateway interface"
else
    bad "a gateway interface is written outside @nodogsplash[0]: $stray_gw"
fi
if grep -qE "gatewayinterface.*(br-mgmt|br-private)|(br-mgmt|br-private).*gatewayinterface" "$SCRIPT"; then
    bad "a role's bridge was given a captive gate — one nodogsplash cannot gate two bridges"
else
    ok "no role's bridge is given a captive gate"
fi

echo "== running any role leaves the running nodogsplash section untouched"
# setup_nodogsplash writes the section once (full setup); the writer under test
# must not touch it, and the fixture's seeded value is the proof.
seed_router 'eth1'
role_set private
run_writer
[ "$(line 'nodogsplash.@nodogsplash[0].gatewayinterface=br-lan' | wc -l)" = 1 ] \
    && ok "role=private: gatewayinterface is still br-lan, exactly once" \
    || bad "role=private disturbed the nodogsplash gateway interface"

echo "== the guards and the enforcement/payment fragments keep their scope"
# Only the RULE lines count here: these fragments document the trust boundary in
# prose (31-*.nft and 32-*.nft both explain in their headers that the management
# path is br-private and why), so a grep of the whole file would read a comment
# as a rule. A rule line is one whose first non-space character is not '#'.
rules() { grep -E '^[[:space:]]*[^#[:space:]].*iifname' "$1" 2>/dev/null; }
for f in 20-nds-enforce.nft 30-backend-firewall.nft 31-admin-board-not-guest-reachable.nft 32-luci-not-guest-reachable.nft; do
    if [ ! -f "$NFT_DIR/$f" ]; then
        bad "$f is missing"
        continue
    fi
    if rules "$NFT_DIR/$f" | grep -q 'br-lan'; then
        ok "$f's rules still match the CAPTIVE bridge by name"
    else
        bad "$f no longer names br-lan in a rule — it would stop covering the captive network"
    fi
    if rules "$NFT_DIR/$f" | grep -qE 'br-mgmt|br-private'; then
        bad "$f has a RULE on a role's bridge ($(rules "$NFT_DIR/$f" | grep -oE 'br-(mgmt|private)' | sort -u | tr '\n' ' ')) — reaching that bridge means plugging in a cable, not joining an open SSID"
    else
        ok "$f has no rule on a role's bridge"
    fi
done
if rules "$NFT_DIR/33-mgmt-bridge-scope.nft" | grep -q 'br-mgmt' &&
   ! rules "$NFT_DIR/33-mgmt-bridge-scope.nft" | grep -qE '"br-lan"|"br-private"'; then
    ok "33-mgmt-bridge-scope.nft is scoped to br-mgmt only in its rules (and is inert while the ports are elsewhere)"
else
    bad "33-mgmt-bridge-scope.nft's rules are not scoped to br-mgmt alone"
fi
# The payment API stays unreachable from every role's bridge except the captive
# one: a network this module cannot gate must not take money it cannot deliver.
if grep -qE 'iifname != \{ "br-lan", "lo" \} tcp dport 2121 counter drop' "$NFT_DIR/30-backend-firewall.nft"; then
    ok "the payment API (:2121) is still br-lan/loopback only"
else
    bad "the :2121 lock no longer reads br-lan/loopback only — a role's bridge could reach the payment API"
fi
if grep -rl 'lan_ports' "$NFT_DIR" 2>/dev/null | grep -q .; then
    bad "a fragment is role-conditional: $(grep -rl 'lan_ports' "$NFT_DIR" | tr '\n' ' ')"
else
    ok "no fragment is role-conditional (the field configures bridges, not rules)"
fi

echo "== both setup paths reach the dispatcher"
if grep -qE '^setup_mgmt_bridge .*$' "$SCRIPT" && grep -qE '^    setup_mgmt_bridge$' "$SCRIPT"; then
    ok "the full-setup path and the verify/repair path both call the role dispatcher"
else
    bad "one of the two setup paths does not reach the role dispatcher"
fi
grep -qE '^setup_private_network .*$' "$SCRIPT" && \
    ok "the private network is written before the role runs (role=private needs its bridge)" \
    || bad "setup_private_network is not called before the role dispatcher"

# ===========================================================================
# 8. the reinstall/upgrade path, end to end
# ===========================================================================
echo "== the same-version (reinstall/upgrade) path places the ports per the role"
seed_router 'eth1'
role_set private
no_upstream
run_same_version
rc=$?
[ "$rc" = 0 ] && ok "same-version run exits 0" \
              || bad "same-version run exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
ports_on network.private_bridge eth1
no_ports_on 'network.@device[0]'
mgmt_scaffolding_absent "same-version, role=private"
grep -q 'network config re-asserted' "$LOGFILE" \
    && ok "the repaired network config is committed and recorded" \
    || bad "the same-version path did not commit the re-placed ports"

# ===========================================================================
# 9. RED controls
# ===========================================================================
echo "== RED controls: each property must fail when its line is removed"
# Build a copy of the SHIPPED script with one fixed string removed. The copy gets
# the same SETUP_FLAG/LOGFILE rewrite as the script under test: a mutant that kept
# the real paths would find no marker on this machine, take the FULL-setup branch,
# and write to the host's /etc/profile.
make_mutant() { # <out> <fixed string to remove>; non-zero when it removed nothing
    local out="$1" strip="$2"
    # A strip string that is not in the shipped script removes NOTHING, and a
    # control that removed nothing always "passes"; and a strip that leaves the
    # script unparseable tests a syntax error, not the property. Both are checked
    # here so a broken control is reported as broken instead of as a pass.
    if ! grep -q -F -- "$strip" "$ROOT/$SCRIPT"; then
        return 1
    fi
    sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
        -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
        "$ROOT/$SCRIPT" | grep -v -F -- "$strip" > "$out"
    grep -q -F -- "$strip" "$out" && return 1
    sh -n "$out" || return 1
    return 0
}

# Each probe runs in a SUBSHELL: a mutant can make the sourced writer abort (an
# unbound variable, a broken branch), and an abort inside a function of a sourced
# file takes the whole shell with it. In a subshell the worst case is one lost
# control, never a lost suite — and a control that produced nothing is reported
# as a failure rather than counted as red.
red_probe() { # <mutant> <check-fn> <label>
    local mutant="$1" checkf="$2" label="$3" fails delta
    fails=$( (
        SCRIPT_UNDER_TEST="$mutant"
        seed_router 'eth1'
        role_set private
        no_upstream
        draw 5a 5a
        run_writer
        delta=$FAIL
        "$checkf"
        printf '%s\n' "$((FAIL - delta))"
    ) 2>/dev/null | tail -n1 )
    case "${fails:-}" in
        ''|*[!0-9]*)
            bad "RED: $label — the probe produced no result (the mutated script aborted), so the control is not evidence"
            ;;
        0)
            bad "RED: $label — the guard passed against a script without it, so it tests nothing"
            ;;
        *)
            ok "RED: $label — the guard fails without it ($fails assertions red)"
            ;;
    esac
}

MUTANT_COPY="$TMP/99-tollgate-setup.mutant"

# (a) role=private REACHES its own placement: without the dispatch arm the role
#     falls through to the refusal and the ports never leave the captive bridge.
check_red_placed_on_private() {
    ports_on network.private_bridge eth1
    no_ports_on 'network.@device[0]'
}
if make_mutant "$MUTANT_COPY" '        private) setup_lan_private ;;'; then
    red_probe "$MUTANT_COPY" check_red_placed_on_private "the private role is dispatched to its own placement"
else
    bad "RED control (a): could not strip the private dispatch arm — the control is vacuous"
fi

# (b) the mover CLEARS the bridges that are not the target: without it the port
#     is on br-private and on the captive bridge at once.
check_red_source_cleared() {
    no_ports_on 'network.@device[0]'
}
if make_mutant "$MUTANT_COPY" 'uci -q delete "$s.ports"'; then
    red_probe "$MUTANT_COPY" check_red_source_cleared "the non-target bridge is cleared, not copied"
else
    bad "RED control (b): could not strip the mover's clearing — the control is vacuous"
fi

# (c) the management bridge is REMOVED when the role moves away from it, or a
#     portless br-mgmt is left reporting a bridge with no way in. The fixture has
#     to BE on br-mgmt first: this control is about the cleanup on the way out.
if make_mutant "$MUTANT_COPY" '    remove_mgmt_bridge'; then
    seed_router 'eth1'
    role_set mgmt
    no_upstream
    draw 5a 5a
    run_writer
    role_unset
    role_set private
    : > "$LOGFILE"
    fails=$( (
        SCRIPT_UNDER_TEST="$MUTANT_COPY"
        run_writer
        delta=$FAIL
        mgmt_scaffolding_absent "RED"
        printf '%s\n' "$((FAIL - delta))"
    ) 2>/dev/null | tail -n1 )
    case "${fails:-}" in
        ''|*[!0-9]*) bad "RED: the management bridge is removed when the ports leave it — the probe produced no result" ;;
        0)           bad "RED: the management bridge is removed when the ports leave it — the guard passed against a script without it, so it tests nothing" ;;
        *)           ok "RED: the management bridge is removed when the ports leave it ($fails assertions red)" ;;
    esac
else
    bad "RED control (c): could not strip the cleanup call — the control is vacuous"
fi

# (d) the UNION: without br-private's ports in it, a switch back from the private
#     role refuses instead of converging (the ports are on a bridge the writer no
#     longer looks at).
if make_mutant "$MUTANT_COPY" '"$private_ports" | grep -v'; then
    seed_router 'eth1'
    role_set private
    no_upstream
    run_writer
    role_unset
    role_set mgmt
    no_upstream
    draw 5a 5a
    fails=$( (
        SCRIPT_UNDER_TEST="$MUTANT_COPY"
        run_writer
        delta=$FAIL
        mgmt_scaffolding_present "RED"
        printf '%s\n' "$((FAIL - delta))"
    ) 2>/dev/null | tail -n1 )
    case "${fails:-}" in
        ''|*[!0-9]*) bad "RED: the union carries the ports across a role change — the probe produced no result" ;;
        0)           bad "RED: the union passed against a script without it, so it tests nothing" ;;
        *)           ok "RED: the union carries the ports across a role change ($fails assertions red)" ;;
    esac
else
    bad "RED control (d): could not strip the union — the control is vacuous"
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
