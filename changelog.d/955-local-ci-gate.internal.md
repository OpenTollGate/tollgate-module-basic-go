- **Local CI gate for PRs.** `scripts/pr-verify.sh` checks a PR (or git ref)
  out into a throwaway worktree at a pinned SHA, runs the offline per-PR set —
  hygiene, contract fences, packaging suites sequential and concurrent,
  uci-defaults shell suites, go-battery — and posts the results table as a PR
  comment via gh, closing the verification gap left by the decommissioned
  GitHub Actions lane and the mirror-push-only ngit lane
  ([#955](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/955)).
