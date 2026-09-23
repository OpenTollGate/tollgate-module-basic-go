#!/usr/bin/env bash
# Assert that no default the package ships under packaging/files/ advertises who
# built the router.
#
# Scope: packaging/files/ — the config/script tree the .ipk/.apk payload stages
# from. The compiled binaries installed by the same package are built from src/
# and are deliberately NOT scanned here: they carry the representative operator
# identities (`c08r4d0r`, `amperstrand`, `origami74` — the 0.07 profit-share
# examples) that README.md and docs/merchant.md document as shipped examples.
# Whether those stay is its own decision; this check covers the defaults a
# device BROADCASTS, where the reader is anyone in radio range.
#
# 99-tollgate-setup generated the default private SSID as
# `c08r4d0r-${RANDOM_SUFFIX}` — a handle repeated in the beacon frame of every
# router that took the default. Any WiFi scanner in range reads it, so flashing
# a router broadcast it to the street. A shipped default has to name the
# product, never the person — and it has to follow the brand selector, so a
# rebranded build does not broadcast another brand's name.
#
# Three invariants, all cheap to check from a checkout:
#
#   A. no operator/maintainer identity literal appears anywhere under
#      packaging/files/;
#   B. the brand selector maps BRAND_HOSTNAME to product names, never handles;
#   C. the generated default private SSID is exactly
#      `${BRAND_HOSTNAME}-Private-${RANDOM_SUFFIX}` — device-unique and derived
#      from the brand selector, so a future edit can neither put a personal
#      handle back nor hardcode one brand's name into every build.
#
# Exit 0 when all three hold, 1 otherwise.

set -euo pipefail

cd "$(dirname "$0")/../.."
ROOT="$(pwd)"
FILES_DIR="$ROOT/packaging/files"
SETUP="$FILES_DIR/etc/uci-defaults/99-tollgate-setup"

# Operator/maintainer handles that must never ride in a shipped default.
# Add a name here when it starts appearing in the tree by accident. Documented
# uses of the shared operator nym live outside packaging/files/ and are
# intentional — see CONTRIBUTING.md, "Branding, the operator nym, and net4sats".
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

if [ ! -f "$SETUP" ]; then
    fail "$SETUP is missing; the generated SSID cannot be checked"
else
    # --- B. the brand selector maps to product names, not handles -----------
    brand_names="$(grep -oE 'BRAND_HOSTNAME="[^"]*"' "$SETUP" \
        | sed -nE 's/.*="([^"]*)"$/\1/p' | sort -u)"
    if [ -z "$brand_names" ]; then
        fail "load_brand() no longer assigns BRAND_HOSTNAME a literal product name; update this check"
    else
        while IFS= read -r brand_name; do
            if printf '%s' "$brand_name" | grep -qE "$IDENTITIES"; then
                fail "brand selector maps BRAND_HOSTNAME to '$brand_name', a maintainer/operator identity"
            elif printf '%s' "$brand_name" | grep -qEv '^[A-Z][A-Za-z0-9]*$'; then
                fail "brand selector maps BRAND_HOSTNAME to '$brand_name', which is not a product name"
            else
                pass "brand selector maps BRAND_HOSTNAME to the product name '$brand_name'"
            fi
        done <<EOF
$brand_names
EOF
    fi

    # --- C. the default private SSID derives from the brand selector --------
    # The generated default is the assignment with a literal right-hand side;
    # the line above it is the `uci -q get` that preserves an existing value,
    # so the default only lands on a router that has none.
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

        if [ "$default_literal" = '${BRAND_HOSTNAME}-Private-${RANDOM_SUFFIX}' ]; then
            pass "default private SSID follows the brand selector (\${BRAND_HOSTNAME}-Private-<suffix>)"
        else
            fail "default private SSID '$default_literal' is not \${BRAND_HOSTNAME}-Private-\${RANDOM_SUFFIX}: a fixed product name ignores the brand selector, so a rebranded build broadcasts the wrong product"
        fi

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
printf 'No packaging/files/ default advertises who built the router.\n'
