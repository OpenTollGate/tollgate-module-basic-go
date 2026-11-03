- **The queue's conflict drift is visible in one command.**
  `scripts/pr-conflict-sweep.sh` merge-trees every open PR head against
  `origin/main` (local, no worktrees, writes nothing) and reports the
  conflicting set — first run caught a release blocker that had silently
  flipped conflicting between manual checks. `--markdown` emits a
  comment-ready table; measures exactly what #920 proposes to fix.
  ([#921](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/921)).
