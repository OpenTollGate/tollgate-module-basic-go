- **The `entry_ui` port tables are CI-owned.** The mapping was declared
  twice — the shell helpers in `99-tollgate-setup`
  (`uhttpd_main_*`/`board_*`) and Go's `uiPortPair` behind `tollgate ui
  links` — and nothing let either side see the other, so a port change on
  one silently diverged the board SPA's cross-links from the listeners the
  setup actually writes. `tests/contract/check-entry-ui-ports.sh` executes
  both tables (the real helpers sourced out of the setup script; `uiPortPair`
  through a throwaway `go test`) and fails the build on any cell
  disagreement, with the D2 port sets and mapping orientation pinned so a
  drift that moves both tables together is caught too. Wired into the
  `build-purity` CI job and the pre-commit hook
  ([#765](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/765)).
