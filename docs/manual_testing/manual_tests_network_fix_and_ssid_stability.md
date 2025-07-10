# Manual Testing Checklist: Network Disconnection Fix & SSID Stability

This document provides the manual testing steps to verify the fixes for the STA disconnection issue and to ensure the TollGate SSID is stable and deterministic.

**Pre-requisites:**
*   An OpenWRT router flashed with a base image.
*   The `tollgate-module-basic-go` package built from the `fix/multiple_ssid_same_radio` branch.
*   Access to the router's command line via SSH.

---

### Test Case 1: Clean Install on Router in STA Mode (Primary Validation)

**Objective:** Verify that installing the package on a router using Wi-Fi for its internet connection does not break the connection.

- [ ] **Setup Router in STA mode:**
    *   Connect to the router's default AP.
    *   Navigate to `Network -> Wireless`.
    *   Click `Scan` on one of the radios (e.g., `radio0`).
    *   Find your upstream Wi-Fi network (e.g., your home Wi-Fi) and click `Join Network`.
    *   In the "Joining Network" screen:
        *   Enter the password for your upstream Wi-Fi.
        *   Assign the firewall-zone `wan`.
    *   Click `Save`, then `Save & Apply`.
    *   **Verification:** SSH into the router and run `ping 8.8.8.8`. It should succeed.

- [ ] **Install TollGate Package:**
    *   Copy the `.ipk` package to the router's `/tmp` directory.
    *   Run `opkg install /tmp/tollgate-module-basic-go_*.ipk`.
    *   The router will reboot automatically after the UCI scripts run.

- [ ] **Post-Install Verification:**
    - [ ] **Check Internet Connectivity:** After the router reboots, SSH back into it and run `ping 8.8.8.8`. **This is the most critical check.** It MUST succeed.
    - [ ] **Check TollGate AP:** Use a phone or laptop to scan for Wi-Fi networks. You should see a single `TollGate-XXXX` network.
    - [ ] **Check Existing STA Connection:** Run `uci show wireless`. Verify that your original STA `wifi-iface` section still exists and is configured correctly.
    - [ ] **Check Reconfigured AP:** In the output of `uci show wireless`, find the `wifi-iface` that is now the TollGate AP. Verify it has `encryption=none` and `ssid=TollGate-XXXX`.

---

### Test Case 2: SSID Stability and Determinism

**Objective:** Verify the SSID is generated correctly and remains consistent.

- [ ] **Check Initial SSID:**
    *   After the first installation, note the full SSID (e.g., `TollGate-ABCD`).
    *   SSH into the router.
    *   Run `cat /sys/class/net/br-lan/address`. Note the MAC address.
    *   Manually verify that the `XXXX` in the SSID corresponds to the last 4 hex characters of the `br-lan` MAC address.

- [ ] **Check SSID after Reboot:**
    *   Reboot the router using the `reboot` command.
    *   After it comes back online, scan for Wi-Fi networks again.
    *   **Verification:** The SSID must be identical to the one noted in the previous step.

- [ ] **Check Helper Script:**
    *   Run `/usr/bin/generate-tollgate-ssid` on the router.
    *   **Verification:** The output of the script must match the SSID being broadcast.

---

### Test Case 3: Clean Install on Router in Default AP Mode

**Objective:** Ensure the installation does not fail or cause problems on a router in its default state (not connected as a client).

- [ ] **Factory Reset Router:**
    *   If necessary, run `firstboot -y && reboot now` to reset the router to its default configuration.

- [ ] **Install TollGate Package:**
    *   Copy the `.ipk` package to `/tmp`.
    *   Run `opkg install /tmp/tollgate-module-basic-go_*.ipk`.
    *   The router will reboot.

- [ ] **Post-Install Verification:**
    - [ ] **Check TollGate AP:** Scan for Wi-Fi networks. You should see the `TollGate-XXXX` SSID.
    - [ ] **Check `uci` config:** Run `uci show wireless` and verify that the default APs have been correctly reconfigured to be the TollGate APs.