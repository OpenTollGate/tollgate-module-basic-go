#!/usr/bin/env bash
# Regenerate tollgate CLI man pages (section 8) from the cobra command tree.
#
# The tollgate binary has a hidden `__gen-man <dir>` subcommand that walks the
# command tree with spf13/cobra's GenManTree and writes one .8 file per command.
# Run this whenever a command, flag, or description changes so the installed
# man pages stay in sync with the binary.
#
# Usage:  scripts/gen-man-pages.sh
# Output: packaging/files/man/man8/*.8  (committed; installed by the ipk/apk)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
CLI_DIR="$REPO_ROOT/src/cmd/tollgate-cli"
MAN_DIR="$REPO_ROOT/packaging/files/man/man8"

echo "==> building tollgate CLI"
cd "$CLI_DIR"
go build -o /tmp/tollgate-genman .

echo "==> generating man pages into $MAN_DIR"
rm -f "$MAN_DIR"/*.8
/tmp/tollgate-genman __gen-man "$MAN_DIR"
rm -f /tmp/tollgate-genman

count=$(ls -1 "$MAN_DIR"/*.8 | wc -l)
echo "==> done: $count man pages in $MAN_DIR"
echo "    review with: man -l $MAN_DIR/tollgate.8"
