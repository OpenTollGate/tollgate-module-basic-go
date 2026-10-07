# PROGRESS — 99-tollgate-setup entry_ui mapping (branch pr/entry-ui-mapping)

Base: upstream/main 2de7955e. Push target: `fork`. No PR. Go side is out of scope.

## Log (one line per cluster)

- [C0] recon done: spec D1-D4/invariants, 99 map (setup_uhttpd L633, tls_identity L739,
  drop_admin_listeners L1158, sanitize L1023, purge L1062), test harness pattern read.
- [C1] (next) reader `entry_ui_resolved()` + mapping vars + D4 marker gate in 99.

## Scope
- Resolve `entry_ui` (board|luci) from /etc/tollgate/config.json via jq.
  Missing file / key / empty / garbage / absent jq -> `board` (fail toward operator).
- entry pair = 8080 + 443; secondary pair = 8090 + 8443. Port SETS never change.
- board: uhttpd.admin = entry pair; uhttpd.main = secondary (home=/www).
- luci: today's mapping (main = entry, admin = secondary).
- D4 gate: honour board ONLY if marker ${TOLLGATE_ENTRY_UI_MARKER:-/etc/tollgate/entry-ui-mapping}
  exists AND content == resolved value; else repair to luci + EXACTLY ONE WARNING naming feed re-vendor.
- Invariant 3 assertable: exactly one section lists each of 8080/443/8090/8443.
