#!/bin/sh
set +e # Continue even if a command fails

echo "Reverting private SSID configuration..."

# --- Delete Network Configuration ---
uci delete network.private
uci delete network.private_bridge

# --- Delete DHCP Configuration ---
uci delete dhcp.private

# --- Delete Wireless Configuration ---
uci delete wireless.admin_radio0
uci delete wireless.admin_radio1

# --- Delete Firewall Configuration ---
uci delete firewall.private_zone
uci delete firewall.private_forwarding

# --- Commit and Apply Changes ---
uci commit
/etc/init.d/network restart
/etc/init.d/dnsmasq restart
/etc/init.d/firewall restart
wifi reload

echo "Revert complete. The private SSID should now be removed."