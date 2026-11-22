- **Bonded STA uplinks are no longer dismantled by the single-STA manager.**
  `upstream_wifi.bonded_sta_names` protects the listed wifi-iface sections from
  `disableOtherSTAInterfaces`, `CleanupStaleSTAs`, and `findAvailableSTAInterface`,
  so a two-radio bond can keep both STAs alive
  ([#1261](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1261)).
