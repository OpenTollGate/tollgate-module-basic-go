#!/usr/bin/env python3
"""Render the ngit release shards from packaging/ngit-release-matrix.json.

WHY A GENERATOR
  ngit-ci runs ONE workflow file per `act` invocation, and that invocation is
  bounded by the coordinator's NGIT_CI_JOB_TIMEOUT_SECS (1800 s here). The full
  14 .ipk + 3 .apk matrix does not fit, and neither does a per-arch grouping of
  it -- a single `upx --ultra-brute` leg has been measured at ~29 minutes on the
  2-CPU job cap, which is most of a budget on its own. Stage 2 is therefore
  sharded into one file per group of legs whose measured cost leaves headroom.

  Those files are ~95% identical (the same resolve-inputs, the same Blossom
  upload, the same per-leg kind-30078 record) and differ only in their static
  matrix. Hand-maintaining eleven copies of that is how a matrix silently loses
  a leg, so the matrix and the shard assignment live in ONE JSON file and these
  files are rendered from it.

  The rendered files ARE committed: act requires static YAML, and a reviewer
  should be able to read the workflow that runs without running a generator.
  `--check` fails when the committed copies drift from the plan, and the
  `release-guard` job runs `--check` before anything is announced, so a stale
  shard cannot reach a release unnoticed.

  Substitution is deliberately `@@TOKEN@@`-based rather than str.format(): the
  bodies are full of `${{ ... }}` workflow expressions and `${...}` shell
  expansions, and a format string would make every one of those a syntax
  hazard.

Usage:
  scripts/ngit-gen-shards.py [--plan PATH] [--outdir DIR] [--check] [--list]

Exit codes:
  0  wrote/validated every shard
  1  --check found drift (or a missing/extra shard file)
  2  the plan or the invocation is unusable
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path

HEADER = """\
# GENERATED FILE -- DO NOT EDIT BY HAND.
#
#   plan   : packaging/ngit-release-matrix.json
#   render : scripts/ngit-gen-shards.py
#   check  : scripts/ngit-gen-shards.py --check   (run by the release-guard job)
#
# This is shard `@@SHARD_ID@@` of the ngit release pipeline's stage 2. Stage 1 is
# .ngit/act/workflows/build-package-binaries.yml and is unchanged.
#
# WHY SHARDS EXIST
#   ngit-ci bounds ONE `act` invocation -- one workflow file -- with the
#   coordinator's NGIT_CI_JOB_TIMEOUT_SECS (@@CEILING@@ s on this deployment).
#   The full 14 .ipk + 3 .apk matrix does not fit that budget: the 5
#   `compression: none` .ipk legs are cheap, `upx --ultra-brute` is minutes of
#   single-threaded CPU per leg on a 2-CPU job cap, and on the first unsharded
#   run (2026-09-12, commit b25d8a28) the 3 .apk/SDK legs never got a slot at
#   all before the ceiling cut the run off. Coverage is not reduced by sharding
#   -- the 14 + 3 matrix is the same, only split across invocations. Never trim
#   a leg to make a shard fit.
#
# WHAT THIS SHARD DOES, AND WHAT IT DELIBERATELY DOES NOT
#   It builds its legs, mirrors each artifact to Blossom, and publishes one
#   kind-30078 record per leg carrying the sha256, the mirror URLs and the
#   release run id. It does NOT publish a kind-1063 announcement: announcements
#   are published by .ngit/act/workflows/build-package-announce.yml, and only
#   once EVERY shard of the same (version, channel, release run) has reported
#   success. A shard that fails or times out therefore leaves the release
#   incomplete and unannounced, instead of announcing a partial matrix that
#   looks finished. See scripts/ngit-release-guard.sh.
#
# HOW IT STARTS
#   Manual replay only (`workflow_dispatch`), so a push to main does not fan out
#   into one act invocation per shard. Start it with scripts/ngit-ci-release.sh,
#   which triggers every shard in order, waits for each kind-9842 result, stops
#   the chain on the first non-success, and only then starts the announce file.
#   The ref it is replayed at carries the version, the channel and the release
#   run:
#
#     refs/heads/release/<version>/<channel>/<release_run>/@@SHARD_ID@@
#
#   which is why every shard of one release publishes the same version and
#   channel even though it is a separate invocation.
#
# MATRIX FOR THIS SHARD
@@MATRIX@@
"""

ENV_BLOCK = """\
env:
  PACKAGE_NAME: "tollgate-wrt"
  SHARD_ID: "@@SHARD_ID@@"
  # blossom1.orangesync.tech is dropped from the GitHub twin's list: measured
  # unreachable from a job container (25 s connect timeout) while the other four
  # answer, and BLOSSOM_MIN_SUCCESS=1 means the dead mirror only bought ~60 s of
  # retries per uploaded file.
  BLOSSOM_SERVERS: "https://blossom.primal.net https://blossom.psbt.me https://blossom2.orangesync.tech https://drive.cashu.email"
  BLOSSOM_MIN_SUCCESS: "1"
  COORDINATION_RELAYS: >-
    wss://relay.damus.io
    wss://nos.lol
    wss://nostr.mom
    wss://relay1.orangesync.tech
    wss://relay2.orangesync.tech
  RELAYS: "wss://relay.damus.io wss://nos.lol wss://nostr.mom wss://relay1.orangesync.tech wss://relay2.orangesync.tech"
"""

# The package-job epoch env is a ${{ ... }} workflow expression, so it must
# never live inside the package-job f-string: f-strings collapse {{ to {,
# which is how the single-brace form reached the committed shards in #441.
# The runner passes that literal through and packaging/build-env.sh's epoch
# validation (non-integer => tg_die) fails every package job. Token-emitted
# like every other brace-bearing line.
EPOCH_ENV_LINE = "      SOURCE_DATE_EPOCH: ${{ needs.resolve-inputs.outputs.source_date_epoch }}"

RESOLVE_INPUTS = """\
  resolve-inputs:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    outputs:
      build_id: ${{ steps.identity.outputs.build_id }}
      package_version: ${{ steps.identity.outputs.package_version }}
      release_channel: ${{ steps.identity.outputs.release_channel }}
      release_run: ${{ steps.identity.outputs.release_run }}
      hashes: ${{ steps.fetch.outputs.hashes }}
      portal_hash: ${{ steps.fetch.outputs.portal_hash }}
      source_date_epoch: ${{ steps.fetch.outputs.source_date_epoch }}
    steps:
      - uses: actions/checkout@v5

      # One step, because the three values have to agree: the version and the
      # channel are what the announcements are keyed by, and the release run is
      # what the announce gate checks every shard of this release against. They
      # all come from the replayed ref, so no shard can invent its own.
      - name: Resolve build id, version, channel and release run
        id: identity
        shell: bash
        run: |
          set -euo pipefail
          : ${GITHUB_OUTPUT:=/tmp/github_output}
          REF="${GITHUB_REF:-}"
          REF_NAME="${GITHUB_REF_NAME:-unknown}"
          BUILD_ID="${GITHUB_SHA:0:8}"
          echo "build_id=${BUILD_ID}" >> "$GITHUB_OUTPUT"

          if [[ "$REF" == refs/tags/* ]]; then
            PACKAGE_VERSION="$REF_NAME"
            case "$REF_NAME" in
              v[0-9]*.[0-9]*.[0-9]*-alpha*) CHANNEL=alpha  ;;
              v[0-9]*.[0-9]*.[0-9]*-beta*)  CHANNEL=beta   ;;
              v[0-9]*.[0-9]*.[0-9])         CHANNEL=stable ;;
              *)                            CHANNEL=dev    ;;
            esac
            RELEASE_RUN="${RELEASE_RUN:-$REF_NAME}"
          elif [[ "$REF_NAME" =~ ^release/([^/]+)/([^/]+)/([^/]+)/[^/]+$ ]]; then
            # refs/heads/release/<version>/<channel>/<release_run>/<shard_id>
            PACKAGE_VERSION="${BASH_REMATCH[1]}"
            CHANNEL="${BASH_REMATCH[2]}"
            RELEASE_RUN="${BASH_REMATCH[3]}"
          else
            SANITIZED_BRANCH_NAME=$(printf '%s' "$REF_NAME" | sed 's|/|-|g')
            COMMIT_HEIGHT=$(git rev-list --count HEAD 2>/dev/null || echo 0)
            PACKAGE_VERSION="${SANITIZED_BRANCH_NAME}.${COMMIT_HEIGHT}.${BUILD_ID}"
            CHANNEL=dev
            RELEASE_RUN="${BUILD_ID}"
          fi

          echo "package_version=${PACKAGE_VERSION}" >> "$GITHUB_OUTPUT"
          echo "release_channel=${CHANNEL}"         >> "$GITHUB_OUTPUT"
          echo "release_run=${RELEASE_RUN}"         >> "$GITHUB_OUTPUT"
          echo "ref=$REF version=$PACKAGE_VERSION channel=$CHANNEL release_run=$RELEASE_RUN"

      - name: Install nak
        run: |
          curl -fsSL "https://github.com/fiatjaf/nak/releases/download/v0.16.2/nak-v0.16.2-linux-amd64" -o /usr/local/bin/nak
          chmod +x /usr/local/bin/nak

      - name: Fetch the stage-1 records
        id: fetch
        env:
          BUILD_ID: ${{ steps.identity.outputs.build_id }}
        run: |
          set -euo pipefail
          : ${GITHUB_OUTPUT:=/tmp/github_output}

          # Returns the newest event line for the addressable record <d>.
          fetch_record() {
            local d="$1" out
            for attempt in $(seq 1 20); do
              out=$(timeout 45 nak req -k 30078 --tag "d=tollgate-build/${BUILD_ID}/${d}" \\
                --limit 1 $COORDINATION_RELAYS < /dev/null 2>/dev/null \\
                | grep -m1 '^{' || true)
              if [ -n "$out" ]; then printf '%s' "$out"; return 0; fi
              echo "  ${d} record not published yet (attempt $attempt/20); waiting 30s" >&2
              sleep 30
            done
            return 1
          }

          # An event's `content` is itself a JSON document encoded as a string,
          # so it has to be parsed twice.
          record_content() {
            printf '%s' "$1" | jq -r '.content' | jq -c .
          }

          BIN_EVENT=$(fetch_record binaries) || {
            echo "ERROR: no binaries record for build id $BUILD_ID." >&2
            echo "Run .ngit/act/workflows/build-package-binaries.yml at this same commit first." >&2
            exit 1
          }
          HASHES=$(record_content "$BIN_EVENT")
          printf '%s' "$HASHES" | jq -e 'type == "object"' > /dev/null || {
            echo "ERROR: binaries record is not a JSON object: $HASHES" >&2; exit 1; }
          missing=""
          for k in arm64 armv7 mipsle-softfloat mips-softfloat amd64; do
            printf '%s' "$HASHES" | jq -e --arg k "$k" 'has($k)' > /dev/null || missing="$missing $k"
          done
          if [ -n "$missing" ]; then
            echo "ERROR: binaries record is missing compile keys:$missing" >&2
            echo "refusing to build a partial architecture set from $HASHES" >&2
            exit 1
          fi
          echo "binaries: $HASHES"
          echo "hashes=$HASHES" >> "$GITHUB_OUTPUT"

          # Reproducibility pin (#383, #441): stage 1 stamps the binaries
          # with a SOURCE_DATE_EPOCH and rides it on this record; packaging
          # must use the SAME epoch or the package mtimes disagree with the
          # binaries' BuildTime. Records from before the field existed fall
          # back to the same derivation stage 1 uses.
          EPOCH=$(printf '%s' "$HASHES" | jq -r '.epoch // empty')
          if [ -z "$EPOCH" ]; then
            EPOCH=$(bash scripts/ngit-commit-epoch.sh "$GITHUB_SHA" 2>/dev/null || true)
          fi
          if [ -z "$EPOCH" ] && [ -n "${{ github.event.head_commit.timestamp }}" ]; then
            EPOCH="$(date -u -d "${{ github.event.head_commit.timestamp }}" +%s)"
          fi
          if [ -z "$EPOCH" ]; then
            EPOCH="$(date +%s)"
            echo "NOTE: stage-1 record carried no epoch; using job clock $EPOCH — package mtimes will NOT rebuild identically" >&2
          fi
          case "$EPOCH" in *[!0-9]*|'') echo "ERROR: epoch is not an integer: $EPOCH" >&2; exit 1 ;; esac
          echo "source_date_epoch=$EPOCH" >> "$GITHUB_OUTPUT"
          echo "SOURCE_DATE_EPOCH=$EPOCH"

          PORTAL_EVENT=$(fetch_record portal) || {
            echo "ERROR: no portal record for build id $BUILD_ID." >&2
            exit 1
          }
          PORTAL_HASH=$(record_content "$PORTAL_EVENT" | jq -r '.sha256')
          [ -n "$PORTAL_HASH" ] && [ "$PORTAL_HASH" != "null" ] || {
            echo "ERROR: portal record has no sha256" >&2; exit 1; }
          echo "portal: $PORTAL_HASH"
          echo "portal_hash=$PORTAL_HASH" >> "$GITHUB_OUTPUT"
"""

# Inputs both package jobs need: the prebuilt binaries and the portal assets,
# which travel over Blossom because actions/upload-artifact is broken here.
FETCH_ASSETS = """\
      - name: Download prebuilt binaries from Blossom
        env:
          HASHES_JSON: ${{ needs.resolve-inputs.outputs.hashes }}
        run: |
          set -euo pipefail
          BINARIES_HASH=$(printf '%s' "$HASHES_JSON" | jq -r '."@@COMPILE_KEY@@"')
          if [ -z "$BINARIES_HASH" ] || [ "$BINARIES_HASH" = "null" ]; then
            echo "ERROR: no Blossom hash for compile_key=@@COMPILE_KEY@@" >&2
            exit 1
          fi
          DOWNLOADED=false
          for server in $BLOSSOM_SERVERS; do
            if curl -fsSL -m 60 -o binaries.tar.gz "${server}/${BINARIES_HASH}"; then
              DOWNLOADED=true
              echo "Downloaded from ${server}"
              break
            fi
          done
          [ "$DOWNLOADED" = "true" ] || { echo "ERROR: failed to download binaries from any mirror" >&2; exit 1; }
          mkdir -p bin
          tar xzf binaries.tar.gz -C bin
          ls -lh bin/

      - name: Download portal assets from Blossom
        env:
          PORTAL_HASH: ${{ needs.resolve-inputs.outputs.portal_hash }}
        run: |
          set -euo pipefail
          DOWNLOADED=false
          for server in $BLOSSOM_SERVERS; do
            if curl -fsSL -m 60 -o portal-assets.tar.gz "${server}/${PORTAL_HASH}"; then
              DOWNLOADED=true
              echo "Downloaded portal assets from ${server}"
              break
            fi
          done
          [ "$DOWNLOADED" = "true" ] || { echo "ERROR: portal assets unavailable on every mirror" >&2; exit 1; }
          rm -rf @@CHECKOUT@@/packaging/files/tollgate-captive-portal-site
          mkdir -p @@CHECKOUT@@/packaging/files/tollgate-captive-portal-site
          tar xzf portal-assets.tar.gz -C @@CHECKOUT@@/packaging/files/tollgate-captive-portal-site
"""

INSTALL_NAK = """\
      - name: Install nak
        run: |
          curl -fsSL "https://github.com/fiatjaf/nak/releases/download/v0.16.2/nak-v0.16.2-linux-amd64" -o /usr/local/bin/nak
          chmod +x /usr/local/bin/nak
          nak --version
"""

# The upload + per-leg record step. Deliberately does NOT publish kind-1063.
UPLOAD_AND_RECORD = """\
      - name: Upload to Blossom and publish the per-leg build record
        env:
          NSEC_HEX: ${{ secrets.NSEC_HEX }}
          BUILD_ID: ${{ needs.resolve-inputs.outputs.build_id }}
          PACKAGE_VERSION: ${{ needs.resolve-inputs.outputs.package_version }}
          RELEASE_CHANNEL: ${{ needs.resolve-inputs.outputs.release_channel }}
          RELEASE_RUN: ${{ needs.resolve-inputs.outputs.release_run }}
        run: |
          set -euo pipefail
          PKG_FILE="${{ steps.build.outputs.package_path }}"
          [ -n "$PKG_FILE" ] && [ -f "$PKG_FILE" ] || { echo "ERROR: no @@FMT@@ to upload at '$PKG_FILE'" >&2; exit 1; }
          PACKAGE_FILENAME=$(basename "$PKG_FILE")

          PKG_HASH=""; ok=0; OK_SERVERS=""
          for server in $BLOSSOM_SERVERS; do
            attempt=1; delay=2
            while [ $attempt -le 3 ]; do
              resp=$(nak blossom upload --server "$server" --sec "$NSEC_HEX" "$PKG_FILE" < /dev/null 2>&1) || true
              srv_hash=$(printf '%s' "$resp" | jq -r '.sha256 // empty' 2>/dev/null || echo "")
              if [ -n "$srv_hash" ] && [ "$srv_hash" != "null" ]; then
                ok=$((ok + 1)); [ -z "$PKG_HASH" ] && PKG_HASH="$srv_hash"
                OK_SERVERS="$OK_SERVERS $server"
                echo "  ok: ${server} -> ${srv_hash}" >&2
                break
              fi
              echo "  attempt $attempt to $server failed; retrying in ${delay}s..." >&2
              echo "  nak output: $(printf '%s' "$resp" | tail -1)" >&2
              sleep $delay; delay=$((delay * 2)); attempt=$((attempt + 1))
            done
          done
          if [ "$ok" -lt "$BLOSSOM_MIN_SUCCESS" ]; then
            echo "ERROR: only ${ok} mirrors succeeded (need >= ${BLOSSOM_MIN_SUCCESS})" >&2
            exit 1
          fi
          echo "  ${ok}/$(echo $BLOSSOM_SERVERS | wc -w) mirrors ok" >&2

          PKG_URLS_JSON="[]"
          for s in $OK_SERVERS; do
            PKG_URLS_JSON=$(printf '%s' "$PKG_URLS_JSON" | jq -c --arg u "${s}/${PKG_HASH}.@@FMT@@" '. + [$u]')
          done
          compression_tag="${{ matrix.compression }}"
          [ -z "$compression_tag" ] && compression_tag="none"

          # NO kind-1063 here, deliberately. Announcements are published by
          # .ngit/act/workflows/build-package-announce.yml and only once every
          # shard of this (version, channel, release run) has succeeded, so a
          # shard that dies at the ceiling cannot leave a release that looks
          # complete. The record below is what that gate reads: it is written
          # only after the Blossom upload succeeded, and it names the release
          # run, which is what makes a stale record from an earlier run
          # distinguishable from this one.
          COORD_CONTENT=$(jq -cn \\
            --arg sha256 "$PKG_HASH" \\
            --arg filename "$PACKAGE_FILENAME" \\
            --argjson urls "$PKG_URLS_JSON" \\
            --arg arch "${{ matrix.architecture }}" \\
            --arg fmt "@@FMT@@" \\
            --arg compression "$compression_tag" \\
            --arg release_run "$RELEASE_RUN" \\
            --arg shard "$SHARD_ID" \\
            '{sha256:$sha256, filename:$filename, urls:$urls, architecture:$arch,
               format:$fmt, compression:$compression, release_run:$release_run, shard:$shard}')

          out=$(nak event --sec "$NSEC_HEX" -k 30078 \\
            --tag "d=tollgate-build/${BUILD_ID}/${{ matrix.architecture }}/@@FMT@@/${compression_tag}" \\
            --tag "r=${BUILD_ID}" \\
            --tag "t=tollgate-build" \\
            -c "$COORD_CONTENT" \\
            $COORDINATION_RELAYS < /dev/null 2>&1) || true
          echo "  30078: $(printf '%s' "$out" | grep -o '"id":"[0-9a-f]\\{64\\}"' | head -1)"
          echo "Uploaded $PACKAGE_FILENAME -> $PKG_HASH (run=$RELEASE_RUN shard=$SHARD_ID)"
"""

IPK_BUILD = """\
      - name: Install UPX
        if: ${{ matrix.compression != 'none' }}
        run: |
          sudo apt-get update
          sudo apt-get install -y upx-ucl

      - name: Build .ipk
        id: build
        run: |
          set -euo pipefail
          : ${GITHUB_OUTPUT:=/tmp/github_output}
          COMPRESSION_SUFFIX=""
          [ "${{ matrix.compression }}" != "none" ] && COMPRESSION_SUFFIX="-${{ matrix.compression }}"
          PACKAGE_FILENAME=${PACKAGE_NAME}_${{ needs.resolve-inputs.outputs.package_version }}_${{ matrix.architecture }}${COMPRESSION_SUFFIX}.ipk
          echo "package_filename=$PACKAGE_FILENAME" >> "$GITHUB_OUTPUT"

          PAYLOAD=$(mktemp -d)
          install -D -m 0755 bin/tollgate-wrt "$PAYLOAD/usr/bin/tollgate-wrt"
          install -D -m 0755 bin/tollgate     "$PAYLOAD/usr/bin/tollgate"

          install -D -m 0755 packaging/files/etc/init.d/tollgate-wrt                          "$PAYLOAD/etc/init.d/tollgate-wrt"
          install -D -m 0755 packaging/files/etc/uci-defaults/90-tollgate-captive-portal-symlink "$PAYLOAD/etc/uci-defaults/90-tollgate-captive-portal-symlink"
          install -D -m 0755 packaging/files/etc/uci-defaults/99-tollgate-setup               "$PAYLOAD/etc/uci-defaults/99-tollgate-setup"
          install -D -m 0755 packaging/files/usr/local/bin/first-login-setup                  "$PAYLOAD/usr/local/bin/first-login-setup"
          install -D -m 0755 packaging/files/usr/bin/check_package_path                       "$PAYLOAD/usr/bin/check_package_path"
          install -D -m 0644 packaging/files/lib/upgrade/keep.d/tollgate                      "$PAYLOAD/lib/upgrade/keep.d/tollgate"
          install -D -m 0755 packaging/files/etc/hotplug.d/iface/95-tollgate-restart          "$PAYLOAD/etc/hotplug.d/iface/95-tollgate-restart"

          mkdir -p \\
            "$PAYLOAD/etc/tollgate/tollgate-captive-portal-site" \\
            "$PAYLOAD/etc/tollgate/ecash" \\
            "$PAYLOAD/etc/crontabs"
          cp -r packaging/files/tollgate-captive-portal-site/. "$PAYLOAD/etc/tollgate/tollgate-captive-portal-site/"
          install -D -m 0644 LICENSE "$PAYLOAD/usr/share/doc/${PACKAGE_NAME}/LICENSE"

          if [ "${{ matrix.compression }}" != "none" ]; then
            COMP="${{ matrix.compression }}"
            UPX_FLAGS="--${COMP#upx-}"
            ls -lh "$PAYLOAD/usr/bin/tollgate-wrt" "$PAYLOAD/usr/bin/tollgate"
            upx $UPX_FLAGS "$PAYLOAD/usr/bin/tollgate-wrt"
            upx $UPX_FLAGS "$PAYLOAD/usr/bin/tollgate"
            ls -lh "$PAYLOAD/usr/bin/tollgate-wrt" "$PAYLOAD/usr/bin/tollgate"
          fi

          mkdir -p artifacts
          env \\
            PKG_NAME="$PACKAGE_NAME" \\
            PKG_VERSION="${{ needs.resolve-inputs.outputs.package_version }}" \\
            ARCH="${{ matrix.architecture }}" \\
            MAINTAINER="TollGate <tollgate@tollgate.me>" \\
            LICENSE="CC0-1.0" \\
            DEPENDS="libc" \\
            PROVIDES="nodogsplash-files" \\
            REPLACES="nodogsplash, base-files" \\
            DESCRIPTION="TollGate Basic Module for OpenWrt" \\
            packaging/build-ipk.sh "$PAYLOAD" "artifacts/$PACKAGE_FILENAME"
          ls -lh "artifacts/$PACKAGE_FILENAME"
          echo "package_path=artifacts/$PACKAGE_FILENAME" >> "$GITHUB_OUTPUT"
"""

APK_BUILD = """\
      # The digest is read from packaging/build-inputs.json, never hand-copied
      # here, so the image a release is built from is the pinned one even when
      # the plan changes. The GitHub twin injects it into its matrix the same
      # way; the ngit twin cannot (its matrix is static), so it resolves it in
      # a step instead of leaving a floating tag.
      - name: Resolve the pinned SDK image reference
        run: |
          set -euo pipefail
          SDK="${{ matrix.sdk }}"
          DIGEST=$(jq -r --arg sdk "$SDK" \\
            '.openwrt_sdk.targets[$sdk].digest // empty' packaging/build-inputs.json)
          [ -n "$DIGEST" ] || { echo "ERROR: no SDK digest pinned for target $SDK in packaging/build-inputs.json" >&2; exit 1; }
          RELEASE=$(jq -r '.openwrt_sdk.release' packaging/build-inputs.json)
          echo "SDK_REF=openwrt/sdk:${SDK}-${RELEASE}@${DIGEST}" >> "${GITHUB_ENV:-/dev/null}"
          echo "resolved SDK_REF=openwrt/sdk:${SDK}-${RELEASE}@${DIGEST}"

      - name: Stage the SDK package tree
        run: |
          set -eu
          COMPRESSION_SUFFIX=""
          [ "${{ matrix.compression }}" != "none" ] && COMPRESSION_SUFFIX="-${{ matrix.compression }}"
          PACKAGE_FILENAME=${PACKAGE_NAME}_${{ needs.resolve-inputs.outputs.package_version }}_${{ matrix.architecture }}${COMPRESSION_SUFFIX}.apk
          echo "PACKAGE_FILENAME=$PACKAGE_FILENAME" >> "${GITHUB_ENV:-/dev/null}"

          STAGE=stage/${PACKAGE_NAME}
          mkdir -p "$STAGE"
          cp -r src-checkout/packaging/. "$STAGE/"
          cp src-checkout/LICENSE "$STAGE/LICENSE"
          install -m 0755 bin/tollgate-wrt "$STAGE/tollgate-wrt"
          install -m 0755 bin/tollgate     "$STAGE/tollgate"
          ls -la "$STAGE" | head

      # A `container:`/`services:` block is refused by this deployment with
      # `startup_failure` (the operator sets NGIT_CI_ACT_CONTAINER_OPTIONS), so
      # the SDK runs as a plain `docker run` of the DIGEST-PINNED image against
      # the dind sidecar instead. The daemon is a SIBLING, not the job's own
      # filesystem, so the package tree is streamed in with
      # `docker exec -i ... tar xzf -` and the built .apk is streamed out with
      # `docker cp`.
      - name: Build the .apk in the OpenWrt SDK
        id: build
        run: |
          set -euo pipefail
          SVC="ngit-sdk-${{ matrix.architecture }}-$$"
          echo "pulling $SDK_REF"
          docker pull "$SDK_REF"
          docker run -d --name "$SVC" --user root -w / "$SDK_REF" sleep infinity
          cleanup() { docker rm -f "$SVC" >/dev/null 2>&1 || true; }
          trap cleanup EXIT

          tar czf - -C stage . | docker exec -i "$SVC" bash -c \\
            "mkdir -p /builder/package/$PACKAGE_NAME && tar xzf - -C /builder/package/$PACKAGE_NAME"

          USE_UPX=0
          UPX_FLAGS=""
          if [ "${{ matrix.compression }}" != "none" ]; then
            USE_UPX=1
            UPX_FLAGS="${{ matrix.compression }}"
            UPX_FLAGS="${UPX_FLAGS#upx-}"
          fi

          docker exec -e PACKAGE_VERSION="${{ needs.resolve-inputs.outputs.package_version }}" \\
            -e USE_UPX="$USE_UPX" -e UPX_FLAGS="$UPX_FLAGS" \\
            -e PKG_NAME="$PACKAGE_NAME" -e DEBUG="$DEBUG" \\
            "$SVC" bash -euo pipefail -c '
              cd /builder
              make defconfig
              {
                echo "CONFIG_PACKAGE_${PKG_NAME}=y"
                echo "CONFIG_PACKAGE_nodogsplash=y"
                echo "CONFIG_PACKAGE_luci=y"
                echo "CONFIG_PACKAGE_jq=y"
                echo "CONFIG_USE_APK=y"
              } >> .config
              make defconfig
              make -j2 $([ "$DEBUG" = "true" ] && echo V=sc || true) package/${PKG_NAME}/compile
            '

          mkdir -p artifacts
          docker cp "$SVC:/builder/bin/packages" artifacts/ 2>/dev/null || true
          PACKAGE_PATH=$(find artifacts -name '*.apk' -type f | head -n1)
          if [ -z "$PACKAGE_PATH" ]; then
            echo "ERROR: no .apk produced" >&2
            find artifacts -maxdepth 6 -type f | head -50
            exit 1
          fi
          ls -lh "$PACKAGE_PATH"
          cp "$PACKAGE_PATH" "artifacts/${PACKAGE_FILENAME:-package.apk}"
          echo "package_path=artifacts/${PACKAGE_FILENAME:-package.apk}" >> "${GITHUB_OUTPUT:-/tmp/github_output}"
"""

SHARD_COMPLETE = """\
  # The record the release gate reads before it announces anything. It is
  # addressed by the RELEASE RUN, not by the commit, so a re-run of one shard
  # cannot satisfy the gate for a different release run -- the staleness that
  # makes "the record exists" a weak claim is designed out. It is addressed by
  # its `d` tag, so NIP-33 replacement means the newest one wins.
  shard-complete:
    needs: [resolve-inputs, package]
    runs-on: ubuntu-latest
    timeout-minutes: 10
    steps:
      - name: Install nak
        run: |
          curl -fsSL "https://github.com/fiatjaf/nak/releases/download/v0.16.2/nak-v0.16.2-linux-amd64" -o /usr/local/bin/nak
          chmod +x /usr/local/bin/nak

      - name: Publish this shard's completion record
        env:
          NSEC_HEX: ${{ secrets.NSEC_HEX }}
          BUILD_ID: ${{ needs.resolve-inputs.outputs.build_id }}
          PACKAGE_VERSION: ${{ needs.resolve-inputs.outputs.package_version }}
          RELEASE_CHANNEL: ${{ needs.resolve-inputs.outputs.release_channel }}
          RELEASE_RUN: ${{ needs.resolve-inputs.outputs.release_run }}
        run: |
          set -euo pipefail
          CONTENT=$(jq -cn \\
            --arg shard "$SHARD_ID" --arg run "$RELEASE_RUN" --arg build "$BUILD_ID" \\
            --arg version "$PACKAGE_VERSION" --arg channel "$RELEASE_CHANNEL" \\
            --argjson legs @@LEG_COUNT@@ \\
            '{shard:$shard, release_run:$run, build_id:$build, version:$version,
               channel:$channel, legs:$legs, status:"success"}')
          out=$(nak event --sec "$NSEC_HEX" -k 30078 \\
            --tag "d=tollgate-build/${RELEASE_RUN}/shard/${SHARD_ID}" \\
            --tag "r=${BUILD_ID}" \\
            --tag "t=tollgate-build-shard" \\
            -c "$CONTENT" $COORDINATION_RELAYS < /dev/null 2>&1) || true
          echo "shard ${SHARD_ID} complete for release run ${RELEASE_RUN}"
          echo "  30078: $(printf '%s' "$out" | grep -o '"id":"[0-9a-f]\\{64\\}"' | head -1)"
"""


def render_shard(shard: dict, plan: dict) -> str:
    shard_id = shard["id"]
    legs = shard["legs"]
    formats = sorted({leg["format"] for leg in legs})
    if len(formats) != 1:
        raise SystemExit(f"shard {shard_id}: a shard must be single-format, got {formats}")
    fmt = formats[0]

    rows = []
    for leg in legs:
        parts = [
            f"architecture: {leg['architecture']}",
            f"compile_key: {leg['compile_key']}",
            f"format: {leg['format']}",
            f"compression: {leg['compression']}",
        ]
        if "sdk" in leg:
            parts.append(f"sdk: {leg['sdk']}")
        rows.append("          - { " + ", ".join(parts) + " }")
    matrix = "\n".join(rows)

    matrix_doc = "\n".join(
        "#   - " + leg["architecture"] + "  " + leg["format"] + "  " + leg["compression"]
        + ("  (sdk " + leg["sdk"] + ")" if "sdk" in leg else "")
        for leg in legs
    )

    header = (HEADER.replace("@@SHARD_ID@@", shard_id)
                    .replace("@@CEILING@@", str(plan["ceiling_secs"]))
                    .replace("@@MATRIX@@", matrix_doc))
    env = ENV_BLOCK.replace("@@SHARD_ID@@", shard_id)

    timeout = min(25, max(5, int(shard.get("budget_secs", 1500)) // 60))
    checkout = "src-checkout" if fmt == "apk" else "."
    fetch = (FETCH_ASSETS.replace("@@COMPILE_KEY@@", "${{ matrix.compile_key }}")
                       .replace("@@CHECKOUT@@", checkout))

    build = IPK_BUILD if fmt == "ipk" else APK_BUILD
    upload = UPLOAD_AND_RECORD.replace("@@FMT@@", fmt)
    complete = SHARD_COMPLETE.replace("@@LEG_COUNT@@", str(len(legs)))
    complete = complete.replace("@@SHARD_ID@@", shard_id)

    package_job = f"""\
  package:
    needs: [resolve-inputs]
    runs-on: ubuntu-latest
    timeout-minutes: {timeout}
    env:
      # The packaging scripts (packaging/build-ipk.sh and the apk SDK lane)
      # stamp mtimes from SOURCE_DATE_EPOCH (#383); it must be the same
      # epoch the binaries were compiled with (resolve-inputs extracts it
      # from the stage-1 record).
@@EPOCH_ENV@@
    strategy:
      fail-fast: false
      matrix:
        include:
{matrix}
    steps:
      - uses: actions/checkout@v5
        with:
          path: {checkout}

      - name: Install build tools
        run: |
          sudo apt-get update
          sudo apt-get install -y curl jq

{INSTALL_NAK}
{fetch}
{build}
{upload}
"""

    package_job = package_job.replace("@@EPOCH_ENV@@", EPOCH_ENV_LINE)

    return "\n".join([header, "on:\n  workflow_dispatch:\n", env, "jobs:\n",
                      RESOLVE_INPUTS, package_job, complete])


def main() -> int:
    ap = argparse.ArgumentParser(description="render the ngit release shards from the plan")
    ap.add_argument("--plan", default="packaging/ngit-release-matrix.json")
    ap.add_argument("--outdir", default=".ngit/act/workflows")
    ap.add_argument("--check", action="store_true", help="fail if the committed shards differ from the plan")
    ap.add_argument("--list", action="store_true", help="print the shard ids and exit")
    args = ap.parse_args()

    root = Path(os.environ.get("WT") or Path(__file__).resolve().parent.parent)
    plan_path = Path(args.plan) if Path(args.plan).is_absolute() else root / args.plan
    if not plan_path.is_file():
        print(f"ERROR: no shard plan at {plan_path}", file=sys.stderr)
        return 2
    try:
        plan = json.loads(plan_path.read_text())
    except json.JSONDecodeError as exc:
        print(f"ERROR: {plan_path} is not valid JSON: {exc}", file=sys.stderr)
        return 2

    shards = plan.get("shards") or []
    if not shards:
        print(f"ERROR: {plan_path} declares no shards", file=sys.stderr)
        return 2
    ids = [s["id"] for s in shards]
    if len(set(ids)) != len(ids):
        print(f"ERROR: duplicate shard ids in {plan_path}", file=sys.stderr)
        return 2
    if args.list:
        print("\n".join(ids))
        return 0

    outdir = root / args.outdir
    outdir.mkdir(parents=True, exist_ok=True)

    expected = {f"build-package-{s['id']}.yml": render_shard(s, plan) for s in shards}
    stale = []
    for name, content in sorted(expected.items()):
        path = outdir / name
        if args.check:
            if not path.is_file():
                stale.append(f"missing:   {name}")
            elif path.read_text() != content:
                stale.append(f"stale:     {name}")
        else:
            path.write_text(content)
            print(f"wrote {path.relative_to(root)}")

    if args.check:
        # A shard file that is in the tree but not in the plan is the same class
        # of bug as a missing one: the plan stops describing what runs.
        known = set(expected) | {"build-package-binaries.yml", "build-package-announce.yml"}
        for path in sorted(outdir.glob("build-package-*.yml")):
            if path.name not in known:
                stale.append(f"unplanned: {path.name}")
        if stale:
            print("ERROR: the committed shards do not match packaging/ngit-release-matrix.json:", file=sys.stderr)
            for item in stale:
                print(f"  {item}", file=sys.stderr)
            print("  fix: scripts/ngit-gen-shards.py", file=sys.stderr)
            return 1
        print(f"OK: {len(expected)} shard file(s) match the plan ({len(ids)} shard(s))")
    return 0


if __name__ == "__main__":
    sys.exit(main())
