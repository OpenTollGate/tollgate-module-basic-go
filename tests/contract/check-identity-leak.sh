#!/usr/bin/env bash
# Assert that nothing the package ships advertises who built it.
#
# 99-tollgate-setup generated the default private SSID as
# `c08r4d0r-${RANDOM_SUFFIX}` — one maintainer's own handle, repeated in the
# beacon frame of every router that took the default. Any WiFi scanner in
# range reads it, so flashing a router broadcast the operator's username to
# the street. A shipped default has to name the product, never the person.
#
# Two invariants, both cheap to check from a checkout:
#
#   A. no maintainer/operator identity literal appears anywhere under
#      packaging/files/ (the tree the .ipk/.apk payload is staged from);
#   B. the generated default private SSID is device-unique
#      (`${RANDOM_SUFFIX}`) and its fixed part carries the product prefix
#      `TollGate-`, so a future edit cannot quietly put a personal one back.
#
# Exit 0 when both hold, 1 otherwise.

set -euo pipefail

cd "$(dirname "$0")/../.."
ROOT="$(pwd)"
FILES_DIR="$ROOT/packaging/files"
SETUP="$FILES_DIR/etc/uci-defaults/99-tollgate-setup"

# Maintainer/operator handles that must never ride in a shipped default.
# Add a name here when it starts appearing in the tree by accident.
IDENTITIES='c08r4d0r|c03rad0r|amperstrand|origami74|felixfelix'

fails=0
pass() { printf '  ok    %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1" >&2; fails=$((fails + 1)); }

printf 'check-identity-leak: root %s\n' "$ROOT"

# --- A. no identity literal anywhere in the shipped tree --------------------
if [ ! -d "$FILES_DIR" ]; then
    fail "packaging/files/ is missing; the shipped tree cannot be checked"
else
    hits="$(grep -rInE "$IDENTITIES" "$FILES_DIR" || true)"
    if [ -n "$hits" ]; then
        printf '%s\n' "$hits" >&2
        fail "packaging/files/ ships a maintainer identity literal (listed above)"
    else
        pass "packaging/files/ carries no maintainer identity literal"
    fi
fi

# --- B. the default private SSID is unique and product-prefixed -------------
if [ ! -f "$SETUP" ]; then
    fail "$SETUP is missing; the generated SSID cannot be checked"
else
    # The generated default is the assignment with a literal right-hand side;
    # the line above it is the `uci -q get` that preserves an existing value.
    default_literal="$(sed -nE 's/^[[:space:]]*private_ssid="([^"]*)".*/\1/p' "$SETUP" | head -1)"

    if [ -z "$default_literal" ]; then
        fail "$SETUP no longer assigns private_ssid a literal default; update this check"
    else
        printf '  info  generated default private SSID: %s\n' "$default_literal"

        case "$default_literal" in
            *'${RANDOM_SUFFIX}'*)
                pass "default private SSID is device-unique (\${RANDOM_SUFFIX})"
                ;;
            *)
                fail "default private SSID '$default_literal' is not device-unique; make it \${RANDOM_SUFFIX}-based"
                ;;
        esac

        prefix="${default_literal%%\$\{RANDOM_SUFFIX\}*}"
        case "$prefix" in
            TollGate-*|TollGate)
                pass "default private SSID carries the product prefix '${prefix}'"
                ;;
            *)
                fail "default private SSID prefix '$prefix' is not the product name 'TollGate-'"
                ;;
        esac

        if printf '%s' "$default_literal" | grep -qE "$IDENTITIES"; then
            fail "default private SSID '$default_literal' names a maintainer/operator identity"
        else
            pass "default private SSID names no maintainer/operator identity"
        fi
    fi
fi

printf '\n'
if [ "$fails" -ne 0 ]; then
    printf '%d identity-leak check(s) FAILED.\n' "$fails" >&2
    exit 1
fi
printf 'No shipped default advertises who built the router.\n'
