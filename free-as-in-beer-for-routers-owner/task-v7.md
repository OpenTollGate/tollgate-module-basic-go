
Terminal output:
```
root@OpenWrt:~# cat /etc/config/wireless 

config wifi-device 'radio0'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi'
        option band '2g'
        option channel '11'
        option htmode 'HT40'
        option disabled '0'
        option cell_density '0'

config wifi-iface 'default_radio0'
        option device 'radio0'
        option network 'lan'
        option mode 'ap'
        option ssid 'TollGate-8ZXF-2.4GHz'
        option encryption 'none'
        option name 'tollgate_2g_open'
        option disabled '0'

config wifi-device 'radio1'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi+1'
        option band '5g'
        option channel '36'
        option htmode 'HE80'
        option disabled '0'

config wifi-iface 'default_radio1'
        option device 'radio1'
        option network 'lan'
        option mode 'ap'
        option ssid 'TollGate-8ZXF-5GHz'
        option encryption 'none'
        option name 'tollgate_5g_open'
        option disabled '0'

config wifi-iface 'wifinet2'
        option device 'radio0'
        option mode 'sta'
        option network 'wwan'
        option ssid 'EnterSSID-2.4GHz'
        option encryption 'psk2'
        option key 'c03rad0r123!'

root@OpenWrt:~# cat /etc/config/network 

config interface 'loopback'
        option device 'lo'
        option proto 'static'
        option ipaddr '127.0.0.1'
        option netmask '255.0.0.0'

config globals 'globals'
        option ula_prefix 'fdb1:5ddf:9d33::/48'

config device
        option name 'br-lan'
        option type 'bridge'
        list ports 'eth1'

config interface 'lan'
        option device 'br-lan'
        option proto 'static'
        option ipaddr '172.16.127.1'
        option netmask '255.255.255.0'
        option ip6assign '60'
        list dns '1.1.1.1'
        list dns '1.0.0.1'
        option domain 'lan'
        option broadcast '172.16.127.255'

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

root@OpenWrt:~# cat /etc/config/nodogsplash 

config nodogsplash
        option enabled '1'
        option fwhook_enabled '1'
        option gatewayinterface 'br-lan'
        option gatewayname 'TollGate-8ZXF Portal'
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

As you can see above, the `c03rad0r-8ZXF-2.4GHz` and `c03rad0r-8ZXF-5GHz` SSIDs didn't get created. Please read @/free-as-in-beer-for-routers-owner/setup_private_ssid_v2.sh to identify the steps that we missed in @/files/etc/uci-defaults/99b-tollgate-setup-private-ssid .

