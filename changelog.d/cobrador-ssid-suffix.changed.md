- **SSID names are configurable without changing the device identity.** The
  guest network is now `!<brand>-<suffix>` and the private network is
  `Private-<operator>-<suffix>`. `ssid_suffix` defaults to the stored device
  code and controls both networks; `private_ssid_operator` defaults to
  `c08r4d0r` (leetspeak for *cobrador*, Spanish for the person who collects the
  toll/fare) and can be overridden by a brand or the operator. The device code
  still owns the hostname and TLS identity, and the suffix/operator persist in
  `/etc/config/tollgate` across reinstalls and upgrades.
