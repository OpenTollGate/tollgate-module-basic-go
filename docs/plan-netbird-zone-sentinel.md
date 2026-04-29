# Plan: Netbird Firewall Zone — Feature-Branch Gating

## Status

The six items from the original handoff (`docs/netbird-handoff.md`) are **already
applied** on `feature/netbird-in-develop`:

1. ✅ `setup_firewall_include()` disabled (no-op) in `99-tollgate-setup`
2. ✅ `setup_netbird_zone()` added and called in the driver section
3. ✅ `DEPENDS:=` (empty) in `packaging/Makefile`
4. ✅ `scripts/add-netbird-zone.sh` copied
5. ✅ `tests/test_firewall_config.sh` created
6. ✅ `packaging/files/etc/config/firewall-tollgate` unchanged (no netbird entries)

## New Requirement

The netbird firewall zone setup must only be active in **feature-branch
packages**, not in **main/production packages**.

## Mechanism: Sentinel File

A sentinel file (`/etc/tollgate/netbird-zone-enabled`) controls whether
`setup_netbird_zone()` runs at router runtime. The sentinel is only installed
into the package when building from a non-main branch.

| Ref | Sentinel in package? | `setup_netbird_zone` runs? |
|-----|---------------------|---------------------------|
| `refs/heads/main` | No | No (returns immediately) |
| `refs/heads/feature/*` | Yes | Yes |
| `refs/pull/*/merge` | Yes | Yes |
| `refs/tags/*` | Yes | Yes |

The function body and its call in the driver section always exist in source.
Without the sentinel the function is an inert no-op.

## Changes

### 1. Create sentinel file

**File:** `packaging/files/etc/tollgate/netbird-zone-enabled`

Empty file. Its presence in the installed package enables the netbird zone.

### 2. Guard `setup_netbird_zone()` in `99-tollgate-setup`

**File:** `packaging/files/etc/uci-defaults/99-tollgate-setup`

Add as the first line of `setup_netbird_zone()`, before the existing
idempotency guard:

```sh
setup_netbird_zone() {
    [ -f /etc/tollgate/netbird-zone-enabled ] || return
    uci -q get firewall.netbird_zone >/dev/null 2>&1 && return
    ...
}
```

### 3. Update `packaging/Makefile`

**File:** `packaging/Makefile`

a) In `Package/$(PKG_NAME)/install`, after the existing
   `$(INSTALL_DIR) $(1)/etc/tollgate` line, add a conditional install:

```makefile
[ -f "$(PKG_MAKEFILE_DIR)files/etc/tollgate/netbird-zone-enabled" ] && $(INSTALL_DATA) $(PKG_MAKEFILE_DIR)files/etc/tollgate/netbird-zone-enabled $(1)/etc/tollgate/ || true
```

This is conditional so the build doesn't fail when CI removes the file from
the staging area for main builds.

b) Add to `FILES_$(PKG_NAME)` list:

```makefile
/etc/tollgate/netbird-zone-enabled
```

### 4. Update CI workflow — `package-ipk` job

**File:** `.github/workflows/build-package.yml`
**Job:** `package-ipk`, after payload assembly (after the `mkdir -p` / `cp -r` block)

Add:

```sh
# Netbird zone: install sentinel for non-main builds only
if [[ "$GITHUB_REF" != "refs/heads/main" ]]; then
    touch "$PAYLOAD/etc/tollgate/netbird-zone-enabled"
fi
```

### 5. Update CI workflow — `package-apk` job

**File:** `.github/workflows/build-package.yml`
**Job:** `package-apk`, after the "Stage SDK package tree" step

Add a new step:

```yaml
- name: Gate netbird zone for main builds
  if: github.ref == 'refs/heads/main'
  run: rm -f /builder/package/${{ env.PACKAGE_NAME }}/files/etc/tollgate/netbird-zone-enabled
```

This removes the sentinel from the SDK staging area so the conditional
install in the Makefile skips it.

### 6. Update `tests/test_firewall_config.sh`

**File:** `tests/test_firewall_config.sh`

Add a new check in the "--- setup_netbird_zone function exists ---" block:

```sh
assert_contains "$NETBIRD_FN" "/etc/tollgate/netbird-zone-enabled" "setup_netbird_zone has sentinel file guard"
```

## Verification

```sh
# Static test (all checks should pass)
sh tests/test_firewall_config.sh

# On a feature-branch build, verify on router:
uci show firewall | grep netbird

# On a main-branch build, verify on router:
ls /etc/tollgate/netbird-zone-enabled  # should not exist
uci show firewall | grep netbird        # should return nothing
```
