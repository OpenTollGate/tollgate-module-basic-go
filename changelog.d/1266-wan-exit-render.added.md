- **Render a paid exit's nftables policy for an ordinary IP network.** The
  alternative enforcement point to a FIPS tunnel: deny by default, one carve-out
  per paid peer with its own counter, every rule scoped to both interfaces, and the
  output parsed by `nft -c` so a rule the kernel refuses fails the build.
  ([#1266](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1266))
