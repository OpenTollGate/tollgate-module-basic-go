- **The FIPS exit now has a durable admission ledger instead of a flat clients
  file.** `src/fips_exit/admission.go` adds a pure layer over the upstream
  fips-exit clients-file format: byte-exact parse and render (invalid lines are
  skipped and reported, never fatal), a per-peer entitlement record keyed by the
  decoded 32-byte x-only key with `granted_at`, `expires_at`, optional byte
  budget, `bytes_used` and a monotonic revision, deterministic JSON with a
  single atomic write through an injected store seam, and one decision per peer
  — keep, revoke-expired, revoke-exhausted or renew — that yields the full new
  clients-file content plus the ordered list of changed addresses. A peer that
  reached neither its expiry nor its budget is unchanged, so a reload never
  churns the file. Grants refuse a zero, duplicate or malformed key, an empty or
  inverted window, a zero byte budget, and an inner-IPv4 collision between two
  distinct peers (`Derive` carries only 22 identity bits, so collisions are
  reachable). Time-only and byte-budget accounting are separate modes; byte mode
  only ever records counters the caller supplies. Startup reconciliation keeps an
  operator's hand-added line, drops a line whose grant expired, and treats every
  unowned line as needing a grant — so a restart never grants free access. The
  package doc states plainly that wiring to fips-exitd (SIGHUP) and to the
  purchase path is a separate step, and that this layer proves nothing about a
  live exit.
