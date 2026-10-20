#!/bin/sh
# gen-build-contract.sh — emit the machine-readable build contract.
#
# WHY THIS EXISTS (the split-brain ledger):
#   The set of binaries a tollgate release ships was declared in THREE
#   places — packaging/Makefile (PREBUILT_*), packaging/local-build-ipk.sh,
#   and scripts/build-sdk-package.sh — and today's lane caught exactly the
#   defect that structure predicts: the CVM daemon was added to the SDK
#   Makefile but not to build-sdk-package.sh, silently breaking every apk
#   build (#1020). Downstream feeds (FreedomTechFeed) re-declare the same
#   facts a FOURTH and FIFTH time, and their Gen-1 feed is three versions
#   behind in precisely these facts.
#
# THE CONTRACT MODEL (one declaration, N consumers, all fenced):
#   - packaging/Makefile's PREBUILT_* lines stay the AUTHORITY for which
#     binaries exist (touching that file is how a binary is added — the
#     generator picks it up, every fence goes red until carriers catch up).
#   - this script is the single WRITER of packaging/build-contract.json.
#   - upstream carriers (build scripts) and downstream feeds are CONSUMERS;
#     tests/contract/check-build-contract.py fences the carriers against
#     the contract on this side, and feeds read the JSON straight out of
#     the release tarball (codeload tarballs are repo snapshots, so the
#     contract ships with every tag and every SHA pin for free).
#   - the module path + ldflags profile per binary live HERE and only here,
#     as the declarative table — they are contract facts, not carrier
#     facts, so carriers must not restate them.
#
# PROFILES (ldflags semantics, mirroring packaging/build-env.sh):
#   service — go_ldflags: cli.Version/GitCommit/BuildTime + GitBranch
#   cli     — cli_ldflags: the service set + main.version (separate module)
#   cvm     — NONE: the daemon's build identity travels in its CEP-6
#             announcement pubkey; a stamped string would be a second,
#             drifting source of identity.
#
# Usage: scripts/gen-build-contract.sh [--out <path>]
#   Default output: packaging/build-contract.json (relative to repo root).
#   Idempotent: same tree in, same bytes out (sorted keys, sorted glob
#   results, LANG/LC_ALL pinned by build-env.sh).
set -eu

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TG_ROOT="$SCRIPT_DIR/.."
export TG_ROOT
. "$TG_ROOT/packaging/build-env.sh"

OUT="$TG_ROOT/packaging/build-contract.json"
if [ "${1:-}" = "--out" ]; then
    [ $# -eq 2 ] || { echo "usage: $0 [--out <path>]" >&2; exit 2; }
    OUT="$2"
fi

command -v jq >/dev/null 2>&1 || tg_die "jq is required (same as the rest of the manifest tooling)"

# ---- version (single source: the VERSION file) ------------------------------

CONTRACT_VERSION="$(tr -d '[:space:]' < "$TG_ROOT/VERSION")"

# ---- language minimum (glob discovery, never a hand list) -------------------
# Every go.mod under src/ contributes; the MAXIMUM is what a build toolchain
# must satisfy. A new module with a higher directive must move this number —
# and the contract consumers (feeds) refuse pins their golang cannot build.

GO_MIN=""
for mod in $(cd "$TG_ROOT" && find src -name go.mod | LC_ALL=C sort); do
    d="$(sed -n 's/^go //p' "$TG_ROOT/$mod" | head -1)"
    [ -n "$d" ] || tg_die "$mod carries no 'go' directive — cannot derive the language minimum"
    if [ -z "$GO_MIN" ]; then GO_MIN="$d"; else
        GO_MIN="$(printf '%s\n%s\n' "$GO_MIN" "$d" | sort -V | tail -1)"
    fi
done
[ -n "$GO_MIN" ] || tg_die "no go.mod found under src/ — cannot derive the language minimum"

# ---- binary set (authority: packaging/Makefile PREBUILT_*) ------------------

prebuilt_names() {
    sed -n 's/^PREBUILT_[A-Z]*:=.*[/)]\(.*\)$/\1/p' "$TG_ROOT/packaging/Makefile" | LC_ALL=C sort -u
}

# name -> module dir + ldflags profile. This table is the one place the
# mapping is written; adding a row without a PREBUILT_* line is caught by
# check-build-contract.py (and vice versa).
module_for() {
    case "$1" in
        tollgate-wrt) printf 'src' ;;
        tollgate)     printf 'src/cmd/tollgate-cli' ;;
        tollgate-cvm) printf 'src/cmd/tollgate-cvm' ;;
        *) return 1 ;;
    esac
}
profile_for() {
    case "$1" in
        tollgate-wrt) printf 'service' ;;
        tollgate)     printf 'cli' ;;
        tollgate-cvm) printf 'cvm' ;;
        *) return 1 ;;
    esac
}

BINARIES_JSON="[]"
for name in $(prebuilt_names); do
    mod="$(module_for "$name" || true)"
    prof="$(profile_for "$name" || true)"
    [ -n "$mod" ] || tg_die "PREBUILT binary '$name' has no module mapping in gen-build-contract.sh — add one (the fence will keep carriers honest once you do)"
    [ -f "$TG_ROOT/$mod/go.mod" ] || tg_die "module dir '$mod' for binary '$name' has no go.mod — wrong mapping?"
    BINARIES_JSON="$(jq -cn --arg n "$name" --arg m "$mod" --arg p "$prof" \
        '$BINARIES + [{name: $n, module: $m, ldflags_profile: $p}]' --argjson BINARIES "$BINARIES_JSON")"
done
[ "$BINARIES_JSON" != "[]" ] || tg_die "no PREBUILT_* binaries found in packaging/Makefile — the contract refuses to ship an empty binary set"

# ---- pinned externals (single source: build-inputs.json) --------------------

GO_PIN="$(jq -r '.go.version' "$TG_BUILD_INPUTS")"
PORTAL_COMMIT="$(jq -r '.portal.commit' "$TG_BUILD_INPUTS")"

# ---- emit -------------------------------------------------------------------

jq -n \
    --arg v "$CONTRACT_VERSION" \
    --arg gm "$GO_MIN" --arg gp "$GO_PIN" \
    --arg pc "$PORTAL_COMMIT" \
    --argjson bins "$BINARIES_JSON" \
    '{
        schema: 1,
        comment: "GENERATED by scripts/gen-build-contract.sh — do not hand-edit. The machine-readable build contract downstream feeds consume from the release tarball: binary set, module map, ldflags profiles, language minimum, pinned externals. Authority: packaging/Makefile PREBUILT_* for binaries; build-inputs.json for pins; the VERSION file for the version. Fence: tests/contract/check-build-contract.py (regeneration diff + carrier coverage).",
        version: $v,
        go: { minimum: $gm, pin: $gp },
        portal_commit: $pc,
        binaries: $bins
    }' > "$OUT"

printf 'wrote %s: version=%s go_min=%s go_pin=%s binaries=%s\n' \
    "$OUT" "$CONTRACT_VERSION" "$GO_MIN" "$GO_PIN" "$(prebuilt_names | tr '\n' ' ')"
