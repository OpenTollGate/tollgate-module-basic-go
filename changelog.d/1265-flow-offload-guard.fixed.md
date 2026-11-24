- **Disable flow offloading, which bypasses portal enforcement entirely.** Every
  access decision this package makes is one nftables base chain,
  `nds_enforce_forward`, hooked at `forward priority -1`. Flow offloading
  defeats that by design: once a flow reaches the software fast path or the
  hardware flow table its packets stop traversing the netfilter hooks, so the
  chain stops seeing them. `ndsctl deauth` — the module's only deauthorization
  mechanism — then cannot revoke an established flow (a client whose session
  expired keeps its TCP/QUIC sockets), and the byte counters behind
  `used >= allotment` stop advancing, so the meter stops metering the traffic it
  is charging for. Neither option was ever declared by this package, leaving
  both one LuCI toggle away from silently removing enforcement. Two new
  declarations now force `firewall.@defaults[0].flow_offloading` and
  `flow_offloading_hw` off — `setup_flow_offload_guard` in the setup driver and
  its install-time twin in `packaging/postinst` — with a drift fence requiring
  both copies to write the same axes, and planted-drift cases that fail if
  either axis, or the call site, is removed
  ([#1265](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1265)).
