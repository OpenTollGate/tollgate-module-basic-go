- **A crash between the mint signing a swap and the wallet saving the proofs
  no longer destroys the received value.** The #719 crash-window lane failed
  on main because only the customer half of the business-transaction record
  ran: the daemon now also replays the wallet's journaled swap intents once
  at boot (gonuts-tollgate #31's pre-persisted intents, re-POSTed verbatim —
  a deterministically signing mint re-issues identical signatures), before
  serving payments. Recovered value lands in the operator wallet; every
  failure shape stays non-fatal and the intents retry on the next boot. The
  gonuts fork is pinned to the merged intent revision pending its release
  tag
  ([#771](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/771)).
