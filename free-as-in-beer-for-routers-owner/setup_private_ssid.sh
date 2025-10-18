#!/bin/sh

# Exit script on any error
set -e

# --- Network Configuration ---
# Create the new 'private' interface
uci set network.private=interface
uci set network.private.proto='static'
uci set network.private.device='br-private'
uci set network.private.ipaddr='10.92.113.1'
uci set network.private.netmask='255.255.255.0'

# Create the new bridge device for the private interface
uci set network.private_bridge=device
uci set network.private_bridge.type='bridge'
uci set network.private_bridge.name='br-private'

# --- DHCP Configuration for Private Network ---
uci set dhcp.private=dhcp
uci set dhcp.private.interface='private'
uci set dhcp.private.start='100'
uci set dhcp.private.limit='150'
uci set dhcp.private.leasetime='12h'

# --- Wireless Configuration ---
# 2.4GHz Radio
uci set wireless.admin_radio0=wifi-iface
uci set wireless.admin_radio0.device='radio0'
uci set wireless.admin_radio0.network='private'
uci set wireless.admin_radio0.mode='ap'
uci set wireless.admin_radio0.ssid='TollGate-Admin'
uci set wireless.admin_radio0.encryption='psk2+ccmp' # WPA2-PSK with CCMP
uci set wireless.admin_radio0.key='securepassword123'
uci set wireless.admin_radio0.disabled='0'

# 5GHz Radio
uci set wireless.admin_radio1=wifi-iface
uci set wireless.admin_radio1.device='radio1'
uci set wireless.admin_radio1.network='private'
uci set wireless.admin_radio1.mode='ap'
uci set wireless.admin_radio1.ssid='TollGate-Admin'
uci set wireless.admin_radio1.encryption='psk2+ccmp' # WPA2-PSK with CCMP
uci set wireless.admin_radio1.key='securepassword123'
uci set wireless.admin_radio1.disabled='0'

# --- Firewall Configuration ---
uci set firewall.private_zone=zone
uci set firewall.private_zone.name='private'
uci set firewall.private_zone.network='private'
uci set firewall.private_zone.input='ACCEPT'
uci set firewall.private_zone.output='ACCEPT'
uci set firewall.private_zone.forward='ACCEPT'

uci set firewall.private_forwarding=forwarding
uci set firewall.private_forwarding.src='private'
uci set firewall.private_forwarding.dest='wan'

# --- Commit and Apply Changes ---
uci commit network
uci commit dhcp
uci commit wireless
uci commit firewall

# Restart services to apply changes
/etc/init.d/network restart
/etc/init.d/dnsmasq restart
/etc/init.d/firewall restart
wifi reload

echo "Private SSID setup complete. Please connect to 'TollGate-Admin' to test."
