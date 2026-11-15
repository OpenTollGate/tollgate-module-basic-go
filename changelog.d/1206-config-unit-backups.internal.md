- Isolate config-manager unit-test backups in owned temporary directories,
  exercise truncated-config recovery without privileged skips, and verify
  corrupt config, identity and install bytes are preserved. The deployment
  backup path and runtime defaults are unchanged. ([#1206](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1206)).
