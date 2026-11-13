Fixed
---
`SettlementTargetFor` returned the first accepted mint on both branches, so a
note already sitting at a trusted mint was redirected to a different one whenever
that mint was not first in `accepted_mints` — forcing a melt and paying its fee
for a note that needed none. `IsTrustedMint` was dead code as a result.

Two spelling bugs in the same helper are fixed with it. `normaliseMintURL`
compared URLs by lower-casing the whole string, so
`https://mint.example:443/Bitcoin` and `https://mint.example/Bitcoin` were
reported as different mints (melting a trusted note for nothing), while
`/Bitcoin` and `/bitcoin` were reported as the same one. It now delegates to
`tollwallet.NormalizeMintURL`, which preserves path case (#375: `/Bitcoin` and
`/Liquid` are different units on one mint) and drops default ports. `merchant`
already normalised through `tollwallet` on the lightning path, so this removes an
in-package inconsistency.

The returned target is the operator's config spelling rather than the caller's
raw string, so downstream mint identity stays canonical.

Note: `SettlementTargetFor` and `NeedsSettlement` have no non-test callers yet;
this makes the contract true before the settlement path is wired up.
