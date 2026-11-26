- **Opt-in LNURLcash redemption rail (scaffold, ships off).** A new
  `lnurlcash` config object declares the intent to accept LNURLcash notes beside
  Cashu: `enabled` (default false, never set by an upgrade), an `issuers`
  allowlist of bare canonical hosts, a per-note `max_note_msat` ceiling, and the
  operator's recorded `acknowledgement` of the disclosure version this build
  presents. Every partial state is refused, so a router cannot serve notes
  because a boolean default flipped. The decision record
  (`docs/architecture/lnurlcash-rail-decision.md`) fixes the boundary: the gate
  redeems with the issuer before granting, never grants on a `cs1` certificate
  alone, and keeps durable intent before the redemption leaves the router.
  No endpoint is wired yet; the redemption path, journal binding and grant are
  the follow-up commits on this branch.
  ([#1269](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1269)).
