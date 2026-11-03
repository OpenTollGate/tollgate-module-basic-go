- **A fresh install no longer leaves `uhttpd.main` with no listeners.** The
  install order runs the module's `99-tollgate-setup` before the staged admin
  writer (`999-tollgate-admin-setup`, gate-last per the #838 credential fix),
  but `99`'s entry_ui gate still waited for the mapping marker — whose only
  writer is `999`, *after* it. On a fresh install `99` therefore repaired
  `entry_ui=board` to the legacy `luci` mapping, put LuCI (`uhttpd.main`) on
  the entry pair, and `999`'s strip loop then removed that pair from every
  non-admin section: LuCI ended with no listener at all and procd crash-looped
  the default uhttpd instance. `99` now also treats the staged mode-aware
  `999` at its install location as proof both halves arrived together
  (content-checked, so a pre-switch script still degrades to `luci`), applies
  the board mapping, and records it in `/etc/tollgate/entry-ui-mapping`
  itself — so a fresh install and a reinstall converge with LuCI on
  :8090/:8443 and the board on :8080/:443. A new sequence fence
  (`tests/packaging/entry-ui-install-sequence_test.sh`) runs the real `99`
  then the real pinned `999` in recipe order and is red against the old
  scripts. ([#899](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/899))
