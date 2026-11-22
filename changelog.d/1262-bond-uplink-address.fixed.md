- **The MPTCP bond receives an uplink's own address, not its gateway.**
  `NetworkEvent.Address` now carries the address that appeared or vanished (and
  the first IPv4 for link events), and the detector passes it to the bond sink.
  Previously `GatewayIP` was passed as the endpoint address, so a DHCP renewal
  could register the router's next hop instead of the interface. An up event
  without an IPv4 address now waits for the address event rather than inventing
  one ([#1262](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1262)).
