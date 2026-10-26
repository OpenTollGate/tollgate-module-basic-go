- **Multi-WAN bonding has a decision record: a bonded uplink is a TollGate upstream
  peer.** `docs/architecture/multi-wan-bonding-decision.md` adopts the multi-hop
  reading — the remote de-aggregation server is an upstream TollGate that the
  router pays through the existing reseller path, so a bonding server needs no
  accounts and no subscriptions. It pins the four things three repositories need
  to agree on: the **tag vocabulary** (`capability=bonding`, `bonding=mptcp`,
  `endpoint=<host:port>`, `accounting=tunnel-bytes`, added to kind 10021 with no
  new event kind), the **metering boundary** (the server's tunnel-side bytes, on
  the `nft` counter plus a per-client `quota` plus a `tc` floor), the **identity
  model** (the advertisement's signing pubkey, with `host:port` as the transport
  address only), and the **enforcement semantics** (default-deny metered chains,
  fail-closed capability reads, and `tips` explicitly rejected as the activation
  gate because nothing consumes it). It records what is deliberately out of
  scope — no specification PR, no PR to the Rust reference, no vendored
  `bondingshouldbefree` (GPL-2.0-only), and no "2×" or failover claims — and it
  leaves four questions open rather than asserting them: whether one reassembled
  MPTCP connection presents one conntrack tuple, the magnitude of retransmission
  over-count, `EOPNOTSUPP` on per-key dynamic quota maps, and state durability
  across reboot. The decision is recorded before the three repository changes so
  the vocabulary is text the server and test repositories can cite.
