#!/usr/bin/env bash
# hygiene_test.sh — proves the #947 hygiene identification logic with live
# fixtures: our leaked module (killed), a foreign :2121 holder (refused,
# still alive), an unrelated-name process on another port (never even a
# candidate), and dry-run (nothing signaled). Run from anywhere:
#   bash tests/happy-path/hygiene_test.sh
set -u
HERE="$(cd "$(dirname "$0")" && pwd)"
. "$HERE/hygiene.sh"

PORT="${HP_HYGIENE_TEST_PORT:-2179}"
FOREIGN_PORT=$((PORT + 1))
FAILED=0

say() { printf 'HYGTEST %s\n' "$*"; }
expect() {
    if [ "$2" = "$3" ]; then say "PASS $1 ($3)"; else say "FAIL $1: expected [$2] got [$3]"; FAILED=1; fi
}
alive() { kill -0 "$1" 2>/dev/null; }

TMP="$(mktemp -d /tmp/hygiene-fixture.XXXXXX)"
trap 'kill $(jobs -p) 2>/dev/null; rm -rf "$TMP"' EXIT
ART="$TMP/artifact/usr/bin"; mkdir -p "$ART"

cat > "$ART/tollgate-wrt" <<'PY'
#!/usr/bin/env python3
import socket, sys, time
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", int(sys.argv[1]))); s.listen(4)
while True: time.sleep(3600)
PY
chmod +x "$ART/tollgate-wrt"

"$ART/tollgate-wrt" "$PORT" & OUR_LEAK=$!
python3 -c "
import socket, time
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', $FOREIGN_PORT)); s.listen(4)
while True: time.sleep(3600)
" & FOREIGN_SVC=$!
bash -c 'exec -a vlab-mockgate sleep 3600' & DECOY_NAME=$!
sleep 1

say "fixtures: our-leak=$OUR_LEAK (cmdline0 under $ART, port $PORT)"
say "          foreign=$FOREIGN_SVC (python exe, port $FOREIGN_PORT)"
say "          decoy=$DECOY_NAME (name vlab-mockgate, no port)"

out="$(hygiene_scan --port "$PORT" --root "$TMP" 2>&1)"
echo "$out" | sed 's/^/    /'
echo "$out" | grep -q "pid=$OUR_LEAK -> ours:cmdline=" && expect "scan identifies our leak" yes yes || expect "scan identifies our leak" yes no
[ "$out" = "${out/REFUSING/}" ] && expect "scan mode never mentions refusal" yes yes || expect "scan mode never mentions refusal" yes no

dry="$(hygiene_teardown --dry-run --port "$PORT" --root "$TMP" 2>&1)"
echo "$dry" | sed 's/^/    /'
alive "$OUR_LEAK" && expect "dry-run leaves our leak alive" yes yes || expect "dry-run leaves our leak alive" yes no

td="$(hygiene_teardown --port "$PORT" --root "$TMP" 2>&1)"
echo "$td" | sed 's/^/    /'
sleep 0.5
alive "$OUR_LEAK" && expect "teardown kills our leak" no yes || expect "teardown kills our leak" no no
alive "$DECOY_NAME" && expect "decoy process untouched" yes yes || expect "decoy process untouched" yes no

foreign_pid_on_our_port() {
    ss -ltn 2>/dev/null | awk -v p=":$PORT" '$4 ~ p"$"' | grep -q . && echo held || echo free
}
expect "port freed after teardown" free "$(foreign_pid_on_our_port)"

python3 -c "
import socket, time
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(('127.0.0.1', $PORT)); s.listen(4)
while True: time.sleep(3600)
" & FOREIGN_ON_PORT=$!
sleep 0.5
tdf="$(hygiene_teardown --port "$PORT" --root "$TMP" 2>&1)"
echo "$tdf" | sed 's/^/    /'
echo "$tdf" | grep -q "REFUSING to kill.*$PORT" && expect "foreign port-holder refused with evidence" yes yes || expect "foreign port-holder refused with evidence" yes no
alive "$FOREIGN_ON_PORT" && expect "foreign port-holder still alive" yes yes || expect "foreign port-holder still alive" yes no
alive "$FOREIGN_SVC" && expect "unrelated listener still alive" yes yes || expect "unrelated listener still alive" yes no

if [ "$FAILED" = "0" ]; then say "ALL PASS"; exit 0; else say "FAILURES PRESENT"; exit 1; fi
