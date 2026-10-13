# Single source of truth for the release version (VERSION at the repo root).
# Only runs when a file that carries the version is staged.
if git diff --cached --name-only \
    | grep -qE '^VERSION$|^src/cli/version\.go$|^packaging/local-build-ipk\.sh$|^packaging/Makefile$|^packaging/files/etc/uci-defaults/99-tollgate-setup$'; then
    echo "🔍 Checking release-version consistency..."
    sh scripts/check-version-sync.sh
fi
