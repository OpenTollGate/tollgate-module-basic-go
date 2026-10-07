- **The crash-injection lane accepts the #502/#700 recovery vocabulary.** The
  recovery assertion grepped only the wallet layer's `pending swap/op …
  recovered`, so the merchant layer's `Owed grant applied` — the line the
  receive-intent resume actually emits when NUT-07 proves the proofs SPENT and
  the owed session lands — read as "value destroyed" and the lane failed a
  healthy main (#719, reproduced with a live post-recovery payment returning
  200). Both vocabularies now pass the assertion.
