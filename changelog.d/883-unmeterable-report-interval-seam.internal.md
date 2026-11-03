- **The unmeterable report interval is per merchant — the last
  harness-written package global is gone.** The #821 review's sibling-site
  sweep flagged `unmeterableSessionReportInterval` as the final live member
  of the class: a package var the bounded-escalation harness swapped while
  the usage-sweep goroutine read it, race-free only while no test ever
  leaked a sweep. It is now `Merchant.unmeterableReportInterval` with the
  old one-minute default as its zero value (#837's recipe), and the two
  bounded tests set it on their own merchant. With it, the merchant binary
  has zero harness-written package globals left
  ([#883](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/883),
  closes [#858](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/858)).
