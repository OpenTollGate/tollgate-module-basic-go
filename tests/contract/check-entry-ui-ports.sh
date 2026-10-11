#!/usr/bin/env bash
# Contract: the entry_ui port table is declared ONCE — Go's uiPortPair,
# emitted by `tollgate ui ports --format shell` — and 99-tollgate-setup
# EVALUATES that output (#746's followup; the single-source-of-truth endgame).
#
# History: the table used to be declared twice (the shell helpers carried
# literal ports; #765's version of this check policed the two tables'
# agreement cell for cell). The duplication is now gone: the setup's
# load_ui_port_table evals the binary emission, and the helpers read the
# loaded variables by indirect expansion. This check therefore guards the
# CONSUMPTION, not a comparison:
#
#   A. CANARY CONSUMPTION — the setup library is sourced and load_ui_port_table
#      is run against a FAKE tollgate CLI emitting canary ports. The helpers
#      must answer with the canary values under both modes: that proves the
#      shell reads what the binary prints, not a stale copy. A hardcoded
#      fallback table fails here (it would answer the real ports, not the
#      canary).
#   B. NO LITERAL TABLE — the four helper bodies must contain no digits at
#      all: pure indirection. This is the tripwire against reintroducing a
#      shell-side table "for safety" — a fallback re-opens the drift hole
#      (#746's lesson).
#   C. THE ONE TABLE — Go's uiPortPair (executed through a throwaway go test,
#      the same trick as before) must dump the full 2 modes × 2 UIs grid, and
#      the shell EMISSION (`tollgate ui ports --format shell` shape, printed
#      by the same test) must agree with it variable-for-variable — the eval
#      contract between the binary and the setup.
#   D. D2 ANCHORS — independent of any source, so a drift that moves uiPortPair
#      itself still fails: the port sets are fixed (entry pair 8080+443,
#      secondary pair 8090+8443) and the orientation is fixed (entry_ui=board
#      → board on the entry pair; entry_ui=luci → LuCI on it).
#
# Exit 0 when the contract holds, 1 otherwise.

set -uo pipefail

cd "$(dirname "$0")/../.." || exit 1
ROOT="$(pwd)"
SETUP="$ROOT/packaging/files/etc/uci-defaults/99-tollgate-setup"
GO_DIR="$ROOT/src/cmd/tollgate-cli"
DUMP_TEST="$GO_DIR/zzz_entryui_portpair_dump_test.go"

fails=0
pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1" >&2; fails=$((fails + 1)); }
info() { printf '  info  %s\n' "$1"; }

printf 'check-entry-ui-ports: root %s\n' "$ROOT"

if [ ! -f "$SETUP" ]; then
    fail "$SETUP is missing; the entry_ui port-table contract cannot be checked"
    printf '\n1 entry_ui port-table check FAILED.\n' >&2
    exit 1
fi
if ! command -v go >/dev/null 2>&1; then
    fail "go is not in PATH — the Go half of the table (uiPortPair) is executed through 'go test'"
    printf '\n1 entry_ui port-table check FAILED.\n' >&2
    exit 1
fi

SANDBOX="$(mktemp -d "${TMPDIR:-/tmp}/entryui-ports.XXXXXX")"
trap 'rm -rf "$SANDBOX"; rm -f "$DUMP_TEST"' EXIT

# --- A. canary consumption: the setup evaluates the binary's output --------

awk '/^# -- driver/{exit} {print}' "$SETUP" > "$SANDBOX/lib.sh"

SHELL_HELPERS='uhttpd_main_http_port uhttpd_main_https_port board_http_port board_https_port'
missing_helpers="$(
    (
        set -u
        # shellcheck source=/dev/null
        . "$SANDBOX/lib.sh"
        for h in $SHELL_HELPERS; do
            command -v "$h" >/dev/null 2>&1 || printf '%s ' "$h"
        done
        command -v load_ui_port_table >/dev/null 2>&1 || printf '%s ' load_ui_port_table
    ) 2>/dev/null
)"
if [ -n "$missing_helpers" ]; then
    fail "99-tollgate-setup no longer defines ${missing_helpers}— the consumption contract moved; update this check with it"
    printf '\n1 entry_ui port-table check FAILED.\n' >&2
    exit 1
fi

CANARY_MAIN_HTTP_BOARD=28080
CANARY_MAIN_HTTP_LUCI=28081
CANARY_MAIN_TLS_BOARD=28443
CANARY_MAIN_TLS_LUCI=28444
CANARY_BOARD_HTTP_BOARD=28090
CANARY_BOARD_HTTP_LUCI=28091
CANARY_BOARD_TLS_BOARD=28445
CANARY_BOARD_TLS_LUCI=28446
cat > "$SANDBOX/bin-tollgate" <<SHIM
#!/bin/sh
[ "\$1 \$2" = 'ui ports' ] || { echo "fake tollgate: unexpected call: \$*" >&2; exit 1; }
[ "\$3 \$4" = '--format shell' ] || { echo "fake tollgate: unexpected args: \$*" >&2; exit 1; }
cat <<'TABLE'
uhttpd_main_http_port_board=${CANARY_MAIN_HTTP_BOARD}
uhttpd_main_http_port_luci=${CANARY_MAIN_HTTP_LUCI}
uhttpd_main_https_port_board=${CANARY_MAIN_TLS_BOARD}
uhttpd_main_https_port_luci=${CANARY_MAIN_TLS_LUCI}
board_http_port_board=${CANARY_BOARD_HTTP_BOARD}
board_http_port_luci=${CANARY_BOARD_HTTP_LUCI}
board_https_port_board=${CANARY_BOARD_TLS_BOARD}
board_https_port_luci=${CANARY_BOARD_TLS_LUCI}
TABLE
SHIM
chmod +x "$SANDBOX/bin-tollgate"

canary_out="$(
    (
        set -u
        # shellcheck source=/dev/null
        . "$SANDBOX/lib.sh"
        TOLLGATE_CLI="$SANDBOX/bin-tollgate"
        LOGFILE=/dev/null
        load_ui_port_table || { echo "LOAD_FAILED"; exit 1; }
        for mode in board luci; do
            ENTRY_UI_MODE="$mode"
            printf 'canary %s luci %s %s\n'  "$mode" "$(uhttpd_main_http_port)"  "$(uhttpd_main_https_port)"
            printf 'canary %s board %s %s\n' "$mode" "$(board_http_port)"        "$(board_https_port)"
        done
    ) 2>/dev/null
)"
if [ -z "$canary_out" ] || printf '%s' "$canary_out" | grep -q LOAD_FAILED; then
    fail "load_ui_port_table failed against a healthy fake emission — the setup cannot load the port table at all"
    printf '\n1 entry_ui port-table check FAILED.\n' >&2
    exit 1
fi

expect_canary() { # <mode> <ui> "<http> <tls>"
    local got
    got="$(printf '%s\n' "$canary_out" | awk -v m="$1" -v u="$2" '$2 == m && $3 == u {print $4" "$5}')"
    if [ "$got" = "$3" ]; then
        pass "entry_ui=$1 → $2 helpers answer the binary's canary ($3)"
    else
        fail "entry_ui=$1 → $2 helpers answer '${got:-<nothing>}', want the canary '$3' — the shell is NOT consuming the binary's emission (a literal fallback table is back?)"
    fi
}
expect_canary board luci  "$CANARY_MAIN_HTTP_BOARD $CANARY_MAIN_TLS_BOARD"
expect_canary board board "$CANARY_BOARD_HTTP_BOARD $CANARY_BOARD_TLS_BOARD"
expect_canary luci luci  "$CANARY_MAIN_HTTP_LUCI $CANARY_MAIN_TLS_LUCI"
expect_canary luci board "$CANARY_BOARD_HTTP_LUCI $CANARY_BOARD_TLS_LUCI"

malformed_verdict="$(
    (
        set -u
        # shellcheck source=/dev/null
        . "$SANDBOX/lib.sh"
        TOLLGATE_CLI=/bin/false
        LOGFILE=/dev/null
        load_ui_port_table >/dev/null 2>&1 && echo KEPT_GOING || echo REFUSED
    ) 2>/dev/null
)"
if [ "$malformed_verdict" = REFUSED ]; then
    pass "a failed emission is refused (no table loaded, loud failure)"
else
    fail "load_ui_port_table tolerated a failed emission — a half-loaded table must be fatal, not a fallback"
fi

# --- B. no literal table: the helper bodies carry no digits at all ---------

# Each helper is a one-line or few-line function; accumulate from its
# definition line through the line carrying the closing brace (inclusive —
# single-line bodies carry it on the definition line itself).
literal_bodies="$(awk '
    /^uhttpd_main_http_port\(\)|^uhttpd_main_https_port\(\)|^board_http_port\(\)|^board_https_port\(\)/ {
        body=$0
        while (body !~ /}/ && (getline line) > 0) body=body line
        if (body ~ /[0-9]/) print body
    }
' "$SETUP" 2>/dev/null)"
if [ -n "$literal_bodies" ]; then
    fail "port helper bodies contain digits — a literal table is back in 99-tollgate-setup (the drift hole #746 closed): $literal_bodies"
else
    pass "the shell helpers are pure lookups — no literal port table exists in 99-tollgate-setup"
fi

# --- C. the one table: uiPortPair's grid and its shell emission ------------

if [ -e "$DUMP_TEST" ]; then
    fail "$DUMP_TEST already exists — refusing to overwrite it; delete it and re-run"
    printf '\n1 entry_ui port-table check FAILED.\n' >&2
    exit 1
fi
cat > "$DUMP_TEST" <<'EOF'
package main

// Code generated by tests/contract/check-entry-ui-ports.sh (#746): a
// throwaway dump of the uiPortPair table and its shell emission, executed
// and deleted by the check. If you are reading this in a diff or a build
// log, the check was interrupted — the file is safe to delete.

import (
	"fmt"
	"testing"
)

func TestContractEntryUIPortPairDump(t *testing.T) {
	for _, mode := range []string{"board", "luci"} {
		for _, ui := range []string{"board", "luci"} {
			httpPort, tlsPort := uiPortPair(ui, mode)
			fmt.Printf("portpair %s %s %s %s\n", mode, ui, httpPort, tlsPort)
		}
	}
	printUIPortsShell()
}
EOF

go_out="$(cd "$GO_DIR" && go test -run '^TestContractEntryUIPortPairDump$' -count=1 -v . 2>&1)"
go_rc=$?
if [ "$go_rc" -ne 0 ]; then
    fail "go test could not execute uiPortPair/printUIPortsShell (the port table's single declaration is broken, renamed or moved):"
    printf '%s\n' "$go_out" | sed 's/^/        /' >&2
    printf '\n1 entry_ui port-table check FAILED.\n' >&2
    exit 1
fi

printf '%s\n' "$go_out" | grep '^portpair ' | sort > "$SANDBOX/go.table"
printf '%s\n' "$go_out" | grep -E '^(uhttpd_main_(http|https)_port|board_(http|https)_port)_(board|luci)=' | sort > "$SANDBOX/emission.table"

validate_table() { # <file> <which half>
    local f="$1" which="$2" n keys
    n="$(grep -c . "$f" || true)"
    if [ "$n" -ne 4 ]; then
        fail "${which} dumped ${n} row(s), expected the 2 modes × 2 UIs grid (4)"
        return 1
    fi
    keys="$(awk '{print $2"/"$3}' "$f" | sort -u | wc -l)"
    if [ "$keys" -ne 4 ]; then
        fail "${which}'s rows do not cover the modes × UIs grid distinctly (${keys} distinct keys)"
        return 1
    fi
    return 0
}

emission_lines="$(grep -c . "$SANDBOX/emission.table" || true)"
shape_ok=1
validate_table "$SANDBOX/go.table" "uiPortPair" || shape_ok=0
if [ "$emission_lines" -ne 8 ]; then
    fail "the shell emission carries ${emission_lines} variable(s), want 8 (4 helpers × 2 modes)"
    shape_ok=0
fi
if [ "$shape_ok" -eq 1 ]; then
    pass "uiPortPair dumps the full 2 modes × 2 UIs grid and the emission all 8 variables"
fi

lookup() {
    awk -v m="$2" -v u="$3" '$2 == m && $3 == u {print $4" "$5}' "$1"
}
emit_lookup() { # <emission-file> <mode> <ui> -> "<http> <tls>"
    local main_http main_tls board_http board_tls
    main_http="$(awk -F= -v m="$2" '$1 == "uhttpd_main_http_port_"m {print $2}' "$1")"
    main_tls="$(awk -F= -v m="$2" '$1 == "uhttpd_main_https_port_"m {print $2}' "$1")"
    board_http="$(awk -F= -v m="$2" '$1 == "board_http_port_"m {print $2}' "$1")"
    board_tls="$(awk -F= -v m="$2" '$1 == "board_https_port_"m {print $2}' "$1")"
    if [ "$3" = luci ]; then
        printf '%s %s' "$main_http" "$main_tls"
    else
        printf '%s %s' "$board_http" "$board_tls"
    fi
}

if [ "$shape_ok" -eq 1 ]; then
    while read -r _ mode ui http tls; do
        go_cell="$http $tls"
        emit_cell="$(emit_lookup "$SANDBOX/emission.table" "$mode" "$ui")"
        if [ "$emit_cell" = "$go_cell" ]; then
            pass "entry_ui=$mode → $ui on $http + $tls — the emitted variables say exactly what uiPortPair says"
        else
            fail "entry_ui=$mode → $ui: uiPortPair puts ${go_cell}, the shell emission puts ${emit_cell:-<nothing>} — the eval contract between the binary and the setup is broken"
        fi
    done < "$SANDBOX/go.table"
fi

# --- D. D2 anchors: on the one table, so moving it fails too ---------------

if [ "$shape_ok" -eq 1 ]; then
    while read -r _ mode ui http tls; do
        case "$http $tls" in
            '8080 443'|'8090 8443') : ;;
            *)
                fail "anchor: entry_ui=$mode → $ui sits on $http + $tls, but D2 fixes the port sets at 8080+443 and 8090+8443 — the ONE table drifted (#746)"
                ;;
        esac
    done < "$SANDBOX/go.table"

    if [ "$(lookup "$SANDBOX/go.table" luci luci)" = '8080 443' ]; then
        pass "anchor: entry_ui=luci keeps LuCI on the entry pair (the mapping every release before 0.6.0 shipped)"
    else
        fail "anchor: entry_ui=luci must put LuCI on the entry pair 8080+443 — the legacy mapping is not free to move (D2)"
    fi
    if [ "$(lookup "$SANDBOX/go.table" board board)" = '8080 443' ]; then
        pass "anchor: entry_ui=board puts the board on the entry pair (D2's default)"
    else
        fail "anchor: entry_ui=board must put the board on the entry pair 8080+443 (D2)"
    fi
fi

printf '\n'
if [ "$fails" -ne 0 ]; then
    printf '%d entry_ui port-table check(s) FAILED.\n' "$fails" >&2
    exit 1
fi
printf "The entry_ui port table has one declaration (Go uiPortPair -> 'tollgate ui ports --format shell'), the setup consumes it, and the D2 anchors hold (8080+443 entry, 8090+8443 secondary, board/luci orientation).\n"
