# hwsim peering smoke test

`peering_scan_assoc.sh` runs the closest automated substitute for two physical
routers: one `mac80211_hwsim` radio hosts a TollGate guest SSID with hostapd,
the other scans and associates as an open-network station.

It validates the radio-level half of the Peering feature: the peer SSID is
visible in a scan and open association succeeds. OpenWrt UCI/ubus, the module
daemon and the full admin flow are covered by the module/cloud-lab tests and
the admin Playwright suite.

Requirements: Linux, passwordless sudo, `hostapd`, `iw`, `wpa_supplicant`,
`mac80211_hwsim`.

```sh
sudo tests/hwsim/peering_scan_assoc.sh wlan0 wlan1
```
