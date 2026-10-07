- **The business-transaction record exists: an ambiguous or interrupted payment
  converges to service or to spendable value, never to silence (#502, the two
  red conformance rows).** Every money-moving `Receive` is now journalled
  durably **before** the money moves — `receive-intents.json`, keyed by the
  customer-quotable reference, carrying the serialized token (root-only 0600,
  like the drain journal) — and resolves only on evidence: the late answer
  itself when the mint finally replies, or NUT-07 checkstate (a new
  `WalletPort.CheckTokenSpent`: any-spent means the atomic swap happened;
  all-unspent means it never did; PENDING is an error, never a guess) when the
  process died or the answer is gone. A spent proof-set records the owed
  entitlement and the existing owed-grant machinery delivers exactly one
  session — across restarts (pending intents reconcile at boot), across
  resubmissions (the customer's retry triggers a synchronous reconciliation
  and answers `payment-received-grant-pending` without a second spend), and
  across ambiguity. An all-unspent answer abandons the attempt: the note
  stays spendable and a resubmission proceeds fresh. A definitive mint
  refusal abandons immediately. Pinned by the journal suite:
  durable-before-the-money-moves, killed-attempt restart convergence (exactly
  one grant, zero second Receives), resubmission-without-second-spend, and
  unspent-abandon-frees-fresh-payment; plus the checkstate semantics trio at
  the wallet layer.
