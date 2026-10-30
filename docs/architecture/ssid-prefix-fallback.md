# SSID-prefix fallback — configurable, not compiled in

Status: decided 2026-10-30.
Supersedes the compiled `TollGate-` / whitelabel prefix table in
`src/wireless_gateway_manager/brands.go`.

## Decision

The captive-SSID prefix is a **zero-dependency selection hint**, not an
identity. The signed kind-10021 advertisement on `:2121` remains the
authority. The recognition set is:

1. `TollGate` — always, with no file and no brand.
2. the local brand from `/etc/tollgate/brand`.
3. optional extra prefixes from `/etc/tollgate/ssid-prefixes`.

A custom skin adds its own name by writing one plain-text file; it does not
need a PR to this repository.

## Why

The compiled list had to grow with every skin (`TollGate-`, `Net4sats-`,
`Archipelago-`, …). That is a cross-repo coupling for a signal that is only
allowed to select candidates for the signed probe. The shell side already
derived its prefix from `/etc/tollgate/brand`; the Go side was the outlier.

BSSID was considered and rejected in
[`discovery-signaling-decision.md`](discovery-signaling-decision.md): 48 bits
cannot carry a meaningful identifier, it cannot change while the AP runs,
duplicate BSSIDs look like an evil twin, and a derived MAC would make one
sighting reveal an operator’s fleet.

## File format

`/etc/tollgate/ssid-prefixes` — one prefix per line:

```text
# Extra captive-SSID prefixes this router should recognise.
Net4sats
Archipelago
```

- `#` comments and blank lines are ignored.
- The trailing `-` and one leading `!` are optional and stripped.
- Case-insensitive.
- Valid: 3–32 characters of `[A-Za-z0-9_]`. Hyphens are excluded because
  `<prefix>-<code>` uses the hyphen as the separator.
- Invalid lines are ignored; a bad file never breaks discovery.

`TollGate` and the local brand are implicit; the file is only for additional
peers.

## Matching

```text
prefixes = dedupe_fold(["TollGate", brand_token] + file_entries)
hasTollGateSSID(ssid):
    name = strip_one_bang(ssid)
    any(name starts with prefix + "-", case-insensitive)
```

The same rule is implemented by:

- Go: `hasTollGateSSID` in `src/wireless_gateway_manager/brands.go`
- shell: `code_from_name` / `ssid_prefix_known` in
  `packaging/files/etc/uci-defaults/99-tollgate-setup`
- installer: `code_from_name` in the device-identity script

## Compatibility

| Router | Recognises |
|---|---|
| Default TollGate | `TollGate-` |
| `brand=net4sats` | `Net4sats-`, `TollGate-` |
| `brand=archipelago` | `Archipelago-`, `TollGate-` |
| Default TollGate buying from another skin | add that prefix to the file |

The only behaviour change is that a default router no longer recognises every
other skin automatically. That is deliberate: the other skin owns its own
defaults, and an operator who needs the peer adds one line.

## Security

A prefix is spoofable. That is acceptable because it never authenticates:
it only causes one signed `:2121` probe, which fails on a non-TollGate. The
signed advertisement (and its price) remain the authority.

## Tests

- `src/wireless_gateway_manager/brands_test.go`: default, brand file, extra
  file, comments/invalid lines, live file change, case folding, one-`!` rule.
- `tests/contract/ssid-naming-fixtures.txt`: the custom brands are seeded
  through the temp file, proving they are recognised through the configurable
  path rather than a compiled table.
- `tests/uci-defaults-device-code_test.sh`: shell adoption from the brand
  file and the extra-prefix file.
