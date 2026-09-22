#!/usr/bin/env bash
# Offline test for the pre-auth :443 allow rule in 99-tollgate-setup's
# setup_nodogsplash() — no router, no nodogsplash daemon, no network needed.
#
# The real bug this guards: uhttpd.main carries `redirect_https=1` whenever a
# cert/key pair is readable, so a captive-LAN client that hits the LuCI port on
# :8080 is answered with `307 Location: https://<router>/`. nodogsplash REJECTs
# :443 for an unauthenticated client unless `allow tcp port 443` is in
# users_to_router — so the redirect dead-ended on a blocked port and LuCI was
# unreachable before authentication: exactly the operator lockout
# docs/architecture/uhttpd-redirect-https-ownership-decision.md exists to
# prevent. The same file already allows :8443 (the admin board's opt-in HTTPS
# port) and the Go CLI adds :443 for its own SSL path
# (src/cmd/tollgate-cli/ssl.go: allowPort443), so the first-boot path is the
# only one missing the rule.
#
# The rule must be matched as a whole value, never as a substring of the
# neighbouring :8443 rule: a state where only "allow tcp port 8443" exists has
# to end up with :443 added, not silently skipped.
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SCRIPT="packaging/files/etc/uci-defaults/99-tollgate-setup"

PASS=0
FAIL=0
ok()  { PASS=$((PASS + 1)); printf 'ok   %s\n' "$1"; }
bad() { FAIL=$((FAIL + 1)); printf 'FAIL %s\n' "$1"; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
mkdir -p "$TMP/bin"

export UCI_STATE="$TMP/uci.state"
export LOGFILE="$TMP/setup.log"
export GATEWAY_NAME="TestGate"
# Separator the shim uses to join a list option's entries on `uci get`.
# Empty = one entry per line (what a modern uci prints); a space reproduces the
# single-line form, where the entry under test may sit anywhere in the line.
export UCI_LIST_SEP="${UCI_LIST_SEP:-}"

cat > "$TMP/bin/uci" <<'SHIM'
#!/usr/bin/env bash
# Minimal uci shim: just enough for 99-tollgate-setup's nodogsplash functions.
# A list option is stored as one "key=value" line per entry; `get` joins them
# with $UCI_LIST_SEP (default newline).
state="${UCI_STATE:?}"
sep="${UCI_LIST_SEP:-}"
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
        if [ -n "$sep" ]; then
            printf '%s\n' "${vals//$'\n'/$sep}"
        else
            printf '%s\n' "$vals"
        fi
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
    commit|revert|show|export) : ;;
    *) : ;;
esac
exit 0
SHIM
chmod +x "$TMP/bin/uci"
PATH="$TMP/bin:$PATH"
export PATH

# Load the setup script's functions without running the first-boot flow.
TOLLGATE_SETUP_LIB_ONLY=1 . "$ROOT/$SCRIPT" >/dev/null 2>&1

KEY="nodogsplash.@nodogsplash[0].users_to_router"
seed() { # seed <list entry>...
    : > "$UCI_STATE"
    printf '%s=nodogsplash\n' 'nodogsplash.@nodogsplash[0]' >> "$UCI_STATE"
    local e
    for e in "$@"; do printf '%s=%s\n' "$KEY" "$e" >> "$UCI_STATE"; done
}
# seed_lines <newline separated entries> [extra entry]... — the LEGACY list's
# entries contain spaces, so it must be passed through intact: `seed $LEGACY`
# word-splits on those spaces and seeds "allow"/"tcp"/"port"/"8443" as four
# separate entries, which silently turned the anchoring case below into an
# empty-list case.
seed_lines() {
    : > "$UCI_STATE"
    printf '%s=nodogsplash\n' 'nodogsplash.@nodogsplash[0]' >> "$UCI_STATE"
    printf '%s\n' "$1" | while IFS= read -r e; do
        [ -n "$e" ] && printf '%s=%s\n' "$KEY" "$e" >> "$UCI_STATE"
    done
    shift
    local e
    for e in "$@"; do printf '%s=%s\n' "$KEY" "$e" >> "$UCI_STATE"; done
}
count_entry() { grep -F -c "$KEY=$1" "$UCI_STATE" 2>/dev/null | tr -d ' '; }
has_entry() { grep -F -q "$KEY=$1" "$UCI_STATE"; }

# The six rules the installer has always written.
LEGACY="allow tcp port 2121
allow tcp port 8080
allow tcp port 2050
allow tcp port 2051
allow tcp port 8090
allow tcp port 8443"

echo "== fresh install adds the pre-auth :443 rule"
seed
setup_nodogsplash >/dev/null 2>&1
if has_entry 'allow tcp port 443'; then
    ok "fresh install: users_to_router gains 'allow tcp port 443'"
else
    bad "fresh install: no 'allow tcp port 443' rule (LuCI's :8080 -> https:// redirect dead-ends on :443)"
fi
for port in 2121 8080 2050 2051 8090 8443; do
    if has_entry "allow tcp port $port"; then
        ok "fresh install: existing 'allow tcp port $port' rule kept"
    else
        bad "fresh install: 'allow tcp port $port' rule went missing"
    fi
done

echo "== re-running the setup does not duplicate the rule"
setup_nodogsplash >/dev/null 2>&1
n=$(count_entry 'allow tcp port 443')
if [ "$n" = 1 ]; then
    ok "second run: exactly one 'allow tcp port 443' rule"
else
    bad "second run: 'allow tcp port 443' rule present $n times (want 1)"
fi

echo "== the :8443 rule must not satisfy the :443 check"
seed_lines "$LEGACY"
setup_nodogsplash >/dev/null 2>&1
if has_entry 'allow tcp port 443'; then
    ok "list ending in 'allow tcp port 8443' still gains the :443 rule"
else
    bad "list ending in 'allow tcp port 8443' did not gain the :443 rule (:443 missing, or :8443 matched as a substring)"
fi
n=$(count_entry 'allow tcp port 8443')
[ "$n" = 1 ] && ok ":8443 rule not duplicated" || bad ":8443 rule present $n times (want 1)"

echo "== idempotent with the legacy list already present"
setup_nodogsplash >/dev/null 2>&1
n=$(count_entry 'allow tcp port 443')
[ "$n" = 1 ] && ok "re-run over the legacy list: exactly one :443 rule" \
              || bad "re-run over the legacy list: :443 rule present $n times (want 1)"

echo "== pre-existing :443 (added by tollgate-cli ssl enable) is not duplicated"
seed_lines "$LEGACY" 'allow tcp port 443'
setup_nodogsplash >/dev/null 2>&1
n=$(count_entry 'allow tcp port 443')
[ "$n" = 1 ] && ok "existing :443 rule left alone (no duplicate)" \
              || bad "existing :443 rule duplicated ($n copies)"
for port in 2121 8080 2050 2051 8090 8443; do
    n=$(count_entry "allow tcp port $port")
    [ "$n" = 1 ] && ok "'allow tcp port $port' still present exactly once" \
                 || bad "'allow tcp port $port' present $n times (want 1)"
done

echo "== single-line (space separated) list form is handled too"
UCI_LIST_SEP=' ' seed_lines "$LEGACY" 'allow tcp port 443'
UCI_LIST_SEP=' ' setup_nodogsplash >/dev/null 2>&1
n=$(count_entry 'allow tcp port 443')
[ "$n" = 1 ] && ok "space separated list: :443 not duplicated when mid-line" \
              || bad "space separated list: :443 present $n times (want 1)"

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
