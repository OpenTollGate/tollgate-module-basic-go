
Terminal output:
```
c03rad0r@RomblonMimaropa:~/tollgate-module-basic-go/free-as-in-beer-for-routers-owner$ ifconfig
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
        TX errors 0  dropped 163 overruns 0  carrier 0  collisions 0

enp0s31f6: flags=4099<UP,BROADCAST,MULTICAST>  mtu 1500
        ether 98:fa:9b:38:71:a7  txqueuelen 1000  (Ethernet)
        RX packets 0  bytes 0 (0.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 0  bytes 0 (0.0 B)
        TX errors 0  dropped 2 overruns 0  carrier 0  collisions 0
        device interrupt 16  memory 0xec200000-ec220000  

enx00e04c36043e: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet 10.14.226.180  netmask 255.255.255.0  broadcast 10.14.226.255
        inet6 fe80::7aaa:708f:f426:5454  prefixlen 64  scopeid 0x20<link>
        inet6 fdaa:bdc9:22ad::413  prefixlen 128  scopeid 0x0<global>
        inet6 fdaa:bdc9:22ad:0:dbbc:905:d616:a100  prefixlen 64  scopeid 0x0<global>
        inet6 fdaa:bdc9:22ad:0:c10e:75eb:e01a:ec14  prefixlen 64  scopeid 0x0<global>
        ether 00:e0:4c:36:04:3e  txqueuelen 1000  (Ethernet)
        RX packets 0  bytes 0 (0.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 0  bytes 0 (0.0 B)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

lo: flags=73<UP,LOOPBACK,RUNNING>  mtu 65536
        inet 127.0.0.1  netmask 255.0.0.0
        inet6 ::1  prefixlen 128  scopeid 0x10<host>
        loop  txqueuelen 1000  (Local Loopback)
        RX packets 91406  bytes 36036222 (36.0 MB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 91406  bytes 36036222 (36.0 MB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

tailscale0: flags=4305<UP,POINTOPOINT,RUNNING,NOARP,MULTICAST>  mtu 1280
        inet6 fe80::61ca:9fc6:77b2:4472  prefixlen 64  scopeid 0x20<link>
        unspec 00-00-00-00-00-00-00-00-00-00-00-00-00-00-00-00  txqueuelen 500  (UNSPEC)
        RX packets 1  bytes 86 (86.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 1278  bytes 446670 (446.6 KB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

veth313b4c6: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 fe80::f0c7:82ff:feeb:b200  prefixlen 64  scopeid 0x20<link>
        ether f2:c7:82:eb:b2:00  txqueuelen 0  (Ethernet)
        RX packets 3  bytes 126 (126.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 3807  bytes 1381737 (1.3 MB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

vethdb67123: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 fe80::2cf0:6bff:fe5e:55d1  prefixlen 64  scopeid 0x20<link>
        ether 2e:f0:6b:5e:55:d1  txqueuelen 0  (Ethernet)
        RX packets 33884  bytes 3570393 (3.5 MB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 36788  bytes 7430629 (7.4 MB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

vethdea16c8: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 fe80::a8a2:77ff:fe63:e93  prefixlen 64  scopeid 0x20<link>
        ether aa:a2:77:63:0e:93  txqueuelen 0  (Ethernet)
        RX packets 31408  bytes 4336525 (4.3 MB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 36092  bytes 4789196 (4.7 MB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

wlp59s0: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet 192.168.2.13  netmask 255.255.255.0  broadcast 192.168.2.255
        inet6 2a01:599:10b:598f:822a:3931:609b:c830  prefixlen 64  scopeid 0x0<global>
        inet6 fe80::dab5:77f0:1328:c347  prefixlen 64  scopeid 0x20<link>
        inet6 2a01:599:10b:598f:ed90:a141:3e68:5579  prefixlen 64  scopeid 0x0<global>
        ether b4:69:21:80:21:04  txqueuelen 1000  (Ethernet)
        RX packets 380744  bytes 462533736 (462.5 MB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 118743  bytes 21891456 (21.8 MB)
        TX errors 0  dropped 141 overruns 0  carrier 0  collisions 0

c03rad0r@RomblonMimaropa:~/tollgate-module-basic-go/free-as-in-beer-for-routers-owner$ ssh root@10.14.226.1


BusyBox v1.36.1 (2025-10-19 16:37:45 UTC) built-in shell (ash)

|   ______      __________      __          ____  _____
|  /_  __/___  / / / ____/___ _/ /____     / __ \/ ___/
|   / / / __ \/ / / / __/ __ `/ __/ _ \   / / / /\__ \
|  / / / /_/ / / / /_/ / /_/ / /_/  __/  / /_/ /___/ /
| /_/  \____/_/_/\____/\__,_/\__/\___/   \____//____/
| Community-built, Sovereign Digital Infrastructure
|
| Website: tollgate.me
| Npub: npub1zzt0d0s2f4lsanpd7nkjep5r79p7ljq7aw37eek64hf0ef6v0mxqgwljrv
|
| LuCi: port 8080
|--------------------------------------------------------------------------|

=== WARNING! =====================================
There is no root password defined on this device!
Use the "passwd" command to set up a new password
in order to prevent unauthorized SSH logins.
--------------------------------------------------
root@OpenWrt:~# cat /etc/config/wireless 

config wifi-device 'radio0'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi'
        option band '2g'
        option channel '1'
        option htmode 'HE20'
        option disabled '0'

config wifi-iface 'default_radio0'
        option device 'radio0'
        option network 'lan'
        option mode 'ap'
        option ssid 'TollGate-UXZB-2.4GHz'
        option encryption 'none'
        option name 'tollgate_2g_open'
        option disabled '0'

config wifi-device 'radio1'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi+1'
        option band '5g'
        option channel '44'
        option htmode 'VHT80'
        option disabled '0'
        option cell_density '0'

config wifi-iface 'default_radio1'
        option device 'radio1'
        option network 'lan'
        option mode 'ap'
        option ssid 'TollGate-UXZB-5GHz'
        option encryption 'none'
        option name 'tollgate_5g_open'
        option disabled '0'

config wifi-iface 'wifinet0'
        option device 'radio0'
        option network 'private'
        option mode 'ap'
        option ssid 'c03rad0r-UXZB-2.4GHz'
        option encryption 'psk2+ccmp'
        option key 'securepassword123'
        option disabled '0'

config wifi-iface 'wifinet1'
        option device 'radio1'
        option network 'private'
        option mode 'ap'
        option ssid 'c03rad0r-UXZB-5GHz'
        option encryption 'psk2+ccmp'
        option key 'securepassword123'
        option disabled '0'

config wifi-iface 'wifinet4'
        option device 'radio1'
        option mode 'sta'
        option network 'wwan'
        option ssid 'EnterSSID-5GHz'
        option encryption 'psk2'
        option key 'c03rad0r123!'

root@OpenWrt:~# cat /etc/config/network 

config interface 'loopback'
        option device 'lo'
        option proto 'static'
        option ipaddr '127.0.0.1'
        option netmask '255.0.0.0'

config globals 'globals'
        option ula_prefix 'fdaa:bdc9:22ad::/48'

config device
        option name 'br-lan'
        option type 'bridge'
        list ports 'eth1'

config interface 'lan'
        option device 'br-lan'
        option proto 'static'
        option ipaddr '10.14.226.1'
        option netmask '255.255.255.0'
        option ip6assign '60'
        list dns '1.1.1.1'
        list dns '1.0.0.1'
        option domain 'lan'
        option broadcast '10.14.226.255'

config interface 'wan'
        option device 'eth0'
        option proto 'dhcp'

config interface 'wan6'
        option device 'eth0'
        option proto 'dhcpv6'

config interface 'wwan'
        option proto 'dhcp'

root@OpenWrt:~# cat /etc/config/firewall

config defaults
        option syn_flood '1'
        option input 'REJECT'
        option output 'ACCEPT'
        option forward 'REJECT'

config zone
        option name 'lan'
        option input 'ACCEPT'
        option output 'ACCEPT'
        option forward 'ACCEPT'
        list network 'lan'

config zone
        option name 'wan'
        option input 'REJECT'
        option output 'ACCEPT'
        option forward 'REJECT'
        option masq '1'
        option mtu_fix '1'
        list network 'wan'
        list network 'wan6'
        list network 'wwan'

config forwarding
        option src 'lan'
        option dest 'wan'

config rule
        option name 'Allow-DHCP-Renew'
        option src 'wan'
        option proto 'udp'
        option dest_port '68'
        option target 'ACCEPT'
        option family 'ipv4'

config rule
        option name 'Allow-Ping'
        option src 'wan'
        option proto 'icmp'
        option icmp_type 'echo-request'
        option family 'ipv4'
        option target 'ACCEPT'

config rule
        option name 'Allow-IGMP'
        option src 'wan'
        option proto 'igmp'
        option family 'ipv4'
        option target 'ACCEPT'

config rule
        option name 'Allow-DHCPv6'
        option src 'wan'
        option proto 'udp'
        option dest_port '546'
        option family 'ipv6'
        option target 'ACCEPT'

config rule
        option name 'Allow-MLD'
        option src 'wan'
        option proto 'icmp'
        option src_ip 'fe80::/10'
        list icmp_type '130/0'
        list icmp_type '131/0'
        list icmp_type '132/0'
        list icmp_type '143/0'
        option family 'ipv6'
        option target 'ACCEPT'

config rule
        option name 'Allow-ICMPv6-Input'
        option src 'wan'
        option proto 'icmp'
        list icmp_type 'echo-request'
        list icmp_type 'echo-reply'
        list icmp_type 'destination-unreachable'
        list icmp_type 'packet-too-big'
        list icmp_type 'time-exceeded'
        list icmp_type 'bad-header'
        list icmp_type 'unknown-header-type'
        list icmp_type 'router-solicitation'
        list icmp_type 'neighbour-solicitation'
        list icmp_type 'router-advertisement'
        list icmp_type 'neighbour-advertisement'
        option limit '1000/sec'
        option family 'ipv6'
        option target 'ACCEPT'

config rule
        option name 'Allow-ICMPv6-Forward'
        option src 'wan'
        option dest '*'
        option proto 'icmp'
        list icmp_type 'echo-request'
        list icmp_type 'echo-reply'
        list icmp_type 'destination-unreachable'
        list icmp_type 'packet-too-big'
        list icmp_type 'time-exceeded'
        list icmp_type 'bad-header'
        list icmp_type 'unknown-header-type'
        option limit '1000/sec'
        option family 'ipv6'
        option target 'ACCEPT'

config rule
        option name 'Allow-IPSec-ESP'
        option src 'wan'
        option dest 'lan'
        option proto 'esp'
        option target 'ACCEPT'

config rule
        option name 'Allow-ISAKMP'
        option src 'wan'
        option dest 'lan'
        option dest_port '500'
        option proto 'udp'
        option target 'ACCEPT'

config include 'nodogsplash'
        option type 'script'
        option path '/usr/lib/nodogsplash/restart.sh'

config rule
        option name 'Allow-TollGate-Protocol'
        option src 'wan'
        option proto 'tcp'
        option dest_port '2121'
        option target 'ACCEPT'

root@OpenWrt:~# 
```

This is the state of the config files after flashing the router with a new openwrt image and letting @/files/etc/uci-defaults/95-random-lan-ip run. Unfortunately, I don't receive an IP address from the router when trying to connect to `c03rad0r-UXZB-2.4GHz`.

What could the reason be? Could it be due to the fact that we didn't set up `br-private` yet?


Terminal output:
```
root@OpenWrt:~# cat /etc/config/network | grep "private"
root@OpenWrt:~# cat /etc/config/network | grep "br"
        option name 'br-lan'
        option type 'bridge'
        option device 'br-lan'
        option broadcast '10.14.226.255'
```

free-as-in-beer-for-routers-owner/setup_private_ssid_v2.sh:7-17
```

# --- Network Configuration ---
uci set network.private=interface
uci set network.private.proto='static'
uci set network.private.device='br-private'
uci set network.private.ipaddr='10.92.113.1'
uci set network.private.netmask='255.255.255.0'

uci set network.private_bridge=device
uci set network.private_bridge.type='bridge'
uci set network.private_bridge.name='br-private'
```

------------------------------------

Debugging attempt 1

I ran @/free-as-in-beer-for-routers-owner/setup_private_ssid_v2.sh and rebooted the router. This created a network called TollGate-Admin which was still managed by nodogsplash after rebooting the router. However, I now get an IP address from `c03rad0r-UXZB-2.4GHz` and I'm able to access the internet without hitting the captive portal. When connecting to `TollGate-UXZB-2.4GHz`, I hit the captive portal as expected before getting access to the internet.

root@OpenWrt:/tmp# ./setup_private_ssid_v2.sh 
Setting up private SSID...
udhcpc: started, v1.36.1
udhcpc: broadcasting discover
udhcpc: no lease, failing
udhcpc: started, v1.36.1
udhcpc: broadcasting discover
udhcpc: no lease, failing
Private SSID setup complete. Please test your connection.
root@OpenWrt:/tmp# reboot
root@OpenWrt:/tmp# Connection to 10.14.226.1 closed by remote host.
Connection to 10.14.226.1 closed.


Now everything is as expected apart from `TollGate-Admin`. Below is the new state of my config files:


Terminal output:
```
root@OpenWrt:~# cat /etc/config/wireless 

config wifi-device 'radio0'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi'
        option band '2g'
        option channel '1'
        option htmode 'HE20'
        option disabled '0'

config wifi-iface 'default_radio0'
        option device 'radio0'
        option network 'lan'
        option mode 'ap'
        option ssid 'TollGate-UXZB-2.4GHz'
        option encryption 'none'
        option name 'tollgate_2g_open'
        option disabled '0'

config wifi-device 'radio1'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi+1'
        option band '5g'
        option channel '44'
        option htmode 'VHT80'
        option disabled '0'
        option cell_density '0'

config wifi-iface 'default_radio1'
        option device 'radio1'
        option network 'lan'
        option mode 'ap'
        option ssid 'TollGate-UXZB-5GHz'
        option encryption 'none'
        option name 'tollgate_5g_open'
        option disabled '0'

config wifi-iface 'wifinet0'
        option device 'radio0'
        option network 'private'
        option mode 'ap'
        option ssid 'c03rad0r-UXZB-2.4GHz'
        option encryption 'psk2+ccmp'
        option key 'securepassword123'
        option disabled '0'

config wifi-iface 'wifinet1'
        option device 'radio1'
        option network 'private'
        option mode 'ap'
        option ssid 'c03rad0r-UXZB-5GHz'
        option encryption 'psk2+ccmp'
        option key 'securepassword123'
        option disabled '0'

config wifi-iface 'wifinet4'
        option device 'radio1'
        option mode 'sta'
        option network 'wwan'
        option ssid 'EnterSSID-5GHz'
        option encryption 'psk2'
        option key 'c03rad0r123!'

root@OpenWrt:~# cat /etc/config/wireless 

config wifi-device 'radio0'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi'
        option band '2g'
        option channel '1'
        option htmode 'HE20'
        option disabled '0'

config wifi-iface 'default_radio0'
        option device 'radio0'
        option network 'lan'
        option mode 'ap'
        option ssid 'TollGate-UXZB-2.4GHz'
        option encryption 'none'
        option name 'tollgate_2g_open'
        option disabled '0'

config wifi-device 'radio1'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi+1'
        option band '5g'
        option channel '44'
        option htmode 'VHT80'
        option disabled '0'
        option cell_density '0'

config wifi-iface 'default_radio1'
        option device 'radio1'
        option network 'lan'
        option mode 'ap'
        option ssid 'TollGate-UXZB-5GHz'
        option encryption 'none'
        option name 'tollgate_5g_open'
        option disabled '0'

config wifi-iface 'wifinet0'
        option device 'radio0'
        option network 'private'
        option mode 'ap'
        option ssid 'c03rad0r-UXZB-2.4GHz'
        option encryption 'psk2+ccmp'
        option key 'securepassword123'
        option disabled '0'

config wifi-iface 'wifinet1'
        option device 'radio1'
        option network 'private'
        option mode 'ap'
        option ssid 'c03rad0r-UXZB-5GHz'
        option encryption 'psk2+ccmp'
        option key 'securepassword123'
        option disabled '0'

config wifi-iface 'wifinet4'
        option device 'radio1'
        option mode 'sta'
        option network 'wwan'
        option ssid 'EnterSSID-5GHz'
        option encryption 'psk2'
        option key 'c03rad0r123!'

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

root@OpenWrt:~# cat /etc/config/network 

config interface 'loopback'
        option device 'lo'
        option proto 'static'
        option ipaddr '127.0.0.1'
        option netmask '255.0.0.0'

config globals 'globals'
        option ula_prefix 'fdaa:bdc9:22ad::/48'

config device
        option name 'br-lan'
        option type 'bridge'
        list ports 'eth1'

config interface 'lan'
        option device 'br-lan'
        option proto 'static'
        option ipaddr '10.14.226.1'
        option netmask '255.255.255.0'
        option ip6assign '60'
        list dns '1.1.1.1'
        list dns '1.0.0.1'
        option domain 'lan'
        option broadcast '10.14.226.255'

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

root@OpenWrt:~# cat /etc/config/firewall

config defaults
        option syn_flood '1'
        option input 'REJECT'
        option output 'ACCEPT'
        option forward 'REJECT'

config zone
        option name 'lan'
        option input 'ACCEPT'
        option output 'ACCEPT'
        option forward 'ACCEPT'
        list network 'lan'

config zone
        option name 'wan'
        option input 'REJECT'
        option output 'ACCEPT'
        option forward 'REJECT'
        option masq '1'
        option mtu_fix '1'
        list network 'wan'
        list network 'wan6'
        list network 'wwan'

config forwarding
        option src 'lan'
        option dest 'wan'

config rule
        option name 'Allow-DHCP-Renew'
        option src 'wan'
        option proto 'udp'
        option dest_port '68'
        option target 'ACCEPT'
        option family 'ipv4'

config rule
        option name 'Allow-Ping'
        option src 'wan'
        option proto 'icmp'
        option icmp_type 'echo-request'
        option family 'ipv4'
        option target 'ACCEPT'

config rule
        option name 'Allow-IGMP'
        option src 'wan'
        option proto 'igmp'
        option family 'ipv4'
        option target 'ACCEPT'

config rule
        option name 'Allow-DHCPv6'
        option src 'wan'
        option proto 'udp'
        option dest_port '546'
        option family 'ipv6'
        option target 'ACCEPT'

config rule
        option name 'Allow-MLD'
        option src 'wan'
        option proto 'icmp'
        option src_ip 'fe80::/10'
        list icmp_type '130/0'
        list icmp_type '131/0'
        list icmp_type '132/0'
        list icmp_type '143/0'
        option family 'ipv6'
        option target 'ACCEPT'

config rule
        option name 'Allow-ICMPv6-Input'
        option src 'wan'
        option proto 'icmp'
        list icmp_type 'echo-request'
        list icmp_type 'echo-reply'
        list icmp_type 'destination-unreachable'
        list icmp_type 'packet-too-big'
        list icmp_type 'time-exceeded'
        list icmp_type 'bad-header'
        list icmp_type 'unknown-header-type'
        list icmp_type 'router-solicitation'
        list icmp_type 'neighbour-solicitation'
        list icmp_type 'router-advertisement'
        list icmp_type 'neighbour-advertisement'
        option limit '1000/sec'
        option family 'ipv6'
        option target 'ACCEPT'

config rule
        option name 'Allow-ICMPv6-Forward'
        option src 'wan'
        option dest '*'
        option proto 'icmp'
        list icmp_type 'echo-request'
        list icmp_type 'echo-reply'
        list icmp_type 'destination-unreachable'
        list icmp_type 'packet-too-big'
        list icmp_type 'time-exceeded'
        list icmp_type 'bad-header'
        list icmp_type 'unknown-header-type'
        option limit '1000/sec'
        option family 'ipv6'
        option target 'ACCEPT'

config rule
        option name 'Allow-IPSec-ESP'
        option src 'wan'
        option dest 'lan'
        option proto 'esp'
        option target 'ACCEPT'

config rule
        option name 'Allow-ISAKMP'
        option src 'wan'
        option dest 'lan'
        option dest_port '500'
        option proto 'udp'
        option target 'ACCEPT'

config rule
        option name 'Allow-TollGate-Protocol'
        option src 'wan'
        option proto 'tcp'
        option dest_port '2121'
        option target 'ACCEPT'

config zone 'private_zone'
        option name 'private'
        option network 'private'
        option input 'ACCEPT'
        option output 'ACCEPT'
        option forward 'ACCEPT'

config forwarding 'private_forwarding'
        option src 'private'
        option dest 'wan'

config rule 'tollgate_in'
        option name 'Allow-TollGate-In'
        option src 'lan'
        option proto 'tcp'
        option dest_port '2121'
        option target 'ACCEPT'

config rule 'relay_in'
        option name 'Allow-Relay-In'
        option src 'lan'
        option proto 'tcp'
        option dest_port '4242'
        option target 'ACCEPT'

root@OpenWrt:~# 
```

Please help me to understand what was wrong with @/files/etc/uci-defaults/95-random-lan-ip and please fix it. 