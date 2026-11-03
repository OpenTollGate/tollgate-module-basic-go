#!/usr/bin/env bash
# Port-table contract (#746): the entry_ui port mapping lives TWICE — the
# shell helpers in 99-tollgate-setup (uhttpd_main_*_port / board_*_port)
# and Go's uiPortPair (src/cmd/tollgate-cli/ui.go) — and the two tables
# must agree cell for cell. Until this fence, a port change on one side
# silently diverged the other (the duplication is drift-prone by
# construction; tracked from the Amperstrand cross-review of #745).
#
# Method (the check-ssid-format.sh pattern — one source, two declarations,
# CI-owned equality): parse BOTH tables into the same 2x2x2 literal matrix
# and compare. No Go build and no shell sourcing needed: both sides declare
# their cells as literals on stable one-line shapes, which is exactly what
# makes the contract mechanically checkable.
#
#   cell(ui, mode) -> (http, tls)
#     entry pair  (8080, 443)  when ui == mode   [the UI that owns entry]
#     secondary   (8090, 8443) otherwise
#
# Shell side mapping: uhttpd_main_* is the LuCI UI's pair, board_* is the
# board UI's pair; both key on $ENTRY_UI_MODE. Go side: uiPortPair(ui,
# mode) returns the entry pair when ui==mode else the secondary pair.
set -u
FAIL=0
ok()  { printf 'ok   %s\n' "$1"; PASS=$((PASS + 1)); }
bad() { printf 'FAIL %s\n' "$1"; FAIL=$((FAIL + 1)); }
PASS=0
ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SETUP="$ROOT/packaging/files/etc/uci-defaults/99-tollgate-setup"
UIGO="$ROOT/src/cmd/tollgate-cli/ui.go"

# ---- shell table: uhttpd_main_http_port() { [ "$ENTRY_UI_MODE" = board ] && printf 8090 || printf 8080; }
shell_cell() { # <func> <mode> -> prints the port literal
    local line board_true board_false
    line="$(grep -m1 -E "^${1}\(\)" "$SETUP")" || return 1
    board_true="$(printf '%s\n' "$line" | sed -n 's/.*&& printf \([0-9]*\).*/\1/p')"
    board_false="$(printf '%s\n' "$line" | sed -n 's/.*|| printf \([0-9]*\).*/\1/p')"
    [ "$2" = board ] && printf '%s' "$board_true" || printf '%s' "$board_false"
}

# ---- Go table: entry pair when ui==mode, else secondary (uiPortPair)
go_entry="$(sed -n 's/.*return "\([0-9]*\)", "\([0-9]*\)".*/\1 \2/p' "$UIGO" | head -1)"
go_secondary="$(sed -n 's/.*return "\([0-9]*\)", "\([0-9]*\)".*/\1 \2/p' "$UIGO" | sed -n 2p)"
[ -n "$go_entry" ] && [ -n "$go_secondary" ] || {
    bad "could not parse uiPortPair's two return literals from ui.go (shape drifted?)"
    printf 'tests: %d passed, %d failed\n' "$PASS" "$FAIL"; exit 1; }

echo "== the shell port helpers and Go uiPortPair agree on all four cells"
for mode in board luci; do
    for ui in board luci; do
        # shell pair for this ui under this mode
        if [ "$ui" = luci ]; then
            sh_pair="$(shell_cell uhttpd_main_http_port "$mode") $(shell_cell uhttpd_main_https_port "$mode")"
        else
            sh_pair="$(shell_cell board_http_port "$mode") $(shell_cell board_https_port "$mode")"
        fi
        # go pair: entry when ui == mode
        if [ "$ui" = "$mode" ]; then go_pair="$go_entry"; else go_pair="$go_secondary"; fi
        if [ "$sh_pair" = "$go_pair" ]; then
            ok "ui=$ui mode=$mode -> $sh_pair (shell == Go)"
        else
            bad "ui=$ui mode=$mode: shell says '$sh_pair', Go uiPortPair says '$go_pair' — the port tables have DRIFTED (#746 class)"
        fi
    done
done

# ---- negative control: a fence that cannot see a planted drift is worthless
echo
echo "== negative control: the comparison sees a planted disagreement"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
cp "$SETUP" "$TMP/setup-drift"
# Plant: flip the board-mode uhttpd_main http port (8090 -> 8091)
sed -i 's/uhttpd_main_http_port()  { \[ "\$ENTRY_UI_MODE" = board \] \&\& printf 8090/uhttpd_main_http_port()  { [ "$ENTRY_UI_MODE" = board ] \&\& printf 8091/' "$TMP/setup-drift" 2>/dev/null
if grep -q 'printf 8091' "$TMP/setup-drift"; then
    planted="$(SETUP="$TMP/setup-drift"; shell_cell uhttpd_main_http_port board)"
    if [ "$planted" = "8091" ]; then
        ok "a planted port flip is extracted (the parser is not inert)"
    else
        bad "the planted flip was NOT extracted — the parser cannot fail, so this fence proves nothing"
    fi
else
    bad "could not plant the control drift — the sed anchor drifted; fix the control, not the fence"
fi

echo
printf 'tests: %d passed, %d failed\n' "$PASS" "$FAIL"
[ "$FAIL" = 0 ] || exit 1
exit 0
