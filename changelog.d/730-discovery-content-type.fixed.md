- **The discovery advertisement answers as JSON.** `GET /` on the payment API
  (:2121) served the kind-10021 advertisement without a `Content-Type`, so Go
  sniffed the body and answered `text/plain; charset=utf-8` — reseller clients
  that negotiate on content type (r2r upstream session managers, and this
  repo's own prober) logged a warning on every probe cycle. The header is now
  exactly `application/json`, with no charset suffix: the prober compares the
  header as a whole string, so a suffix would have kept the warning alive
  ([#730](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/730)).
