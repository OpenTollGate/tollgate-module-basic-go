#!/usr/bin/env bash
# Offline tests for the ADMIN TLS IDENTITY of
# packaging/files/etc/uci-defaults/99-tollgate-setup (RC defect, bench MT3000).
#
# The defect, measured read-only on the bench (GL-MT3000, OpenWrt 25.12.5,
# package tollgate-wrt 0.6.0_alpha4_pre17-r1, module pin 2796d96c, 2026-09-26):
#
#   tollgate ssl status            -> "SSL: not configured"
#   /etc/uhttpd.crt                -> 561 bytes, subject CN=OpenWrt,
#                                     SAN DNS:OpenWrt, dated with the image
#   uci show uhttpd                -> redirect_https='1', cert='/etc/uhttpd.crt'
#   curl http://192.168.1.1:8080/  -> 307 https://192.168.1.1/  and then a hard
#                                     certificate error in a browser: the
#                                     certificate covers neither the hostname
#                                     (tollgate-OQ3Q) nor the LAN IP
#
# Two halves of the fix, both pinned here:
#
#   1. The install/setup path PROVISIONS a real identity instead of inheriting
#      the image's placeholder. It drives the existing generator
#      (`tollgate ssl apply -y`, shared with the CLI — no second certificate
#      generator), non-interactively, with the service reload left to this
#      script because uci-defaults runs before procd starts the services.
#   2. `redirect_https` is a DERIVED value whose guard now requires a certificate
#      that actually COVERS this router (`tollgate ssl covers`, the module's own
#      x509 SAN check). Readable-and-non-empty was satisfied by the vendor
#      placeholder, which is what turned the :8080 -> https:// hop on.
#
# Three layers:
#
#   A. Function-level cases over the derived-value guard matrix (the script is
#      sourced in TOLLGATE_SETUP_LIB_ONLY=1 mode and the real function is called).
#   B. Function-level cases over the provisioning step: the exact
#      non-interactive invocation, idempotence, and a missing CLI.
#   C. End-to-end runs of the real driver against a fake uci/apk/shadow, a fake
#      `tollgate` CLI that behaves like the real generator, and fake service init
#      scripts: a fresh install, a same-version reinstall, an upgrade of a RUNNING
#      router, and an install with no CLI at all.
#   D. The FAILING NEGATIVE CONTROL: the same fresh-install fixture run with the
#      PRE-CHANGE behaviour injected (existence-only guard, no provisioning),
#      which must leave redirect_https=1 on the placeholder certificate. That is
#      what proves the C-case assertions are written against the real defect.
#
# The harness copies the shipped script into a temp dir and rewrites the absolute
# paths into the live system (flag file, log, the two service init scripts, the
# CLI, the provisioned identity, the image's own cert/key pair, /etc/profile,
# /proc/sys/kernel/hostname, /etc/config/nodogsplash) plus the two credential
# paths the script already exposes as SHADOW_FILE/PASSWD_FILE. Nothing on the
# host is touched: no /etc/uhttpd.crt, no service, no router.
#
# Usage: bash tests/uci-defaults-admin-tls-identity_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"
# The script under test. The default is the shipped one; TOLLGATE_SETUP_SCRIPT
# points the same suite at another copy, which is how the RED side of the review
# findings is reproducible:
#   git show <base>:packaging/files/etc/uci-defaults/99-tollgate-setup > /tmp/parent-99
#   TOLLGATE_SETUP_SCRIPT=/tmp/parent-99 bash tests/uci-defaults-admin-tls-identity_test.sh
SCRIPT="${TOLLGATE_SETUP_SCRIPT:-packaging/files/etc/uci-defaults/99-tollgate-setup}"
SHIPPED_VERSION="v0.6.0-alpha4"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/init.d" "$TMP/etc" "$TMP/config"

# ------------------------------------------------------------- harness paths
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
PROFILE_FILE="$TMP/profile"
HOSTNAME_FILE="$TMP/kernel-hostname"
NDS_CONFIG="$TMP/config/nodogsplash"
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
UHTTPD_INIT="$TMP/init.d/uhttpd"
NDS_INIT="$TMP/init.d/nodogsplash"
TOLLGATE_CLI="$TMP/bin/tollgate"
PROVISIONED_CERT="$TMP/etc/tollgate/ssl/server.crt"
PROVISIONED_KEY="$TMP/etc/tollgate/ssl/server.key"
UHTTPD_IMAGE_CERT="$TMP/etc/uhttpd.crt"
UHTTPD_IMAGE_KEY="$TMP/etc/uhttpd.key"
# An identity the OPERATOR installed and owns: a CA-signed pair (or the pair
# their own `tollgate ssl apply <cert> <key>` installed) that uhttpd.main already
# presents. Nothing in this harness parses it; the fake CLI decides coverage by
# path, exactly the way the real check decides it by parsing.
OPERATOR_CERT="$TMP/etc/operator-ca.crt"
OPERATOR_KEY="$TMP/etc/operator-ca.key"
OPTOUT_FILE="$TMP/etc/tollgate/ssl/tls-identity-removed"
export UCI_STATE="$TMP/uci.state"
export CLI_CALLS="$TMP/cli-calls"
export UHTTPD_CALLS="$TMP/uhttpd-calls"
export NDS_CALLS="$TMP/nds-calls"
export FAKE_COVERAGE="$TMP/coverage"
export UHTTPD_RUNNING="$TMP/uhttpd-running"
export NDS_RUNNING="$TMP/nds-running"
export COMMITTED="$TMP/committed-nodogsplash"
export PROVISIONED_CERT PROVISIONED_KEY OPTOUT_FILE OPERATOR_CERT OPERATOR_KEY
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd"

# ---------------------------------------------------------------- fake uci
# Flat-file stand-in: one "key=value" line per option, list options stored as one
# line per entry and rendered that way by `get`, like the real uci. `show` and
# `export <config>` print that config's lines, which is all the setup script's
# diff-based commit logic needs. Only the verbs the script and the CLI fake use
# are implemented; anything else is a harmless no-op that succeeds.
cat > "$TMP/bin/uci" <<'SHIM'
#!/bin/sh
state="${UCI_STATE:?}"
q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"
shift || true
values() { grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-; }
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
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        printf '%s\n' "$1" >> "$state"
        ;;
    add_list)   printf '%s\n' "$1" >> "$state" ;;
    add)        printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state" ;;
    delete)
        key="${1%%=*}"
        grep -v -F -- "$key=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    del_list)
        # Whole-line match: entries carry spaces ("allow tcp port 8090").
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    show|export)
        grep -F -- "$1." "$state" 2>/dev/null || true
        ;;
    commit)
        grep -F 'nodogsplash.@nodogsplash[0].users_to_router=' "$state" 2>/dev/null \
            | cut -d= -f2- > "${COMMITTED:?}"
        ;;
    revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"

# --------------------------------------------------------------- fake apk
cat > "$TMP/bin/apk" <<'SHIM'
#!/bin/sh
# Only `apk list --installed` is consulted, for the setup-version fallback.
if [ "${1:-}" = "list" ]; then
    printf '%s\n' "tollgate-wrt-${FAKE_APK_VERSION:-0.6.0_alpha4-r1} aarch64_cortex-a53 {tollgate-wrt} (GPL-3.0-only) [installed]"
fi
exit 0
SHIM
chmod +x "$TMP/bin/apk"

# -------------------------------------------------------------- fake pidof
# The service-running probes the setup script performs: the uhttpd one added by
# this change and the nodogsplash one that already existed.
cat > "$TMP/bin/pidof" <<'SHIM'
#!/bin/sh
case "${1:-}" in
    uhttpd)      [ -f "${UHTTPD_RUNNING:?}" ] ;;
    nodogsplash) [ -f "${NDS_RUNNING:?}" ] ;;
    *)           exit 1 ;;
esac
SHIM
chmod +x "$TMP/bin/pidof"

# ------------------------------------------------------------ fake iptables
# `iptables -S ndsRTR` answers nothing, so the nodogsplash convergence step
# treats the ruleset as unmeasurable and leaves the service alone.
cat > "$TMP/bin/iptables" <<'SHIM'
#!/bin/sh
[ "${1:-}" = "-S" ] && exit 1
exit 0
SHIM
chmod +x "$TMP/bin/iptables"

PATH="$TMP/bin:$PATH"
export PATH

# --------------------------------------------------------- fake tollgate CLI
# Stands in for the real binary. The verbs the setup path uses behave like the
# real ones:
#   * `ssl covers [<cert>]` exits 0 only for the identity that covers this
#     router, and refuses a missing or empty file outright — the CONTRACT of the
#     real x509 SAN check.
#   * `ssl apply -y --no-restart` provisions an identity the way the real
#     generator does: writes cert+key, points uhttpd.main at them, adds the :443
#     listeners, commits uhttpd — and must NOT touch any service, because the
#     reload is the setup script's job.
cat > "$TOLLGATE_CLI" <<'SHIM'
#!/bin/sh
printf '%s\n' "$*" >> "${CLI_CALLS:?}"
sub="${1:-}"
shift || true
case "$sub" in
    ui)
        verb="${1:-}"
        shift || true
        case "$verb" in
            ports)
                # The port table's single source (the real binary's uiPortPair
                # emission), reproduced so the driver's load_ui_port_table
                # evaluates exactly what the router would.
                cat <<'TABLE'
uhttpd_main_http_port_board=8090
uhttpd_main_http_port_luci=8080
uhttpd_main_https_port_board=8443
uhttpd_main_https_port_luci=443
board_http_port_board=8080
board_http_port_luci=8090
board_https_port_board=443
board_https_port_luci=8443
TABLE
                exit 0
                ;;
        esac
        ;;
    ssl)
        verb="${1:-}"
        shift || true
        case "$verb" in
            covers)
                cert="${1:-}"
                [ -n "$cert" ] || exit 1
                [ -r "$cert" ] && [ -s "$cert" ] || exit 1
                # Two identities cover this router in this harness: the one the
                # generator provisions, and the one the OPERATOR installed and
                # owns. The image's placeholder covers neither.
                case "$cert" in
                    "$PROVISIONED_CERT"|"${OPERATOR_CERT:-NO-OPERATOR-IDENTITY}") ;;
                    *) exit 1 ;;
                esac
                [ "$(cat "${FAKE_COVERAGE:?}" 2>/dev/null)" = "covering" ] || exit 1
                echo "covers: yes — SANs cover this router"
                exit 0
                ;;
            apply)
                mkdir -p "$(dirname "$PROVISIONED_CERT")"
                # Plain marker text, not PEM: nothing in this harness parses the
                # bytes (the fake CLI decides coverage by path, like the real
                # check decides it by parsing), and a real-looking PEM blob in a
                # test fixture trips the fleet's secret scanner.
                printf '%s\n' "PROVISIONED-CERT-FIXTURE" > "$PROVISIONED_CERT"
                printf '%s\n' "PROVISIONED-KEY-FIXTURE" > "$PROVISIONED_KEY"
                printf 'covering\n' > "${FAKE_COVERAGE:?}"
                uci set "uhttpd.main.cert=$PROVISIONED_CERT"
                uci set "uhttpd.main.key=$PROVISIONED_KEY"
                uci add_list "uhttpd.main.listen_https=0.0.0.0:443"
                uci add_list "uhttpd.main.listen_https=[::]:443"
                uci commit uhttpd
                echo "Done. Self-signed HTTPS enabled"
                exit 0
                ;;
            status)
                [ -s "${PROVISIONED_CERT:-/nonexistent}" ] && echo "SSL: configured" || echo "SSL: not configured"
                exit 0
                ;;
            remove)
                rm -f "$PROVISIONED_CERT" "$PROVISIONED_KEY"
                # The real `ssl remove` records the operator's decision in the
                # opt-out marker; the setup path must see exactly that.
                mkdir -p "$(dirname "${OPTOUT_FILE:?}")"
                printf 'TLS identity removed by an operator\n' > "${OPTOUT_FILE:?}"
                printf 'placeholder\n' > "${FAKE_COVERAGE:?}"
                echo "Done. HTTPS removed."
                exit 0
                ;;
        esac
        ;;
esac
exit 0
SHIM
chmod +x "$TOLLGATE_CLI"

# ------------------------------------------------- fake service init scripts
cat > "$UHTTPD_INIT" <<'SHIM'
#!/bin/sh
printf '%s\n' "${1:-}" >> "${UHTTPD_CALLS:?}"
case "${1:-}" in
    status|running) [ -f "${UHTTPD_RUNNING:?}" ] ;;
esac
exit 0
SHIM
chmod +x "$UHTTPD_INIT"

cat > "$NDS_INIT" <<'SHIM'
#!/bin/sh
printf '%s\n' "${1:-}" >> "${NDS_CALLS:?}"
case "${1:-}" in
    status|running) [ -f "${NDS_RUNNING:?}" ] ;;
esac
exit 0
SHIM
chmod +x "$NDS_INIT"

# --------------------------------------------------------- script under test
# The body — every writer of uhttpd.main and the whole driver — is the shipped
# code, run by /bin/sh, with only the absolute live-system paths redirected.
build_script() { # build_script <source-file> [extra-sed...]
    local src="$1"
    shift
    sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
        -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
        -e "s|^UHTTPD_INIT=\"/etc/init\.d/uhttpd\"\$|UHTTPD_INIT=\"$UHTTPD_INIT\"|" \
        -e "s|^TOLLGATE_CLI=\"/usr/bin/tollgate\"\$|TOLLGATE_CLI=\"$TOLLGATE_CLI\"|" \
        -e "s|^PROVISIONED_CERT=\"/etc/tollgate/ssl/server\.crt\"\$|PROVISIONED_CERT=\"$PROVISIONED_CERT\"|" \
        -e "s|^PROVISIONED_KEY=\"/etc/tollgate/ssl/server\.key\"\$|PROVISIONED_KEY=\"$PROVISIONED_KEY\"|" \
        -e "s|^UHTTPD_IMAGE_CERT=\"/etc/uhttpd\\.crt\"\$|UHTTPD_IMAGE_CERT=\"$UHTTPD_IMAGE_CERT\"|" \
        -e "s|^UHTTPD_IMAGE_KEY=\"/etc/uhttpd\\.key\"\$|UHTTPD_IMAGE_KEY=\"$UHTTPD_IMAGE_KEY\"|" \
        -e "s|^TLS_IDENTITY_OPTOUT=\"/etc/tollgate/ssl/tls-identity-removed\"\$|TLS_IDENTITY_OPTOUT=\"$OPTOUT_FILE\"|" \
        -e "s|^NDS_INIT=\"/etc/init\.d/nodogsplash\"\$|NDS_INIT=\"$NDS_INIT\"|" \
        -e "s|> */proc/sys/kernel/hostname|> $HOSTNAME_FILE|" \
        -e "s|/etc/profile|$PROFILE_FILE|g" \
        -e "s|/etc/config/nodogsplash|$NDS_CONFIG|g" \
        "$@" "$src" > "$SCRIPT_UNDER_TEST"
    sed -i "s|^SETUP_VERSION=\"__TOLLGATE_VERSION__\"\$|SETUP_VERSION=\"$SHIPPED_VERSION\"|" \
        "$SCRIPT_UNDER_TEST"
}

# ------------------------------------------------------ negative control rule
# The PRE-CHANGE rule, verbatim from the shipped script before this change: the
# existence/size guard, and no notion of the certificate having to COVER this
# router. Written out here rather than read from git history because CI checks
# out the PR branch at fetch-depth=1 — no upstream ref exists in that checkout —
# and the point of a negative control is that it fails when the fix is reverted,
# not that it reads a particular ref.
run_pre_change_rule() {
    uci -q delete uhttpd.main.listen_https
    uci -q delete uhttpd.main.cert
    uci -q delete uhttpd.main.key
    if [ -f "$UHTTPD_IMAGE_CERT" ] && [ -f "$UHTTPD_IMAGE_KEY" ]; then
        uci add_list uhttpd.main.listen_https='0.0.0.0:443'
        uci add_list uhttpd.main.listen_https='[::]:443'
        uci set uhttpd.main.cert="$UHTTPD_IMAGE_CERT"
        uci set uhttpd.main.key="$UHTTPD_IMAGE_KEY"
    fi
    if [ -r "$UHTTPD_IMAGE_CERT" ] && [ -s "$UHTTPD_IMAGE_CERT" ] &&
       [ -r "$UHTTPD_IMAGE_KEY" ] && [ -s "$UHTTPD_IMAGE_KEY" ]; then
        uci set uhttpd.main.redirect_https='1'
    else
        uci set uhttpd.main.redirect_https='0'
    fi
}

# ------------------------------------------------------------- fixtures/state
# A router as the bench left it: the image's placeholder certificate is in place
# and uhttpd.main already points at it, with the redirect armed. That is the
# whole premise of the defect.
seed_placeholder_identity() {
    mkdir -p "$(dirname "$UHTTPD_IMAGE_CERT")"
    # Marker text rather than a PEM blob: the real file on the bench is DER
    # (561 bytes, CN=OpenWrt, SAN DNS:OpenWrt), nothing in this harness parses it,
    # and a real-looking PEM block in a fixture trips the fleet's secret scanner.
    printf '%s\n' "IMAGE-PLACEHOLDER-CERT (561 bytes, DER: CN=OpenWrt, SAN DNS:OpenWrt)" > "$UHTTPD_IMAGE_CERT"
    printf '%s\n' "IMAGE-PLACEHOLDER-KEY" > "$UHTTPD_IMAGE_KEY"
    printf 'placeholder\n' > "$FAKE_COVERAGE"
    printf '%s\n' \
        'uhttpd.main=uhttpd' \
        "uhttpd.main.cert=$UHTTPD_IMAGE_CERT" \
        "uhttpd.main.key=$UHTTPD_IMAGE_KEY" \
        'uhttpd.main.listen_http=0.0.0.0:8080' \
        'uhttpd.main.listen_http=[::]:8080' \
        'uhttpd.main.listen_https=0.0.0.0:443' \
        'uhttpd.main.listen_https=[::]:443' \
        'uhttpd.main.redirect_https=1' >> "$UCI_STATE"
}

seed_state() { # a stock, freshly-flashed box
    : > "$UCI_STATE"
    : > "$CLI_CALLS"
    : > "$UHTTPD_CALLS"
    : > "$NDS_CALLS"
    : > "$COMMITTED"
    rm -f "$PROVISIONED_CERT" "$PROVISIONED_KEY" "$FLAG" "$OPTOUT_FILE"
    rm -f "$UHTTPD_RUNNING" "$NDS_RUNNING"
    : > "$PROFILE_FILE"
    printf '%s\n' \
        'system.@system[0]=system' \
        'system.@system[0].hostname=tollgate-OQ3Q' \
        'network.lan=interface' \
        'network.lan.ipaddr=192.168.1.1/24' \
        'network.lan.netmask=255.255.255.0' \
        'wireless.radio0=wifi-device' \
        'wireless.radio0.band=2g' \
        'wireless.radio1=wifi-device' \
        'wireless.radio1.band=5g' \
        'nodogsplash.@nodogsplash[0]=nodogsplash' \
        'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2121' \
        'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 8080' \
        'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2050' \
        'nodogsplash.@nodogsplash[0].users_to_router=allow tcp port 2051' >> "$UCI_STATE"
    printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
    : > "$PASSWD_FILE"
    seed_placeholder_identity
}

# A router whose uhttpd.main already presents an identity the OPERATOR owns: a
# CA-signed certificate (or the pair their own `tollgate ssl apply <cert> <key>`
# installed). The image's placeholder pair stays on disk — it is what uhttpd.main
# presented BEFORE the operator replaced it, and the question under test is
# whether an install keeps the operator's choice or goes back to the image's.
seed_operator_identity() {
    printf 'x\n' > "$OPERATOR_CERT"
    printf 'x\n' > "$OPERATOR_KEY"
    grep -v -e '^uhttpd.main.cert=' -e '^uhttpd.main.key=' "$UCI_STATE" > "$UCI_STATE.tmp" 2>/dev/null
    mv "$UCI_STATE.tmp" "$UCI_STATE"
    printf '%s\n' \
        "uhttpd.main.cert=$OPERATOR_CERT" \
        "uhttpd.main.key=$OPERATOR_KEY" >> "$UCI_STATE"
    printf 'covering\n' > "$FAKE_COVERAGE"
}

# ------------------------------------------------------------------ readbacks
uci_now()      { grep -F -- "$1=" "$UCI_STATE" 2>/dev/null | head -n1 | cut -d= -f2-; }
redirect_now() { uci_now 'uhttpd.main.redirect_https'; }
cert_now()     { uci_now 'uhttpd.main.cert'; }
listen_https_now() { local n; n="$(grep -c -F -- 'uhttpd.main.listen_https=' "$UCI_STATE" 2>/dev/null)"; printf '%s' "${n:-0}"; }
cli_calls()    { cat "$CLI_CALLS" 2>/dev/null || true; }
reload_count() { local n; n="$(grep -c -x 'reload' "$UHTTPD_CALLS" 2>/dev/null)"; printf '%s' "${n:-0}"; }

# ------------------------------------------------------------- driver runs
run_driver() { # run_driver <marker|__ABSENT__>
    case "$1" in
        __ABSENT__) rm -f "$FLAG" ;;
        *)          printf '%s\n' "$1" > "$FLAG" ;;
    esac
    : > "$LOGFILE"
    sh "$SCRIPT_UNDER_TEST" >"$TMP/run.out" 2>"$TMP/run.err"
    return $?
}

build_script "$ROOT/$SCRIPT" || bad "harness could not build the script under test"
if grep -q "^TOLLGATE_CLI=\"$TOLLGATE_CLI\"\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^UHTTPD_IMAGE_CERT=\"$UHTTPD_IMAGE_CERT\"\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^TLS_IDENTITY_OPTOUT=\"$OPTOUT_FILE\"\$" "$SCRIPT_UNDER_TEST"; then
    ok "harness redirected the CLI, the provisioned identity, the opt-out marker and the service init paths"
else
    bad "harness could not redirect the new absolute paths in the copied script"
fi
if sh -n "$ROOT/$SCRIPT" 2>"$TMP/syntax.err"; then
    ok "the shipped setup script is valid /bin/sh"
else
    bad "the shipped setup script does not parse: $(head -n 2 "$TMP/syntax.err" | tr '\n' ' ')"
fi

echo
echo "== A. the derived-value guard (function level, real functions sourced)"
if ( TOLLGATE_SETUP_LIB_ONLY=1 sh -c ". '$SCRIPT_UNDER_TEST'; command -v setup_uhttpd_tls_identity >/dev/null 2>&1" ) 2>/dev/null; then
    ok "the setup script sources in lib-only mode with the new functions defined"
else
    bad "cannot source the setup script's functions (setup_uhttpd_tls_identity missing?)"
fi

# guard_case <label> <fixture> <cli-coverage> <want-redirect> <want-cert> <want-listen>
guard_case() {
    local label="$1" fixture="$2" coverage="$3" want_redirect="$4" want_cert="$5" want_listen="$6"
    seed_state
    mkdir -p "$(dirname "$PROVISIONED_CERT")"
    case "$fixture" in
        placeholder)        : ;;
        none)               rm -f "$UHTTPD_IMAGE_CERT" "$UHTTPD_IMAGE_KEY" ;;
        empty)              : > "$UHTTPD_IMAGE_CERT" ;;
        provisioned)        printf 'x\n' > "$PROVISIONED_CERT"; printf 'x\n' > "$PROVISIONED_KEY" ;;
        stale-provisioned)  rm -f "$UHTTPD_IMAGE_CERT" "$UHTTPD_IMAGE_KEY"
                            printf 'x\n' > "$PROVISIONED_CERT"; printf 'x\n' > "$PROVISIONED_KEY" ;;
    esac
    printf '%s\n' "$coverage" > "$FAKE_COVERAGE"
    ( TOLLGATE_SETUP_LIB_ONLY=1 sh -c ". '$SCRIPT_UNDER_TEST'; setup_uhttpd_tls_identity" ) 2>/dev/null
    local got_redirect got_cert got_listen
    got_redirect="$(redirect_now)"; got_cert="$(cert_now)"; got_listen="$(listen_https_now)"
    if [ "$got_redirect" = "$want_redirect" ] && [ "$got_cert" = "$want_cert" ] && [ "$got_listen" = "$want_listen" ]; then
        ok "$label: redirect_https=$got_redirect cert=$(basename "${got_cert:-none}") listen_https entries=$got_listen"
    else
        bad "$label: got redirect_https=$got_redirect cert=$got_cert listen_https=$got_listen, want redirect_https=$want_redirect cert=$want_cert listen_https=$want_listen"
    fi
}

echo "-- the vendor placeholder must NOT enable the :8080 -> https:// hop"
guard_case "placeholder only"                placeholder        placeholder 0 "$UHTTPD_IMAGE_CERT" 2
guard_case "provisioned identity covers"     provisioned        covering    1 "$PROVISIONED_CERT"   2
guard_case "stale identity no longer covers" stale-provisioned  placeholder 0 "$PROVISIONED_CERT"   2
guard_case "no certificate at all"           none               placeholder 0 ""                    0
guard_case "zero-length certificate"         empty              placeholder 0 ""                    0

echo
echo "== B. provisioning the identity (function level)"
seed_state
( TOLLGATE_SETUP_LIB_ONLY=1 sh -c ". '$SCRIPT_UNDER_TEST'; provision_tls_identity" ) 2>/dev/null
if grep -q -x 'ssl apply -y --no-restart' "$CLI_CALLS"; then
    ok "provision: the generator is invoked exactly as 'ssl apply -y --no-restart' (non-interactive, no service churn)"
else
    bad "provision: CLI calls were '$(cli_calls | tr '\n' ';')', want 'ssl apply -y --no-restart'"
fi

seed_state
printf 'x\n' > "$PROVISIONED_CERT"; printf 'x\n' > "$PROVISIONED_KEY"; printf 'covering\n' > "$FAKE_COVERAGE"
( TOLLGATE_SETUP_LIB_ONLY=1 sh -c ". '$SCRIPT_UNDER_TEST'; provision_tls_identity" ) 2>/dev/null
if ! grep -q '^ssl apply' "$CLI_CALLS"; then
    ok "provision: idempotent — an identity that already covers this router is probed but never re-keyed"
else
    bad "provision: re-provisioned an identity that already covers the router ($(cli_calls | tr '\n' ';'))"
fi

seed_state
( TOLLGATE_SETUP_LIB_ONLY=1 sh -c ". '$SCRIPT_UNDER_TEST'; TOLLGATE_CLI=/nonexistent/tollgate; provision_tls_identity" ) 2>/dev/null
if [ ! -s "$CLI_CALLS" ] && [ ! -s "$PROVISIONED_CERT" ]; then
    ok "provision: a missing CLI is skipped without invoking anything and without writing an identity"
else
    bad "provision: invoked a CLI that does not exist, or wrote an identity anyway"
fi

echo "-- an operator who removed the identity is not re-keyed behind their back"
seed_state
mkdir -p "$(dirname "$OPTOUT_FILE")"
printf 'TLS identity removed by an operator\n' > "$OPTOUT_FILE"
: > "$LOGFILE"
( TOLLGATE_SETUP_LIB_ONLY=1 sh -c ". '$SCRIPT_UNDER_TEST'; provision_tls_identity" ) 2>/dev/null
if [ ! -s "$CLI_CALLS" ] && [ ! -s "$PROVISIONED_CERT" ]; then
    ok "opt-out: the marker suppresses provisioning — no CLI call, no identity written"
else
    bad "opt-out: provisioned over the operator's removal (CLI calls: '$(cli_calls | tr '\n' ';')')"
fi
if grep -q "removed by the operator" "$LOGFILE"; then
    ok "opt-out: the decision is named in the setup log, not silently skipped"
else
    bad "opt-out: the log says nothing about the opt-out ($(tail -n 1 "$LOGFILE" 2>/dev/null))"
fi
# ... and the marker only suppresses PROVISIONING: the derived guard still runs
# and still refuses a placeholder, which is what keeps :8080 reachable.
( TOLLGATE_SETUP_LIB_ONLY=1 sh -c ". '$SCRIPT_UNDER_TEST'; setup_uhttpd_tls_identity" ) 2>/dev/null
[ "$(redirect_now)" = 0 ] && [ "$(cert_now)" = "$UHTTPD_IMAGE_CERT" ] \
    && ok "opt-out: the uhttpd contract is still asserted (image identity kept, redirect_https=0)" \
    || bad "opt-out: cert=$(cert_now) redirect_https=$(redirect_now), want $UHTTPD_IMAGE_CERT / 0"

echo
echo "== C. the install path (real driver, end to end)"

seed_state
run_driver __ABSENT__
rc=$?
[ "$rc" = 0 ] && ok "fresh install: run exits 0" \
              || bad "fresh install: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
if grep -q -x 'ssl apply -y --no-restart' "$CLI_CALLS"; then
    ok "fresh install: the setup path provisioned a TLS identity through the shared generator"
else
    bad "fresh install: the setup path never provisioned an identity (CLI calls: '$(cli_calls | tr '\n' ';')')"
fi
[ "$(cert_now)" = "$PROVISIONED_CERT" ] \
    && ok "fresh install: uhttpd.main.cert is the provisioned identity" \
    || bad "fresh install: uhttpd.main.cert is $(cert_now), want $PROVISIONED_CERT"
[ "$(redirect_now)" = 1 ] \
    && ok "fresh install: redirect_https=1 now that a covering identity exists" \
    || bad "fresh install: redirect_https is $(redirect_now), want 1"
[ "$(listen_https_now)" = 2 ] \
    && ok "fresh install: the :443 listeners are the ones paired with that identity" \
    || bad "fresh install: listen_https entry count is $(listen_https_now), want 2"
[ "$(reload_count)" = 0 ] \
    && ok "fresh install: uhttpd was NOT reloaded (procd starts it once, from the committed config)" \
    || bad "fresh install: uhttpd reloaded $(reload_count) times before procd started it"

seed_state
run_driver "$SHIPPED_VERSION"       # same version -> verify/repair branch
rc=$?
[ "$rc" = 0 ] && ok "same-version install: run exits 0" \
              || bad "same-version install: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
if grep -q -x 'ssl apply -y --no-restart' "$CLI_CALLS"; then
    ok "same-version install: the verify/repair path provisions too (an upgraded router is the one carrying the placeholder)"
else
    bad "same-version install: no provisioning on the verify/repair path"
fi
[ "$(cert_now)" = "$PROVISIONED_CERT" ] && [ "$(redirect_now)" = 1 ] \
    && ok "same-version install: covering identity adopted, redirect_https=1" \
    || bad "same-version install: cert=$(cert_now) redirect_https=$(redirect_now)"

echo "-- an upgrade of a RUNNING router gets the new identity delivered"
seed_state
touch "$UHTTPD_RUNNING"
run_driver "$SHIPPED_VERSION"
[ "$(reload_count)" = 1 ] \
    && ok "running router: uhttpd reloaded exactly once to deliver the new identity" \
    || bad "running router: uhttpd reload count is $(reload_count), want 1"
[ "$(redirect_now)" = 1 ] && [ "$(cert_now)" = "$PROVISIONED_CERT" ] \
    && ok "running router: committed config carries the covering identity and the derived redirect" \
    || bad "running router: cert=$(cert_now) redirect_https=$(redirect_now)"
rm -f "$UHTTPD_RUNNING"

echo "-- an install must not undo a deliberate 'tollgate ssl remove'"
seed_state
run_driver __ABSENT__
grep -q -x 'ssl apply -y --no-restart' "$CLI_CALLS" \
    && ok "opt-out round trip: precondition — the fresh install provisioned an identity" \
    || bad "opt-out round trip: precondition failed, the fresh install provisioned nothing"
"$TOLLGATE_CLI" ssl remove -y >/dev/null 2>&1
[ -f "$OPTOUT_FILE" ] \
    && ok "opt-out round trip: 'ssl remove' records the operator's decision in the marker" \
    || bad "opt-out round trip: 'ssl remove' left no marker, so the next install would re-provision"
: > "$CLI_CALLS"
: > "$LOGFILE"
run_driver "$SHIPPED_VERSION"      # the reinstall/upgrade path
rc=$?
[ "$rc" = 0 ] && ok "opt-out round trip: the later install still completes" \
              || bad "opt-out round trip: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
if ! grep -q '^ssl apply' "$CLI_CALLS"; then
    ok "opt-out round trip: the later install did NOT re-provision the removed identity"
else
    bad "opt-out round trip: the later install provisioned over the operator's removal ($(cli_calls | tr '\n' ';'))"
fi
[ "$(redirect_now)" = 0 ] && [ "$(cert_now)" = "$UHTTPD_IMAGE_CERT" ] \
    && ok "opt-out round trip: the uhttpd contract holds (image identity, redirect_https=0, :8080 stays reachable)" \
    || bad "opt-out round trip: cert=$(cert_now) redirect_https=$(redirect_now), want $UHTTPD_IMAGE_CERT / 0"
grep -q "removed by the operator" "$LOGFILE" \
    && ok "opt-out round trip: the install log names the opt-out" \
    || bad "opt-out round trip: the install log does not name the opt-out"

echo "-- provisioning unavailable (ssl apply fails): nothing can be provisioned, so the hop must stay OFF"
# The CLI binary itself is a hard dependency of the driver now — its port
# table is the single source 99-tollgate-setup evaluates, and a missing
# binary is a fatal, next-boot-retried install failure by design. The
# invariant this scenario has always pinned is narrower: when TLS
# PROVISIONING cannot happen, the image identity survives and the redirect
# stays off. Model that with the CLI present but its ssl verb failing, which
# is the same tolerance path the old no-CLI fixture exercised.
seed_state
cp "$TOLLGATE_CLI" "$TMP/tollgate-cli.full"
cat > "$TOLLGATE_CLI" <<'SHIM'
#!/bin/sh
printf '%s\n' "$*" >> "${CLI_CALLS:?}"
sub="${1:-}"
shift || true
case "$sub" in
    ui)
        [ "$1 $2 $3" = 'ports --format shell' ] || exit 1
        cat <<'TABLE'
uhttpd_main_http_port_board=8090
uhttpd_main_http_port_luci=8080
uhttpd_main_https_port_board=8443
uhttpd_main_https_port_luci=443
board_http_port_board=8080
board_http_port_luci=8090
board_https_port_board=443
board_https_port_luci=8443
TABLE
        exit 0
        ;;
    ssl) echo "fake tollgate: ssl unavailable" >&2; exit 1 ;;
esac
exit 1
SHIM
chmod +x "$TOLLGATE_CLI"
run_driver __ABSENT__
rc=$?
[ "$rc" = 0 ] && ok "provisioning down: the install still completes" \
              || bad "provisioning down: exit $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
[ "$(cert_now)" = "$UHTTPD_IMAGE_CERT" ] \
    && ok "provisioning down: the image's certificate is kept as the fallback identity (the TLS listener survives)" \
    || bad "provisioning down: uhttpd.main.cert is $(cert_now), want the image's own certificate (fallback)"
[ "$(redirect_now)" = 0 ] \
    && ok "provisioning down: redirect_https=0 — an unverifiable identity never redirects (:8080 stays reachable, no lockout)" \
    || bad "provisioning down: redirect_https is $(redirect_now), want 0"
# The scenario-scoped stub is replaced by the full fake again: the reinstall
# round trip below must see the CLI that CAN provision, not this one.
cp "$TMP/tollgate-cli.full" "$TOLLGATE_CLI"

echo
echo "== D. negative control: the PRE-CHANGE rule accepts the placeholder"
# The same fixture both rules are run against: a freshly-flashed router whose TLS
# identity is the image's placeholder and nothing else — the state the bench was
# in, and the state the 'no CLI available' case above leaves behind.
seed_state
run_pre_change_rule
[ "$(redirect_now)" = 1 ] && [ "$(cert_now)" = "$UHTTPD_IMAGE_CERT" ] \
    && ok "negative control: the pre-change rule derives redirect_https=1 from the placeholder — the defect" \
    || bad "negative control: the pre-change rule gave redirect_https=$(redirect_now) cert=$(cert_now); the fixture does not reproduce the defect"

seed_state
( TOLLGATE_SETUP_LIB_ONLY=1 sh -c ". '$SCRIPT_UNDER_TEST'; setup_uhttpd_tls_identity" ) 2>/dev/null
[ "$(redirect_now)" = 0 ] && [ "$(cert_now)" = "$UHTTPD_IMAGE_CERT" ] \
    && ok "the shipped rule derives redirect_https=0 from the same fixture (the fix, same input)" \
    || bad "the shipped rule gave redirect_https=$(redirect_now) cert=$(cert_now) on the negative-control fixture"

echo
echo "== E. an operator's own covering identity survives an install (review finding F2)"
# The install path keyed its idempotence on THIS SCRIPT'S OWN output path
# (/etc/tollgate/ssl/server.crt): nothing was generated while that file covered
# the router, and everything was generated the moment it did not. A router whose
# uhttpd.main already presented a certificate the OPERATOR owns — a CA-signed one
# installed by hand, or the pair their own `tollgate ssl apply <cert> <key>`
# installed — therefore had that certificate replaced by a fresh self-signed
# identity on the next install or upgrade: a trusted certificate silently
# downgraded, and a covering identity re-keyed behind the owner's back.
#
# Idempotence is a question about the CERTIFICATE uhttpd.main presents, not about
# the path this script writes. The harness copy is rebuilt here because the
# "no CLI" case above rewrites TOLLGATE_CLI in it.
build_script "$ROOT/$SCRIPT"

echo "-- function level: nothing is generated while a covering identity is already served"
seed_state
seed_operator_identity
( TOLLGATE_SETUP_LIB_ONLY=1 sh -c ". '$SCRIPT_UNDER_TEST'; provision_tls_identity" ) 2>/dev/null
if grep -q '^ssl apply' "$CLI_CALLS"; then
    bad "operator identity: provision_tls_identity re-keyed a router that already serves a covering certificate ($(cli_calls | tr '\n' ';'))"
else
    ok "operator identity: provision_tls_identity left a covering configured identity alone"
fi
[ ! -s "$PROVISIONED_CERT" ] \
    && ok "operator identity: no self-signed identity was generated beside the operator's pair" \
    || bad "operator identity: a self-signed identity was written over the operator's pair"

( TOLLGATE_SETUP_LIB_ONLY=1 sh -c ". '$SCRIPT_UNDER_TEST'; setup_uhttpd_tls_identity" ) 2>/dev/null
[ "$(cert_now)" = "$OPERATOR_CERT" ] \
    && ok "operator identity: uhttpd.main.cert is still the operator's certificate" \
    || bad "operator identity: uhttpd.main.cert is $(cert_now), want the operator's $OPERATOR_CERT"
[ "$(redirect_now)" = 1 ] \
    && ok "operator identity: the covering identity still arms the derived :8080 -> https:// hop" \
    || bad "operator identity: redirect_https=$(redirect_now), want 1 — the operator's certificate covers this router"

echo "-- end to end: a same-version reinstall on that router changes nothing"
seed_state
seed_operator_identity
run_driver "$SHIPPED_VERSION"
rc=$?
[ "$rc" = 0 ] && ok "operator identity: the reinstall exits 0" \
              || bad "operator identity: the reinstall exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
if grep -q '^ssl apply' "$CLI_CALLS"; then
    bad "operator identity: the reinstall ran the generator over the operator's certificate ($(cli_calls | tr '\n' ';'))"
else
    ok "operator identity: the reinstall never ran the generator"
fi
[ "$(cert_now)" = "$OPERATOR_CERT" ] && [ "$(redirect_now)" = 1 ] \
    && ok "operator identity: the router still serves its own certificate, hop armed" \
    || bad "operator identity: cert=$(cert_now) redirect_https=$(redirect_now), want $OPERATOR_CERT / 1"

echo
printf 'tests: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
