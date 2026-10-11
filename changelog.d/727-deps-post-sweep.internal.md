- **The four post-sweep dependabot alerts are cleared.**
  `scripts/token-recovery` bumps `google.golang.org/grpc` to v1.83.1
  (GHSA-2v4p-qf9q-27wj high, GHSA-vp52-pcj8-j9qc high,
  GHSA-qc2q-p7wx-3px3 medium), and the root module's OpenTelemetry
  graph resolves `otlptrace` to v1.47.0 (GHSA-8wmf-6v46-5gfg low) by
  releasing a stale explicit pin
  ([#727](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/727)).
