#!/usr/bin/env bash
# Offline tests for the `entry_ui` mapping of
# packaging/files/etc/uci-defaults/99-tollgate-setup and the D4 marker gate.
#
# The contract (docs/architecture/default-ui-and-entry-port-decision.md, D1-D4):
#
#   * one flat scalar `entry_ui ∈ {board, luci}` in /etc/tollgate/config.json
#     decides which UI answers the ENTRY pair (:8080 + :443) and which answers
#     the SECONDARY pair (:8090 + :8443). The port SETS never change — only
#     which uhttpd section binds which pair;
#   * the switch FAILS TOWARD `board`: a missing config.json, a missing key, an
#     empty or unparseable value and an absent jq all resolve to `board`, and no
#     value may leave an admin UI with no listener (invariant 6);
#   * D4: `board` is honoured only when the mode-aware 92-tollgate-admin-setup
#     announced the mapping it applied (a marker; overridable through
#     TOLLGATE_ENTRY_UI_MARKER, content = the resolved value). Without it, 99
#     repairs to the legacy `luci` mapping — today's — and logs exactly one
#     WARNING naming the feed re-vendor;
#   * invariant 3 stays assertable: exactly one uhttpd section lists each of
#     :8080, :443, :8090, :8443.
#
# Offline: no router, no SDK, no network. The shipped driver runs against a fake
# uci/apk/passwd/hexdump, a fake tollgate CLI and fake service init scripts, in
# a temp sandbox — the same seam tests/uci-defaults-admin-tls-identity_test.sh
# and tests/packaging/configui-8090-single-owner_test.sh use. Nothing on the
# host is touched.
#
# The default script under test is the shipped one; TOLLGATE_SETUP_SCRIPT points
# the same suite at another copy, which is how the RED side is reproducible:
#   git show upstream/main:packaging/files/etc/uci-defaults/99-tollgate-setup \
#       > ../99-parent
#   TOLLGATE_SETUP_SCRIPT=../99-parent bash tests/uci-defaults-entry-ui-mapping_test.sh
#
# Usage: bash tests/uci-defaults-entry-ui-mapping_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
SCRIPT="${TOLLGATE_SETUP_SCRIPT:-packaging/files/etc/uci-defaults/99-tollgate-setup}"
SHIPPED_VERSION="v0.6.0-alpha4"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/init.d" "$TMP/etc/tollgate"

# ------------------------------------------------------------- harness paths
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
ENTRY_UI_CONFIG="$TMP/etc/tollgate/config.json"
ENTRY_UI_MARKER="$TMP/etc/tollgate/entry-ui-mapping"
UHTTPD_INIT="$TMP/init.d/uhttpd"
NDS_INIT="$TMP/init.d/nodogsplash"
TOLLGATE_CLI="$TMP/bin/tollgate"
PROVISIONED_CERT="$TMP/etc/tollgate/ssl/server.crt"
PROVISIONED_KEY="$TMP/etc/tollgate/ssl/server.key"
UHTTPD_IMAGE_CERT="$TMP/etc/uhttpd.crt"
UHTTPD_IMAGE_KEY="$TMP/etc/uhttpd.key"
OPTOUT_FILE="$TMP/etc/tollgate/ssl/tls-identity-removed"
# An identity the OPERATOR installed and owns: uhttpd.main already presents it
# and the fake CLI says it covers this router.
OPERATOR_CERT="$TMP/etc/operator-ca.crt"
OPERATOR_KEY="$TMP/etc/operator-ca.key"
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
ADMIN_HOME='/www/tollgate'

export UCI_STATE="$TMP/uci.state"
export CLI_CALLS="$TMP/cli-calls"
export UHTTPD_CALLS="$TMP/uhttpd-calls"
export FAKE_COVERAGE="$TMP/coverage"
export UHTTPD_RUNNING="$TMP/uhttpd-running"
export TOLLGATE_ENTRY_UI_MARKER="$ENTRY_UI_MARKER"
export ENTRY_UI_JQ_BIN="$TMP/bin/jq"
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd"
export ROUTER_HOME_DIR="$TMP/router-home"
export DEVICE_CODE_STORE="$TMP/etc/tollgate"
export PROVISIONED_CERT PROVISIONED_KEY OPTOUT_FILE OPERATOR_CERT OPERATOR_KEY

# ---------------------------------------------------------------- fake uci
# Flat-file stand-in: one "key=value" line per option. `delete <section>` drops
# the section AND its options (a shim that only dropped the section line would
# make the deletion assertions pass vacuously).
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
    add_list) printf '%s\n' "$1" >> "$state" ;;
    del_list)
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    add) printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state" ;;
    delete)
        grep -v -F -e "$1=" -e "$1." "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    show|export) cat "$state" ;;
    commit|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"

# --------------------------------------------------------------- fake jq
# The one query the reader uses. A missing file, a missing key and a non-JSON
# body all print nothing — exactly what `jq -r '.entry_ui // empty'` does — so
# the shim cannot flatter the fail-closed matrix.
cat > "$TMP/bin/jq" <<'SHIM'
#!/bin/sh
[ "${1:-}" = "-r" ] && shift
expr="${1:-}"; shift || true
file="${1:-}"
case "$expr" in
    ".entry_ui // empty")
        sed -n 's/.*"entry_ui"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$file" 2>/dev/null | head -n1
        ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/jq"

# --------------------------------------------------------------- fake apk
cat > "$TMP/bin/apk" <<'SHIM'
#!/bin/sh
if [ "${1:-}" = "list" ]; then
    printf '%s\n' "tollgate-wrt-${FAKE_APK_VERSION:-0.6.0_alpha4-r1} aarch64_cortex-a53 {tollgate-wrt} (GPL-3.0-only) [installed]"
fi
exit 0
SHIM
chmod +x "$TMP/bin/apk"

# -------------------------------------------------------------- fake passwd
export PASSWD_LOG="$TMP/passwd.log"
cat > "$TMP/bin/passwd" <<'SHIM'
#!/bin/sh
printf '%s\n' "$*" >> "${PASSWD_LOG:?}"
IFS= read -r p1 || p1=""
IFS= read -r p2 || p2=""
[ -n "$p1" ] || exit 1
printf '%s\n' "$p1" > "$PASSWD_LOG.seen"
awk -F: -v OFS=: -v h='$1$stub$abcdefghijklmnopqrstuv' \
    '$1 == "root" { $2 = h } { print }' "${SHADOW_FILE:?}" > "${SHADOW_FILE}.tmp" 2>/dev/null
mv "${SHADOW_FILE}.tmp" "${SHADOW_FILE}"
exit 0
SHIM
chmod +x "$TMP/bin/passwd"

# ------------------------------------------------------------- fake hexdump
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

# -------------------------------------------------------------- fake pidof
cat > "$TMP/bin/pidof" <<'SHIM'
#!/bin/sh
case "${1:-}" in
    uhttpd)      [ -f "${UHTTPD_RUNNING:?}" ] ;;
    nodogsplash) exit 1 ;;
    *)           exit 1 ;;
esac
SHIM
chmod +x "$TMP/bin/pidof"

# ------------------------------------------------------------ fake iptables
cat > "$TMP/bin/iptables" <<'SHIM'
#!/bin/sh
[ "${1:-}" = "-S" ] && exit 1
exit 0
SHIM
chmod +x "$TMP/bin/iptables"

PATH="$TMP/bin:$PATH"
export PATH

# --------------------------------------------------------- fake tollgate CLI
# Only the verbs the setup path uses. `ssl covers` treats the OPERATOR pair as
# covering this router and the image placeholder as covering nothing — the
# contract of the real x509 SAN check, with coverage decided by path exactly the
# way the real check decides it by parsing.
cat > "$TOLLGATE_CLI" <<'SHIM'
#!/bin/sh
printf '%s\n' "$*" >> "${CLI_CALLS:?}"
sub="${1:-}"
shift || true
case "$sub" in
    ssl)
        verb="${1:-}"
        shift || true
        case "$verb" in
            covers)
                cert="${1:-}"
                [ -n "$cert" ] || exit 1
                [ -r "$cert" ] && [ -s "$cert" ] || exit 1
                case "$cert" in
                    "${OPERATOR_CERT:-NO}"|"${PROVISIONED_CERT:-NO}") ;;
                    *) exit 1 ;;
                esac
                [ "$(cat "${FAKE_COVERAGE:?}" 2>/dev/null)" = covering ] || exit 1
                echo "covers: yes — SANs cover this router"
                exit 0
                ;;
            apply)
                mkdir -p "$(dirname "$PROVISIONED_CERT")"
                printf '%s\n' 'PROVISIONED-CERT-FIXTURE' > "$PROVISIONED_CERT"
                printf '%s\n' 'PROVISIONED-KEY-FIXTURE' > "$PROVISIONED_KEY"
                printf 'covering\n' > "${FAKE_COVERAGE:?}"
                uci set "uhttpd.main.cert=$PROVISIONED_CERT"
                uci set "uhttpd.main.key=$PROVISIONED_KEY"
                uci commit uhttpd
                exit 0
                ;;
            status)
                [ -s "${PROVISIONED_CERT:-/nonexistent}" ] && echo "SSL: configured" || echo "SSL: not configured"
                exit 0
                ;;
            remove)
                rm -f "$PROVISIONED_CERT" "$PROVISIONED_KEY"
                mkdir -p "$(dirname "${OPTOUT_FILE:?}")"
                printf 'TLS identity removed by an operator\n' > "$OPTOUT_FILE"
                printf 'placeholder\n' > "${FAKE_COVERAGE:?}"
                exit 0
                ;;
        esac
        ;;
esac
exit 0
SHIM
chmod +x "$TOLLGATE_CLI"

# ------------------------------------------------- fake service init scripts
for svc in "$UHTTPD_INIT" "$NDS_INIT"; do
    cat > "$svc" <<'SHIM'
#!/bin/sh
printf '%s\n' "${1:-}" >> "${UHTTPD_CALLS:-/dev/null}"
case "${1:-}" in
    status|running) [ -f "${UHTTPD_RUNNING:-/nonexistent}" ] ;;
esac
exit 0
SHIM
    chmod +x "$svc"
done

# --------------------------------------------------------- script under test
# The body — every writer of uhttpd.main and the whole driver — is the shipped
# code, run by /bin/sh, with only the absolute live-system paths redirected.
build_script() { # build_script <source-file>
    sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
        -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
        -e "s|^UHTTPD_INIT=\"/etc/init\.d/uhttpd\"\$|UHTTPD_INIT=\"$UHTTPD_INIT\"|" \
        -e "s|^TOLLGATE_CLI=\"/usr/bin/tollgate\"\$|TOLLGATE_CLI=\"$TOLLGATE_CLI\"|" \
        -e "s|^PROVISIONED_CERT=\"/etc/tollgate/ssl/server\.crt\"\$|PROVISIONED_CERT=\"$PROVISIONED_CERT\"|" \
        -e "s|^PROVISIONED_KEY=\"/etc/tollgate/ssl/server\.key\"\$|PROVISIONED_KEY=\"$PROVISIONED_KEY\"|" \
        -e "s|^UHTTPD_IMAGE_CERT=\"/etc/uhttpd\.crt\"\$|UHTTPD_IMAGE_CERT=\"$UHTTPD_IMAGE_CERT\"|" \
        -e "s|^UHTTPD_IMAGE_KEY=\"/etc/uhttpd\.key\"\$|UHTTPD_IMAGE_KEY=\"$UHTTPD_IMAGE_KEY\"|" \
        -e "s|^TLS_IDENTITY_OPTOUT=\"/etc/tollgate/ssl/tls-identity-removed\"\$|TLS_IDENTITY_OPTOUT=\"$OPTOUT_FILE\"|" \
        -e "s|^ENTRY_UI_CONFIG=\"/etc/tollgate/config\.json\"\$|ENTRY_UI_CONFIG=\"$ENTRY_UI_CONFIG\"|" \
        -e "s|^NDS_INIT=\"/etc/init\.d/nodogsplash\"\$|NDS_INIT=\"$NDS_INIT\"|" \
        -e "s|> */proc/sys/kernel/hostname|> $TMP/kernel-hostname|" \
        -e "s|/etc/profile|$TMP/profile|g" \
        -e "s|/etc/config/nodogsplash|$TMP/config/nodogsplash|g" \
        -e "s|/etc/tollgate/brand|$TMP/etc/tollgate/brand|g" \
        "$1" > "$SCRIPT_UNDER_TEST"
    sed -i "s|^SETUP_VERSION=\"__TOLLGATE_VERSION__\"\$|SETUP_VERSION=\"$SHIPPED_VERSION\"|" \
        "$SCRIPT_UNDER_TEST"
}

build_script "$ROOT/$SCRIPT" || bad "harness could not build the script under test"
if grep -q "^ENTRY_UI_CONFIG=\"$ENTRY_UI_CONFIG\"\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^ENTRY_UI_MARKER=\"\${TOLLGATE_ENTRY_UI_MARKER" "$SCRIPT_UNDER_TEST" &&
   grep -q "^TOLLGATE_CLI=\"$TOLLGATE_CLI\"\$" "$SCRIPT_UNDER_TEST"; then
    ok "harness redirected the config.json path, kept the marker env override and redirected the CLI"
else
    bad "harness could not redirect the entry_ui config path in the copied script"
fi
if sh -n "$ROOT/$SCRIPT" 2>"$TMP/syntax.err"; then
    ok "the shipped setup script is valid /bin/sh"
else
    bad "the shipped setup script does not parse: $(head -n 2 "$TMP/syntax.err" | tr '\n' ' ')"
fi
# The module never writes uhttpd.admin's listeners: that section is the portal's
# 92 (D3 — the only writer that knows the brand webroot). This file only moves
# uhttpd.main between the two pairs.
if grep -qE 'add_list[[:space:]]+uhttpd\.admin\.listen_' "$ROOT/$SCRIPT"; then
    bad "the shipped script ADDS a uhttpd.admin listener (D3: 92 owns that section)"
else
    ok "the shipped script adds no uhttpd.admin listener (D3: 92 owns that section)"
fi

# ------------------------------------------------------------- fixtures
state() { printf '%s\n' "$@" >> "$UCI_STATE"; }
section_opt() { grep -F -- "uhttpd.$1.$2=" "$UCI_STATE" 2>/dev/null | head -n1 | cut -d= -f2-; }
listen_count() { grep -F -x -c -- "uhttpd.$1.listen_$2=$3" "$UCI_STATE" 2>/dev/null | tr -d ' '; }
claimants() { # <port>
    grep -E -- "^uhttpd\.[^.]+\.listen_https?=.*:${1}$" "$UCI_STATE" 2>/dev/null |
        sed -e 's/^uhttpd\.//' -e 's/\..*//' | sort -u
}
port_claimant() { claimants "$1" | head -n1; }

seed_host() {
    state 'network.lan=interface' 'network.lan.ipaddr=192.168.1.1' 'network.lan.netmask=255.255.255.0' \
          'system.@system[0]=system' 'system.@system[0].hostname=OpenWrt' \
          'wireless.radio0=wifi-device' 'wireless.radio0.band=2g' \
          'wireless.radio1=wifi-device' 'wireless.radio1.band=5g' \
          'wireless.default_radio0=wifi-iface' 'wireless.default_radio0.device=radio0' \
          'wireless.default_radio0.mode=ap' 'wireless.default_radio0.ssid=OpenWrt' \
          'wireless.default_radio1=wifi-iface' 'wireless.default_radio1.device=radio1' \
          'wireless.default_radio1.mode=ap' 'wireless.default_radio1.ssid=OpenWrt' \
          'nodogsplash.@nodogsplash[0]=nodogsplash' \
          'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2121'
}

# The board's uhttpd.admin as the portal's 92 writes it, on the pair that mode
# puts it on (the entry pair in board mode, the secondary pair in luci mode).
seed_admin() { # seed_admin <http-port> <https-port>
    state 'uhttpd.admin=uhttpd' \
          "uhttpd.admin.listen_http=0.0.0.0:$1" \
          "uhttpd.admin.listen_http=[::]:$1" \
          "uhttpd.admin.listen_https=0.0.0.0:$2" \
          "uhttpd.admin.listen_https=[::]:$2" \
          "uhttpd.admin.home=$ADMIN_HOME" \
          'uhttpd.admin.ubus_prefix=/ubus' \
          'uhttpd.admin.error_page=/index.html'
}

# LuCI's uhttpd.main, pre-99, pointing at the entry pair with a covering
# identity (so the derived redirect is exercised, not just the listeners).
seed_main() { # seed_main <http-port> <https-port>
    mkdir -p "$(dirname "$OPERATOR_CERT")"
    printf 'OPERATOR-CERT-FIXTURE\n' > "$OPERATOR_CERT"
    printf 'OPERATOR-KEY-FIXTURE\n' > "$OPERATOR_KEY"
    printf 'covering\n' > "$FAKE_COVERAGE"
    state 'uhttpd.main=uhttpd' 'uhttpd.main.home=/www' \
          "uhttpd.main.cert=$OPERATOR_CERT" "uhttpd.main.key=$OPERATOR_KEY" \
          "uhttpd.main.listen_http=0.0.0.0:$1" \
          "uhttpd.main.listen_http=[::]:$1" \
          "uhttpd.main.listen_https=0.0.0.0:$2" \
          "uhttpd.main.listen_https=[::]:$2"
}

seed_base() {
    : > "$UCI_STATE"
    : > "$CLI_CALLS"
    : > "$UHTTPD_CALLS"
    : > "$LOGFILE"
    rm -f "$FLAG" "$ENTRY_UI_MARKER" "$PROVISIONED_CERT" "$PROVISIONED_KEY" \
          "$UHTTPD_IMAGE_CERT" "$UHTTPD_IMAGE_KEY" "$UHTTPD_RUNNING" "$OPTOUT_FILE"
    mkdir -p "$TMP/config" "$(dirname "$UHTTPD_IMAGE_CERT")"
    printf 'IMAGE-PLACEHOLDER-CERT (DER: CN=OpenWrt, SAN DNS:OpenWrt)\n' > "$UHTTPD_IMAGE_CERT"
    printf 'IMAGE-PLACEHOLDER-KEY\n' > "$UHTTPD_IMAGE_KEY"
    printf 'placeholder\n' > "$FAKE_COVERAGE"
    printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
    : > "$PASSWD_FILE"
    : > "$TMP/profile"
    seed_host
}

write_config() { printf '%s\n' "$1" > "$ENTRY_UI_CONFIG"; }

# The two mappings, as literal port lists (assertion 1 of the record).
assert_board_mapping() { # <label>
    local label="$1"
    [ "$(listen_count main 'http' '0.0.0.0:8090')" = 1 ] && [ "$(listen_count main 'http' '[::]:8090')" = 1 ] \
        && ok "$label: uhttpd.main (LuCI) is on the secondary :8090 pair" \
        || bad "$label: uhttpd.main :8090 listeners are $(listen_count main http '0.0.0.0:8090')/$(listen_count main http '[::]:8090') (want 1/1)"
    [ "$(listen_count main 'https' '0.0.0.0:8443')" = 1 ] && [ "$(listen_count main 'https' '[::]:8443')" = 1 ] \
        && ok "$label: uhttpd.main (LuCI) is on the secondary :8443 pair" \
        || bad "$label: uhttpd.main :8443 listeners are $(listen_count main https '0.0.0.0:8443')/$(listen_count main https '[::]:8443') (want 1/1)"
    [ "$(listen_count main 'http' '0.0.0.0:8080')" = 0 ] && [ "$(listen_count main 'https' '0.0.0.0:443')" = 0 ] \
        && ok "$label: uhttpd.main holds NO entry-pair listener" \
        || bad "$label: uhttpd.main still holds an entry-pair listener (8080=$(listen_count main http '0.0.0.0:8080') 443=$(listen_count main https '0.0.0.0:443'))"
    [ "$(section_opt main home)" = /www ] \
        && ok "$label: uhttpd.main's home is /www" \
        || bad "$label: uhttpd.main's home is '$(section_opt main home)' (want /www)"
    [ "$(listen_count admin 'http' '0.0.0.0:8080')" = 1 ] && [ "$(listen_count admin 'http' '[::]:8080')" = 1 ] \
        && ok "$label: uhttpd.admin (the board) is on the entry :8080 pair" \
        || bad "$label: uhttpd.admin :8080 listeners are $(listen_count admin http '0.0.0.0:8080')/$(listen_count admin http '[::]:8080') (want 1/1)"
    [ "$(listen_count admin 'https' '0.0.0.0:443')" = 1 ] \
        && ok "$label: uhttpd.admin (the board) is on the entry :443 pair" \
        || bad "$label: uhttpd.admin :443 listeners are $(listen_count admin https '0.0.0.0:443') (want 1)"
    [ "$(section_opt admin home)" = "$ADMIN_HOME" ] && [ "$(section_opt admin error_page)" = /index.html ] \
        && ok "$label: the entry board keeps its webroot and error_page" \
        || bad "$label: the board's home/error_page are '$(section_opt admin home)'/'$(section_opt admin error_page)'"
    assert_single_bind "$label"
}

assert_luci_mapping() { # <label>
    local label="$1"
    [ "$(listen_count main 'http' '0.0.0.0:8080')" = 1 ] && [ "$(listen_count main 'http' '[::]:8080')" = 1 ] \
        && ok "$label: uhttpd.main (LuCI) is on the entry :8080 pair" \
        || bad "$label: uhttpd.main :8080 listeners are $(listen_count main http '0.0.0.0:8080')/$(listen_count main http '[::]:8080') (want 1/1)"
    [ "$(listen_count main 'https' '0.0.0.0:443')" = 1 ] && [ "$(listen_count main 'https' '[::]:443')" = 1 ] \
        && ok "$label: uhttpd.main (LuCI) is on the entry :443 pair" \
        || bad "$label: uhttpd.main :443 listeners are $(listen_count main https '0.0.0.0:443')/$(listen_count main https '[::]:443') (want 1/1)"
    [ "$(listen_count main 'http' '0.0.0.0:8090')" = 0 ] && [ "$(listen_count main 'https' '0.0.0.0:8443')" = 0 ] \
        && ok "$label: uhttpd.main holds NO secondary-pair listener" \
        || bad "$label: uhttpd.main keeps a secondary-pair listener (8090=$(listen_count main http '0.0.0.0:8090') 8443=$(listen_count main https '0.0.0.0:8443'))"
    [ "$(section_opt main home)" = /www ] \
        && ok "$label: uhttpd.main's home is /www" \
        || bad "$label: uhttpd.main's home is '$(section_opt main home)' (want /www)"
    [ "$(listen_count admin 'http' '0.0.0.0:8090')" = 1 ] && [ "$(listen_count admin 'https' '0.0.0.0:8443')" = 1 ] \
        && ok "$label: uhttpd.admin (the board) is on the secondary :8090/:8443 pair" \
        || bad "$label: uhttpd.admin secondary listeners are $(listen_count admin http '0.0.0.0:8090')/$(listen_count admin https '0.0.0.0:8443') (want 1/1)"
    [ "$(section_opt admin home)" = "$ADMIN_HOME" ] \
        && ok "$label: the board's webroot is unchanged" \
        || bad "$label: the board's home is '$(section_opt admin home)' (want $ADMIN_HOME)"
    assert_single_bind "$label"
}

# Invariant 3: exactly one section lists each of the four ports (assertion 3).
assert_single_bind() { # <label>
    local label="$1" p n
    for p in 8080 443 8090 8443; do
        n="$(claimants "$p" | grep -c .)"
        if [ "$n" = 1 ]; then
            ok "$label: exactly one section binds :$p ($(port_claimant "$p"))"
        else
            bad "$label: :$p is claimed by $(claimants "$p" | tr '\n' ',' )(want exactly one section)"
        fi
    done
}

# The port SET never changes, only which UI answers each pair.
assert_port_set_unchanged() { # <label>
    local label="$1" p missing=""
    for p in 8080 443 8090 8443; do
        [ -n "$(claimants "$p")" ] || missing="$missing $p"
    done
    [ -z "$missing" ] && ok "$label: all four ports of the two pairs still bound" \
                      || bad "$label: ports unbound after the run:$missing"
}

# ------------------------------------------------------------------ drivers
run_driver() { # run_driver <marker|__ABSENT__> [ENV=VAL ...]
    local marker="$1"; shift
    if [ "$marker" = "__ABSENT__" ]; then rm -f "$ENTRY_UI_MARKER"; else printf '%s\n' "$marker" > "$ENTRY_UI_MARKER"; fi
    : > "$LOGFILE"
    env "$@" sh "$SCRIPT_UNDER_TEST" > "$TMP/run.out" 2> "$TMP/run.err"
    return $?
}

warn_count() { grep -c 'WARNING' "$LOGFILE" 2>/dev/null | tr -d ' '; }
# Case-insensitive: the assertion is that the WARNING NAMES the feed re-vendor that
# closes the gap, not how the sentence is capitalised.
warn_names_revendor() { grep -qi 're-vendor the feed' "$LOGFILE" 2>/dev/null; }

echo
echo "== A. resolving entry_ui (fail-closed toward board, D1/invariant 6)"
# The reader is the shipped function, sourced directly (no driver).
reader_case() { # reader_case <label> <config|__ABSENT__> <jq-bin> <want>
    local label="$1" content="$2" jqbin="$3" want="$4" got
    if [ "$content" = __ABSENT__ ]; then rm -f "$ENTRY_UI_CONFIG"; else printf '%s\n' "$content" > "$ENTRY_UI_CONFIG"; fi
    got="$( ENTRY_UI_JQ_BIN="$jqbin" TOLLGATE_SETUP_LIB_ONLY=1 \
            sh -c ". '$SCRIPT_UNDER_TEST'; entry_ui_configured" 2>/dev/null )"
    if [ "$got" = "$want" ]; then
        ok "$label -> $got"
    else
        bad "$label -> '$got' (want $want)"
    fi
}
reader_case 'entry_ui="board"'       '{"entry_ui":"board"}'      "$TMP/bin/jq" board
reader_case 'entry_ui="luci"'        '{"entry_ui":"luci"}'       "$TMP/bin/jq" luci
reader_case 'key absent'             '{"mint_url":"x"}'          "$TMP/bin/jq" board
reader_case 'value empty'            '{"entry_ui":""}'           "$TMP/bin/jq" board
reader_case 'value garbage'          '{"entry_ui":"nonsense"}'   "$TMP/bin/jq" board
reader_case 'config.json absent'     '__ABSENT__'                "$TMP/bin/jq" board
reader_case 'jq absent'              '{"entry_ui":"luci"}'       /nonexistent/jq board
reader_case 'config body not JSON'   'not json at all'           "$TMP/bin/jq" board

echo
echo "== B. entry_ui=board with the D4 marker present (the atomic release)"
seed_base; write_config '{"entry_ui":"board"}'; seed_main 8080 443; seed_admin 8080 443
run_driver board
rc=$?
[ "$rc" = 0 ] && ok "board: the driver exits 0" \
              || bad "board: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
assert_board_mapping "board"
assert_port_set_unchanged "board"
[ "$(warn_count)" = 0 ] && ok "board: no D4 WARNING when the marker is present" \
                         || bad "board: $(warn_count) WARNING line(s) with the marker present"

echo
echo "== C. entry_ui=luci reproduces today's mapping"
seed_base; write_config '{"entry_ui":"luci"}'; seed_main 8080 443; seed_admin 8090 8443
run_driver __ABSENT__
rc=$?
[ "$rc" = 0 ] && ok "luci: the driver exits 0" \
              || bad "luci: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
assert_luci_mapping "luci"
assert_port_set_unchanged "luci"

echo
echo "== D. the fail-closed matrix resolves to the board mapping"
# A missing config.json, a missing key, an empty value, a garbage value and an
# absent jq ALL resolve to `board` — and none of them leaves an admin UI with no
# listener (asserted by counting listeners, not by reading log lines).
fail_closed_case() { # fail_closed_case <label> <config|__ABSENT__> [ENV=VAL ...]
    local label="$1" content="$2"; shift 2
    seed_base; seed_main 8080 443; seed_admin 8080 443
    if [ "$content" = __ABSENT__ ]; then rm -f "$ENTRY_UI_CONFIG"; else printf '%s\n' "$content" > "$ENTRY_UI_CONFIG"; fi
    run_driver board "$@"
    rc=$?
    [ "$rc" = 0 ] || bad "$label: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
    assert_board_mapping "$label"
    local admin_ui main_ui
    admin_ui="$(( $(listen_count admin http '0.0.0.0:8080') + $(listen_count admin https '0.0.0.0:443') ))"
    main_ui="$(( $(listen_count main http '0.0.0.0:8090') + $(listen_count main https '0.0.0.0:8443') ))"
    [ "$admin_ui" -gt 0 ] && [ "$main_ui" -gt 0 ] \
        && ok "$label: neither UI is left unbound (board listeners=$admin_ui, luci listeners=$main_ui)" \
        || bad "$label: an admin UI is unbound (board=$admin_ui luci=$main_ui)"
}
fail_closed_case "no config.json"            __ABSENT__
fail_closed_case "missing key"               '{"mint_url":"x"}'
fail_closed_case "empty value"               '{"entry_ui":""}'
fail_closed_case "garbage value"             '{"entry_ui":"nonsense"}'
fail_closed_case "absent jq"                 '{"entry_ui":"board"}' ENTRY_UI_JQ_BIN=/nonexistent/jq

echo
echo "== E. the D4 marker gate"
# direction 1: entry_ui=board with NO marker -> repair to luci, one WARNING.
seed_base; write_config '{"entry_ui":"board"}'; seed_main 8080 443; seed_admin 8090 8443
run_driver __ABSENT__
rc=$?
[ "$rc" = 0 ] && ok "no marker: the driver exits 0" \
              || bad "no marker: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
assert_luci_mapping "no marker (repaired)"
assert_port_set_unchanged "no marker (repaired)"
if [ "$(warn_count)" = 1 ]; then
    ok "no marker: exactly one WARNING is logged"
else
    bad "no marker: $(warn_count) WARNING line(s) logged (want exactly 1)"
fi
warn_names_revendor \
    && ok "no marker: the WARNING names the feed re-vendor" \
    || bad "no marker: the WARNING does not name the feed re-vendor ($(grep 'WARNING' "$LOGFILE" 2>/dev/null | head -n1))"

# direction 2: the marker alone, with entry_ui=luci, changes nothing (negative
# control — a stray marker must not move a luci router).
seed_base; write_config '{"entry_ui":"luci"}'; seed_main 8080 443; seed_admin 8090 8443
run_driver board
rc=$?
[ "$rc" = 0 ] && ok "negative control: the driver exits 0" \
              || bad "negative control: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
assert_luci_mapping "negative control (marker + luci)"
[ "$(warn_count)" = 0 ] && ok "negative control: no WARNING for a luci router" \
                         || bad "negative control: $(warn_count) WARNING line(s) for a luci router"

echo
echo "== F. the marker is honoured on a same-version reinstall too"
# The verify/repair path is the one every reinstall takes; the mapping must not
# be a full-setup-only property.
seed_base; write_config '{"entry_ui":"board"}'; seed_main 8080 443; seed_admin 8080 443
printf '%s\n' "$SHIPPED_VERSION" > "$FLAG"
run_driver board
rc=$?
[ "$rc" = 0 ] && ok "reinstall: the driver exits 0" \
              || bad "reinstall: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
assert_board_mapping "reinstall (board)"

echo
printf 'tests: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
