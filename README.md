# Tollgate Module - tip01 (go)

This Tollgate module is a payment gateway that allows users to pay for internet access using Cashu tokens. It provides an HTTP server that handles payments and manages access control.

## Design Documents

The following design documents are available for the various components of this module:

* [src/config_manager/HLDD.md](src/config_manager/HLDD.md)
* [src/config_manager/LLDD.md](src/config_manager/LLDD.md)
* [src/janitor/HLDD.md](src/janitor/HLDD.md)
* [src/janitor/LLDD.md](src/janitor/LLDD.md)
* [src/HLDD.md](src/HLDD.md)
* [src/LLDD.md](src/LLDD.md)
* [src/integrating_modules.md](src/integrating_modules.md)

# Compile for ATH79 (GL-AR300 NOR)

```bash
cd ./src
env GOOS=linux GOARCH=mips GOMIPS=softfloat go build -o tip01 -trimpath -ldflags="-s -w"

# Hint: copy to connected router 
scp -O tip01 root@192.168.8.1:/tmp/tip01
```

# Compile for GL-MT3000

## Build

```bash
cd ./src
env GOOS=linux GOARCH=arm64 go build -o tollgate-basic -trimpath -ldflags="-s -w"

# Hint: copy to connected router 
scp -O tollgate-basic root@192.168.1.1:/usr/bin/tollgate-basic 
```

## Required Firewall rules 

First, test if the tip01 is up by going to your router's ip on port `2122`. You should get a JSON response with your IP and mac address.

Add to `/etc/config/firewall`:
```uci
config rule
	option name 'Allow-tip01-In'
	option src 'lan'
	option proto 'tcp'
	option dest_port '2122' # tip01 port
	option target 'ACCEPT'

config redirect
	option name 'TollGate - Nostr tip01 DNAT'
	option src 'lan'
	option dest 'lan'
	option proto 'tcp'
	option src_dip '192.168.21.21'
	option src_dport '2121'
	option dest_ip '192.168.X.X' # Router IP
	option dest_port '2122' # tip01 port
	option target 'DNAT'

config redirect
        option name 'TollGate - Nostr tip01 DNAT port'
        option src 'lan'
        option dest 'lan'
        option proto 'tcp'
        option src_dip '192.168.X.X' # Router IP
        option src_dport '2121'
        option dest_ip '192.168.X.X' # Router IP
        option dest_port '2122' # tip01 port
        option target 'DNAT'
```

Run `service firewall restart` to make changes go into effect.

To test the firewall rule, go to `192.168.21.21:2122`. You should be greeted with the same JSON.

## OpenNDS rules
**Prerequisite: OpenNDS is installed**

To allow unauthenticated clients to reach the tip01, we need to explicitly allow access.

Add to `/etc/config/opennds` under `config opennds`:
```uci
config opennds
    list users_to_router 'allow tcp port 2122' # tip01 port
    list preauthenticated_users 'allow tcp port 2122 to 192.168.21.21'
```

Run `service opennds restart` to make changes go into effect.

## License
This project is licensed under the GNU General Public License v3.0 - see the [LICENSE](LICENSE) file for details.
