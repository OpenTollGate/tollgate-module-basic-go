## Why

The v0.4.0 → v0.5.0 upgrade path is the **highest-risk item** in the release
plan (tollgate-module-basic-go#154) but has **zero test coverage**. The cloud
lab already has GCP nested-virtualization VMs with snapshot/restore capability
(`lib/cloud_lab/shc.py`) — this feature adds the missing pieces to make
upgrade testing repeatable, fast, and automated.

## What exists today

- `lib/cloud_lab/gcp.py` — GCP instance lifecycle (up/down/ssh/status).
- `lib/cloud_lab/shc.py` — snapshot API (`create_snapshot`,
  `restore_snapshot`, `delete_snapshot`). **Not exposed via CLI.**
- `lib/cloud_lab/worker/vms.py` — QEMU/KVM worker with OpenWrt qcow2 images.
- `scripts/cloud-lab.py` — CLI with `up`, `down`, `ssh`, `submit`, `status`.
- `tests/scenarios/test_two_router_cloud.py` — two-router tests via eth1.
- No upgrade test exists anywhere.

## Proposal

### Phase 1: Expose snapshot management via CLI

Add subcommands to `scripts/cloud-lab.py`:

```
cloud-lab snapshot create <name>     # snapshot current VM state
cloud-lab snapshot restore <name>    # restore VM to snapshot
cloud-lab snapshot list              # list available snapshots
cloud-lab snapshot delete <name>     # delete a snapshot
```

The `shc.py` API already has the endpoints. This is wiring only.

### Phase 2: Baseline snapshot workflow

Add a Makefile target and script:

```makefile
.PHONY: upgrade-baseline
upgrade-baseline:
	# Boot fresh VM, install v0.4.0, configure, snapshot
	python scripts/upgrade-baseline.py --version v0.4.0
```

`scripts/upgrade-baseline.py`:
1. `cloud-lab up` (fresh VM from OpenWrt base image).
2. Build v0.4.0 ipk from `git checkout v0.4.0 && make package`.
3. Install the ipk on the VM.
4. Configure: write `/etc/tollgate/config.json` with realistic settings
   (mints, profit-share, reseller_mode=true).
5. Verify v0.4.0 smoke passes.
6. `cloud-lab snapshot create v0.4.0-baseline`.
7. `cloud-lab down`.

This snapshot is reusable for every upgrade test run.

### Phase 3: `test_upgrade.py` scenario

New file `tests/scenarios/test_upgrade.py`:

```python
import json
import pytest
from lib.router import Router

pytestmark = [pytest.mark.api, pytest.mark.upgrade]


@pytest.fixture(scope="module")
def upgraded_router(backend):
    """Restore v0.4.0 baseline, upgrade to target, yield router."""
    # Phase 1: restore baseline
    # (handled by cloud-lab submit --from-snapshot v0.4.0-baseline)

    router = Router.from_env(backend=backend)

    # Phase 2: capture pre-upgrade state
    pre_config = router.run("cat /etc/tollgate/config.json")
    pre_uci_wireless = router.run("uci show wireless")
    pre_uci_uhttpd = router.run("uci show uhttpd")

    # Phase 3: install target package
    router.install_package(os.environ["TARGET_IPK_URL"])

    # Phase 4: reboot to trigger uci-defaults
    router.reboot(timeout=120)

    yield router, {
        "config": pre_config,
        "wireless": pre_uci_wireless,
        "uhttpd": pre_uci_uhttpd,
    }


class TestConfigMigration:
    def test_config_version_bumped(self, upgraded_router):
        router, pre = upgraded_router
        cfg = json.loads(router.run("cat /etc/tollgate/config.json"))
        assert cfg["config_version"] >= "v0.0.8"

    def test_upstream_wifi_populated(self, upgraded_router):
        router, pre = upgraded_router
        cfg = json.loads(router.run("cat /etc/tollgate/config.json"))
        assert cfg.get("upstream_wifi", {}).get("scan_interval_seconds", 0) > 0

    def test_user_settings_preserved(self, upgraded_router):
        router, pre = upgraded_router
        cfg = json.loads(router.run("cat /etc/tollgate/config.json"))
        pre_cfg = json.loads(pre["config"])
        assert cfg["metric"] == pre_cfg["metric"]
        assert cfg["step_size"] == pre_cfg["step_size"]
        assert len(cfg["accepted_mints"]) == len(pre_cfg["accepted_mints"])


class TestServiceHealth:
    def test_service_running(self, upgraded_router):
        router, _ = upgraded_router
        assert router.service_running("tollgate-wrt")

    def test_api_health(self, upgraded_router):
        router, _ = upgraded_router
        health = router.api_get("/health")
        assert health["status"] == "ok"

    def test_no_crash_loop(self, upgraded_router):
        router, _ = upgraded_router
        # Check procd didn't respawn the service >3 times
        log = router.run("logread | grep tollgate-wrt | tail -20")
        assert "respawn" not in log or log.count("respawn") < 3


class TestUCIDefaults:
    def test_ap_interfaces_exist(self, upgraded_router):
        router, _ = upgraded_router
        wireless = router.run("uci show wireless")
        assert "default_radio0" in wireless
        assert "default_radio1" in wireless

    def test_uhttpd_port(self, upgraded_router):
        router, _ = upgraded_router
        uhttpd = router.run("uci show uhttpd")
        assert "8080" in uhttpd

    def test_ipv6_disabled_on_lan(self, upgraded_router):
        router, _ = upgraded_router
        dhcp = router.run("uci show dhcp.lan")
        assert "ra='disabled'" in dhcp or "ra='disabled'" in dhcp
```

### Phase 4: Cloud submission

Add a cloud-lab submit variant:

```sh
cloud-lab submit \
  --from-snapshot v0.4.0-baseline \
  --target-ipk-url <url-to-v0.5.0-ipk> \
  -- pytest tests/scenarios/test_upgrade.py -v
```

The worker:
1. Restores the baseline snapshot.
2. Installs the target ipk.
3. Reboots.
4. Runs the pytest suite.
5. Reports results.

### Phase 5: CI integration

Add a GitHub Actions workflow that runs the upgrade test on every push to
`main` and on PRs touching `config_manager/` or `packaging/files/etc/`.

## Cloud vs. physical division

| Test | Cloud | Physical |
|---|---|---|
| Config migration (R1) | ✅ Primary | — |
| UCI-defaults behavior (R2-R8) | ✅ Primary | Validation |
| API health post-upgrade | ✅ Primary | — |
| WiFi manager post-upgrade | ❌ (needs mac80211_hwsim) | ✅ Primary |
| Captive portal post-upgrade | ❌ | ✅ Primary |

## Acceptance criteria

- [ ] `cloud-lab snapshot {create,restore,list,delete}` CLI commands work.
- [ ] `scripts/upgrade-baseline.py` creates a reusable v0.4.0 snapshot.
- [ ] `tests/scenarios/test_upgrade.py` passes on a v0.4.0 → main upgrade.
- [ ] `cloud-lab submit --from-snapshot` workflow is documented.
- [ ] CI runs the upgrade test on PRs touching config or packaging.

## Related

- tollgate-module-basic-go upgrade risk register: (link TBD)
- tollgate-module-basic-go config_version bug: (link TBD)
- Release plan tollgate-module-basic-go#154
- Existing cloud test: `tests/scenarios/test_two_router_cloud.py`
