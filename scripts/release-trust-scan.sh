#!/usr/bin/env bash
# release-trust-scan.sh — the fake-release detector for the Nostr channel.
#
# Where verify_publication.sh GATES a known release (it checks that the
# events a version SHOULD have exist, are signed by trusted publishers,
# and serve matching bytes), this tool DISCOVERS: it scans every kind-1063
# announcement for the package across the channel relays, classifies each
# publisher against a trust set, and flags the two shapes that matter:
#
#   UNTRUSTED  an announcement from a key outside the trust set — anyone
#              can publish a 1063; the bytes only matter once you decide
#              to trust the signer. This is the "fake release" class,
#              including the ones WE deliberately publish as drills
#              (docs/release-trust-model.md): a drill fake must land
#              here, visibly, or the detector is broken.
#   CONFLICT   the same (version, arch, format) announced with DIFFERENT
#              sha256 digests — the highest-signal forgery indicator,
#              because it fires even when a trusted key is compromised:
#              two trusted keys (or one key twice) claiming different
#              bytes for one artifact is never legitimate.
#
# Trust is caller-scoped by design: TRUSTED_PUBKEYS defaults to the two
# release publishers (AGENTS.md), but a consumer who only trusts their
# own republish key sets exactly that. The scan never downloads; byte
# verification of a chosen artifact stays with verify_publication.sh.
#
# Usage: scripts/release-trust-scan.sh [<version-filter>]
#   version-filter  optional prefix match on the v tag (e.g. v0.6.0)
#
# Env:
#   SCAN_PACKAGE     package name tag (default tollgate-wrt)
#   TRUSTED_PUBKEYS  space-separated trusted publisher hex keys
#   SCAN_RELAYS      space-separated relay list
#
# Exit codes:
#   0  scan complete, no conflicts (untrusted announcements are REPORTED,
#      not failed — a drill fake must not break CI; consumers act on the
#      report, operators can grep it)
#   1  CONFLICT found — different digests for one (version, arch, format).
#      That is never legitimate and is always actionable.
#   2  unusable call (nak/jq missing, no events at all)
set -euo pipefail

SCAN_PACKAGE="${SCAN_PACKAGE:-tollgate-wrt}"
TRUSTED_PUBKEYS="${TRUSTED_PUBKEYS:-5075e61f0b048148b60105c1dd72bbeae1957336ae5824087e52efa374f8416a 6cfc53c04bda7d58dd4dd0471d66f6a4ea7d3e123e78006e0e0c1abc1208ac0d}"
SCAN_RELAYS="${SCAN_RELAYS:-wss://relay.damus.io wss://nos.lol wss://nostr.mom wss://relay1.orangesync.tech wss://relay2.orangesync.tech}"
FILTER="${1:-}"

for bin in nak jq; do
  command -v "$bin" >/dev/null 2>&1 || { echo "release-trust-scan: $bin not on PATH" >&2; exit 2; }
done

# One line per event: the fields the classifier needs, tab-separated.
# Limit is deliberately high: the point is total discovery, and kind-1063
# volume for one package is small.
EVENTS="$(nak req -k 1063 --tag "n=$SCAN_PACKAGE" --limit 500 $SCAN_RELAYS 2>/dev/null | \
  jq -r 'select(.kind==1063) |
    [.id, .pubkey,
     ([.tags[] | select(.[0]=="v") | .[1]][0] // "?"),
     ([.tags[] | select(.[0]=="c") | .[1]][0] // "?"),
     ([.tags[] | select(.[0]=="A") | .[1]][0] // "?"),
     ([.tags[] | select(.[0]=="format") | .[1]][0] // "?"),
     ([.tags[] | select(.[0]=="x") | .[1]][0] // "?")
    ] | @tsv' | sort -u)"

[ -n "$EVENTS" ] || { echo "release-trust-scan: no kind-1063 events for $SCAN_PACKAGE on $(echo $SCAN_RELAYS | wc -w) relays" >&2; exit 2; }

printf '%-22s %-4s %-26s %-5s %-9s %-7s %s\n' VERSION CH ARCH FMT KEY DIGEST
printf '%s\n' "----------------------------------------------------------------------------------------"

trusted_count=0 untrusted_count=0 conflict=0
declare -A SEEN_DIGESTS

while IFS=$'\t' read -r id pubkey v c A fmt x; do
  [ -n "$FILTER" ] && case "$v" in "$FILTER"*) ;; *) continue;; esac
  keyclass="UNTRUSTED"
  for t in $TRUSTED_PUBKEYS; do
    [ "$pubkey" = "$t" ] && keyclass="trusted"
  done
  if [ "$keyclass" = "trusted" ]; then trusted_count=$((trusted_count+1)); else untrusted_count=$((untrusted_count+1)); fi

  # Conflict detection: same (v, A, fmt) must never carry two digests.
  # Actionable on immutable version channels (stable/beta/alpha/rc): a
  # tag is never legitimately re-announced with different bytes. The dev
  # channel's mutable version strings (branch.height.sha) get rebuilt on
  # force-pushes, so a dev conflict is reported as noise, never gated.
  group="$v|$A|$fmt"
  prev="${SEEN_DIGESTS[$group]:-}"
  if [ -n "$prev" ] && [ "$prev" != "$x" ]; then
    marker="CONFLICT"
    case "$c" in
      dev) marker="conflict(dev-rebuild-noise)";;
      *) conflict=1;;
    esac
    printf '%-22s %-4s %-26s %-5s %-9s %-7s %s  <-- %s: also %s\n' "$v" "$c" "$A" "$fmt" "$keyclass" "${pubkey:0:7}" "${x:0:15}" "$marker" "${prev:0:15}"
  else
    SEEN_DIGESTS[$group]="$x"
    printf '%-22s %-4s %-26s %-5s %-9s %-7s %s\n' "$v" "$c" "$A" "$fmt" "$keyclass" "${pubkey:0:7}" "${x:0:15}"
  fi
done <<< "$EVENTS"

printf '%s\n' "----------------------------------------------------------------------------------------"
echo "release-trust-scan: $trusted_count trusted, $untrusted_count untrusted announcement(s) — untrusted entries are the fake-release class (see docs/release-trust-model.md); act on your own trust set"
if [ "$conflict" = 1 ]; then
  echo "release-trust-scan: CONFLICT — one (version, arch, format) carries different digests; that is never legitimate" >&2
  exit 1
fi
exit 0
