#!/usr/bin/env bash
# shellcheck disable=SC2016
# Contract: the NDS pre-auth FORWARD allowlist must execute — which, in the
# shipped ordering, only happens at nftables priority -2 (#754).
#
# nodogsplash compiles uci `preauthenticated_users` into priority-0 chains,
# and the package's own nds_enforce_forward (20-nds-enforce.nft) terminates
# unmarked forwarded traffic with a reject at priority -1: the compiled
# accepts are dead letter — bench-verified on v0.6.0-rc1 with packet-level
# attribution (reject counter climbing, allow chain at 0 packets, the
# prio -1 accept workaround flipping the flow green):
# https://github.com/OpenTollGate/tollgate-module-basic-go/issues/754#issuecomment-6066054958
#
# The fix: /usr/local/bin/tollgate-nds-preauth-render emits the allowlist as
# its own -2 base chain. This test drives the REAL renderer against a stub
# `uci` carrying a fixture allowlist — the verdict comment's repro shape
# (`allow tcp port 3080 to 10.0.2.2`, a multi-port entry, an any-destination
# entry, and the malformed/deny entries that must be refused loudly) — and
# asserts on the fragment it would install, plus the shipped 20- file's
# ordering invariants.

set -u
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
RENDER="$ROOT/packaging/files/usr/local/bin/tollgate-nds-preauth-render"
ENFORCE="$ROOT/packaging/files/etc/nftables.d/20-nds-enforce.nft"

printf 'check-nds-preauth-nft: root %s\n' "$ROOT"

fail=0
ok()   { printf '  PASS: %s\n' "$1"; }
bad()  { printf '  FAIL: %s\n' "$1"; fail=$((fail+1)); }

# --- the stub environment: uci serves a fixture allowlist ----------------
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin" "$WORK/etc/nftables.d"

cat > "$WORK/bin/uci" <<'EOF'
#!/bin/sh
# Stub uci: only the exact query the renderer makes, answered from the
# fixture (newline-separated list, exactly as BusyBox uci emits lists).
case "$*" in
    *"nodogsplash.@nodogsplash[0].preauthenticated_users")
        printf '%s\n' \
            "allow tcp port 3080 to 10.0.2.2" \
            "allow tcp port 8190/8383 to 192.168.13.221" \
            "allow udp port 123 to any" \
            "deny tcp port 22 to 10.0.2.2" \
            "allow tcp port 8080" \
            "allow sctp port 9 to 10.0.2.2" \
            "allow tcp port 8081 to mint.example.com" \
            "allow tcp port 99999 to 10.0.2.2"
        exit 0
        ;;
esac
exit 1
EOF
chmod +x "$WORK/bin/uci"

# The fragment lands in the sandbox, not /etc on the CI runner.
export TOLLGATE_NDS_PREAUTH_FRAG="$WORK/etc/nftables.d/21-nds-preauth-allow.nft"
FRAG="$TOLLGATE_NDS_PREAUTH_FRAG"

run_renderer() {
    PATH="$WORK/bin:$PATH" sh "$RENDER" >/dev/null 2>"$WORK/stderr.log"
}

# --- 1. the fragment renders, as a -2 chain with policy accept -----------
run_renderer || { bad "renderer exited nonzero"; cat "$WORK/stderr.log"; exit 1; }
[ -f "$FRAG" ] && ok "fragment written" || { bad "fragment not written"; exit 1; }

grep -q 'chain nds_preauth_allow' "$FRAG" \
    && ok "chain nds_preauth_allow present" || bad "chain missing"
grep -qE 'type filter hook forward priority -2; policy accept' "$FRAG" \
    && ok "priority -2, policy accept" || bad "priority/policy line wrong"

# --- 2. every well-formed entry becomes the accept it names ---------------
grep -q 'meta nfproto ipv4 ip daddr 10.0.2.2 tcp dport { 3080 } counter accept' "$FRAG" \
    && ok "single-port entry (the verdict repro's exact shape)" \
    || bad "single-port entry missing/wrong"
grep -q 'meta nfproto ipv4 ip daddr 192.168.13.221 tcp dport { 8190, 8383 } counter accept' "$FRAG" \
    && ok "multi-port entry expands to a set" \
    || bad "multi-port entry missing/wrong"
grep -q 'meta nfproto ipv4 udp dport { 123 } counter accept' "$FRAG" \
    && ok "'to any' omits the daddr match" \
    || bad "'to any' entry missing/wrong"

grep -q 'meta nfproto ipv4 tcp dport { 8080 } counter accept' "$FRAG" \
    && ok "hostless entry renders as to-any (nodogsplash's own semantics)" \
    || bad "hostless entry missing/wrong"

# --- 3. nothing else is emitted: the refuse-to-guess contract -------------
n_accepts="$(grep -c 'counter accept' "$FRAG")"
[ "$n_accepts" = "4" ] \
    && ok "exactly 4 accepts (nothing guessed for refused entries)" \
    || bad "expected 4 accept rules, found $n_accepts"
grep -q 'sctp' "$FRAG" && bad "unsupported protocol emitted" || ok "unsupported protocol refused"
grep -q 'mint.example.com' "$FRAG" && bad "DNS destination emitted" || ok "DNS destination refused"
grep -q 'dport { 22 }' "$FRAG" && bad "deny entry became an accept" || ok "deny entry not emitted"
grep -q '99999' "$FRAG" && bad "out-of-range port emitted" || ok "out-of-range port refused"
grep -qi 'skipping' "$WORK/stderr.log" \
    && ok "every refused entry logged loudly" \
    || bad "no skip warnings on stderr"

# --- 4. idempotence: a second run is byte-identical ------------------------
cp "$FRAG" "$WORK/first.nft"
run_renderer
cmp -s "$FRAG" "$WORK/first.nft" \
    && ok "re-render is byte-identical (idempotent)" \
    || bad "second render differs"

# --- 5. the shipped enforce chain keeps the ordering the -2 chain beats ---
[ -f "$ENFORCE" ] || { bad "shipped 20-nds-enforce.nft not found"; exit 1; }
grep -q 'hook forward priority -1' "$ENFORCE" \
    && ok "20- keeps its priority -1 hook (the -2 chain runs ahead of it)" \
    || bad "20- no longer hooks at -1 — the -2 ordering premise is gone"
grep -q 'counter reject with icmp type port-unreachable' "$ENFORCE" \
    && ok "20- keeps its terminal reject" \
    || bad "20- terminal reject missing"
grep -q '21-nds-preauth-allow' "$ENFORCE" \
    && ok "20- documents the -2 fragment" \
    || bad "20- does not reference the generated fragment"

# --- 6. an empty allowlist leaves a harmless empty chain -------------------
cat > "$WORK/bin/uci" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod +x "$WORK/bin/uci"
run_renderer || { bad "renderer failed on empty list"; }
n_accepts="$(grep -c 'counter accept' "$FRAG" || true)"
[ "$n_accepts" = "0" ] \
    && ok "empty allowlist renders an empty chain" \
    || bad "empty allowlist still carries $n_accepts accept(s)"

printf 'check-nds-preauth-nft: %s\n' "$([ "$fail" = 0 ] && echo PASS || echo "FAIL ($fail)")"
exit $([ "$fail" = 0 ] && echo 0 || echo 1)
