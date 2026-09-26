#!/usr/bin/env bash
# Offline test for the pre-auth allow list's ADMIN-surface entries in
# 99-tollgate-setup's assert_nodogsplash_allow_entries() — no router, no
# nodogsplash daemon, no network needed.
#
# This file used to be tests/uci-defaults-nodogsplash-443_test.sh and asserted
# the OPPOSITE of what it asserts now: that `:8080` and `:443` are ADDED to
# nodogsplash's pre-authentication allow list, so that a captive-LAN client
# hitting the LuCI port on `:8080` could follow uhttpd.main's
# `307 Location: https://<router>/` to a TLS listener that answered instead of
# dead-ending on a REJECTed port (docs/architecture/luci-https-pre-auth-reachability-decision.md,
# 2026-09-22).
#
# That contract is reversed deliberately, and the reason is measured, not
# theoretical: on the bench MT3000 (2026-09-25, pre17, first-hand from a MAC the
# box had never seen) a paying guest whose plain-HTTP `http://<router>/` was
# dead — nothing listened on `:80` for a client nodogsplash does not intercept —
# fell through to `https://<router>/` and was answered with the **LuCI login**.
# The operator reported exactly that as "the install is broken". A
# customer-facing network must not answer a browser with the router's admin
# login; `:80` now serves a redirect stub to the portal (see
# tests/uci-defaults-trusted-entry-80_test.sh), and `:8080`/`:443` come off the
# guest path here and are dropped on the captive bridge by
# packaging/files/etc/nftables.d/32-luci-not-guest-reachable.nft.
#
# What the list must hold is therefore the customer journey and nothing else:
# `:2121` (backend API), `:2050` (NDS gateway port), `:2051` (portal SPA).
#
# `:443` has one more writer in the tree — tollgate-cli's `ssl enable`
# (src/cmd/tollgate-cli/ssl.go) — so it is not enough to stop writing it: the
# entry must be REMOVED on every setup path, or every router the CLI ever
# enabled TLS on keeps the pre-auth allowance forever.
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
    del_list)
        # `uci del_list cfg.sec.opt=value` drops every entry equal to value.
        # Whole-line match: the entries under test contain spaces.
        grep -v -F -x -- "$1" "$state" > "$state.tmp" 2>/dev/null
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
# separate entries, which silently turns an anchoring case into an empty-list
# case.
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
has_no_entry() { ! grep -F -q "$KEY=$1" "$UCI_STATE"; }

# The list an operator's router carries today: the customer-journey rules, the
# `:8080`/`:443` LuCI allowance this writer (and tollgate-cli) wrote, and the
# `:8090`/`:8443` board allowance the feed's 92-tollgate-admin-setup re-asserts.
LEGACY="allow tcp port 2121
allow tcp port 8080
allow tcp port 2050
allow tcp port 2051
allow tcp port 8090
allow tcp port 8443
allow tcp port 443"
# The customer journey: must survive, exactly once each.
JOURNEY_PORTS="2121 2050 2051"
# Admin surfaces: must never be in this list.
ADMIN_PORTS="8080 443 8090 8443"

echo "== a fresh install writes the customer journey and nothing else"
seed
setup_nodogsplash >/dev/null 2>&1
for port in $JOURNEY_PORTS; do
    if has_entry "allow tcp port $port"; then
        ok "fresh install: 'allow tcp port $port' rule written"
    else
        bad "fresh install: 'allow tcp port $port' rule missing (the guest cannot reach the portal/API)"
    fi
done
for port in $ADMIN_PORTS; do
    if has_no_entry "allow tcp port $port"; then
        ok "fresh install: 'allow tcp port $port' absent (an admin surface is not pre-auth reachable)"
    else
        bad "fresh install: 'allow tcp port $port' is in users_to_router — a pre-auth guest can reach the router's administration login"
    fi
done

echo "== re-running the setup does not duplicate a journey rule"
setup_nodogsplash >/dev/null 2>&1
for port in $JOURNEY_PORTS; do
    n=$(count_entry "allow tcp port $port")
    [ "$n" = 1 ] && ok "second run: exactly one 'allow tcp port $port' rule" \
                 || bad "second run: 'allow tcp port $port' rule present $n times (want 1)"
done

echo "== the deployed fleet's admin allowances are removed"
seed_lines "$LEGACY"
setup_nodogsplash >/dev/null 2>&1
for port in $JOURNEY_PORTS; do
    n=$(count_entry "allow tcp port $port")
    [ "$n" = 1 ] && ok "legacy list: 'allow tcp port $port' still present exactly once" \
                 || bad "legacy list: 'allow tcp port $port' present $n times (want 1)"
done
for port in $ADMIN_PORTS; do
    n=$(count_entry "allow tcp port $port")
    [ "$n" = 0 ] && ok "legacy list: 'allow tcp port $port' removed" \
                 || bad "legacy list: 'allow tcp port $port' present $n times (want 0) — a deployed router keeps the pre-auth admin allowance"
done

echo "== idempotent with the legacy list already present"
setup_nodogsplash >/dev/null 2>&1
for port in $JOURNEY_PORTS; do
    n=$(count_entry "allow tcp port $port")
    [ "$n" = 1 ] && ok "re-run over the legacy list: exactly one 'allow tcp port $port'" \
                 || bad "re-run over the legacy list: 'allow tcp port $port' present $n times (want 1)"
done
for port in $ADMIN_PORTS; do
    n=$(count_entry "allow tcp port $port")
    [ "$n" = 0 ] && ok "re-run over the legacy list: 'allow tcp port $port' absent" \
                 || bad "re-run over the legacy list: 'allow tcp port $port' present $n times (want 0)"
done

echo "== the removal is by whole value, never by substring"
# `:443` and `:8443` differ by one character, and a hand-written rule on a
# neighbouring port is the operator's, not ours. `del_list` matches whole
# entries; a removal written as a substring/sed match would take all three.
seed_lines "allow tcp port 8443
allow tcp port 4443" 'allow tcp port 443'
setup_nodogsplash >/dev/null 2>&1
n=$(count_entry 'allow tcp port 443')
[ "$n" = 0 ] && ok "the :443 allowance was removed" \
             || bad "the :443 allowance present $n times (want 0)"
n=$(count_entry 'allow tcp port 4443')
[ "$n" = 1 ] && ok "the operator's own 'allow tcp port 4443' rule survived a :443 removal" \
             || bad "'allow tcp port 4443' present $n times (want 1) — the :443 removal matched a neighbouring port"
n=$(count_entry 'allow tcp port 8443')
[ "$n" = 0 ] && ok "the :8443 board allowance was removed on its own value" \
             || bad "the :8443 board allowance present $n times (want 0)"

echo "== single-line (space separated) list form is handled too"
UCI_LIST_SEP=' ' seed_lines "$LEGACY"
UCI_LIST_SEP=' ' setup_nodogsplash >/dev/null 2>&1
for port in $JOURNEY_PORTS; do
    n=$(count_entry "allow tcp port $port")
    [ "$n" = 1 ] && ok "space separated list: 'allow tcp port $port' present exactly once" \
                 || bad "space separated list: 'allow tcp port $port' present $n times (want 1)"
done
for port in $ADMIN_PORTS; do
    n=$(count_entry "allow tcp port $port")
    [ "$n" = 0 ] && ok "space separated list: 'allow tcp port $port' absent" \
                 || bad "space separated list: 'allow tcp port $port' present $n times (want 0)"
done

echo "== the nodogsplash convergence step compares the right sets"
# converge_nodogsplash_runtime compares the CONFIGURED list with the RUNNING
# ndsRTR chain and reloads on a difference. The configured side is derived from
# the same list, so a withdrawal has to show up there as a difference to fix —
# otherwise a router that keeps serving an old allow list is never converged.
# Checked here at the source level: the runtime side filters `-j ACCEPT` rules
# for `--dport`, and the gateway port is dropped from both sides because
# nodogsplash injects it itself.
if grep -q 'nodogsplash_drop_entry "tcp/\$(nodogsplash_gateway_port)"' "$ROOT/$SCRIPT"; then
    ok "both sides of the convergence comparison drop the NDS-injected gateway port"
else
    bad "the convergence comparison no longer normalises the gateway port"
fi
for fn in nodogsplash_configured_accept_entries nodogsplash_runtime_accept_entries; do
    if grep -q "^$fn()" "$ROOT/$SCRIPT"; then
        ok "convergence: $fn() still exists"
    else
        bad "convergence: $fn() is gone — the runtime ruleset cannot be compared with the config"
    fi
done

echo
echo "passed=$PASS failed=$FAIL"
[ "$FAIL" = 0 ]
