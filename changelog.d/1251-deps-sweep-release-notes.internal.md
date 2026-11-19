- **Security-relevant dependency sweeps are release-noted.** The #885
  security sweep (grpc x3 advisories + otel family lockstep) and the
  x/crypto chain bumps landed without any changelog entry, so they would
  have been omitted from release notes — operators auditing advisories
  against shipped versions had no paper trail in the changelog. This
  fragment backfills the class: dependency/security changes always carry
  a fragment, per the changelog policy
  ([#1055 tracked in the review-debt audit; fragment carried by [#1251](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1251)))
  ([#885](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/885)).
