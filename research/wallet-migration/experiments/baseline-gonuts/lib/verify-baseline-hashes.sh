#!/bin/sh
# verify which public values the detect-secrets "Hex High Entropy String"
# findings in the baseline correspond to.
set -eu
check() {
    h=$(printf '%s' "$2" | sha1sum | cut -d' ' -f1)
    if [ "$h" = "$1" ]; then
        echo "MATCH  $1  <- $3"
    else
        echo "no     $1  (sha1=$h for $3)"
    fi
}
# The three values below are PUBLIC: a Nostr event id (the router's published
# kind:10021 advertisement) and the router's merchant pubkey. detect-secrets
# flags their sha1s as high-entropy hex; `pragma: allowlist secret` records the
# audit inline. Verified: this script re-derives the hashes.
check fce0f01da4a74b83df74c39cad959f4877499ff6 "1e5fe653748bf9fd1a137f8aca0aa05a777a96642aa5845e02e1f2e8704f0d12" "advertisement event id (kind 10021)"  # pragma: allowlist secret
check df0a368d6f490ca1926c1eab3193c269bbc5c682 "5cf217341cba4da0595ab8b4bd7faae4f17c9a1c79260128a3924f7d8b4c7e46" "router merchant pubkey (public by design)"  # pragma: allowlist secret
check fb08a102e623f7f59028a237298e4148ce8f20de "d249f192994ab7cae5cb86526c96e0ee23bea2df47f3d04d0e1118691699fe95" "advertisement event id (kind 10021, other run)"  # pragma: allowlist secret
