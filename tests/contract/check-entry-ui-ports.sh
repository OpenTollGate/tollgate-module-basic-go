#!/usr/bin/env bash
# Contract: the entry_ui port mapping is declared exactly twice, and the two
# declarations must agree (#746):
#
#   1. the shell helpers in packaging/files/etc/uci-defaults/99-tollgate-setup
#      — uhttpd_main_http_port / uhttpd_main_https_port (LuCI's listener,
#      uhttpd.main) and board_http_port / board_https_port (the board's
#      listener, uhttpd.admin) — which read $ENTRY_UI_MODE;
#   2. Go's uiPortPair (src/cmd/tollgate-cli/ui.go), the table
#      `tollgate ui links` answers the board SPA's cross-links from (#745).
#
# Neither side can see the other: the shell runs at package install time on
# the router, the Go table serves the rpcd/SPA path, so a port change on one
# side silently diverges the other — the SPA would advertise a port that
# answers a different UI (or nothing at all). #745's review made the "must
# match" rule explicit; this check is its CI-owned enforcement.
#
# Like check-ssid-format.sh, the contract is asserted BEHAVIOURALLY, not by
# grepping sources — both tables are pure and offline-executable, so the real
# code runs:
#
#   A. the shell helpers are sourced out of 99-tollgate-setup (driver
#      stripped, the same awk cut check-ssid-format.sh uses) and called under
#      ENTRY_UI_MODE=board and ENTRY_UI_MODE=luci;
#   B. uiPortPair is executed by a throwaway `go test` written into
#      src/cmd/tollgate-cli (the package is `main` and cannot be imported)
#      over the same mode × UI grid.
#
# The two 2×2 grids must then be cell-for-cell identical.
#
# Cross-equality alone cannot catch a drift that changes BOTH tables in one
# PR, so the D2 anchors of
# docs/architecture/default-ui-and-entry-port-decision.md are pinned directly
# (the port sets never change; the mapping orientation is fixed):
#
#   entry pair = 8080 + 443        secondary pair = 8090 + 8443
#   entry_ui=board -> board on the entry pair, LuCI on the secondary one
#   entry_ui=luci  -> LuCI on the entry pair (the mapping every release
#                     before 0.6.0 shipped), the board on the secondary one
#
# Exit 0 when the contract holds, 1 otherwise.

set -uo pipefail

cd "$(dirname "$0")/../.." || exit 1
ROOT="$(pwd)"
SETUP="$ROOT/packaging/files/etc/uci-defaults/99-tollgate-setup"
GO_DIR="$ROOT/src/cmd/tollgate-cli"
# A throwaway file: written, executed and deleted by this check. The name is
# deliberately improbable; if a file by that name ever exists the check
# refuses to run rather than clobber it.
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

# --- A. the shell table: source the real helpers out of 99-tollgate-setup ---
#
# Everything above the driver marker is library code (the same cut
# check-ssid-format.sh makes); the driver itself reads and writes /etc, so a
# checkout cannot execute it — but the port helpers are pure prints over
# $ENTRY_UI_MODE.
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
    ) 2>/dev/null
)"
if [ -n "$missing_helpers" ]; then
    fail "99-tollgate-setup no longer defines ${missing_helpers}— the shell half of the port table moved; update this check with it"
    printf '\n1 entry_ui port-table check FAILED.\n' >&2
    exit 1
fi

# dump_shell prints `portpair <mode> <ui> <http> <tls>` for the 2×2 grid.
# uhttpd.main IS LuCI's listener (the setup script's own mapping), so the
# uhttpd_main_* helpers answer for ui=luci and board_* for ui=board.
dump_shell() {
    (
        set -u
        # shellcheck source=/dev/null
        . "$SANDBOX/lib.sh"
        # Never touch the host: the sourced log() would append here if any
        # library path ever grows one.
        LOGFILE=/dev/null
        for mode in board luci; do
            ENTRY_UI_MODE="$mode"
            printf 'portpair %s luci %s %s\n'  "$mode" "$(uhttpd_main_http_port)"  "$(uhttpd_main_https_port)"
            printf 'portpair %s board %s %s\n' "$mode" "$(board_http_port)"        "$(board_https_port)"
        done
    ) 2>/dev/null
}

# --- B. the Go table: execute uiPortPair through a throwaway go test -------
#
# src/cmd/tollgate-cli is package main, so the one function cannot be
# imported — it is exercised in place by a generated test that prints the
# same grid, then deleted by the trap above. A rename or deletion of
# uiPortPair fails here as a compile error naming the symbol.
if [ -e "$DUMP_TEST" ]; then
    fail "$DUMP_TEST already exists — refusing to overwrite it; delete it and re-run"
    printf '\n1 entry_ui port-table check FAILED.\n' >&2
    exit 1
fi
cat > "$DUMP_TEST" <<'EOF'
package main

// Code generated by tests/contract/check-entry-ui-ports.sh (#746): a
// throwaway dump of the uiPortPair table, executed and deleted by the check.
// If you are reading this in a diff or a build log, the check was
// interrupted — the file is safe to delete.

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
}
EOF

# -v is required: without it a PASSING test's stdout is buffered away.
go_out="$(cd "$GO_DIR" && go test -run '^TestContractEntryUIPortPairDump$' -count=1 -v . 2>&1)"
go_rc=$?
if [ "$go_rc" -ne 0 ]; then
    fail "go test could not execute uiPortPair (the Go half of the port table is broken, renamed or moved):"
    printf '%s\n' "$go_out" | sed 's/^/        /' >&2
    printf '\n1 entry_ui port-table check FAILED.\n' >&2
    exit 1
fi

printf '%s\n' "$go_out" | grep '^portpair ' | sort > "$SANDBOX/go.table"
dump_shell | sort > "$SANDBOX/shell.table"

# --- shape: each table must be the full 2 modes x 2 UIs grid ---------------

validate_table() { # <file> <which half>
    local f="$1" which="$2" n keys
    n="$(grep -c . "$f" || true)"
    if [ "$n" -ne 4 ]; then
        fail "${which} table dumped ${n} row(s), expected the 2 modes × 2 UIs grid (4)"
        return 1
    fi
    keys="$(awk '{print $2"/"$3}' "$f" | sort -u | wc -l)"
    if [ "$keys" -ne 4 ]; then
        fail "${which} table's rows do not cover the modes × UIs grid distinctly (${keys} distinct keys)"
        return 1
    fi
    return 0
}

shape_ok=1
validate_table "$SANDBOX/shell.table" "shell (99-tollgate-setup)" || shape_ok=0
validate_table "$SANDBOX/go.table" "Go (uiPortPair)" || shape_ok=0
if [ "$shape_ok" -eq 1 ]; then
    pass "both tables dump the full 2 modes × 2 UIs grid"
fi

# --- the contract itself: cell-for-cell agreement ---------------------------

# lookup <table-file> <mode> <ui> -> "<http> <tls>"
lookup() {
    awk -v m="$2" -v u="$3" '$2 == m && $3 == u {print $4" "$5}' "$1"
}

if [ "$shape_ok" -eq 1 ]; then
    while read -r _ mode ui http tls; do
        go_cell="$http $tls"
        shell_cell="$(lookup "$SANDBOX/shell.table" "$mode" "$ui")"
        if [ "$shell_cell" = "$go_cell" ]; then
            pass "entry_ui=$mode → $ui on $http + $tls — shell and Go agree"
        else
            fail "entry_ui=$mode → $ui: 99-tollgate-setup puts ${shell_cell:-<nothing>}, uiPortPair puts ${go_cell} — the two port tables disagree (#746): change them together or not at all"
        fi
    done < "$SANDBOX/go.table"
fi

# --- D2 anchors: independent of the sources, so a drift that moves BOTH
# tables in one PR still fails. Checked on the shell table; the equality
# above extends the verdict to the Go one.

if [ "$shape_ok" -eq 1 ]; then
    while read -r _ mode ui http tls; do
        case "$http $tls" in
            '8080 443'|'8090 8443') : ;;
            *)
                fail "anchor: entry_ui=$mode → $ui sits on $http + $tls, but D2 fixes the port sets at 8080+443 and 8090+8443 — BOTH tables drifted together (#746)"
                ;;
        esac
    done < "$SANDBOX/shell.table"

    if [ "$(lookup "$SANDBOX/shell.table" luci luci)" = '8080 443' ]; then
        pass "anchor: entry_ui=luci keeps LuCI on the entry pair (the mapping every release before 0.6.0 shipped)"
    else
        fail "anchor: entry_ui=luci must put LuCI on the entry pair 8080+443 — the legacy mapping is not free to move (D2)"
    fi
    if [ "$(lookup "$SANDBOX/shell.table" board board)" = '8080 443' ]; then
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
printf 'Both entry_ui port tables agree (shell 99-tollgate-setup and Go uiPortPair: 8080+443 entry, 8090+8443 secondary, board/luci orientation per D2).\n'
