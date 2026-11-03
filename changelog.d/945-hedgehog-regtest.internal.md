- **Add a regtest end-to-end lifecycle test for Hedgehog, and record its
  evidence.** Behind the `regtest` build tag, it funds a real taproot output,
  drives the whole protocol, and asks bitcoind whether it accepts the result:

  - `OPEN` — the channel output is funded and confirmed (height 196)
  - `PROPOSE` — the **payer produces a cheque with the payee absent**, and the
    test asserts the payer's half **cannot** spend alone
  - `ACCEPT` — the **payee completes it alone**, splitting 99,750,000 / 250,000
  - `CLOSE` — **Bitcoin Core accepts the spend**; broadcast `2d4f7a79…`
  - `CSV` — the relative-lock window expires on a real chain after 7 blocks

  This is the consensus-level evidence for the package. Everything else validates
  against fixtures or btcd's own engine; this one is a real node deciding.
