## Problem

`config_version` was **not bumped** between v0.4.0 and v0.5.0 despite breaking
changes to the `Config` struct. Both versions report `config_version: "v0.0.7"`.
This means the migration logic in `EnsureDefaultConfig` never fires on upgrade,
and old v0.4.0 configs load as-is into the new struct with **zero-value fields
for newly-added sections**.

## Evidence

### Struct diff (v0.4.0 → v0.5.0 `src/config_manager/config_manager_config.go`)

| Field | v0.4.0 | v0.5.0 | Upgrade impact |
|---|---|---|---|
| `UpstreamWifi UpstreamWifiConfig` | **absent** | **added** (13 int fields) | Old config → all fields zero. `ScanIntervalSeconds=0`, `LostThreshold=0`, `SignalFloor=0`, etc. |
| `Relays []string` | present | **removed** | Old config's `relays` array silently dropped by Go JSON decoder. Safe but data lost. |

### Both versions have the same config_version

```
# v0.4.0
ConfigVersion: "v0.0.7",

# v0.5.0 (current main @ 612860a)
ConfigVersion: "v0.0.7",
```

### Migration logic is version-gated (`config_manager_config.go:315`)

```go
if config.ConfigVersion != defaultConfig.ConfigVersion {
    log.Printf("WARNING: Config version mismatch, backing up and recreating")
    // backup and overwrite with defaults
    return defaultConfig, SaveConfig(filePath, defaultConfig)
}
```

Since both are `v0.0.7`, this branch **never executes** on upgrade.

### Consequence: `UpstreamWifi` gets zero values

The upstream WiFi manager (new in v0.5.0, PR #109) reads `cfg.UpstreamWifi` on
startup. A v0.4.0 config loaded into the v0.5.0 binary produces:

```json
{
  "config_version": "v0.0.7",
  "upstream_wifi": {
    "scan_interval_seconds": 0,
    "fast_check_seconds": 0,
    "lost_threshold": 0,
    "signal_floor": 0,
    ...
  }
}
```

`ScanIntervalSeconds=0` likely causes a tight scan loop or division-by-zero.
`LostThreshold=0` means the manager considers a gateway "lost" on the first
failed check. This is a **silent, unrecoverable breakage** — no error, no log,
just a WiFi manager running with garbage parameters.

## Fix

### Step 1: Bump config_version to v0.0.8

```diff
-       ConfigVersion: "v0.0.7",
+       ConfigVersion: "v0.0.8",
```

This ensures the migration logic fires for any config that was `v0.0.7`.

### Step 2: Add field-level migration (not just version-gated replace)

The current migration path **destroys** the old config and replaces it with
defaults. For an upgrade, we want to **preserve user settings** and only
populate new sections with defaults. Add a migration function:

```go
func migrateConfig(config *Config, defaultConfig *Config) *Config {
    // Populate UpstreamWifi defaults if the section is zero-valued
    // (old config that didn't have this field)
    if config.UpstreamWifi.ScanIntervalSeconds == 0 {
        config.UpstreamWifi = defaultConfig.UpstreamWifi
    }
    // Future migrations chain here
    config.ConfigVersion = defaultConfig.ConfigVersion
    return config
}
```

Call this from `EnsureDefaultConfig` when `config.ConfigVersion !=
defaultConfig.ConfigVersion`, **instead of** the current destructive replace.

### Step 3: Add a test

```go
func TestConfigMigration_v007_to_v008(t *testing.T) {
    oldConfig := Config{
        ConfigVersion: "v0.0.7",
        Metric: "bytes",
        StepSize: 22020096,
        // UpstreamWifi not set → zero value
    }
    migrated := migrateConfig(&oldConfig, NewDefaultConfig())
    assert(migrated.ConfigVersion == "v0.0.8")
    assert(migrated.UpstreamWifi.ScanIntervalSeconds == 300) // default
    assert(migrated.Metric == "bytes") // preserved
}
```

## Acceptance criteria

- [ ] `config_version` bumped to `v0.0.8` in `NewDefaultConfig()`
- [ ] Field-level migration function added (preserve user settings, populate
      new sections with defaults)
- [ ] Test: v0.0.7 config → migrate → v0.0.8 config with preserved fields +
      populated `UpstreamWifi`
- [ ] Test: migration preserves `accepted_mints`, `profit_share`,
      `upstream_detector`, `upstream_session_manager` user settings
- [ ] The destructive "backup and recreate" path is now only for truly
      corrupt configs, not version upgrades

## Related

- Upgrade risk register: (link TBD)
- PR #109 — added `UpstreamWifi` section
- PR #81 — removed relay module (`Relays` field)
- Release plan #154 — "Upgrade from v0.4.0 — HIGH" risk line
