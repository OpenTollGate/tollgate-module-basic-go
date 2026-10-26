#!/usr/bin/env bash
# Scaffold a desk-run record pre-filled with date, tree SHA, and the
# sanitization checklist. Usage: new-run.sh <device-slug> [scenario]
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/../.." && pwd)"
SLUG="${1:?usage: new-run.sh <device-slug> [scenario]}"
SCENARIO="${2:-user-shaped happy path}"
DATE="$(date -u +%Y-%m-%d)"
SHA="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo nogit)"
OUT="$HERE/$SLUG-$DATE.md"
[ ! -e "$OUT" ] || { echo "refusing to overwrite $OUT" >&2; exit 1; }
sed -e "s|<YYYY-MM-DD>|$DATE|" \
    -e "s|<device-slug>|$SLUG|" \
    -e "s|<one-line scenario>|$SCENARIO|" \
    -e "s|<sha>|$SHA|" \
    "$HERE/template.md" > "$OUT"
ART="$HERE/artifacts/$SLUG-$DATE"
mkdir -p "$ART"
cat >&2 <<EOF
scaffolded: $OUT
artifact dir: $ART  (put logs/hashes here; commit evidence-worthy files only)

BEFORE COMMITTING — sanitize:
  [ ] no SSIDs, WiFi passphrases, or network names
  [ ] no private keys, identities.json bodies, or token strings
  [ ] every NIT in section 5 links a filed issue (file-nit.sh)
  [ ] section 7 states what was NOT tested
EOF
