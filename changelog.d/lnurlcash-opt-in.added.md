- **LNURLcash notes can be accepted as an opt-in payment method.** When
  `lnurlcash.enabled` is set, the gate accepts a withdraw note, performs the
  LUD-03 redemption against the note's issuer itself, and grants access only
  after the issuer has paid an invoice **we** supplied — the customer needs no
  internet connection, but the gate does. Off by default: with the flag absent
  the endpoint does not exist. Issuer custody is an explicit, disclosed
  trade-off (see `docs/architecture/lnurlcash-opt-in-payment-method-decision.md`).
