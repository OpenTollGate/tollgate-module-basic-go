- **A dead relay can no longer fake a successful CVM announcement.** The
  control-plane announce path drained `PublishMany` with `_ = result` and
  always returned nil, so announcing into an unreachable relay still logged
  "announced 11316/11317" — for a control plane whose liveness is the
  product, a false-success heartbeat. Refusals are now logged per relay,
  and zero acceptance for either announcement is an error the daemon
  reports (the periodic re-announce is the retry); the policy is pinned by
  tests through an injectable publisher seam (#980 F5, found by the #971
  vlab E2E)
  ([#986](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/986)).
