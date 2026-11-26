- **Opt-in LNURLcash redemption rail (ships off).** A new `lnurlcash` config
  object declares the intent to accept LNURLcash notes beside Cashu: `enabled`
  (default false, never set by an upgrade), an `issuers` allowlist of bare
  canonical hosts, a per-note `max_note_msat` ceiling, and the operator's
  recorded `acknowledgement` of the disclosure version this build presents.
  Every partial state is refused, so a router cannot serve notes because a
  boolean default flipped. When enabled, `POST /lnurlcash/redeem` redeems the
  note at its issuer and the grant comes from the settled quote — never from a
  `cs1` certificate, which is an input only. Durable intent is journalled before
  the note leaves the router, so a crash or an ambiguous melt keeps the note
  reserved rather than allowing a second melt. The route is registered only when
  opted in and carries its own rate limit.
  ([#1269](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1269)).
