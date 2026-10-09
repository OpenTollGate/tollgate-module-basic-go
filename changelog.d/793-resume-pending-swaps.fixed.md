- **A crash between the mint accepting a swap and the wallet saving the
  proofs no longer destroys the received value — when the mint honours
  NUT-19 replay within its cache window.** Every swap now journals its
  intent (exact request bytes, secrets, blinding factors) atomically with
  the counter reservation before the irreversible POST (gonuts v0.13.1),
  and every wallet load replays pending intents in the background — off
  the boot critical path, never re-spending, keeping failed replays
  recorded for the next load. The boundary: a mint returns the identical
  swap signatures only from its NUT-19 response cache (cdk-mintd default:
  in-memory, ~60 s TTL); beyond the window a mint that already processed
  the swap refuses with 11001 already-spent, and the intent stays
  journaled — visible, not lost — pending a NUT-09 /restore fallback
  (fork-level follow-up, tracked separately). Operators reconciling boot
  logs: the boot "Wallet Balance" line excludes recovered value; the
  recovery pass logs its own recovered total
  ([#793](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/793)).
