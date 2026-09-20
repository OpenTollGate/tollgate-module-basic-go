#!/usr/bin/env bash
# verify_publication.sh — post-publish gate for the Nostr release channel.
#
# Runs in CI after the publish stage: asserts that the just-published kind-1063
# events actually exist on the relays and that the artifacts are servable
# from enough mirrors with the correct sha256. Turns silent publish failures
# (the v0.6.0-alpha1 incident: tag fired during the Actions outage, zero
# events, zero assets) and mirror rot (v0.5.0: 2 of 3 mirrors 404) into a
# red build.
#
# Usage: scripts/verify_publication.sh <version> <channel> [<expectation-source>]
#   expectation-source:
#     <matrix-json>  the build matrix (`include[]` with .architecture and
#                    .ipk/.apk flags) — the GitHub lane passes the output of
#                    define-package-matrix, so expectations come from what the
#                    build matrix declared, never from whatever happened to get
#                    published.
#     -              expectations come from $VERIFY_EXPECT instead: one
#                    `arch/format` pair per line. The ngit lane uses this,
#                    because ngit-ci does not support a dynamic matrix; its
#                    expectations are extracted from the static matrix in
#                    `.ngit/act/workflows/build-package.yml` by
#                    scripts/ngit-matrix-expectations.sh, so they still come
#                    from the matrix rather than from the relays.
#
# Env:
#   VERIFY_RELAYS      space-separated relay list (default: the 5 channel relays)
#   VERIFY_MIRRORS     mirrors that must serve each verified artifact (default 2)
#   VERIFY_DOWNLOAD    sample (default: first ipk + first apk) | all
#   VERIFY_PUBLISHERS  space-separated publisher pubkeys an announcement may
#                      carry. Default: BOTH release publishers — the historical
#                      GitHub Actions key (5075e61f…) and the ngit / Nostr CI
#                      key (6cfc53c0…), see AGENTS.md "Publisher pubkeys".
#   PUBLISHER_PUBKEY   overrides VERIFY_PUBLISHERS (kept for callers that pin
#                      one key)
#   VERIFY_EXPECT      explicit `arch/format` expectation list (used when the
#                      expectation source is `-`)
#   VERIFY_SCOPE       comma-separated format allowlist applied to the
#                      expectations, default "ipk,apk". Anything excluded is
#                      printed as NOT VERIFIED — a narrowed pass must never
#                      read like a full one.
#
# Exit codes:
#   0  every expectation is announced, and every sampled artifact is fetchable
#      from >= VERIFY_MIRRORS mirrors with the sha256 in its `x` tag
#   1  verification failed; each failure line names the (arch, format) pair,
#      the mirror and, for a mismatch, both prefixes of the digest
#   2  the call itself is unusable (missing argument, malformed expectation,
#      nak/jq absent, nothing left in scope) — never reported as a pass
set -euo pipefail

usage() {
  echo "usage: scripts/verify_publication.sh <version> <channel> [<matrix-json>|-]" >&2
}

VERSION="${1:-}"
CHANNEL="${2:-}"
MATRIX="${3:--}"
if [ -z "$VERSION" ] || [ -z "$CHANNEL" ]; then usage; exit 2; fi

VERIFY_RELAYS="${VERIFY_RELAYS:-wss://relay.damus.io wss://nos.lol wss://nostr.mom wss://relay1.orangesync.tech wss://relay2.orangesync.tech}"
VERIFY_MIRRORS="${VERIFY_MIRRORS:-2}"
VERIFY_DOWNLOAD="${VERIFY_DOWNLOAD:-sample}"
VERIFY_SCOPE="${VERIFY_SCOPE:-ipk,apk}"
VERIFY_EXPECT="${VERIFY_EXPECT:-}"
# Both live release publishers. A consumer (or a gate) that filtered only the
# historical key saw NOTHING published after 2026-08-27, because the ngit
# pipeline publishes under the new dedicated CI key (#410/#418).
VERIFY_PUBLISHERS="${VERIFY_PUBLISHERS:-5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a 6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d}"
PUBLISHER_PUBKEY="${PUBLISHER_PUBKEY:-}"
[ -n "$PUBLISHER_PUBKEY" ] && VERIFY_PUBLISHERS="$PUBLISHER_PUBKEY"

fail=0
note() { printf '%s\n' "$*"; }
bad() { printf 'FAIL: %s\n' "$*" >&2; fail=1; }

command -v nak >/dev/null || { echo "nak not on PATH" >&2; exit 2; }
command -v jq >/dev/null || { echo "jq not on PATH" >&2; exit 2; }
command -v curl >/dev/null || { echo "curl not on PATH" >&2; exit 2; }

note "== verify_publication: $VERSION channel=$CHANNEL mirrors>=$VERIFY_MIRRORS download=$VERIFY_DOWNLOAD scope=$VERIFY_SCOPE"

# --- 0. expectations, and the format scope they are filtered by
if [ "$MATRIX" = "-" ]; then
  [ -n "$VERIFY_EXPECT" ] || {
    echo "ERROR: expectation source '-' needs VERIFY_EXPECT (a list of arch/format pairs)" >&2
    exit 2
  }
  expected=$(printf '%s\n' "$VERIFY_EXPECT" | tr ' ' '\n' | grep -v '^$' | sort -u)
else
  expected=$(printf '%s' "$MATRIX" | jq -r '
    [.include[] | .architecture as $a |
       (if .ipk then "\($a)/ipk" else empty end),
       (if .apk then "\($a)/apk" else empty end)] | unique[]' 2>/dev/null) || {
    echo "ERROR: expectation source is neither '-' nor a build matrix JSON" >&2
    exit 2
  }
fi

malformed=$(printf '%s\n' "$expected" | grep -vE '^[^/]+/(ipk|apk)$' || true)
if [ -n "$malformed" ]; then
  echo "ERROR: malformed expectation(s) — want arch/format with format in {ipk,apk}:" >&2
  printf '%s\n' "$malformed" >&2
  exit 2
fi
[ -n "$(printf '%s\n' "$expected" | grep . || true)" ] || {
  echo "ERROR: the expectation set is empty — refusing to verify nothing" >&2
  exit 2
}

if [ -n "$VERIFY_SCOPE" ]; then
  in_scope=$(printf '%s\n' "$expected" | awk -F/ -v scope=",$VERIFY_SCOPE," 'index(scope, "," $2 ",")')
  excluded=$(printf '%s\n' "$expected" | awk -F/ -v scope=",$VERIFY_SCOPE," '!index(scope, "," $2 ",")')
else
  in_scope="$expected"; excluded=""
fi
if [ -n "$excluded" ]; then
  note "SCOPE: $VERIFY_SCOPE — these expectations are NOT verified by this run:"
  printf '%s\n' "$excluded" | sed 's/^/         /' | while IFS= read -r l; do note "$l"; done
fi
[ -n "$(printf '%s\n' "$in_scope" | grep . || true)" ] || {
  echo "ERROR: scope '$VERIFY_SCOPE' excluded every expectation — nothing to verify" >&2
  exit 2
}

# --- 1. fetch events for the release publishers + package + channel, filter
# version client-side (combined relay-side v/A filters are unreliable on some
# relays; the channel filter relay-side bounds the newest-N window so older
# stable events are not crowded out by high-frequency dev builds)
pub_args=()
for p in $VERIFY_PUBLISHERS; do pub_args+=(-a "$p"); done
# shellcheck disable=SC2086  # VERIFY_RELAYS is an intentional space-separated list
events=$(nak req $VERIFY_RELAYS -l 200 -k 1063 "${pub_args[@]}" \
  --tag n=tollgate-wrt --tag "c=$CHANNEL" 2>/dev/null | grep '^{' || true)
[ -n "$events" ] || { bad "no kind-1063 events from any relay for either release publisher (${VERIFY_PUBLISHERS// /, })"; exit 1; }

scope_filter='.'
[ -n "$VERIFY_SCOPE" ] && scope_filter='select(.format as $f | ($scope | split(",")) | index($f))'
matching=$(printf '%s\n' "$events" | jq -c --arg v "$VERSION" --arg c "$CHANNEL" --arg scope "$VERIFY_SCOPE" '
  select(([.tags[] | select(.[0]=="v" and .[1]==$v)] | length > 0) and
         ([.tags[] | select(.[0]=="c" and .[1]==$c)] | length > 0))
  | {id: .id,
     pubkey: .pubkey,
     arch: [.tags[] | select(.[0]=="A") | .[1]][0],
     format: [.tags[] | select(.[0]=="format") | .[1]][0],
     compression: ([.tags[] | select(.[0]=="compression") | .[1]][0] // "none"),
     x: [.tags[] | select(.[0]=="x") | .[1]][0],
     urls: [.tags[] | select(.[0]=="url") | .[1]]}
  | select(.compression == "none")
  | '"$scope_filter" || true)
[ -n "$matching" ] || {
  bad "zero events match v=$VERSION c=$CHANNEL${VERIFY_SCOPE:+ within scope $VERIFY_SCOPE} (published nowhere — alpha1-class failure)"
  exit 1
}
note "events matching version+channel: $(printf '%s\n' "$matching" | jq -s 'length')"
note "announcing publishers: $(printf '%s\n' "$matching" | jq -r .pubkey | sort -u | tr '\n' ' ')"
unseen=$(printf '%s\n' "$matching" | jq -r .pubkey | sort -u | grep -v '5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a' || true)
if [ -n "$unseen" ]; then
  note "note: these are NOT from the historical GitHub Actions key (5075e61f…) — a"
  note "      consumer filtering on that key alone sees none of them (#418)."
fi

# --- 2. every (arch, format) in scope that the matrix declared must have a
# published compression=none announcement
published=$(printf '%s\n' "$matching" | jq -r '.arch + "/" + .format' | sort -u)
while IFS= read -r exp; do
  [ -n "$exp" ] || continue
  if printf '%s\n' "$published" | grep -qx "$exp"; then
    note "  ok: $exp"
  else
    bad "expected artifact missing from channel: $exp (no kind-1063 with v=$VERSION c=$CHANNEL from either release publisher)"
  fi
done <<EOF
$in_scope
EOF

# --- 3. mirror verification (download + sha256 vs the x tag)
verify_artifact() { # $1 = arch/format label, $2 = event json line
  local label="$1" ev="$2" x urls ok=0 idx=0 url sha size tmpf
  tmpf=$(mktemp /tmp/verify-publication.XXXXXX)
  trap 'rm -f "$tmpf"' RETURN
  x=$(printf '%s' "$ev" | jq -r .x)
  urls=$(printf '%s' "$ev" | jq -r '.urls[]')
  [ -n "$x" ] || { bad "$label: event without x tag: $ev"; return; }
  if [ "$(printf '%s\n' "$urls" | grep -c .)" -lt "$VERIFY_MIRRORS" ]; then
    bad "$label: only $(printf '%s\n' "$urls" | grep -c .) url tag(s) listed (need >= $VERIFY_MIRRORS mirrors)"
  fi
  while IFS= read -r url; do
    [ -n "$url" ] || continue
    [ "$ok" -ge "$VERIFY_MIRRORS" ] && break
    idx=$((idx + 1))
    size=$(curl -fsSL --max-time 120 "$url" -o "$tmpf" 2>/dev/null && wc -c < "$tmpf" | tr -d ' ' || echo 0)
    if [ "$size" = "0" ]; then
      note "  $label: mirror down: $(printf '%s' "$url" | sed 's|.*//||; s|/.*||') ($url)"
      continue
    fi
    sha=$(sha256sum "$tmpf" | awk '{print $1}')
    if [ "$sha" = "$x" ]; then
      ok=$((ok + 1)); note "  $label: mirror ok ($ok/$VERIFY_MIRRORS) $(printf '%s' "$url" | sed 's|.*//||; s|/.*||')"
    else
      bad "$label: sha256 mismatch from $url: got ${sha:0:16}… expected ${x:0:16}…"
    fi
  done <<EOF
$urls
EOF
  [ "$ok" -ge "$VERIFY_MIRRORS" ] || bad "$label: only $ok/$VERIFY_MIRRORS mirrors served a correct copy"
}

to_verify=$(printf '%s\n' "$matching" | jq -c '.')
if [ "$VERIFY_DOWNLOAD" = "all" ]; then
  while IFS= read -r ev; do
    [ -n "$ev" ] || continue
    verify_artifact "$(printf '%s' "$ev" | jq -r '.arch + "/" + .format')" "$ev"
  done <<EOF
$to_verify
EOF
else
  first_ipk=$(printf '%s\n' "$to_verify" | jq -c 'select(.format=="ipk")' | head -1)
  first_apk=$(printf '%s\n' "$to_verify" | jq -c 'select(.format=="apk")' | head -1)
  for ev in $first_ipk $first_apk; do
    [ -n "$ev" ] && [ "$ev" != "null" ] || continue
    label=$(printf '%s' "$ev" | jq -r '.arch + "/" + .format')
    note "verifying (sample): $label"
    verify_artifact "$label" "$ev"
  done
fi

if [ "$fail" = "1" ]; then
  echo "verify_publication: FAIL" >&2
  exit 1
fi
note "verify_publication: PASS"
