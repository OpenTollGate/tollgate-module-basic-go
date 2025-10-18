
Terminal output:
```
root@OpenWrt:/tmp# chmod +x setup_private_ssid.sh 
root@OpenWrt:/tmp# ./setup_private_ssid.sh 
udhcpc: started, v1.36.1
udhcpc: broadcasting discover
udhcpc: no lease, failing
udhcpc: started, v1.36.1
udhcpc: broadcasting discover
udhcpc: read error: Network is down, reopening socket
udhcpc: no lease, failing
You cannot use UCI in firewall includes!
Include '/etc/config/firewall-tollgate' failed with exit code 1
Private SSID setup complete. Please connect to 'TollGate-Admin' to test.
root@OpenWrt:/tmp# cat setup_private_ssid.sh 
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
```



Whats going wrong? How can we fix it? 

 
Terminal output:
```

root@OpenWrt:/tmp# cat /etc/config/wireless 

config wifi-device 'radio0'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi'
        option band '2g'
        option channel '2'
        option htmode 'HT40'
        option disabled '0'
        option cell_density '0'

config wifi-iface 'default_radio0'
        option device 'radio0'
        option network 'lan'
        option mode 'ap'
        option ssid 'TollGate-2AA2-2.4GHz'
        option encryption 'none'
        option name 'tollgate_2g_open'
        option disabled '0'

config wifi-device 'radio1'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi+1'
        option band '5g'
        option channel '149'
        option htmode 'VHT80'

config wifi-iface 'default_radio1'
        option device 'radio1'
        option network 'lan'
        option mode 'ap'
        option ssid 'TollGate-2AA2-5GHz'
        option encryption 'none'
        option name 'tollgate_5g_open'

config wifi-iface 'wifinet2'
        option device 'radio1'
        option mode 'sta'
        option network 'wwan'
        option ssid 'c03rad0r'
        option encryption 'sae'
        option key 'c03rad0r123'
        option ocv '0'

config wifi-iface 'admin_radio0'
        option device 'radio0'
        option network 'private'
        option mode 'ap'
        option ssid 'TollGate-Admin'
        option encryption 'psk2+ccmp'
        option key 'securepassword123'
        option disabled '0'

config wifi-iface 'admin_radio1'
        option device 'radio1'
        option network 'private'
        option mode 'ap'
        option ssid 'TollGate-Admin'
        option encryption 'psk2+ccmp'
        option key 'securepassword123'
        option disabled '0'

root@OpenWrt:/tmp# cat /etc/config/network 

config interface 'loopback'
        option device 'lo'
        option proto 'static'
        option ipaddr '127.0.0.1'
        option netmask '255.0.0.0'

config globals 'globals'
        option ula_prefix 'fdb3:3231:8ed9::/48'

config device
        option name 'br-lan'
        option type 'bridge'
        list ports 'eth1'

config interface 'lan'
        option device 'br-lan'
        option proto 'static'
        option ipaddr '10.92.112.1'
        option netmask '255.255.255.0'
        option ip6assign '60'
        list dns '1.1.1.1'
        list dns '1.0.0.1'
        option domain 'lan'
        option broadcast '10.92.112.255'

config interface 'wan'
        option device 'eth0'
        option proto 'dhcp'

config interface 'wan6'
        option device 'eth0'
        option proto 'dhcpv6'

config interface 'wwan'
        option proto 'dhcp'

config interface 'private'
        option proto 'static'
        option device 'br-private'
        option ipaddr '10.92.113.1'
        option netmask '255.255.255.0'

config device 'private_bridge'
        option type 'bridge'
        option name 'br-private'

root@OpenWrt:/tmp# cat /etc/config/nodogsplash 

config nodogsplash
        option enabled '1'
        option fwhook_enabled '1'
        option gatewayinterface 'br-lan'
        option gatewayname 'TollGate-2AA2 Portal'
        option maxclients '250'
        option preauthidletimeout '30'
        option authidletimeout '120'
        option sessiontimeout '1200'
        option checkinterval '600'
        list authenticated_users 'allow all'
        list users_to_router 'allow tcp port 22'
        list users_to_router 'allow tcp port 23'
        list users_to_router 'allow tcp port 53'
        list users_to_router 'allow udp port 53'
        list users_to_router 'allow udp port 67'
        list users_to_router 'allow tcp port 80'
        list users_to_router 'allow tcp port 2121'
        list users_to_router 'allow tcp port 8080'

root@OpenWrt:/tmp# 
```


Terminal output:
```
c03rad0r@RomblonMimaropa:~/tollgate-module-basic-go$ ifconfig
br-7921012c8a80: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet 172.18.0.1  netmask 255.255.0.0  broadcast 172.18.255.255
        inet6 fe80::4091:bdff:fec3:bde7  prefixlen 64  scopeid 0x20<link>
        ether 42:91:bd:c3:bd:e7  txqueuelen 0  (Ethernet)
        RX packets 0  bytes 0 (0.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 0  bytes 0 (0.0 B)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

docker0: flags=4099<UP,BROADCAST,MULTICAST>  mtu 1500
        inet 172.17.0.1  netmask 255.255.0.0  broadcast 172.17.255.255
        ether 7a:40:e7:84:08:6f  txqueuelen 0  (Ethernet)
        RX packets 0  bytes 0 (0.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 0  bytes 0 (0.0 B)
        TX errors 0  dropped 68 overruns 0  carrier 0  collisions 0

enp0s31f6: flags=4099<UP,BROADCAST,MULTICAST>  mtu 1500
        ether 98:fa:9b:38:71:a7  txqueuelen 1000  (Ethernet)
        RX packets 0  bytes 0 (0.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 0  bytes 0 (0.0 B)
        TX errors 0  dropped 2 overruns 0  carrier 0  collisions 0
        device interrupt 16  memory 0xec200000-ec220000  

enx00e04c683d2d: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet 10.92.112.106  netmask 255.255.255.0  broadcast 10.92.112.255
        inet6 fdb3:3231:8ed9:0:d567:4863:dee3:65fb  prefixlen 64  scopeid 0x0<global>
        inet6 fdb3:3231:8ed9:0:f0f3:82e9:ad13:27c3  prefixlen 64  scopeid 0x0<global>
        inet6 fe80::9bff:6668:342:7107  prefixlen 64  scopeid 0x20<link>
        inet6 fdb3:3231:8ed9::653  prefixlen 128  scopeid 0x0<global>
        ether 00:e0:4c:68:3d:2d  txqueuelen 1000  (Ethernet)
        RX packets 0  bytes 0 (0.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 0  bytes 0 (0.0 B)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

lo: flags=73<UP,LOOPBACK,RUNNING>  mtu 65536
        inet 127.0.0.1  netmask 255.0.0.0
        inet6 ::1  prefixlen 128  scopeid 0x10<host>
        loop  txqueuelen 1000  (Local Loopback)
        RX packets 35847  bytes 29916395 (29.9 MB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 35847  bytes 29916395 (29.9 MB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

tailscale0: flags=4305<UP,POINTOPOINT,RUNNING,NOARP,MULTICAST>  mtu 1280
        inet6 fe80::61ca:9fc6:77b2:4472  prefixlen 64  scopeid 0x20<link>
        unspec 00-00-00-00-00-00-00-00-00-00-00-00-00-00-00-00  txqueuelen 500  (UNSPEC)
        RX packets 1  bytes 86 (86.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 453  bytes 172710 (172.7 KB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

veth313b4c6: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 fe80::f0c7:82ff:feeb:b200  prefixlen 64  scopeid 0x20<link>
        ether f2:c7:82:eb:b2:00  txqueuelen 0  (Ethernet)
        RX packets 3  bytes 126 (126.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 1391  bytes 540934 (540.9 KB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

vethdb67123: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 fe80::2cf0:6bff:fe5e:55d1  prefixlen 64  scopeid 0x20<link>
        ether 2e:f0:6b:5e:55:d1  txqueuelen 0  (Ethernet)
        RX packets 11477  bytes 1212230 (1.2 MB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 12569  bytes 2595096 (2.5 MB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

vethdea16c8: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 fe80::a8a2:77ff:fe63:e93  prefixlen 64  scopeid 0x20<link>
        ether aa:a2:77:63:0e:93  txqueuelen 0  (Ethernet)
        RX packets 10632  bytes 1469538 (1.4 MB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 12321  bytes 1697763 (1.6 MB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

wlp59s0: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 fe80::2c2f:8894:9f3c:2cf3  prefixlen 64  scopeid 0x20<link>
        ether b4:69:21:80:21:04  txqueuelen 1000  (Ethernet)
        RX packets 65674  bytes 97961903 (97.9 MB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 12959  bytes 1552922 (1.5 MB)
        TX errors 0  dropped 15 overruns 0  carrier 0  collisions 0
```

It looks like the password protected SSID was created successfully, but I can't `ping 8.8.8.8` when I connect to this SSID. What could the issue be? Could part of the problem be that we didn't gat an IP address from the TollGate when connecting to `TollGate-Admin`? How can we debug this? 

