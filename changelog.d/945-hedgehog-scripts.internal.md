- **Add `src/hedgehog`, the tapscript layer of the Hedgehog two-party channel
  protocol, with every leaf and tree validated against Bitcoin Core.** Hedgehog
  lets a payer create a payment unilaterally and a payee consume it later with no
  interaction — the shape TollGate needs, where the customer may have no WAN but
  the router is a 24/7 service. The operator has accepted the on-chain overhead
  for large or long-lived purchases.

  The package emits BIP-342-correct tapscript (`OP_CHECKSIG` +
  `OP_CHECKSIGADD`, never `OP_CHECKMULTISIG`, which Core refuses in tapscript),
  uses the BIP-341 NUMS internal key so there is no key path, and takes an
  explicit NESTED tree API because a flat list cannot express Core's
  `tr(K,{a,{b,c}})` shape.

  Tests validate output keys against Core's descriptor compiler rather than
  against this package's own arithmetic. That cross-check immediately caught a
  real bug: the 2-of-2 leaf ended with `OP_EQUAL` where Core's `multi_a` builder
  ends with `OP_NUMEQUAL` — a silent commitment change, not a build error.
