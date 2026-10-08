- **Mint outages recover in seconds, not poll cycles.** Health transitions
  rode the 5-minute proactive poll: a returned mint needed three poll
  successes (~15 min) to be readmitted, the aggressive recovery loop was
  one-shot per downgrade so an outage that outlived its window never got it
  back, and a mint that had just served a complete payment still counted as
  down until probes agreed. A successful swap now readmits the mint
  immediately (the success-side health event), a probe answering OK for a
  down mint arms the 15-second recovery burst, and the burst re-arms after
  its window expires — all while the steady-state probe rate and every
  Retry-After are honoured, and the recovery threshold still protects
  against flapping when other mints are serving
  ([#777](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/777)).
