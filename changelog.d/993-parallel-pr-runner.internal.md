- **A parallel PR test runner with portal caching.**
  `scripts/pr-verify-parallel.sh` takes PR numbers as explicit dispatch input
  (opt-in, the #881 zero-risk precedent) and verifies them in parallel
  lanes: one worktree per PR, a private tmpfs on /tmp per lane (legacy-suite
  /tmp collisions can no longer cross lanes), the shared warm Go build
  cache, and a portal-bundle cache keyed by the pinned portal commit
  (hydrate instead of the ~90 s build; PRs that retarget the pin get a fresh
  key). A hard trust gate refuses PRs whose diff touches workflow or runner
  surfaces; worktrees are cleaned per lane. Measured on the queue: 376 s/PR
  sequential gate → 470 s for 4 concurrent lanes (~3.2x, ~3.9x modelled at
  8-PR queue scale)
  ([#987](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/993)).
