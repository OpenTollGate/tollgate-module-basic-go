- **The offline setup suites no longer share one log file.**
  `99-tollgate-setup` pinned its log path unconditionally, so every test
  suite that runs the shipped script off-router was funnelled into the
  same `/tmp/tollgate-setup.log`: concurrent runs (parallel lanes, two
  worktrees) truncated and grepped one file, and the packaging-suites
  CI lane intermittently went red on main with a passing log tail. The
  log path is now overridable — router behaviour is unchanged — and a
  guard in the credential suite fails by name if the assignment ever
  becomes unconditional again.
  ([#948](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/948))
