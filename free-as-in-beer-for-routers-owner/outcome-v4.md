
Terminal output:
```
c03rad0r@RomblonMimaropa:~/tollgate-module-basic-go$ git diff main files/etc/uci-defaults/99-tollgate-setup
```

Please use the above diff command to see what has changed since we branched off of main. 


Terminal output:
```
-# Commit wireless changes
-uci commit wireless
-log_message "Committed wireless configuration"
-
-# 5. Configure NoDogSplash
-log_message "Configuring NoDogSplash"
-if ! uci -q get nodogsplash.@nodogsplash[0]; then
-    # Create nodogsplash section if it doesn't exist
-    uci add nodogsplash nodogsplash
-    log_message "Created new nodogsplash configuration"
-else
-    log_message "NoDogSplash configuration already exists"
-fi
-
-# Set basic NoDogSplash configuration with matching gateway name
-uci_safe_set nodogsplash @nodogsplash[0] enabled '1'
-uci_safe_set nodogsplash @nodogsplash[0] gatewayname "${GATEWAY_NAME} Portal"
-uci_safe_set nodogsplash @nodogsplash[0] gatewayinterface 'br-lan'
-
-# Ensure TollGate protocol port is allowed
-uci add_list nodogsplash.@nodogsplash[0].users_to_router='allow tcp port 2121'
-uci add_list nodogsplash.@nodogsplash[0].users_to_router='allow tcp port 8080'
-uci commit nodogsplash
-log_message "NoDogSplash configuration completed"
-
-# 6. Add TollGate port to firewall (if not in firewall-tollgate)
-log_message "Checking for TollGate protocol port in firewall rules"
-if ! grep -q "port 2121" /etc/config/firewall-tollgate 2>/dev/null; then
-    # Add rule directly to main firewall if not in tollgate-specific file
-    uci add firewall rule
-    uci_safe_set firewall @rule[-1] name 'Allow-TollGate-Protocol'
-    uci_safe_set firewall @rule[-1] src 'wan'
-    uci_safe_set firewall @rule[-1] proto 'tcp'
-    uci_safe_set firewall @rule[-1] dest_port '2121'
-    uci_safe_set firewall @rule[-1] target 'ACCEPT'
-    uci commit firewall
-    log_message "Added firewall rule for TollGate protocol port 2121"
-else
-    log_message "TollGate protocol port already in firewall rules"
-fi
-
```

We seem to have removed quite a lot of things including the firewall and nodogsplash configuration. Please explain the concequences to me and justify the removal of each of the things that was removed. Please add things back if their removal is hard to justify. 

Below is the state of our config files after flashing with @/files/etc/uci-defaults/99-tollgate-setup 


Terminal output:
```
root@OpenWrt:~# cat /etc/config/wireless 

config wifi-device 'radio0'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi'
        option band '2g'
        option channel '1'
        option htmode 'HE20'
        option disabled '1'

config wifi-iface 'default_radio0'
        option device 'radio0'
        option network 'lan'
        option mode 'ap'
        option ssid 'OpenWrt'
        option encryption 'none'

config wifi-device 'radio1'
        option type 'mac80211'
        option path 'platform/soc/18000000.wifi+1'
        option band '5g'
        option channel '36'
        option htmode 'HE80'
        option disabled '1'

config wifi-iface 'default_radio1'
        option device 'radio1'
        option network 'lan'
        option mode 'ap'
        option ssid 'OpenWrt'
        option encryption 'none'

root@OpenWrt:~# cat /etc/config/network 

config interface 'loopback'
        option device 'lo'
        option proto 'static'
        option ipaddr '127.0.0.1'
        option netmask '255.0.0.0'

config globals 'globals'
        option ula_prefix 'fd88:a2d6:2d98::/48'

config device
        option name 'br-lan'
        option type 'bridge'
        list ports 'eth1'

config interface 'lan'
        option device 'br-lan'
        option proto 'static'
        option ipaddr '172.23.69.1'
        option netmask '255.255.255.0'
        option ip6assign '60'
        list dns '1.1.1.1'
        list dns '1.0.0.1'
        option domain 'lan'
        option broadcast '172.23.69.255'

config interface 'wan'
        option device 'eth0'
        option proto 'dhcp'

config interface 'wan6'
        option device 'eth0'
        option proto 'dhcpv6'

root@OpenWrt:~# cat /etc/config/firewall

config defaults
        option syn_flood '1'
        option input 'REJECT'
        option output 'ACCEPT'
        option forward 'REJECT'

config zone
        option name 'lan'
        list network 'lan'
        option input 'ACCEPT'
        option output 'ACCEPT'
        option forward 'ACCEPT'

config zone
        option name 'wan'
        list network 'wan'
        list network 'wan6'
        option input 'REJECT'
        option output 'ACCEPT'
        option forward 'REJECT'
        option masq '1'
        option mtu_fix '1'

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

root@OpenWrt:~# cat /etc/config/nodogsplash 

# The options available here are an adaptation of the settings used in nodogsplash.conf.
# See https://github.com/nodogsplash/nodogsplash/blob/master/resources/nodogsplash.conf

config nodogsplash
  # Set to 0 to disable nodogsplash
  option enabled 1

  # Set to 0 to disable hook that makes nodogsplash restart when the firewall restarts.
  # This hook is needed as a restart of Firewall overwrites nodogsplash iptables entries.
  option fwhook_enabled '1'

  # WebRoot
  # Default: /etc/nodogsplash/htdocs
  #
  # The local path where the splash page content resides.
  # ie. Serve the file splash.html from this directory
  #option webroot '/etc/nodogsplash/htdocs'

  # Use plain configuration file
  #option config '/etc/nodogsplash/nodogsplash.conf'

  # Use this option to set the device nodogsplash will bind to.
  # The value may be an interface section in /etc/config/network or a device name such as br-lan.
  option gatewayinterface 'br-lan'

  # GatewayPort
  # Default: 2050
  #
  # Nodogsplash's own http server uses gateway address as its IP address.
  # The port it listens to at that IP can be set here; default is 2050.
  #
  #option gatewayport '2050'


  option gatewayname 'OpenWrt Nodogsplash'
  option maxclients '250'

  # Enables debug output (0-3)
  #option debuglevel '1'

  # Client timeouts in minutes
  option preauthidletimeout '30'
  option authidletimeout '120'
  # Session Timeout is the interval after which clients are forced out (a value of 0 means never)
  option sessiontimeout '1200'

  # The interval in seconds at which nodogsplash checks client timeout status
  option checkinterval '600'

  # Enable BinAuth Support.
  # If set, a program is called with several parameters on authentication (request) and deauthentication.
  # Request for authentication:
  # $<BinAuth> auth_client <client_mac> '<username>' '<password>'
  #
  # The username and password values may be empty strings and are URL encoded.
  # The program is expected to output the number of seconds the client
  # is to be authenticated. Zero or negative seconds will cause the authentification request
  # to be rejected. The same goes for an exit code that is not 0.
  # The output may contain a user specific download and upload limit in KBit/s:
  # <seconds> <upload> <download>
  #
  # Called on authentication or deauthentication:
  # $<BinAuth> <*auth|*deauth> <incoming_bytes> <outgoing_bytes> <session_start> <session_end>
  #
  # "client_auth": Client authenticated via this script.
  # "client_deauth": Client deauthenticated by the client via splash page.
  # "idle_deauth": Client was deauthenticated because of inactivity.
  # "timeout_deauth": Client was deauthenticated because the session timed out.
  # "ndsctl_auth": Client was authenticated manually by the ndsctl tool.
  # "ndsctl_deauth": Client was deauthenticated by the ndsctl tool.
  # "shutdown_deauth": Client was deauthenticated by Nodogsplash terminating.
  #
  # Values session_start and session_start are in seconds since 1970 or 0 for unknown/unlimited.
  #
  #option binauth '/bin/myauth.sh'
  # Enable PreAuth Support.
  #
  # A simple login script is provided in the package.
  # This generates a login page asking for usename and email address.
  # User logins are recorded in the log file /tmp/ndslog.log
  # Details of how the script works are contained in comments in the script itself.
  #
  # The Preauth program will output html code that will be served to the client by NDS
  # Using html GET the Preauth program may call:
  # /nodogsplash_preauth/ to ask the client for more information
  # or
  # /nodogsplash_auth/ to authenticate the client
  #
  # The Preauth program should append at least the client ip to the query string
  # (using html input type hidden) for all calls to /nodogsplash_preauth/
  # It must also obtain the client token using ndsctl (or the original query string if fas_secure_enabled=0)
  # for NDS authentication when calling /nodogsplash_auth/
  #
  #option preauth '/usr/lib/nodogsplash/login.sh'

  # Your router may have several interfaces, and you
  # probably want to keep them private from the gatewayinterface.
  # If so, you should block the entire subnets on those interfaces, e.g.:
  #list authenticated_users 'block to 192.168.0.0/16'
  #list authenticated_users 'block to 10.0.0.0/8'

  # Typical ports you will probably want to open up.
  #list authenticated_users 'allow tcp port 22'
  #list authenticated_users 'allow tcp port 53'
  #list authenticated_users 'allow udp port 53'
  #list authenticated_users 'allow tcp port 80'
  #list authenticated_users 'allow tcp port 443'
  # Or for happy customers allow all
  list authenticated_users 'allow all'

  # For preauthenticated users to resolve IP addresses in their
  # initial request not using the router itself as a DNS server,
  # Leave commented to help prevent DNS tunnelling
  #list preauthenticated_users 'allow tcp port 53'
  #list preauthenticated_users 'allow udp port 53'

  # Allow ports for SSH/Telnet/DNS/DHCP/HTTP/HTTPS
  list users_to_router 'allow tcp port 22'
  list users_to_router 'allow tcp port 23'
  list users_to_router 'allow tcp port 53'
  list users_to_router 'allow udp port 53'
  list users_to_router 'allow udp port 67'
  list users_to_router 'allow tcp port 80'

  # MAC addresses that are / are not allowed to access the splash page
  # Value is either 'allow' or 'block'. The allowedmac or blockedmac list is used.
  #option macmechanism 'allow'
  #list allowedmac '00:00:C0:01:D0:0D'
  #list allowedmac '00:00:C0:01:D0:1D'
  #list blockedmac '00:00:C0:01:D0:2D'

  # MAC addresses that do not need to authenticate
  #list trustedmac '00:00:C0:01:D0:1D'

  # Nodogsplash uses specific HEXADECIMAL values to mark packets used by iptables as a bitwise mask.
  # This mask can conflict with the requirements of other packages such as mwan3, sqm etc
  # Any values set here are interpreted as in hex format.
  #
  # List: fw_mark_authenticated
  # Default: 30000 (0011|0000|0000|0000|0000 binary)
  #
  # List: fw_mark_trusted
  # Default: 20000 (0010|0000|0000|0000|0000 binary)
  #
  # List: fw_mark_blocked
  # Default: 10000 (0001|0000|0000|0000|0000 binary)
  #
  #option fw_mark_authenticated '30000'
  #option fw_mark_trusted '20000'
  #option fw_mark_blocked '10000'
```

I am no longer able to reach luci on `http://172.23.69.1:8080/` and I no longer see the `c03rad0r-xxxx-xxGHz` or the `TollGate-xxxx-xxGHz` SSIDs anymore. How can we resolve this?

