- **Setup never binds an AP to a network that does not exist, never silently
  renames an operator-chosen SSID, and never silently opens an operator's
  AP.** On a split-plane DUT whose config has no `network.lan`, the
  unconditional `network=lan` rebind bridged every AP to a ghost network —
  clients associate, DHCP dies, the portal is unreachable — and the staged
  operator SSID was silently renamed on top (bench-verified on rc1, exactly
  the verdict's repro). The binding is now decided: stock routers keep the
  byte-identical `lan`; a config without it adopts the network owning the NDS
  gatewayinterface (the portal plane); a config with neither preserves the
  section's binding or leaves it unbound — never a ghost, every non-stock
  decision logged. The SSID follows the one-way discipline of the hostname
  (#444): machine-shaped names (empty, the current derivation,
  `<anything>-<device code>`) re-derive from the stored code; an operator's
  name is preserved and said so — and an adopted operator section keeps its
  encryption untouched (the one-way rewrite is about generated names, never
  credentials), while the module's own public APs stay open by contract. The
  postinst now runs a topology self-check: every enabled AP a member of an
  existing bridge, exactly one connected route per portal subnet — the
  silent-blackhole signatures — named loudly in syslog
  ([#786](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/786)).
