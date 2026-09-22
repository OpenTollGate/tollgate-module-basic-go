# Conformance lane (fast subset)

Go-side implementation of the shared conformance / fault-injection matrix
([#503](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/503)),
twin of [tollgate-module-basic-rust#15](https://github.com/Amperstrand/tollgate-module-basic-rust/issues/15).
The matrix spec, the fault proxy, and the verdict-table format are
**co-owned in PRTA** —
[physical-router-test-automation/tests/conformance](https://github.com/OpenTollGate/physical-router-test-automation/tree/main/tests/conformance) —
and this lane consumes them from there. It never vendors a fork of the
matrix.

## What it drives

The five `fast`-subset scenarios:

| Scenario | Mechanism |
|---|---|
| `duplicate-post-sequential` | same token POSTed twice, sequentially |
| `duplicate-post-concurrent` | same token POSTed twice in parallel |
| `swap-timeout-retry` | the proxy processes the daemon's first swap at the mint but drops the response (`drop_response`) — the ambiguous-outcome window |
| `pay-kill-post-receive-pre-session` | the proxy fires a `notify_on: response` webhook to an unroutable TEST-NET target exactly after the mint processed the swap — the ~5s block holds the swap response open while the runner `docker kill`s the daemon in the #403 window; the runner restarts it and the aftermath phase measures the retry |
| `mint-alias-spellings` | config mint URL (normal spelling) vs token mint URL (case + trailing slash variant); payment must succeed and the wallet must hold exactly one canonical mint entry |

Per-scenario invariants (`no-fund-loss`, `no-double-count`,
`no-output-reuse`, `service-or-refund`, `operator-spendable`,
`retry-safe`, `restart-converges`) are recorded as
`pass` / `fail` / `pending` in `.conformance/verdicts-go.json` using the
PRTA verdict-table format. `pending` means blocked on a tracked issue
(full fund reconciliation and the refund path need the payment-record
store, #502 / #403) — pending is a first-class verdict, not a skip.
The `no-output-reuse` invariant is measured from the proxy's
blinded-message observations: a deterministically derived output sighted
twice on swap/melt routes is the #257/#266/#480 brick class.

## Topology

The PRTA fault proxy runs **as a lab service** (`tg-faultproxy`, added by
`docker-compose.conformance.yml`); the generated
`.conformance/runtime-config.json` points `accepted_mints` at it
(`http://faultproxy:9090`), so every daemon→mint call passes the proxy.
Tokens are minted by cdk-cli through the same proxy URL so the token's
embedded mint URL agrees with the config. NUT-07 spentness checks go
direct to the mint — ground truth must not pass through the thing under
fault. Container→host traffic is deliberately avoided (firewalled on
many hosts): the runner drives the proxy through its published port and
`docker kill`s the daemon itself, inside the ~5s window the notify
webhook's unroutable TEST-NET target holds the swap response open.

## Running

From `tests/cloud-lab/`:

```bash
./conformance/run-conformance.sh         # full lane, teardown at the end
KEEP=1 ./conformance/run-conformance.sh  # leave the lab up for inspection
```

Prerequisites:

- docker (the lane skips cleanly with exit 0 without it);
- a checkout of `physical-router-test-automation` next to this repo (or
  `PRTA_CONFORMANCE_DIR` pointing at its `tests/conformance`) — the lane
  skips cleanly without it;
- `socat` (installed into the tollgate image by this lane's Dockerfile
  change) for the host-side `wallet info` call over the CLI socket.

Isolation: same knobs as the other lanes — `COMPOSE_PROJECT_NAME` and
`CLOUD_LAB_EXTRA_COMPOSE`. The lab pins `172.28.0.0/16`, so only one
cloud lab can run on the host at a time.

## What a red verdict means

A `fail` row is a measured invariant violation on main, with evidence in
`.conformance/evidence/` (fault-proxy observations, daemon logs, the kill
marker, wallet info). That is the lane working as designed: #503 exists
because this class of behavior was asserted, not measured. Expect the
kill and swap-timeout scenarios to be honest about the open #403/#258
window until the payment-record store lands.
