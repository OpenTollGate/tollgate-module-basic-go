- **New `maintainer_npub` config field** — names the maintainer's Nostr public
  key (bech32 `npub1…`). It is the authorization boundary of the CVM control
  plane and the discovery key for the maintainer dashboard: one subscription on
  the router's announcement finds every tollgate a maintainer owns. Empty means
  "unclaimed" and is legal; a malformed key is refused at write time
  (`config set maintainer_npub <npub>`), because a stored typo becomes a fleet
  no dashboard can find.
