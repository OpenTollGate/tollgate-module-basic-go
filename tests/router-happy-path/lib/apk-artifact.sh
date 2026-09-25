#!/usr/bin/env bash
#
# apk-artifact.sh -- resolve the package under test into an extracted tree.
#
# Sourced by run.sh (no side effects on source). Provides:
#
#   rhp_prepare_artifact <path> <cache_root>
#       <path> is either an already-extracted directory, or a package file
#       (`.apk` / `.ipk`). Prints the extracted tree path on stdout on success;
#       prints a human-readable error on stderr and returns non-zero otherwise.
#
# Extraction notes (learned the hard way; see tests/happy-path/README.md):
#   * An OpenWrt 25.12 `.apk` is an ADB **v3 container**, not a tarball and not
#     gzip/zstd. `tar`/`unzip` cannot read it. `apk-tools` (the 3.x/static
#     branch) can: `apk.static extract --allow-untrusted --destination`.
#   * `.ipk` (opkg, 24.10 and older) IS a gzipped tarball -> `tar -xzf`.
#   * We never guess: after extraction we require the tree to actually contain
#     `etc/` or `usr/`, otherwise the run fails loudly instead of comparing
#     against an empty directory (which would make every identity check "pass"
#     by absence).
#
# Extraced trees are cached under <cache_root>/<sha256-of-package>/ so repeated
# runs do not re-extract. A package change (different sha256) always re-extracts.

RHP_DOCKER_IMAGE="${RHP_DOCKER_IMAGE:-alpine:edge}"

_rhp_die() { printf 'rhp-apk: %s\n' "$*" >&2; return 1; }

# _rhp_sha256 <file> -> hex digest
_rhp_sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | cut -d' ' -f1
    else
        printf '%s' "$(openssl dgst -sha256 -r "$1" | cut -d' ' -f1)"
    fi
}

# _rhp_looks_extracted <dir>
_rhp_looks_extracted() {
    [ -d "$1/etc" ] || [ -d "$1/usr" ] || [ -d "$1/www" ]
}

# _rhp_extract <package> <dest>
_rhp_extract() {
    local pkg="$1" dest="$2"
    case "$pkg" in
        *.ipk)
            tar -xzf "$pkg" -C "$dest" || return 1
            # .ipk carries data.tar.gz; tar -xzf already yields the payload.
            ;;
        *.apk)
            # Every extractor's stdout goes to stderr: run.sh captures this
            # function's stdout as the artifact PATH, and apk-tools is chatty.
            if command -v apk >/dev/null 2>&1 && apk --version 2>/dev/null | grep -q 'apk-tools 3'; then
                apk extract --allow-untrusted --destination "$dest" "$pkg" >&2 || return 1
            elif command -v apk.static >/dev/null 2>&1; then
                apk.static extract --allow-untrusted --destination "$dest" "$pkg" >&2 || return 1
            elif command -v docker >/dev/null 2>&1; then
                local pdir pbase
                pdir="$(cd "$(dirname "$pkg")" && pwd)"
                pbase="$(basename "$pkg")"
                mkdir -p "$dest"
                # -e RHP_UID/GID + the chown: the container extracts as root, so
                # without this the cache belongs to uid 0 and the invoking user
                # cannot remove or refresh it.
                docker run --rm -e RHP_UID="$(id -u)" -e RHP_GID="$(id -g)" \
                    -v "$pdir":/rhp-in:ro -v "$dest":/rhp-out \
                    "$RHP_DOCKER_IMAGE" sh -c \
                    'apk add --no-cache apk-tools-static >/dev/null 2>&1 &&
                     /sbin/apk.static extract --allow-untrusted --destination /rhp-out "/rhp-in/$0" &&
                     chown -R "$RHP_UID:$RHP_GID" /rhp-out' \
                    "$pbase" >&2 || return 1
            else
                _rhp_die "cannot extract $pkg: need apk-tools 3 (apk/apk.static) or docker"
                return 1
            fi
            ;;
        *)
            _rhp_die "unsupported package type: $pkg (expected .apk, .ipk, or an extracted directory)"
            return 1
            ;;
    esac
    return 0
}

rhp_prepare_artifact() {
    local src="${1:-}" cache_root="${2:-/var/tmp/rhp-artifacts}"

    [ -n "$src" ] || { _rhp_die "no artifact given (use --apk FILE or --artifact-dir DIR)"; return 2; }
    [ -e "$src" ] || { _rhp_die "artifact not found: $src"; return 2; }

    if [ -d "$src" ]; then
        src="$(cd "$src" && pwd)"
        _rhp_looks_extracted "$src" || {
            _rhp_die "directory $src does not look like an extracted package (no etc/, usr/ or www/)"
            return 1
        }
        printf 'RHPNOTE artifact: using extracted tree %s (no extraction needed)\n' "$src" >&2
        printf '%s\n' "$src"
        return 0
    fi

    local sha cache
    sha="$(_rhp_sha256 "$src")"
    cache="$cache_root/${sha:0:16}-$(basename "$src")"

    if _rhp_looks_extracted "$cache"; then
        printf 'RHPNOTE artifact: reusing cached extraction of %s (%s)\n' \
            "$(basename "$src")" "$cache" >&2
        printf '%s\n' "$cache"
        return 0
    fi

    mkdir -p "$cache" || return 1
    printf 'RHPNOTE artifact: extracting %s (sha256 %s) -> %s\n' \
        "$(basename "$src")" "$sha" "$cache" >&2
    if ! _rhp_extract "$src" "$cache"; then
        _rhp_die "extraction failed; removing $cache" >&2
        rm -rf "$cache"
        return 1
    fi
    if ! _rhp_looks_extracted "$cache"; then
        _rhp_die "extraction produced no etc/ usr/ or www/ in $cache (wrong container format?)"
        rm -rf "$cache"
        return 1
    fi
    printf 'RHPNOTE artifact: extracted %s\n' "$sha" >&2
    printf '%s\n' "$cache"
    return 0
}
