#!/bin/sh
# Refresh or verify the pinned inputs in packaging/build-inputs.json.
#
#   check  (default) — compare manifest values against the live registries;
#                      exit 1 on drift (CI guard).
#   update           — rewrite the manifest's SDK digests, Go tarball sha256
#                      and Node tarball sha256 with the live values. Version
#                      literals (go/node/upx versions, portal commit) are
#                      NEVER bumped here — those are intentional decisions.
#
# Requirements: curl, jq. See docs/reproducible-builds.md.
set -eu

MODE="${1:-check}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
TG_ROOT="$SCRIPT_DIR/.."
export TG_ROOT
. "$TG_ROOT/packaging/build-env.sh"

TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

reg_token() { curl -s "https://auth.docker.io/token?service=registry.docker.io&scope=repository:openwrt/sdk:pull" | jq -r .token; }

sdk_digest() {
    _tag="$1"
    _token="$(reg_token)"
    curl -sI "https://registry-1.docker.io/v2/openwrt/sdk/manifests/$_tag" \
        -H "Authorization: Bearer $_token" \
        -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json' \
        | tr -d '\r' | awk 'tolower($1)=="docker-content-digest:"{print $2}'
}

go_sha() {
    curl -s "https://go.dev/dl/?mode=json&include=all" \
        | jq -r --arg v "go$GO_VERSION" '.[] | select(.version==$v) | .files[]
          | select(.filename==($v + ".linux-amd64.tar.gz")) | .sha256'
}

node_sha() {
    curl -s "https://nodejs.org/download/release/v$NODE_VERSION/SHASUMS256.txt" \
        | awk -v f="node-v$NODE_VERSION-linux-x64.tar.gz" '$2==f{print $1}'
}

drift=0
report() { # <what> <manifest> <live> <mode>
    if [ "$2" != "$3" ]; then
        drift=$((drift + 1))
        printf 'DRIFT %s: manifest=%s live=%s\n' "$1" "$2" "$3" >&2
        if [ "$4" = update ]; then printf '%s' "$3" > "$TMP/changed"; fi
    fi
}

# SDK digests per target
for target in $(jq -r '.openwrt_sdk.targets | keys[]' "$TG_BUILD_INPUTS"); do
    tag="$target-$SDK_RELEASE"
    live="$(sdk_digest "$tag")"
    pinned="$(jq -r --arg t "$target" '.openwrt_sdk.targets[$t].digest' "$TG_BUILD_INPUTS")"
    if [ "$MODE" = update ] && [ -n "$live" ] && [ "$live" != "$pinned" ]; then
        jq --arg t "$target" --arg d "$live" \
            '.openwrt_sdk.targets[$t].digest = $d' "$TG_BUILD_INPUTS" > "$TMP/bi.json" && mv "$TMP/bi.json" "$TG_BUILD_INPUTS"
        printf 'UPDATED sdk %s -> %s\n' "$target" "$live"
    else
        report "sdk:$target" "$pinned" "${live:-<unreachable>}" "$MODE"
    fi
done

# Toolchain tarball hashes for the pinned versions
for pair in "go:$(go_sha):.go.tarball_linux_amd64.sha256" \
            "node:$(node_sha):.node.tarball_linux_x64.sha256"; do
    what="${pair%%:*}"; rest="${pair#*:}"
    live="${rest%%:*}"; path="${rest#*:}"
    pinned="$(jq -r "$path" "$TG_BUILD_INPUTS")"
    if [ "$MODE" = update ] && [ -n "$live" ] && [ "$live" != "$pinned" ]; then
        jq --arg h "$live" "$path = \$h" "$TG_BUILD_INPUTS" > "$TMP/bi.json" && mv "$TMP/bi.json" "$TG_BUILD_INPUTS"
        printf 'UPDATED %s tarball sha256 -> %s\n' "$what" "$live"
    else
        report "$what:tarball" "$pinned" "${live:-<unreachable>}" "$MODE"
    fi
done

if [ "$drift" -gt 0 ]; then
    printf '%d pinned input(s) drifted from the live registry. Run: scripts/update-build-inputs.sh update\n' "$drift" >&2
    exit 1
fi
echo "build-inputs.json matches the live registries."
