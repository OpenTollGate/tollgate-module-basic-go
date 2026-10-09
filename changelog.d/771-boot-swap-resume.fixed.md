- **A crash between the mint signing a swap and the wallet saving the proofs
  no longer destroys the received value — when the mint honours NUT-19
  replay within its cache window.** The #719 crash-window lane failed on
  main because only the customer half of the business-transaction record
  ran: the daemon now also replays the wallet's journaled swap intents once
  at boot (gonuts-tollgate #31's pre-persisted intents, re-POSTed verbatim —
  a deterministically signing mint re-issues identical signatures), before
  serving payments. Recovery holds while the re-POST lands inside the
  mint's replay cache (cdk-mintd's default: in-memory, ~60 s TTL); beyond
  that window a mint that already processed the swap refuses with 11001
  already-spent, and the intent stays journaled — visible, not lost —
  pending a NUT-09 /restore fallback (a fork-level follow-up, tracked
  separately). Within the window, recovered value lands in the operator
  wallet; every failure shape stays non-fatal and the intents retry on the
  next boot. The gonuts fork is pinned to the merged intent revision
  pending its release tag
  ([#771](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/771)).
