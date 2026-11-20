#!/usr/bin/env bash
# Guard test for the tollgate-wrt init script's stop path (issue #27).
#
# A manual/setsid-started tollgate-wrt instance must not outlive service
# management: `service tollgate-wrt restart|stop` has to sweep processes
# procd does not own, or they keep serving :2121 with stale in-memory
# state (rate limiter, wallet caches) across every "restart".
#
# The init script is exercised through a stub environment: a fake
# /etc/rc.common harness sources it and invokes stop_service(), with
# pgrep/kill/sleep stubs on PATH that record calls and simulate a stray
# process. No OpenWrt required:
#
#   bash packaging/tests/initd_stop_test.sh
#
# Exits non-zero if any assertion fails.

set -uo pipefail

REPO_ROOT=$(git -C "$(dirname -- "$0")" rev-parse --show-toplevel)
INITD="$REPO_ROOT/packaging/files/etc/init.d/tollgate-wrt"

fail=0
pass=0
ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; [ $# -gt 1 ] && printf '       %s\n' "$2"; }

[ -f "$INITD" ] || { echo "FAIL: init script not found: $INITD"; exit 1; }

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT
BIN="$TMP/bin"
mkdir -p "$BIN"

# --- static contract -------------------------------------------------------

grep -q '^stop_service()' "$INITD"
if [ $? -eq 0 ]; then ok "init script defines stop_service() (the procd stop hook)"; else
    bad "init script defines stop_service()" "procd calls this hook before terminating its instances; without it there is no place to sweep strays"
fi

if grep -q '^stop()' "$INITD"; then
    bad "init script must NOT override stop()" "an overridden stop() replaces rc.common's procd_kill teardown (issue #27 root cause)"
else
    ok "init script does not override stop()"
fi

grep -q 'procd_set_param pidfile' "$INITD"
if [ $? -eq 0 ]; then ok "start_service registers a pidfile for the managed instance"; else
    bad "start_service registers a pidfile" "procd_set_param pidfile gives operators a managed-instance pid file"
fi

# --- dynamic stop path -----------------------------------------------------
#
# fake rc.common: sources the init script, then invokes stop_service with
# stubs. pgrep/sleep stub via PATH; kill is overridden as a shell FUNCTION
# because `kill` is a POSIX builtin — a PATH stub can never intercept it
# (functions take precedence over builtins). The stray process is pid
# 4242; the pgrep stub reports it while the state file exists, and only a
# SIGKILL (recorded by the kill stub) removes it — so the test also proves
# the TERM→KILL escalation against a TERM-immune stray.

cat > "$TMP/rc.common" <<'HARNESS'
#!/bin/sh
# test harness standing in for /etc/rc.common
. "$1"
kill()  { "$BIN/kill"  "$@"; }
sleep() { "$BIN/sleep" "$@"; }
stop_service
HARNESS

cat > "$BIN/pgrep" <<'STUB'
#!/bin/sh
# usage: pgrep -f <pattern>; reports the stray while it is alive
[ -f "$(dirname "$0")/../stray.alive" ] && echo 4242
exit 0
STUB

cat > "$BIN/kill" <<'STUB'
#!/bin/sh
# usage: kill [-SIG] <pids...>; TERM bounces off the stray, KILL removes it
log="$(dirname "$0")/../kill.log"
sig=TERM
case "$1" in
    -*) sig="${1#-}"; shift ;;
esac
for pid in "$@"; do
    echo "$sig $pid" >> "$log"
done
if [ "$sig" = "KILL" ] || [ "$sig" = "9" ]; then
    rm -f "$(dirname "$0")/../stray.alive"
fi
exit 0
STUB

cat > "$BIN/sleep" <<'STUB'
#!/bin/sh
# collapse the graceful-wait loop: the stubbed pgrep result never changes
# within a run, so make every sleep instant
exit 0
STUB

chmod +x "$BIN/pgrep" "$BIN/kill" "$BIN/sleep"
touch "$TMP/stray.alive"

BIN="$BIN" PATH="$BIN:$PATH" sh "$TMP/rc.common" "$INITD" >/dev/null 2>&1

if grep -q 'TERM 4242' "$TMP/kill.log" 2>/dev/null; then
    ok "stop_service sends SIGTERM to pgrep-matched stray instances"
else
    bad "stop_service SIGTERMs stray instances" "kill.log: $(cat "$TMP/kill.log" 2>/dev/null)"
fi

if grep -Eq '(KILL|9) 4242' "$TMP/kill.log" 2>/dev/null; then
    ok "stop_service escalates to SIGKILL when the stray survives TERM"
else
    bad "stop_service escalates to SIGKILL" "a TERM-immune stray must be forced (kill.log: $(cat "$TMP/kill.log" 2>/dev/null))"
fi

if [ ! -f "$TMP/stray.alive" ]; then
    ok "stray instance is gone after stop_service"
else
    bad "stray instance still alive after stop_service"
fi

printf '\n%d passed, %d failed\n' "$pass" "$fail"
[ "$fail" -eq 0 ]
