#!/usr/bin/env bash
# Offline test for setup_management_alias() (99-tollgate-setup) — no router.
#
# The package points the fixed name `tollgate.lan` at the router's MANAGEMENT
# address (network.private.ipaddr) so the board and LuCI are reachable by name
# on EVERY install path (the wizard used to be the only writer). It must:
#   * write "<ip> tollgate.lan tollgate.local" to the hosts file and
#     dhcp.@dnsmasq[0].address=/tollgate.lan/<ip>;
#   * be idempotent (a second run adds no duplicate);
#   * NOT set nodogsplash gatewaydomainname (#428: redirect loop);
#   * do nothing when there is no management address.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0; FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"
export UCI_STATE="$TMP/uci.state"
export LOGFILE="$TMP/setup.log"
export HOSTS_FILE="$TMP/hosts"
: > "$UCI_STATE"; : > "$LOGFILE"; printf '127.0.0.1 localhost\n' > "$HOSTS_FILE"

cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
state="${UCI_STATE:?}"; q=0
[ "${1:-}" = "-q" ] && { q=1; shift; }
cmd="${1:-}"; shift || true
case "$cmd" in
  get)
    vals="$(grep -F -- "$1=" "$state" 2>/dev/null | cut -d= -f2-)"
    if [ -z "$vals" ]; then [ "$q" = 1 ] || echo "uci: Entry not found" >&2; exit 1; fi
    printf '%s\n' "$vals" ;;
  add_list) printf '%s\n' "$1" >> "$state" ;;
  del_list) grep -v -F -x -- "$1" "$state" > "$state.t" 2>/dev/null; mv "$state.t" "$state" ;;
  set) key="${1%%=*}"; grep -v -F -- "$key=" "$state" > "$state.t" 2>/dev/null; mv "$state.t" "$state"; printf '%s\n' "$1" >> "$state" ;;
  delete|commit|revert) : ;;
  *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"
PATH="$TMP/bin:$PATH"; export PATH

TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1 || true
command -v setup_management_alias >/dev/null 2>&1 || { echo "FAIL: setup_management_alias not exposed" >&2; exit 2; }

seed() { : > "$UCI_STATE"; [ -n "${1:-}" ] && printf 'network.private.ipaddr=%s\n' "$1" >> "$UCI_STATE"; : > "$LOGFILE"; printf '127.0.0.1 localhost\n' > "$HOSTS_FILE"; }

echo "== sets the alias to the management IP (CIDR stripped) =="
seed "10.238.60.1/24"
setup_management_alias
grep -q "^10.238.60.1 tollgate.lan tollgate.local$" "$HOSTS_FILE" \
    && ok "hosts gets '<ip> tollgate.lan tollgate.local'" \
    || bad "hosts missing the alias ($(cat "$HOSTS_FILE"))"
grep -q "dhcp.@dnsmasq\[0\].address=/tollgate.lan/10.238.60.1" "$UCI_STATE" \
    && ok "dnsmasq address=/tollgate.lan/<ip>" \
    || bad "dnsmasq address missing ($(grep -c . "$UCI_STATE") lines)"

echo "== idempotent =="
setup_management_alias
[ "$(grep -c ' tollgate\.lan' "$HOSTS_FILE")" = "1" ] \
    && ok "hosts has exactly one alias line" \
    || bad "hosts alias duplicated ($(grep -c ' tollgate\.lan' "$HOSTS_FILE"))"
[ "$(grep -c '/tollgate.lan/' "$UCI_STATE")" = "1" ] \
    && ok "dnsmasq has exactly one alias entry" \
    || bad "dnsmasq alias duplicated ($(grep -c '/tollgate.lan/' "$UCI_STATE"))"

echo "== does NOT set nodogsplash gatewaydomainname (#428) =="
grep -q "gatewaydomainname" "$UCI_STATE" \
    && bad "gatewaydomainname was set — re-arms the #428 redirect loop" \
    || ok "no gatewaydomainname set"

echo "== an existing alias is replaced, not appended =="
printf '192.168.2.1 tollgate.lan tollgate.local\n' >> "$HOSTS_FILE"
seed "10.99.99.1"
setup_management_alias
[ "$(grep -c ' tollgate\.lan' "$HOSTS_FILE")" = "1" ] && grep -q "^10.99.99.1 tollgate.lan" "$HOSTS_FILE" \
    && ok "stale alias replaced with the current address" \
    || bad "stale alias not replaced ($(cat "$HOSTS_FILE"))"

echo "== no management address -> no alias =="
seed ""
setup_management_alias
grep -q "tollgate.lan" "$HOSTS_FILE" \
    && bad "wrote the alias without an address" \
    || ok "no alias without network.private.ipaddr"

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
