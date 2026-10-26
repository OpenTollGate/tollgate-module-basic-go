- **The rebrand gutter's uhttpd section-vocabulary check no longer reads
  comments.** The tokenizer extracted `uhttpd.<name>` tokens from the
  uci-defaults with a bare grep, prose included, so a comment mentioning a
  foreign section (#723: a stale `uhttpd.luci`-style note) tripped "unknown
  uhttpd section" — a false positive against a check whose object is shipped
  config, not documentation. The extractor now drops full-line comments whole
  and strips ` #` onward from mixed lines (whitespace-anchored, so `${v#x}`,
  `${#v}` and URL fragments survive) before extracting tokens, and a new
  planted-fixture control asserts both directions: comment-only names stay
  invisible while a foreign section in active code still fails the check
  ([#734](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/734)).
