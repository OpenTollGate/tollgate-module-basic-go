# Manual Testing Plan: IP Randomization and install.json Versioning

This document outlines the manual testing steps to verify the IP randomization and `install.json` versioning features on an OpenWRT router.

## I. Prerequisites

- [ ] Access to an OpenWRT router.
- [ ] The latest `tollgate-module-basic-go` package (`.ipk` file) with the recent changes.
- [ ] `jq` installed on the router (`opkg install jq`).

---

## II. Test Case 1: Fresh Install

**Objective:** To verify that a fresh installation correctly randomizes the LAN IP and creates a versioned `install.json`.

**Steps:**

1.  [ ] **Reset the router to a clean state:**
    *   If a previous version of `tollgate-module-basic-go` is installed, remove it: `opkg remove tollgate-module-basic-go`.
    *   Remove the `/etc/tollgate` directory: `rm -rf /etc/tollgate`.
    *   Reset the network configuration to a known default (e.g., `192.168.1.1`):
        ```sh
        uci set network.lan.ipaddr='192.168.1.1'
        uci commit network
        /etc/init.d/network restart
        ```

2.  [ ] **Install the new package:**
    *   Copy the `.ipk` file to the router's `/tmp` directory.
    *   Install the package: `opkg install /tmp/<package_name>.ipk`.

3.  [ ] **Verify IP Randomization:**
    *   Check the LAN IP address: `uci get network.lan.ipaddr`.
    *   **Expected Result:** The IP address should *not* be `192.168.1.1`. It should be a randomized IP address.

4.  [ ] **Verify `install.json`:**
    *   Check the content of `/etc/tollgate/install.json`: `cat /etc/tollgate/install.json | jq`.
    *   **Expected Result:**
        *   The `config_version` field should exist and be set to `"v0.0.2"`.
        *   The `ip_address_randomized` field should be `true`.

---

## III. Test Case 2: Upgrade from Unversioned `install.json`

**Objective:** To verify that an upgrade from a version with an unversioned `install.json` correctly migrates the file and randomizes the IP.

**Steps:**

1.  [ ] **Set up the unversioned state:**
    *   Reset the router as in Test Case 1.
    *   Create an unversioned `/etc/tollgate/install.json` file:
        ```sh
        mkdir -p /etc/tollgate
        echo '{"ip_address_randomized":false}' > /etc/tollgate/install.json
        ```

2.  [ ] **Install the new package:**
    *   Install the package as in Test Case 1.

3.  [ ] **Verify IP Randomization:**
    *   Check the LAN IP address: `uci get network.lan.ipaddr`.
    *   **Expected Result:** The IP address should be randomized.

4.  [ ] **Verify `install.json` Migration:**
    *   Check the content of `/etc/tollgate/install.json`: `cat /etc/tollgate/install.json | jq`.
    *   **Expected Result:**
        *   The `config_version` field should exist and be set to `"v0.0.1"`.
        *   The `ip_address_randomized` field should be `true`.

---

## IV. Test Case 3: Upgrade with Randomized IP

**Objective:** To verify that an upgrade on a system where the IP has already been randomized does *not* re-randomize the IP.

**Steps:**

1.  [ ] **Set up the randomized state:**
    *   Reset the router as in Test Case 1.
    *   Create a versioned `/etc/tollgate/install.json` with `ip_address_randomized: true`:
        ```sh
        mkdir -p /etc/tollgate
        echo '{"config_version":"v0.0.2", "ip_address_randomized":true}' > /etc/tollgate/install.json
        ```
    *   Set a custom, non-default IP address:
        ```sh
        uci set network.lan.ipaddr='10.20.30.1'
        uci commit network
        /etc/init.d/network restart
        ```

2.  [ ] **Install the new package:**
    *   Install the package as in Test Case 1.

3.  [ ] **Verify IP is not re-randomized:**
    *   Check the LAN IP address: `uci get network.lan.ipaddr`.
    *   **Expected Result:** The IP address should remain `10.20.30.1`.

4.  [ ] **Verify `install.json`:**
    *   Check the content of `/etc/tollgate/install.json`: `cat /etc/tollgate/install.json | jq`.
    *   **Expected Result:** The file should be unchanged, with `ip_address_randomized: true`.