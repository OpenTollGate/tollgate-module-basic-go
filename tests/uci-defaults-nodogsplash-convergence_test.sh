#!/usr/bin/env bash
# Offline tests for the nodogsplash POLICY-CONVERGENCE step of
# packaging/files/etc/uci-defaults/99-tollgate-setup (card t_3ac1bb9d).
#
# The real bug this guards: `users_to_router` is nodogsplash's PRE-AUTHENTICATION
# allow list, and nodogsplash turns that list into a ruleset ONCE, when it starts
# (the `ndsRTR` iptables chain it installs for `br-lan`). Nothing in the
# packaging path reloads the service: the module's uci-defaults script
# deliberately does not restart services (correct on a fresh boot, where procd
# starts nodogsplash once, with the freshly written config) and the postinst that
# actually ships in the published package (measured 2026-09-25: extracted from
# /lib/apk/db/scripts.tar.gz on the bench) runs the uci-defaults scripts and then
# restarts only `tollgate-wrt`. So after an install/upgrade of a RUNNING router
# the config says one thing and the process does another:
#
#   uci show nodogsplash | grep users_to_router   -> 8090 absent
#   iptables -S ndsRTR                            -> -A ndsRTR ... --dport 8090 -j ACCEPT
#   curl http://<router>:8090/ from a br-lan box  -> 200   (measured on the bench)
#
# ...until an operator runs `/etc/init.d/nodogsplash restart`, which dropped it
# to 000 with the portal unaffected.
#
# The step under test therefore compares the CONFIGURED pre-auth TCP ports with
# the ports the RUNNING ruleset actually accepts, and reloads the service when
# they differ — never on first boot, and never when they already match.
#
# Nothing here needs a router, the SDK or the network: the setup script is copied
# into a temp dir (its three absolute paths — SETUP_FLAG, LOGFILE and NDS_INIT —
# are rewritten by sed, and the test asserts the rewrite landed) and run with
# /bin/sh against fake `uci`, `apk`, `pidof` and `iptables` shims plus a fake
# nodogsplash init script. The fake service regenerates its "running" ruleset
# from the COMMITTED config on reload, exactly like the real one reads
# /etc/config/nodogsplash — which is what makes the assertions below able to
# tell a reload that ran after `uci commit` from one that ran before it.
#
# Usage: bash tests/uci-defaults-nodogsplash-convergence_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin" "$TMP/init.d"

# Ports the shipped writer is expected to keep in the allow list, and the two
# the admin-board fix removed. The convergence step does not care which ports
# they are — it compares sets — but the fixtures use the real ones so a failure
# reads like the incident.
KEY="nodogsplash.@nodogsplash[0].users_to_router"
CORE_PORTS="2121 8080 2050 2051 443"
ADMIN_PORT=8090

# ---------------------------------------------------------------- fake apk
FAKE_VERSION="0.6.0_alpha4-r1"
export FAKE_APK_VERSION="$FAKE_VERSION"
cat > "$TMP/bin/apk" <<'SHIM'
#!/bin/sh
# fake apk — only `apk list --installed` is consulted by the setup script.
if [ "${1:-}" = "list" ]; then
    printf '%s\n' "tollgate-wrt-${FAKE_APK_VERSION} aarch64_cortex-a53 {tollgate-wrt} (GPL-3.0-only) [installed]"
fi
exit 0
SHIM
chmod +x "$TMP/bin/apk"

# ---------------------------------------------------------------- fake uci
# Flat-file stand-in: one "key=value" line per option, list options stored as one
# line per entry. `commit` additionally publishes the nodogsplash list to
# $COMMITTED — the file the fake service reads when it regenerates its ruleset,
# standing in for /etc/config/nodogsplash.
export UCI_STATE="$TMP/uci.state"
export COMMITTED="$TMP/committed-nodogsplash"
export UCI_LIST_SEP="${UCI_LIST_SEP:-}"
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
    add_list)
        printf '%s\n' "$1" >> "$state"
        ;;
    add)
        printf '%s=%s\n' "${2:-section}" "${1:-unknown}" >> "$state"
        ;;
    delete)
        grep -v -F -- "$1=" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    del_list)
        # entries carry spaces ("allow tcp port 8090") -> whole-line match
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
        mv "$state.tmp" "$state"
        ;;
    commit)
        # publish what is on disk for the fake service to read (the real service
        # regenerates its ruleset from /etc/config/nodogsplash, which only a
        # commit writes — that is what makes an ordering bug visible)
        grep -F 'nodogsplash.@nodogsplash[0].users_to_router=' "$state" 2>/dev/null \
            | cut -d= -f2- > "${COMMITTED:?}"
        ;;
    show|export|revert) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"

# --------------------------------------------------------------- fake pidof
# `pidof nodogsplash` succeeds only while the file $NDS_RUNNING exists.
export NDS_RUNNING="$TMP/nds-running"
cat > "$TMP/bin/pidof" <<'SHIM'
#!/bin/sh
[ -f "${NDS_RUNNING:?}" ]
SHIM
chmod +x "$TMP/bin/pidof"

# ------------------------------------------------------------ fake iptables
# Only `iptables -S ndsRTR` — the chain nodogsplash installs for the captive
# bridge, which is where a users_to_router entry ends up — is consulted. A
# missing state file means the chain does not exist (exit 1), like the real one.
export IPTS_STATE="$TMP/iptables-ndsRTR"
cat > "$TMP/bin/iptables" <<'SHIM'
#!/bin/sh
if [ "${1:-}" = "-S" ] && [ "${2:-}" = "ndsRTR" ]; then
    [ -f "${IPTS_STATE:?}" ] || exit 1
    cat "$IPTS_STATE"
    exit 0
fi
exit 0
SHIM
chmod +x "$TMP/bin/iptables"

export PATH="$TMP/bin:$PATH"

# --------------------------------------------- fake nodogsplash init script
# Stands in for /etc/init.d/nodogsplash. `reload`/`restart` regenerate the
# running ruleset from the COMMITTED config — the real service's behaviour, and
# the reason an ordering bug (converge before commit) shows up as a stale
# ruleset rather than as a silent pass.
export SERVICE_CALLS="$TMP/service-calls"
export FAKE_SERVICE_EXIT="${FAKE_SERVICE_EXIT:-0}"
FAKE_INIT="$TMP/init.d/nodogsplash"
cat > "$FAKE_INIT" <<'SHIM'
#!/bin/sh
case "${1:-}" in
    reload|restart|start|stop|status|running)
        printf '%s\n' "$1" >> "${SERVICE_CALLS:?}"
        if [ "${1:-}" = "status" ] || [ "${1:-}" = "running" ]; then
            [ -f "${NDS_RUNNING:?}" ]
            exit $?
        fi
        [ "${FAKE_SERVICE_EXIT:-0}" = "0" ] || exit "$FAKE_SERVICE_EXIT"
        # Regenerate the pre-auth accept rules from the committed config.
        : > "${IPTS_STATE:?}"
        printf '%s\n' '-A ndsRTR -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT' >> "$IPTS_STATE"
        printf '%s\n' '-A ndsRTR -m mark --mark 0x20000/0x30000 -j ACCEPT' >> "$IPTS_STATE"
        printf '%s\n' '-A ndsRTR -p tcp -m tcp --dport 2050 -j ACCEPT' >> "$IPTS_STATE"
        while IFS= read -r entry; do
            case "$entry" in
                *"allow tcp port "*)
                    printf -- '-A ndsRTR -p tcp -m tcp --dport %s -j ACCEPT\n' \
                        "${entry##*allow tcp port }" >> "$IPTS_STATE"
                    ;;
            esac
        done < "${COMMITTED:?}"
        printf '%s\n' '-A ndsRTR -j REJECT --reject-with icmp-port-unreachable' >> "$IPTS_STATE"
        ;;
esac
exit 0
SHIM
chmod +x "$FAKE_INIT"

# --------------------------------------------------- the script under test
# The three absolute paths the script owns are redirected; the body — including
# every allow-list write and the convergence step — is the shipped code, run by
# /bin/sh.
FLAG="$TMP/tollgate-setup-done"
LOGFILE="$TMP/setup.log"
SCRIPT_UNDER_TEST="$TMP/99-tollgate-setup"
sed -e "s|^SETUP_FLAG=\"/etc/tollgate-setup-done\"\$|SETUP_FLAG=\"$FLAG\"|" \
    -e "s|^LOGFILE=/tmp/tollgate-setup\.log\$|LOGFILE=$LOGFILE|" \
    -e "s|^NDS_INIT=\"/etc/init.d/nodogsplash\"\$|NDS_INIT=\"$FAKE_INIT\"|" \
    "$ROOT/$SCRIPT" > "$SCRIPT_UNDER_TEST"
if grep -q "^SETUP_FLAG=\"$FLAG\"\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^LOGFILE=$LOGFILE\$" "$SCRIPT_UNDER_TEST" &&
   grep -q "^NDS_INIT=\"$FAKE_INIT\"\$" "$SCRIPT_UNDER_TEST"; then
    ok "test harness redirected SETUP_FLAG, LOGFILE and NDS_INIT in the copied script"
else
    bad "could not redirect SETUP_FLAG/LOGFILE/NDS_INIT in the copied setup script"
fi

if sh -n "$ROOT/$SCRIPT" 2>"$TMP/syntax.err"; then
    ok "the shipped setup script is valid /bin/sh"
else
    bad "the shipped setup script does not parse: $(head -n 2 "$TMP/syntax.err" | tr '\n' ' ')"
fi

# The same driver also runs the admin-credential gate, which reads the live
# /etc/shadow and would invoke `passwd root` on an empty hash. Pin both files to
# fixtures — a router whose credential is already set, so it is a no-op here.
export SHADOW_FILE="$TMP/shadow"
export PASSWD_FILE="$TMP/passwd.db"
printf 'root:$1$fixture$0123456789abcdef:0:0:99999:7:::\n' > "$SHADOW_FILE"
: > "$PASSWD_FILE"

# ------------------------------------------------------------- fixtures/helpers
seed_uci() { # seed_uci <port>... — the CONFIGURED users_to_router list
    : > "$UCI_STATE"
    printf '%s=nodogsplash\n' 'nodogsplash.@nodogsplash[0]' >> "$UCI_STATE"
    local p
    for p in "$@"; do printf '%s=%s\n' "$KEY" "allow tcp port $p" >> "$UCI_STATE"; done
    # /etc/config/nodogsplash holds what was last committed; before this run that
    # is the pre-run list.
    cp "$UCI_STATE" "$TMP/uci.state.pre"
    uci commit nodogsplash 2>/dev/null
}
seed_runtime() { # seed_runtime <port>... — the rules the RUNNING process installed
    : > "$IPTS_STATE"
    printf '%s\n' '-A ndsRTR -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT' >> "$IPTS_STATE"
    printf '%s\n' '-A ndsRTR -m mark --mark 0x20000/0x30000 -j ACCEPT' >> "$IPTS_STATE"
    printf '%s\n' '-A ndsRTR -p tcp -m tcp --dport 2050 -j ACCEPT' >> "$IPTS_STATE"
    local p
    for p in "$@"; do
        printf -- '-A ndsRTR -p tcp -m tcp --dport %s -j ACCEPT\n' "$p" >> "$IPTS_STATE"
    done
    printf '%s\n' '-A ndsRTR -j REJECT --reject-with icmp-port-unreachable' >> "$IPTS_STATE"
}
runtime_ports() { # sorted TCP ports the RUNNING ruleset accepts
    grep -- '-j ACCEPT' "$IPTS_STATE" 2>/dev/null \
        | sed -n 's/.*--dport \([0-9][0-9]*\).*/\1/p' | sort -n -u | tr '\n' ' ' \
        | sed 's/ $//'
}
configured_ports() { # sorted TCP ports the CONFIGURED list holds
    grep -F "$KEY=" "$UCI_STATE" 2>/dev/null | cut -d= -f2- \
        | tr "'" '\n' | sed -n 's/^ *allow tcp port \([0-9][0-9]*\) *$/\1/p' \
        | sort -n -u | tr '\n' ' ' | sed 's/ $//'
}
reloads() { grep -c -E '^(reload|restart)$' "$SERVICE_CALLS" 2>/dev/null | tr -d ' '; }
reset_calls() { : > "$SERVICE_CALLS"; }

running() { : > "$NDS_RUNNING"; }
not_running() { rm -f "$NDS_RUNNING"; }

run_same_version() { # run the copied script with the flag already set
    printf '%s\n' "$FAKE_VERSION" > "$FLAG"
    : > "$LOGFILE"
    reset_calls
    sh "$SCRIPT_UNDER_TEST" >/dev/null 2>"$TMP/run.err"
    return $?
}
converged_log() { grep -c 'nodogsplash policy convergence' "$LOGFILE" 2>/dev/null | tr -d ' '; }

# ----------------------------------------------------------------------------
# 1. THE REPORTED DEFECT: the configured list is already correct, the RUNNING
#    ruleset is stale. This is the shape measured on the bench after the pre16
#    install (uci without :8090, ndsRTR still accepting it).
# ----------------------------------------------------------------------------
echo "== a stale RUNNING ruleset converges even when the configured list is already correct"
seed_uci $CORE_PORTS
seed_runtime $CORE_PORTS $ADMIN_PORT
running
run_same_version
rc=$?
[ "$rc" = 0 ] && ok "same-version run exits 0" \
              || bad "same-version run exited $rc (stderr: $(head -n 3 "$TMP/run.err" | tr '\n' ' '))"
[ "$(reloads)" = 1 ] && ok "the service was reloaded exactly once" \
                     || bad "expected exactly 1 reload, saw $(reloads)"
[ "$(runtime_ports)" = "$(configured_ports)" ] \
    && ok "the running ruleset now matches the configured list [$CORE_PORTS]" \
    || bad "running ruleset [$(runtime_ports)] still differs from the configured list [$(configured_ports)]"
case " $(runtime_ports) " in
    *" $ADMIN_PORT "*) bad "the removed :$ADMIN_PORT accept rule is still in the running ruleset" ;;
    *) ok "the removed :$ADMIN_PORT accept rule is gone from the running ruleset" ;;
esac
grep -q 'nodogsplash policy convergence: reloaded' "$LOGFILE" 2>/dev/null \
    && ok "the reload is logged as 'nodogsplash policy convergence: reloaded'" \
    || bad "no 'nodogsplash policy convergence: reloaded' line in the log ($(head -n 2 "$LOGFILE" 2>/dev/null | tr '\n' ' '))"
grep -qE 'accept rules [0-9]+ -> [0-9]+' "$LOGFILE" 2>/dev/null \
    && ok "the log line carries the pre-auth accept-rule count before -> after" \
    || bad "the convergence log line does not report the accept-rule count"

# ----------------------------------------------------------------------------
# 2. Steady state: nothing changed and the ruleset matches — the service must NOT
#    be restarted (an unconditional restart on every setup run would drop every
#    live session and is exactly what "first boot is fine, upgrades are not" got
#    wrong in the other direction).
# ----------------------------------------------------------------------------
echo "== a matching ruleset is left alone (no needless restart)"
seed_uci $CORE_PORTS
seed_runtime $CORE_PORTS
running
run_same_version
[ "$(reloads)" = 0 ] && ok "no reload when the running ruleset already matches" \
                     || bad "reloaded $(reloads) time(s) with nothing to converge"
grep -q 'already matches' "$LOGFILE" 2>/dev/null \
    && ok "the no-op is logged as 'already matches'" \
    || bad "no 'already matches' line in the log"

# ----------------------------------------------------------------------------
# 3. First boot: uci-defaults runs from /etc/init.d/boot BEFORE procd starts
#    services, so nodogsplash is not up and must not be touched — procd starts it
#    once, with the freshly written config. (This is the invariant the module's
#    "no service restarts here" rule was protecting; it must survive the fix.)
# ----------------------------------------------------------------------------
echo "== first boot: nodogsplash is not running, so nothing is restarted"
seed_uci $CORE_PORTS
seed_runtime $CORE_PORTS $ADMIN_PORT
not_running
run_same_version
[ "$(reloads)" = 0 ] && ok "no reload while nodogsplash is not running (first boot)" \
                     || bad "reloaded $(reloads) time(s) although the service was not running"
grep -qi 'not running' "$LOGFILE" 2>/dev/null \
    && ok "the first-boot skip is logged" \
    || bad "the first-boot skip is not logged"

# ----------------------------------------------------------------------------
# 4. An unwritable/absent ruleset reading must not turn into a restart loop:
#    when the chain cannot be read at all there is nothing to compare, so the
#    step reports it and leaves the service alone.
# ----------------------------------------------------------------------------
echo "== an unreadable ruleset is reported, not restarted in a loop"
seed_uci $CORE_PORTS
rm -f "$IPTS_STATE"
running
run_same_version
rc=$?
[ "$rc" = 0 ] && ok "run still exits 0 when the ruleset cannot be read" \
              || bad "run exited $rc when the ruleset could not be read"
[ "$(reloads)" = 0 ] && ok "no reload when there is no ruleset to compare against" \
                     || bad "reloaded $(reloads) time(s) with no readable ruleset"
grep -q 'nodogsplash policy convergence' "$LOGFILE" 2>/dev/null \
    && ok "the unreadable-ruleset case is logged" \
    || bad "the unreadable-ruleset case is not logged"

# ----------------------------------------------------------------------------
# 5. A failing reload must be logged and must not fail the setup run.
# ----------------------------------------------------------------------------
echo "== a failing reload is logged and non-fatal"
seed_uci $CORE_PORTS
seed_runtime $CORE_PORTS $ADMIN_PORT
running
FAKE_SERVICE_EXIT=1 run_same_version
rc=$?
[ "$rc" = 0 ] && ok "run still exits 0 after a failed reload" \
              || bad "run exited $rc after a failed reload"
grep -q 'reload' "$LOGFILE" 2>/dev/null \
    && ok "the attempted reload is logged even when it fails" \
    || bad "the failed reload is not logged"
FAKE_SERVICE_EXIT=0

# ----------------------------------------------------------------------------
# 6. Ordering: the driver's full-setup path must converge AFTER commit_all — the
#    service reads /etc/config, not the uci delta, so a reload that ran before
#    the commit would regenerate the OLD ruleset and silently converge to the
#    wrong policy. The full setup is not executed here (it rewrites /etc/profile
#    and the live network config), so the order is asserted on the driver text
#    and the step itself is exercised through the lib-only seam below.
# ----------------------------------------------------------------------------
echo "== the full-setup driver converges after commit_all"
commit_line=$(grep -n '^commit_all$' "$SCRIPT" | tail -n1 | cut -d: -f1)
converge_line=$(grep -n '^converge_nodogsplash_runtime$' "$SCRIPT" | tail -n1 | cut -d: -f1)
if [ -n "$commit_line" ] && [ -n "$converge_line" ]; then
    if [ "$converge_line" -gt "$commit_line" ]; then
        ok "converge_nodogsplash_runtime runs after commit_all (line $converge_line > $commit_line)"
    else
        bad "converge_nodogsplash_runtime runs before commit_all (line $converge_line < $commit_line) — the service would reload the pre-commit config"
    fi
else
    bad "the full-setup driver does not call converge_nodogsplash_runtime after commit_all"
fi

# ----------------------------------------------------------------------------
# 7. The step through the lib-only seam: same contract when called directly, and
#    idempotent (a second call after convergence does nothing).
# ----------------------------------------------------------------------------
echo "== the convergence step is idempotent on its own"
TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1
# Sourcing runs the script's top-level assignments, so NDS_INIT is back to the
# absolute path the router uses; point it at the fixture the driver would use.
NDS_INIT="$FAKE_INIT"
if command -v converge_nodogsplash_runtime >/dev/null 2>&1; then
    ok "script exposes converge_nodogsplash_runtime()"
    seed_uci $CORE_PORTS
    seed_runtime $CORE_PORTS $ADMIN_PORT
    running
    reset_calls
    converge_nodogsplash_runtime >/dev/null 2>&1
    [ "$(reloads)" = 1 ] && ok "writer: one reload converges the stale ruleset" \
                         || bad "writer: expected 1 reload, saw $(reloads)"
    reset_calls
    converge_nodogsplash_runtime >/dev/null 2>&1
    [ "$(reloads)" = 0 ] && ok "writer: a second call is a no-op (already converged)" \
                         || bad "writer: the second call reloaded $(reloads) time(s)"
else
    bad "script does not expose converge_nodogsplash_runtime() — the allow list has no runtime convergence"
fi

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
