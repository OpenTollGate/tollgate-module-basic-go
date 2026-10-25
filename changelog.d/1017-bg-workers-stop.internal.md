- **A shut-down merchant stops its payout routine and its lightning-quote
  janitor.** Both were `for range ticker.C` loops that nothing could ever
  stop: the payout routine ran one goroutine per accepted mint and the janitor
  one more, and a runtime downgrade left them all ticking for the life of the
  process — moving wallet money and holding merchant state on a merchant
  nobody owned. In the test binary they outlived the suite (leak census
  2026-10-20: 21 payout + 3 janitor goroutines live at exit on `main`, 7 + 1
  even with #887's sweep fix). `Shutdown` now retires the sweep, the payout
  routine and the janitor — each waiting out its in-flight pass — BEFORE the
  wallet closes, the test drains cover all three workers, and the suite's
  leak guard fails any run that leaks one of them. The mint health tracker is
  deliberately untouched: it is shared with the degraded merchant that
  survives a downgrade, and its proactive checks are what upgrades back
  (#733 follow-up).
