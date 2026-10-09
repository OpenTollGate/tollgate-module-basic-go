- **A crashed swap's value is recovered automatically on the next wallet
  load.** A swap that died between the mint's acceptance and the proof save
  used to lose the mint-issued outputs for good — the secrets existed only
  in memory (#497). Every swap now persists its intent before the
  irreversible POST (gonuts v0.13.1), and wallet load replays pending
  intents in the background — off the boot critical path, keeping failed
  replays recorded for the next load, never re-spending
  ([#793](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/793)).
