#!/usr/bin/env bash
# verify_publication.sh — post-publish gate for the Nostr release channel.
#
# Runs in CI after publish-metadata: asserts that the just-published kind-1063
# events actually exist on the relays and that the artifacts are servable
# from enough mirrors with the correct sha256. Turns silent publish failures
# (the v0.6.0-alpha1 incident: tag fired during the Actions outage, zero
# events, zero assets) and mirror rot (v0.5.0: 2 of 3 mirrors 404) into a
# red build.
#
# Usage: scripts/verify_publication.sh <version> <channel> <matrix-json>
#   matrix-json: the define-package-matrix `matrix` output (include[] with
#   .architecture and .ipk/.apk flags) — expectations come from the build
#   matrix, never from whatever happened to get published.
#
# Env:
#   VERIFY_RELAYS     space-separated relay list (default: the 5 channel relays)
#   VERIFY_MIRRORS    mirrors that must serve each verified artifact (default 2)
#   VERIFY_DOWNLOAD   sample (default: first ipk + first apk) | all
#   PUBLISHER_PUBKEY  hex pubkey of the CI publisher
set -euo pipefail

VERSION="${1:?usage: verify_publication.sh <version> <channel> <matrix-json>}"
CHANNEL="${2:?missing channel}"
MATRIX="${3:?missing matrix-json}"

VERIFY_RELAYS="${VERIFY_RELAYS:-wss://relay.damus.io wss://nos.lol wss://nostr.mom wss://relay1.orangesync.tech wss://relay2.orangesync.tech}"
VERIFY_MIRRORS="${VERIFY_MIRRORS:-2}"
VERIFY_DOWNLOAD="${VERIFY_DOWNLOAD:-sample}"
PUBLISHER_PUBKEY="${PUBLISHER_PUBKEY:-5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a}"

fail=0
note() { printf '%s\n' "$*"; }
bad() { printf 'FAIL: %s\n' "$*" >&2; fail=1; }

command -v nak >/dev/null || { echo "nak not on PATH" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq not on PATH" >&2; exit 2; }

note "== verify_publication: $VERSION channel=$CHANNEL mirrors>=$VERIFY_MIRRORS download=$VERIFY_DOWNLOAD"

# --- 1. fetch events for this publisher + package + channel, filter version
# client-side (combined relay-side v/A filters are unreliable on some relays;
# the channel filter relay-side bounds the newest-N window so older stable
# events are not crowded out by high-frequency dev builds)
# shellcheck disable=SC2086  # VERIFY_RELAYS is an intentional space-separated list
events=$(nak req $VERIFY_RELAYS -l 200 -k 1063 -a "$PUBLISHER_PUBKEY" \
  --tag n=tollgate-wrt --tag "c=$CHANNEL" 2>/dev/null | grep '^{' || true)
[ -n "$events" ] || { bad "no kind-1063 events from any relay for the publisher"; exit 1; }

matching=$(printf '%s\n' "$events" | jq -c '
  select(([.tags[] | select(.[0]=="v" and .[1]==$v)] | length > 0) and
         ([.tags[] | select(.[0]=="c" and .[1]==$c)] | length > 0))
  | {id: .id,
     arch: [.tags[] | select(.[0]=="A") | .[1]][0],
     format: [.tags[] | select(.[0]=="format") | .[1]][0],
     compression: ([.tags[] | select(.[0]=="compression") | .[1]][0] // "none"),
     x: [.tags[] | select(.[0]=="x") | .[1]][0],
     urls: [.tags[] | select(.[0]=="url") | .[1]]}
  | select(.compression == "none")' \
  --arg v "$VERSION" --arg c "$CHANNEL" || true)
[ -n "$matching" ] || { bad "zero events match v=$VERSION c=$CHANNEL (published nowhere — alpha1-class failure)"; exit 1; }
note "events matching version+channel: $(printf '%s\n' "$matching" | jq -s 'length')"

# --- 2. expectations from the build matrix: every (arch, format) pair the
# matrix built must have a published compression=none event
expected=$(printf '%s' "$MATRIX" | jq -r '
  [.include[] | .architecture as $a |
     (if .ipk then "\($a)/ipk" else empty end),
     (if .apk then "\($a)/apk" else empty end)] | unique[]')
published=$(printf '%s\n' "$matching" | jq -r '.arch + "/" + .format' | sort -u)
while IFS= read -r exp; do
  if printf '%s\n' "$published" | grep -qx "$exp"; then
    note "  ok: $exp"
  else
    bad "expected artifact missing from channel: $exp"
  fi
done <<EOF
$expected
EOF

# --- 3. mirror verification (download + sha256 vs the x tag)
verify_artifact() { # $1 = event json line
  local ev="$1" x urls ok=0 idx=0 url sha size tmpf
  tmpf=$(mktemp /tmp/verify-publication.XXXXXX)
  trap 'rm -f "$tmpf"' RETURN
  x=$(printf '%s' "$ev" | jq -r .x)
  urls=$(printf '%s' "$ev" | jq -r '.urls[]')
  [ -n "$x" ] || { bad "event without x tag: $ev"; return; }
  [ "$(printf '%s\n' "$urls" | wc -l)" -ge "$VERIFY_MIRRORS" ] \
    || bad "fewer than $VERIFY_MIRRORS url tags listed"
  while IFS= read -r url; do
    [ "$ok" -ge "$VERIFY_MIRRORS" ] && break
    idx=$((idx + 1))
    size=$(curl -fsSL --max-time 120 "$url" -o "$tmpf" 2>/dev/null && wc -c < "$tmpf" | tr -d ' ' || echo 0)
    if [ "$size" = "0" ]; then
      note "  mirror down: $(printf '%s' "$url" | sed 's|.*//||; s|/.*||')"
      continue
    fi
    sha=$(sha256sum "$tmpf" | awk '{print $1}')
    if [ "$sha" = "$x" ]; then
      ok=$((ok + 1)); note "  mirror ok ($ok/$VERIFY_MIRRORS)"
    else
      bad "sha256 mismatch from $url: got ${sha:0:16}… expected ${x:0:16}…"
    fi
  done <<EOF
$urls
EOF
  [ "$ok" -ge "$VERIFY_MIRRORS" ] || bad "only $ok/$VERIFY_MIRRORS mirrors served a correct copy"
}

to_verify=$(printf '%s\n' "$matching" | jq -c '.')
if [ "$VERIFY_DOWNLOAD" = "all" ]; then
  while IFS= read -r ev; do verify_artifact "$ev"; done <<EOF
$to_verify
EOF
else
  first_ipk=$(printf '%s\n' "$to_verify" | jq -c 'select(.format=="ipk")' | head -1)
  first_apk=$(printf '%s\n' "$to_verify" | jq -c 'select(.format=="apk")' | head -1)
  for ev in $first_ipk $first_apk; do
    [ -n "$ev" ] && [ "$ev" != "null" ] || continue
    note "verifying (sample): $(printf '%s' "$ev" | jq -r '.arch + "/" + .format')"
    verify_artifact "$ev"
  done
fi

if [ "$fail" = "1" ]; then
  echo "verify_publication: FAIL" >&2
  exit 1
fi
note "verify_publication: PASS"
