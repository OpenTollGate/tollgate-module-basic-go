- **Buying again while a session is still live now tops it up instead of handing
  back the time already spent.** `AddAllotment` grew the allotment of a live
  session *and* rewrote `StartTime`, the anchor its meter counts from. On the
  `milliseconds` metric that re-based the meter to zero: two 600 s purchases on a
  session with 120 s of use left 1200 s running from the second purchase — 1080 s
  of paid time over 1320 s sold, so the customer's second purchase bought its
  allotment *plus* a refund of the first, and the gate timer (`openGateForSession`)
  armed for the same refunded horizon. On `bytes` the fresh anchor was harmless but
  the top-up re-opens the gate, which records a new metering baseline.

  A top-up of a **live** session now only grows the watermark and leaves the anchor
  alone; Reset stays on the "no live session" branch, so a purchase after expiry
  still starts a fresh clock (unchanged, and still pinned by
  `TestRepurchaseAfterExpiryDoesNotResurrectStaleAllotment`). A purchase whose live
  record is on a *different* metric is not a top-up either — the comparison would be
  bytes against a millisecond allotment — so that record is retired and the purchase
  starts a fresh clock on the configured metric.

  The same change collapses "when does this session end" into one definition. The
  comparison was spelled twice (`usage < session.Allotment` in the byte sweep,
  `elapsed >= session.Allotment` in the milliseconds lookup) and the instant a third
  time in the timer; all three now read `sessionWithinWatermark` /
  `sessionPaidThroughUnix`, so the sweep, the lookup and the gate timer cannot
  disagree by a unit.

  No new route: `POST /` **is** the top-up rail once "buy while live" extends
  rather than resets, so it keeps the in-flight duplicate guard, the #502
  receive-intent journal, owed-grant recording and the pre-Receive validations
  instead of re-implementing them on a second money path. The watermark remains
  memory-only (R2 forbids a durable per-address table); a restart still loses paid
  time as before. Durable entitlement, the D5 client identity fork and Spillman
  channels are explicitly out of scope — see
  `docs/architecture/incremental-topup-watermark-decision.md`
  ([#1179](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1179)).
