
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
        TX errors 0  dropped 35 overruns 0  carrier 0  collisions 0

enp0s31f6: flags=4099<UP,BROADCAST,MULTICAST>  mtu 1500
        ether 98:fa:9b:38:71:a7  txqueuelen 1000  (Ethernet)
        RX packets 0  bytes 0 (0.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 0  bytes 0 (0.0 B)
        TX errors 0  dropped 2 overruns 0  carrier 0  collisions 0
        device interrupt 16  memory 0xec200000-ec220000  

lo: flags=73<UP,LOOPBACK,RUNNING>  mtu 65536
        inet 127.0.0.1  netmask 255.0.0.0
        inet6 ::1  prefixlen 128  scopeid 0x10<host>
        loop  txqueuelen 1000  (Local Loopback)
        RX packets 9805  bytes 27618498 (27.6 MB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 9805  bytes 27618498 (27.6 MB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

tailscale0: flags=4305<UP,POINTOPOINT,RUNNING,NOARP,MULTICAST>  mtu 1280
        inet6 fe80::61ca:9fc6:77b2:4472  prefixlen 64  scopeid 0x20<link>
        unspec 00-00-00-00-00-00-00-00-00-00-00-00-00-00-00-00  txqueuelen 500  (UNSPEC)
        RX packets 1  bytes 86 (86.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 192  bytes 76643 (76.6 KB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

veth313b4c6: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 fe80::f0c7:82ff:feeb:b200  prefixlen 64  scopeid 0x20<link>
        ether f2:c7:82:eb:b2:00  txqueuelen 0  (Ethernet)
        RX packets 3  bytes 126 (126.0 B)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 593  bytes 240050 (240.0 KB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

vethdb67123: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 fe80::2cf0:6bff:fe5e:55d1  prefixlen 64  scopeid 0x20<link>
        ether 2e:f0:6b:5e:55:d1  txqueuelen 0  (Ethernet)
        RX packets 4553  bytes 478652 (478.6 KB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 5014  bytes 1054891 (1.0 MB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

vethdea16c8: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet6 fe80::a8a2:77ff:fe63:e93  prefixlen 64  scopeid 0x20<link>
        ether aa:a2:77:63:0e:93  txqueuelen 0  (Ethernet)
        RX packets 4193  bytes 578042 (578.0 KB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 4897  bytes 694460 (694.4 KB)
        TX errors 0  dropped 0 overruns 0  carrier 0  collisions 0

wlp59s0: flags=4163<UP,BROADCAST,RUNNING,MULTICAST>  mtu 1500
        inet 10.92.112.199  netmask 255.255.255.0  broadcast 10.92.112.255
        inet6 fdb3:3231:8ed9::413  prefixlen 128  scopeid 0x0<global>
        inet6 fdb3:3231:8ed9:0:41a8:1f28:9f13:f0f  prefixlen 64  scopeid 0x0<global>
        inet6 fe80::e190:fde7:ee06:429a  prefixlen 64  scopeid 0x20<link>
        inet6 fdb3:3231:8ed9:0:470d:4d51:9b30:44f8  prefixlen 64  scopeid 0x0<global>
        ether b4:69:21:80:21:04  txqueuelen 1000  (Ethernet)
        RX packets 452658  bytes 616743739 (616.7 MB)
        RX errors 0  dropped 0  overruns 0  frame 0
        TX packets 122037  bytes 18575678 (18.5 MB)
        TX errors 0  dropped 16 overruns 0  carrier 0  collisions 0

c03rad0r@RomblonMimaropa:~/tollgate-module-basic-go$ ssh root@10.92.112.1


BusyBox v1.36.1 (2025-10-05 16:50:05 UTC) built-in shell (ash)

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

root@OpenWrt:~# cat /etc/config/network 

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

root@OpenWrt:~# cat /etc/config/nodogsplash 

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
```

I want to keep the open TollGate SSIDs, but I also want to create an additional password protected SSID for the owner of the router to connect to so that they can connect to their router with a password and their communication doesn't get intercepted by the captive portal (nodogsplash). 

I want to research options to ensure that the captive portal manages connectivity on the open SSID while not interfering with access to the gateway when users connect to the password protected SSID. 

Please ask me up to five questions for additional context. Then please make a design document tracking the things we already know and the things we need to find out before trying to implement this. 