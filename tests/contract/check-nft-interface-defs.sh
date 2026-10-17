#!/usr/bin/env bash
# shellcheck disable=SC2016
# Contract: the nftables guards must not hardcode the captive bridge's name
# (#757) — they resolve $tg_portal_if / $tg_private_if from
# 00-tollgate-defs.nft, rendered from the router's own config.
#
# Why: a renamed captive bridge (uci set network.@device[0].name='br-portal')
# used to leave the guards dropping everything not from "br-lan" — the portal
# SPA could not reach :2121, payments never arrived, and nothing in logread
# named the expected bridge (bench-verified on rc1: the guard's own drop
# counter doing the killing, zero operator-visible diagnostics):
# https://github.com/OpenTollGate/tollgate-module-basic-go/issues/757#issuecomment-6066079520
#
# Layers asserted here:
#   A. Behavioural — the real renderer against a stub uci: the gatewayinterface
#      becomes tg_portal_if, garbage is refused with a warning, the private
#      bridge name is honoured, unset falls back to the shipped default.
#   B. Structural — no shipped guard carries an interface literal in a rule
#      line; the static default 00- defines both names; the init WARNs when
#      the portal interface is missing; the package installs the pieces.
#   C. Parse — when nft and user namespaces are available, the assembled
#      fw4 table (00- + 20/30/31/32) parses with BOTH the shipped default
#      and a renamed bridge's defines: the rename path is proven at the
#      ruleset level, not assumed.

set -u
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
RENDER="$ROOT/packaging/files/usr/local/bin/tollgate-nft-interfaces-render"
NFTDIR="$ROOT/packaging/files/etc/nftables.d"
INIT="$ROOT/packaging/files/etc/init.d/tollgate-wrt"
MK="$ROOT/packaging/Makefile"

printf 'check-nft-interface-defs: root %s\n' "$ROOT"

fail=0
ok()  { printf '  PASS: %s\n' "$1"; }
bad() { printf '  FAIL: %s\n' "$1"; fail=$((fail+1)); }

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin"

# --- A. the renderer, driven through a stub uci -----------------------------
write_uci() {
    cat > "$WORK/bin/uci" <<EOF
#!/bin/sh
case "\$*" in
    *"nodogsplash.@nodogsplash[0].gatewayinterface") echo "$1"; exit 0 ;;
    *"network.private_bridge.name") echo "$2"; exit 0 ;;
esac
exit 1
EOF
    chmod +x "$WORK/bin/uci"
}

DEFS="$WORK/00-tollgate-defs.nft"
export TOLLGATE_NFT_DEFS_FRAG="$DEFS"

write_uci "br-portal" "br-mgmt"
PATH="$WORK/bin:$PATH" sh "$RENDER" >/dev/null 2>"$WORK/stderr.log" \
    && ok "renderer ran" || { bad "renderer exited nonzero"; cat "$WORK/stderr.log"; }
grep -q 'define tg_portal_if = "br-portal"' "$DEFS" \
    && ok "gatewayinterface becomes tg_portal_if" || bad "tg_portal_if wrong"
grep -q 'define tg_private_if = "br-mgmt"' "$DEFS" \
    && ok "private bridge name honoured" || bad "tg_private_if wrong"

write_uci "not!an!ifname" "br-mgmt"
PATH="$WORK/bin:$PATH" sh "$RENDER" >/dev/null 2>"$WORK/stderr.log"
grep -q 'define tg_portal_if = "br-lan"' "$DEFS" \
    && ok "garbage interface refused, shipped default restored" || bad "garbage interface emitted"
grep -qi "not a usable interface name" "$WORK/stderr.log" \
    && ok "refusal logged loudly" || bad "no warning for garbage interface"

write_uci "" "br-mgmt"
cat > "$WORK/bin/uci" <<'EOF'
#!/bin/sh
exit 1
EOF
chmod +x "$WORK/bin/uci"
PATH="$WORK/bin:$PATH" sh "$RENDER" >/dev/null 2>"$WORK/stderr.log"
grep -q 'define tg_portal_if = "br-lan"' "$DEFS" && ok "unset falls back to br-lan" || bad "unset fallback wrong"
grep -q 'define tg_private_if = "br-private"' "$DEFS" && ok "missing private falls back to br-private" || bad "private fallback wrong"

# idempotence
cp "$DEFS" "$WORK/first.defs"
PATH="$WORK/bin:$PATH" sh "$RENDER" >/dev/null 2>&1
cmp -s "$DEFS" "$WORK/first.defs" && ok "re-render is byte-identical" || bad "second render differs"

# --- B. structural: no literals left, the safety net ships ------------------
grep -hE '^\s*(meta )?.*(iifname|oifname)' "$NFTDIR"/2*.nft "$NFTDIR"/3*.nft | grep '"' | grep -v '\$tg_' | grep -qv '^\s*#' \
    && bad "a rule line still carries an interface literal" \
    || ok "no rule-line interface literals in any guard"
grep -q 'define tg_portal_if = "br-lan"' "$NFTDIR/00-tollgate-defs.nft" \
    && grep -q 'define tg_private_if = "br-private"' "$NFTDIR/00-tollgate-defs.nft" \
    && ok "static default defines ship (unresolved-define guard)" \
    || bad "shipped 00-tollgate-defs.nft lacks the default defines"
grep -q 'ip link show' "$INIT" && grep -q 'user.warn' "$INIT" \
    && ok "init WARNs when the portal interface is missing (#757's ask)" \
    || bad "init has no missing-interface WARN"
grep -q 'tollgate-nft-interfaces-render' "$MK" \
    && ok "Makefile installs the renderer" || bad "renderer not in Makefile"
grep -q '00-tollgate-defs.nft' "$MK" \
    && ok "Makefile manifests the defs fragment" || bad "defs fragment not in Makefile manifest"

# --- C. parse the assembled ruleset, default AND renamed --------------------
if command -v nft >/dev/null 2>&1 && unshare -Urn true 2>/dev/null; then
    assemble() {
        # defs-file first (the fw4 lexical-include order), then every guard
        echo "table inet fw4 {"
        cat "$1" \
            "$NFTDIR/20-nds-enforce.nft" \
            "$NFTDIR/30-backend-firewall.nft" \
            "$NFTDIR/31-admin-board-not-guest-reachable.nft" \
            "$NFTDIR/32-luci-not-guest-reachable.nft"
        echo "}"
    }
    assemble "$NFTDIR/00-tollgate-defs.nft" > "$WORK/assembled-default.nft"
    if unshare -Urn nft -c -f "$WORK/assembled-default.nft" 2>"$WORK/nft.err"; then
        ok "assembled ruleset parses with the shipped default defines"
    else
        bad "assembled ruleset fails to parse (default defines)"; head -3 "$WORK/nft.err"
    fi
    # The rename leg: the verdict's exact shape — portal on br-portal.
    sed 's/define tg_portal_if = "br-lan"/define tg_portal_if = "br-portal"/' \
        "$NFTDIR/00-tollgate-defs.nft" > "$WORK/renamed-defs.nft"
    {
        echo "table inet fw4 {"
        cat "$WORK/renamed-defs.nft" \
            "$NFTDIR/20-nds-enforce.nft" \
            "$NFTDIR/30-backend-firewall.nft" \
            "$NFTDIR/31-admin-board-not-guest-reachable.nft" \
            "$NFTDIR/32-luci-not-guest-reachable.nft"
        echo "}"
    } > "$WORK/assembled-renamed.nft"
    if unshare -Urn nft -c -f "$WORK/assembled-renamed.nft" 2>>"$WORK/nft.err"; then
        ok "assembled ruleset parses with a renamed captive bridge"
    else
        bad "assembled ruleset fails to parse with renamed defines"; head -3 "$WORK/nft.err"
    fi
else
    printf '  SKIP: nft/userns unavailable — parse legs not run (bench owns the dataplane proof)\n'
fi

printf 'check-nft-interface-defs: %s\n' "$([ "$fail" = 0 ] && echo PASS || echo "FAIL ($fail)")"
exit $([ "$fail" = 0 ] && echo 0 || echo 1)
