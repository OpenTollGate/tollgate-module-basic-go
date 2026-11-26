- **LNURLcash notes can be accepted as an opt-in payment method.** When
  `lnurlcash.enabled` is set, the gate accepts a withdraw note, performs the
  LUD-03 redemption against the note's issuer itself, and grants access only
  after **its own settlement mint** confirms the quote it invoiced has been
  paid — the issuer's acceptance is necessary but never sufficient, and a
  `cs1` certificate is never read as payment. The customer needs no internet
  connection; the gate does. Off by default: with the flag absent the endpoint
  does not exist. Issuer custody is an explicit, disclosed trade-off (see
  `docs/architecture/lnurlcash-opt-in-payment-method-decision.md`).
  ([#1268](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1268))
