- **A mint whose keyset collides with another mint's can no longer be
  registered — the NUT-13 residue-collision guard is wired.** The
  Conduition cashu disclosure's short-term fix existed in the wallet
  fork as dead code: NUT-13 derives secrets from the keyset ID reduced
  mod 2³¹−1, so two keysets with colliding residues make the wallet
  derive identical preimages for both — and a malicious mint can use an
  honest mint's NUT-09 /restore endpoint as an oracle to steal its
  proofs. gonuts-tollgate v0.14.0 (fork PR #37, tagged with the identical
  tree as v0.13.2) runs the guard inside `SaveKeyset` — the one point
  every keyset writer converges on — refusing cross-mint collisions,
  colliding rotations and exact cross-mint duplicates with both mints
  and IDs named, exempting a keyset's own re-saves, and leaving no
  partial state on refusal; it also verifies that a fetched keyset ID
  derives from the mint's published keys on every fetch path. The bump is
  unified across every carrier by `scripts/bump-gonuts.sh` — including
  `scripts/token-recovery`, the fifth carrier a hand bump missed, now
  riding the fork's counter-discipline fixes instead of predating them.
  Existing wallets are unaffected (the guard fires only on new
  registrations); this repo's fake-mint fixtures were made honest under
  the same rule
  ([#705](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/705))
  ([#781](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/781)).
