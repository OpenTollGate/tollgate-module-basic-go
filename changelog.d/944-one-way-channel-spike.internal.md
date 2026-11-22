- **A one-way payment channel cannot serve as a non-custodial bearer
  instrument for this product: spike result, with the reproducible rig that
  proves it.** Four channel constructions were built and executed against
  Bitcoin Core v31.1 consensus, not against a simulator. No variant is both
  non-custodial and payer-offline — the custodian is the payee (`RED.txt` T3:
  Core *accepts* a 100% theft), the payee-up-to-the-ladder-top (60% of a
  5-state ladder extractable after 2 granules served, `GREEN.txt` T9), or the
  mint on the project's own Cashu-Spilman direction. Against the module's
  advertised price of one satoshi per 6.12 hours, on-chain settlement is
  3–4 orders of magnitude too expensive (71,600% overhead at the module's own
  `MinBalance`). The spike also corrects the record on #239 — `ForwardFirstProof`
  has no production caller and `bootstrap.go` performs no mint interaction, so
  the "cold start is partially answered" reading was wrong. Rig, six recorded
  errata and every table are in `research/one-way-channel-spike/`.
  ([#944](https://github.com/felixfelix-bot/tollgate-module-basic-go/pull/944)).
