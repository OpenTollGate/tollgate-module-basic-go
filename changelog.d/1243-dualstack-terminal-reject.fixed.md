- **Make the captive-portal terminal reject family-neutral.** The enforced
  forward chain (`20-nds-enforce.nft`, and its renderer
  `tollgate-nds-preauth-render`) qualified every rule with `meta nfproto ipv4`,
  including the terminal `reject` that stops pre-authenticated traffic leaving
  the captive bridge. Forwarded IPv6 from `$tg_portal_if` therefore never
  reached the chain at all and fell through to fw4's lan→wan v6 accept. That is
  currently unreachable only because IPv6 is disabled globally (#874); the
  moment #783 re-enables it, a pre-authenticated client leaks exactly as the
  bench-verified #148/#783 bypass did. The mark rules stay IPv4-qualified
  deliberately — NoDogSplash 5.0.2 sets no IPv6 marks, so widening them would
  reject paying v6 clients too — and the terminal reject alone now carries the
  dual-stack guarantee via the family-agnostic `icmpx` reject type. The
  pristine-empty vs rendered-byte-identical contract is preserved, and the
  contract test gains a planted-drift case that fails if the reject is
  re-qualified ([#783](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/783)).
