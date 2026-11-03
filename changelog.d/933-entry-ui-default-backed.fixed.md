- **`entry_ui` now records the mapping the deployment can actually back.** An
  install without the captive-portal feed (the ipk-only lane) converges the
  legacy port table in `99-tollgate-setup`, because the D4 marker that gates
  the board mapping cannot exist — its writer is not installed. The daemon
  then wrote the declared default, `entry_ui=board`, into `config.json`
  **after** that convergence, so config claimed `board` while nothing
  listened on `:8090`/`:8443`: a config/reality divergence by construction,
  the state the PRTA `test_entry_ui_port_mapping_contract` soak check flags
  (#928). Every path that writes an entry_ui the operator never chose — the
  fresh-install write, the invalid-JSON rewrite, the migration's empty-field
  fill — now records `board` only when the marker announces the board
  mapping, and the backed legacy `luci` mapping otherwise (one WARNING naming
  the feed re-vendor). The declared default stays `board` (fully-vendored
  installs are unchanged), values you set yourself are never rewritten, and a
  feed-less box now answers `jq -r .entry_ui /etc/tollgate/config.json` with
  `luci` from the first boot — agreeing with its own listeners. The marker
  path's shell/Go declarations are fenced by the new
  `tests/contract/check-entry-ui-marker-path.sh` CI check.
  ([#933](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/933))
