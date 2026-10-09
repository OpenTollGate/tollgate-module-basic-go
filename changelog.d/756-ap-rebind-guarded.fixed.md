- **Setup never binds an AP to a network that does not exist, and never
  silently renames an operator-chosen SSID.** On a split-plane DUT whose
  config has no `network.lan`, the unconditional `network=lan` rebind bridged
  every AP to a ghost network — clients associate, DHCP dies, the portal is
  unreachable — and the staged operator SSID was silently renamed on top
  (bench-verified on rc1, exactly the verdict's repro). The binding is now
  decided: stock routers keep the byte-identical `lan`; a config without it
  adopts the network owning the NDS gatewayinterface (the portal plane); a
  config with neither preserves the section's binding or leaves it unbound —
  never a ghost, every non-stock decision logged. The SSID follows the
  one-way discipline of the hostname (#444): machine-shaped names
  (empty, the current derivation, `<anything>-<device code>`) re-derive from
  the stored code; an operator's name is preserved and said so (#756).
