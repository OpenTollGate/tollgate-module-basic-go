- **The five changelog fragments that predated the numeric-name convention
  are renamed and tailed.** `changelog.d/` entries from #827, #828, #704,
  #850 and #907 carried slug-only names (four also lacked PR-link tails)
  — they now follow `<pr-number>-<slug>.<type>.md` so the release fold
  attributes them and the duplicate check can match
  ([#891](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/891)).
