#!/usr/bin/env bash
# shellcheck disable=SC2016
# Contract: the NDS pre-auth FORWARD allowlist must execute — which, in the
# kernel's actual semantics, means INSIDE nds_enforce_forward, ahead of its
# first rule (#754).
#
# nodogsplash compiles uci `preauthenticated_users` into priority-0 chains,
# and the package's nds_enforce_forward (20-nds-enforce.nft) drops/rejects
# first — the compiled accepts are dead letter (bench-verified on rc1 with
# packet-level attribution):
# https://github.com/OpenTollGate/tollgate-module-basic-go/issues/754#issuecomment-6066054958
#
# The first fix shipped the allowlist as a SEPARATE base chain at priority
# -2. That shape was REFUTED live on an nft 1.1.7 three-namespace rig (the
# #782 stack review): a base chain's `accept` ends evaluation in that chain
# only — the -1 chain still rejected every allowlisted flow, both counters
# climbing. The bench-proven shape is the issue's own runtime workaround:
# the accepts as the FIRST rules of nds_enforce_forward itself. The renderer
# emits exactly that, and this test pins:
#   - the accepts are the chain's FIRST rules (ahead of the pre-auth
#     mark-drop and the terminal reject — order, not just presence)
#   - the refuse-to-guess grammar contract (nothing broadened)
#   - the shipped 20- file is byte-identical to the renderer's
#     empty-allowlist output (the template is canonical; drift fails CI)
#   - the refuted -2 fragment is removed on sight (upgrade hygiene)
#   - the enforce semantics survive every render (mark rules, -1 hook,
#     terminal reject), and the result parses where nft + userns exist

set -u
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
RENDER="$ROOT/packaging/files/usr/local/bin/tollgate-nds-preauth-render"
SHIPPED="$ROOT/packaging/files/etc/nftables.d/20-nds-enforce.nft"

printf 'check-nds-preauth-nft: root %s\n' "$ROOT"

fail=0
ok()   { printf '  PASS: %s\n' "$1"; }
bad()  { printf '  FAIL: %s\n' "$1"; fail=$((fail+1)); }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin" "$WORK/etc/nftables.d"

# The stub's EMISSION MODEL is the contract this suite nearly got wrong:
# the rig re-verify proved BusyBox `uci -q get` on a list emits values
# QUOTE-WRAPPED when they contain spaces and joins multi-entry lists
# SPACE-JOINED ON ONE LINE — the first stub here served newline-separated
# bare values, CI was green while the rig was red, and the rendered
# allowlist shipped empty. The fixtures below emit the real shapes; the
# tokenizer must parse all of them.
#
# fixture files under $WORK/uci-emission:
#   multi   — one line, entries quote-wrapped and space-joined (the rig shape)
#   single  — one line, one quote-wrapped entry (single-entry list)
#   legacy  — newline-separated bare values (defensive: some builds split)
write_uci_stub() {
    # Quoted heredoc: the stub reads $UCI_EMISSION at ITS runtime, not at
    # stub-write time (the suite runs set -u; an unquoted heredoc would
    # expand the variable here, unbound).
    cat > "$WORK/bin/uci" <<'STUBEOF'
#!/bin/sh
case "$*" in
    *"nodogsplash.@nodogsplash[0].preauthenticated_users")
        cat "$UCI_EMISSION"
        exit 0
        ;;
esac
exit 1
STUBEOF
    chmod +x "$WORK/bin/uci"
}
write_uci_stub

cat > "$WORK/uci-multi" <<'EOF'
'allow tcp port 3080 to 10.0.2.2' 'allow tcp port 8190/8383 to 192.168.13.221' 'allow udp port 123 to any' 'deny tcp port 22 to 10.0.2.2' 'allow tcp port 8080' 'allow sctp port 9 to 10.0.2.2' 'allow tcp port 8081 to mint.example.com' 'allow tcp port 99999 to 10.0.2.2'
EOF
cat > "$WORK/uci-single" <<'EOF'
'allow tcp port 3080 to 10.0.2.2'
EOF
cat > "$WORK/uci-legacy" <<'EOF'
allow tcp port 3080 to 10.0.2.2
allow tcp port 8190/8383 to 192.168.13.221
allow udp port 123 to any
deny tcp port 22 to 10.0.2.2
allow tcp port 8080
allow sctp port 9 to 10.0.2.2
allow tcp port 8081 to mint.example.com
allow tcp port 99999 to 10.0.2.2
EOF

FRAG="$WORK/etc/nftables.d/20-nds-enforce.nft"
STALE="$WORK/etc/nftables.d/21-nds-preauth-allow.nft"
export TOLLGATE_NDS_ENFORCE_FRAG="$FRAG"
export TOLLGATE_NDS_STALE_FRAG="$STALE"

run_renderer() {
    UCI_EMISSION="${UCI_EMISSION:-$WORK/uci-multi}" \
    PATH="$WORK/bin:$PATH" sh "$RENDER" >/dev/null 2>"$WORK/stderr.log"
}

# A leftover from the refuted shape must disappear on sight.
printf 'chain nds_preauth_allow {\n    type filter hook forward priority -2; policy accept\n}\n' > "$STALE"

run_renderer || { bad "renderer exited nonzero"; cat "$WORK/stderr.log"; exit 1; }
[ -f "$FRAG" ] && ok "the enforce fragment was rendered" || { bad "fragment not written"; exit 1; }
[ ! -f "$STALE" ] && ok "the refuted -2 fragment removed on sight" || bad "stale -2 fragment survives"

grep -q 'chain nds_enforce_forward' "$FRAG" \
    && ok "the (single) enforce chain present" || bad "chain missing"
if grep -q 'nds_preauth_allow' "$FRAG"; then
    bad "a separate allow chain exists — the refuted shape"
else
    ok "no separate allow chain (accepts live IN the enforce chain)"
fi
grep -qE 'type filter hook forward priority -1; policy accept' "$FRAG" \
    && ok "the enforce chain keeps its priority -1 hook" || bad "priority/policy line wrong"

# --- the ordering that IS the fix: accepts before the first mark rule -------
allow_line=$(grep -n 'counter accept' "$FRAG" | head -1 | cut -d: -f1)
mark_line=$(grep -n 'meta mark & 0x00030000' "$FRAG" | head -1 | cut -d: -f1)
reject_line=$(grep -n 'counter reject with icmp type port-unreachable' "$FRAG" | head -1 | cut -d: -f1)
if [ -n "$allow_line" ] && [ -n "$mark_line" ] && [ "$allow_line" -lt "$mark_line" ] && [ "$allow_line" -lt "$reject_line" ]; then
    ok "allowlist accepts are the chain's FIRST rules (ahead of the mark-drop and the reject)"
else
    bad "accepts not ahead of the mark/reject rules (allow=$allow_line mark=$mark_line reject=$reject_line)"
fi

# --- the rendered rules, verbatim -------------------------------------------
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

# --- the refuse-to-guess contract: exactly the allowlist, nothing else ------
n_accepts="$(grep -c 'counter accept' "$FRAG")"
# 4 allowlist accepts + 2 mark accepts (trusted/authenticated) = 6
[ "$n_accepts" = "6" ] \
    && ok "exactly the 4 allowlist accepts + the 2 mark accepts (nothing guessed)" \
    || bad "expected 6 accept rules, found $n_accepts"
grep -q 'sctp' "$FRAG" && bad "unsupported protocol emitted" || ok "unsupported protocol refused"
grep -q 'mint.example.com' "$FRAG" && bad "DNS destination emitted" || ok "DNS destination refused"
grep -q 'dport { 22 }' "$FRAG" && bad "deny entry became an accept" || ok "deny entry not emitted"
grep -q '99999' "$FRAG" && bad "out-of-range port emitted" || ok "out-of-range port refused"
grep -qi 'skipping' "$WORK/stderr.log" \
    && ok "every refused entry logged loudly" \
    || bad "no skip warnings on stderr"
grep -qi 'removed the superseded fragment' "$WORK/stderr.log" \
    && ok "the stale-fragment removal logged" \
    || bad "stale removal silent"

# --- idempotence --------------------------------------------------------------
cp "$FRAG" "$WORK/first.nft"
run_renderer
cmp -s "$FRAG" "$WORK/first.nft" \
    && ok "re-render is byte-identical (idempotent)" \
    || bad "second render differs"

# --- the enforce semantics survive every render -------------------------------
grep -q 'meta mark & 0x00030000 == 0x00010000 counter drop' "$FRAG" \
    && ok "the pre-auth mark-drop rule survives" || bad "mark-drop rule missing"
grep -q 'counter reject with icmp type port-unreachable' "$FRAG" \
    && ok "the terminal reject survives" || bad "terminal reject missing"

# --- single-entry list: one quote-wrapped value on one line ------------------
UCI_EMISSION="$WORK/uci-single" run_renderer
n_accepts="$(grep -c 'counter accept' "$FRAG")"
[ "$n_accepts" = "3" ] \
    && ok "single-entry list: exactly its accept + the 2 mark accepts" \
    || bad "single-entry list: expected 3 accepts, found $n_accepts"
grep -q 'meta nfproto ipv4 ip daddr 10.0.2.2 tcp dport { 3080 } counter accept' "$FRAG" \
    && ok "single-entry list: the entry's rule rendered" \
    || bad "single-entry list: rule missing"

# --- legacy emission (newline-separated bare values) parses identically ------
UCI_EMISSION="$WORK/uci-legacy" run_renderer
n_accepts="$(grep -c 'counter accept' "$FRAG")"
[ "$n_accepts" = "6" ] \
    && ok "legacy newline-bare emission: same 4 allowlist accepts as the rig shape" \
    || bad "legacy emission: expected 6 accepts, found $n_accepts"

# back to the rig shape for the remaining legs
UCI_EMISSION="$WORK/uci-multi" run_renderer

# --- the template-is-canonical contract: empty list == shipped file ----------
cat > "$WORK/bin/uci" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod +x "$WORK/bin/uci"
run_renderer || bad "renderer failed on empty list"
cmp -s "$FRAG" "$SHIPPED" \
    && ok "empty-allowlist output is byte-identical to the shipped 20- file (no template drift)" \
    || { bad "shipped 20- file drifted from the renderer's canonical template"; diff "$FRAG" "$SHIPPED" | head -6; }

# --- parse the assembled ruleset where nft + userns exist ---------------------
if command -v nft >/dev/null 2>&1 && unshare -Urn true 2>/dev/null; then
    { echo "table inet fw4 {"; cat "$WORK/first.nft"; echo "}"; } > "$WORK/assembled.nft"
    if unshare -Urn nft -c -f "$WORK/assembled.nft" 2>"$WORK/nft.err"; then
        ok "rendered-with-allowlist fragment parses (nft -c under userns)"
    else
        bad "rendered fragment fails to parse"; head -3 "$WORK/nft.err"
    fi
else
    printf '  SKIP: nft/userns unavailable — parse leg not run (bench owns the dataplane proof)\n'
fi

printf 'check-nds-preauth-nft: %s\n' "$([ "$fail" = 0 ] && echo PASS || echo "FAIL ($fail)")"
exit $([ "$fail" = 0 ] && echo 0 || echo 1)
