- **The admin-UI URL contract is documented in `docs/access-urls.md`.** The new
  doc states the canonical access paths (`http://tollgate.lan:8090/` →
  `https://tollgate.lan:8443/` for the config UI board, `http://tollgate.lan:8080/`
  → `https://tollgate.lan/` for LuCI), explains the single-port-owner `uhttpd`
  design that makes `https://<host>:8090` and `https://<host>:8080` impossible,
  and notes that guests on the open SSID are intentionally dropped on all four
  admin ports. The release-candidate tester guide (`docs/rc-tester-guide.md`) now
  references the contract and no longer presents LuCI's URL as the primary
  "admin UI" entry point
  ([#698](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/698)).
