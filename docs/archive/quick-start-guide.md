## Quickstart Guide: Installing TollGate WRT

This guide will walk you through installing the TollGate WRT package (`.ipk`) on a router that is already running OpenWRT.

### 1. Download and Transfer the Package

First, you need to get the TollGate installation file onto your router.

1.  Go to the latest release on the TollGate releases page.
2.  Find the TollGate WRT `.ipk` file that matches your router's architecture (e.g., `aarch64_cortex-a72.ipk`, `mips_24kc.ipk`).
3.  Copy the package to the `/tmp/` directory on your router. Replace `<architecture>.ipk` with the name of the file you downloaded and `<router_ip>` with your router's IP address.

    ```bash
    scp <architecture>.ipk root@<router_ip>:/tmp/tollgate.ipk
    ```
    > **Note:** For workshop devices, the password may be `c03rad0r123`. For a new device, there may be no password set.

### 2. Install TollGate

Now, SSH into the router to run the installation command.

1.  Connect to your router via SSH:
    ```bash
    ssh root@<router_ip>
    ```

2.  Navigate to the `/tmp` directory and install the package. The `--force-reinstall` flag is useful for ensuring a clean installation.
    ```bash
    cd /tmp
    opkg install --force-reinstall tollgate.ipk
    ```

3.  Reboot the router to apply all changes.
    ```bash
    reboot
    ```
    > **Warning:** After rebooting, the router's IP address will likely change to a new, randomly generated one (e.g., `10.92.112.1`). The new open Wi-Fi network will be named something like `TollGate-XXXX-2.4GHz`.

### 3. Verify the Installation

After the router reboots, check that TollGate is running correctly.

1.  Connect your computer to the new `TollGate-XXXX-...` Wi-Fi network.
2.  Open a terminal and use `curl` to check the TollGate API endpoint. Replace `<new_router_ip>` with the router's new IP address.
    ```bash
    curl http://<new_router_ip>:2121
    ```
3.  If successful, you will see a JSON output containing the Tollgate price advertisement, similar to this:
    ```json
    {"kind":10021,"id":"...","pubkey":"...","tags":[["metric","milliseconds"],...]}
    ```

### 4. Connect to Upstream Internet

Your TollGate needs an internet connection to function. Use the LuCi web interface to connect it to an existing Wi-Fi network (like your home or mobile hotspot).

1.  In your browser, navigate to the LuCi admin panel at `http://<new_router_ip>:8080`.
2.  Go to **Network** -> **Wireless**.
3.  Find the `radio0` (2.4GHz) or `radio1` (5GHz) section and click **Scan**.
4.  Find the upstream Wi-Fi network you want to connect to and click **Join Network**.
5.  Enter the Wi-Fi password and follow the prompts to save the configuration.
6.  To confirm you have an upstream connection, SSH into the router and test connectivity:
    ```bash
    ping 1.1.1.1
    ```

### 5. Configure Your Tollgate

Now it's time to set your prices and, most importantly, where to receive your earnings.

#### Set Your Payout Address (Crucial!)

To receive your share of the profits, you must set your Lightning Address.

1.  SSH into your router and open the identities configuration file:
    ```bash
    vi /etc/tollgate/identities.json
    ```
2.  Find the `"owner"` identity in the `public_identities` list:
    ```json
    {
      "name": "owner",
      "pubkey": "[on_setup]",
      "lightning_address": "tollgate@minibits.cash"
    }
    ```
3.  **You MUST change the `lightning_address`** from the default to your own Lightning Address to receive payouts. Save the file after making your changes.

#### Set Pricing and Profit Share

You can configure pricing, payout thresholds, and the developer profit share in `/etc/tollgate/config.json`.

1.  SSH into your router and open the main configuration file:
    ```bash
    vi /etc/tollgate/config.json
    ```
2.  **To set the price:** Adjust the `step_size` (in milliseconds) and `price_per_step` (in sats). For example, `step_size: 600000` is 10 minutes.
3.  **To set profit share:** The `profit_share` section defines how earnings are split. By default, 79% goes to the `owner` (you) and 21% to the `developer`. You can adjust these `factor` values as you see fit.
4.  **To set payout thresholds:** The `accepted_mints` section contains a `min_payout_amount`. This defines the balance the router must accumulate before it attempts to pay out to the owner and developer addresses.

### Troubleshooting

*   **Nothing shows up on port `:2121`**:
    Check the Tollgate logs for errors.
    ```bash
    logread | grep tollgate
    ```

*   **Captive portal does not show up**:
    This usually means the router is offline. Follow the steps in section **4. Connect to Upstream Internet** to ensure the device has a working internet connection.