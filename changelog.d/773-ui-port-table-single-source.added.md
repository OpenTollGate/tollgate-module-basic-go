- **The entry_ui port table has one source: the binary.** `tollgate ui ports
  --format shell` emits the admin-UI port table as shell-evaluable variables,
  and `99-tollgate-setup` evaluates that output instead of carrying its own
  copy — the duplication that let the shell and Go tables drift (#746) is
  gone, along with any fallback that could re-open it: a missing or malformed
  emission fails the install loudly and retries on the next boot
  ([#773](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/773)).
