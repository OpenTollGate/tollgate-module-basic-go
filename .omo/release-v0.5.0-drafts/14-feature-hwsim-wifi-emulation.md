## Why

The cloud lab currently can't run WiFi-dependent tests (upstream WiFi manager,
captive portal). `tests/scenarios/test_two_router_cloud.py` explicitly skips
WiFi tests and uses eth1 instead. If `mac80211_hwsim` works in the cloud
worker's KVM VMs, we could emulate WiFi in software and move even WiFi tests
to the cloud — dramatically increasing CI coverage without physical hardware.

## Evidence this might work

- `tests/api/test_mac80211_hwsim.py` already exists — suggesting someone
  started exploring this.
- The cloud worker uses QEMU/KVM with OpenWrt qcow2 images
  (`lib/cloud_lab/worker/vms.py`).
- `mac80211_hwsim` is a Linux kernel module that creates virtual WiFi
  interfaces. It works inside KVM VMs if the kernel module is available.
- `lib/cloud_lab/worker/wifi.py` already references `hwsim` (line 121):
  "OpenWrt complication: baked snapshot has 2 local hwsim radios with netifd."

## Proposal

### Phase 1: Verify mac80211_hwsim works in the cloud worker

Run a quick experiment:

```sh
cloud-lab up
cloud-lab ssh -- "modprobe mac80211_hwsim radios=2 && iw dev"
# If we see two hwsim interfaces, it works.
```

### Phase 2: Expose hwsim radios to the OpenWrt VM

The cloud worker's VM setup (`lib/cloud_lab/worker/vms.py`) needs to:
1. Load `mac80211_hwsim` on the host kernel.
2. Pass the hwsim radios to the guest VM (or load inside the guest).
3. Configure OpenWrt to recognize the hwsim radios as `radio0` / `radio1`.

The `wifi.py` reference suggests this was partially explored. Need to
understand what works and what doesn't.

### Phase 3: WiFi-dependent tests in the cloud

Once hwsim works, these tests can move to the cloud:

| Test | Currently | Could move to cloud? |
|---|---|---|
| `test_upstream_wifi.py` | Physical only | ✅ if hwsim works |
| `test_two_router.py` WiFi discovery | Physical only | ✅ if hwsim works |
| WiFi manager post-upgrade | Physical only | ✅ if hwsim works |
| Captive portal (OS detection) | Physical only | ❌ (needs real captive portal detection) |
| QR scanner (#95) | Physical only | ❌ (needs real camera + browser) |

### Phase 4: CI integration

Add a cloud-lab submit variant for WiFi tests:

```sh
cloud-lab submit --wifi-hwsim -- pytest tests/scenarios/test_upstream_wifi.py -v
```

## Out of scope

- Captive portal OS detection — requires real OS-level captive portal
  behavior, not just a WiFi interface.
- QR scanner testing — requires real device cameras and browsers.
- Physical-radio-specific behavior (driver quirks, signal attenuation).

## Acceptance criteria

- [ ] `mac80211_hwsim` loads successfully in the cloud worker VM.
- [ ] OpenWrt recognizes two hwsim radios as `radio0` / `radio1`.
- [ ] One WiFi-dependent test (e.g. `test_upstream_wifi.py`) passes in the
      cloud lab.
- [ ] Documentation on running WiFi tests in the cloud.

## Related

- Existing hwsim test: `tests/api/test_mac80211_hwsim.py`
- Cloud worker WiFi code: `lib/cloud_lab/worker/wifi.py`
- Cloud two-router test: `tests/scenarios/test_two_router_cloud.py`
