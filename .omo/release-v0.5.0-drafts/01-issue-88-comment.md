<!--
Target: Comment on https://github.com/OpenTollGate/tollgate-module-basic-go/issues/88
Title idea: "Cross-ref with tag-readiness #169 and PR #170 — what's verified, what isn't"
-->

## What's actually verified on hardware at `04ae54e`

Cross-referencing the full tag-readiness report (linked in #169):
<https://github.com/OpenTollGate/physical-router-test-automation/blob/feat/tag-readiness-suite/docs/tag-readiness-reports/TEST-REPORT-main-04ae54e.md>

The **funded autopay** test passed on two GL-MT3000 routers on OpenWrt 24.10.4:

> alpha funded (1137 sats @ nofee), selected pricing (`required_amount=5`),
> sent Cashu to beta, beta authenticated alpha, `network_ok=true`,
> `ping 1.1.1.1` 0% loss through beta.

So the **gate-open half** of the router-to-router autopay flow is verified on
hardware. PR #170 then hardens the first-attempt flakiness the same report
flagged:

> the autopay sometimes fails its first `open gate` attempt
> (`payment rejected: failed to open gate: exit status 1`) then recovers via
> the token-recovery path within ~60–90s.

## What is NOT verified on hardware

This issue (#88) is specifically about the **bandwidth-metered close cycle**:
ndsctl reports 0 usage forever for paid MACs that were never seen on the
upstream's LAN, so the session never closes when the allotment is reached.

The tag-readiness funded-autopay test only proves the gate **opens** and
**internet flows**. It does **not**:

1. Generate sustained LAN traffic from the paid MAC through the upstream.
2. Assert `ndsctl json <mac>` reports non-zero `downloaded`/`uploaded` for the
   paid MAC on the upstream.
3. Assert the session **closes** when the byte allotment is reached.

So #88 remains **unverified** on hardware. The combination of PR #170 (first-
attempt fix) and the verified funded-autopay-open is necessary but **not
sufficient** to close #88.

## Proposal

Three coordinated actions:

1. **Land PR #170** — first-attempt reliability; orthogonal to #88 but
   necessary for any two-router test to be reliable.
2. **Add a new hardware test** on `physical-router-test-automation` that proves
   the bandwidth-metered close-cycle works (or surfaces the bug). Drafted in
   a companion issue there — see "Related" below.
3. **Decide on the deeper fix path** — the three options in the issue body
   (proactive `ndsctl auth`, decouple from nodogsplash, hybrid) remain on the
   table. The hardware test result should drive the decision: if the close-
   cycle works after #170, #88 is effectively resolved by #170 + the existing
   `TEMPORARY WORKAROUND` in `upstream_session_manager/tollgate_prober.go`. If
   it doesn't, we need one of the deeper fixes.

## Related

- Companion test issue (drafted, link TBD): bandwidth-metered reseller
  close-cycle on hardware.
- PR #170: fix(valve): retry ndsctl auth — first-attempt flakiness.
- Tag-readiness report: see #169 for link.
