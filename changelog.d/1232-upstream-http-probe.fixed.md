- **Upstream Internet probes recognize each endpoint's success response.**
  The Firefox fallback accepts its exact success body when the primary endpoint
  is unavailable. Captive redirects and incomplete HTTP responses remain
  unsuccessful instead of falling through to a successful endpoint
  ([#1232](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1232)).
