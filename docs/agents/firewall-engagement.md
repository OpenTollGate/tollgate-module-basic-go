# Firewall, nftables, and topology-naming rules of engagement

Topic doctrine split out of `AGENTS.md` (#920) so firewall-incident rules
grow in their own file instead of appending to the shared core. Indexed in
AGENTS.md's "Topic doctrine" section — this file is the engagement rulebook
for anything touching `/etc/nftables.d/`, interface names, or the firewall
reload path.

Every rule here was learned from a bench-verified failure on the 0.6.0
train (#754, #755, #756, #757, PR #782's first design, and the 2026-10-09
stop-ship reviews). If a change touches `/etc/nftables.d/`, interface
names, or anything that reloads the firewall, this file governs it.
These are the OPERATIONAL rules; the structural rules — one declaration
per fact, fenced duplication, glob discovery — are the environment-truth
doctrine in `AGENTS.md`, and the two cite each other where they meet.

- **Accept is not final across base chains.** In nftables only `drop` and
  `reject` terminate; a packet accepted by one base chain still traverses
  later base chains on the same hook (nftables wiki, "Configuring
  chains"). An accept in a separate earlier-priority chain can therefore
  NEVER shield a flow from a later chain's reject — a fully green
  render-level contract suite hid exactly this bug in PR #782's first
  design (a −2 accept chain "ahead of" the −1 reject), and only a live
  dataplane rig caught it. To exempt flows from a terminal rule: put the
  accepts in the SAME chain ahead of it, or set a mark an earlier rule in
  that chain already honors.
- **Never install firewall rules at runtime** (`nft add/insert`): `fw4
  reload` rebuilds the ruleset from files and silently wipes them.
  Firewall state lives only in `/etc/nftables.d/*.nft` files (fragments
  rewritten by a renderer on convergence are the sanctioned pattern).
- **No interface-name literals in rule lines — shipped or GENERATED.**
  fw4 includes `/etc/nftables.d/*.nft` inside `table inet fw4` in lexical
  order, so `$tg_portal_if` / `$tg_private_if` from
  `00-tollgate-defs.nft` (re-rendered from the router's own config,
  #757) are in scope for every later include. Generated fragments must
  use the defines too. The fencing contract for literals — static and
  generated fragments alike are asserted against the one port/interface
  table — is the "Firewall fragments" rule in `AGENTS.md`'s
  environment-truth doctrine; the known open instance (the Go-side
  `34-admin-access-scope.nft` generator in
  `src/cli/operator_settings.go` emitting literal bridge names) is the
  #796 F2 backlog item, and #601's planned static `33-` fragment must
  follow the same rule.
- **Same-priority base chains have unspecified order.** NDS's
  `ip filter FORWARD` (iptables-nft) and fw4's `inet fw4 forward` both
  sit at priority `filter`; the whole enforcement bridge relies on their
  relative registration order (see `20-nds-enforce.nft`'s header). Never
  add a new base chain at a priority where cross-chain ordering matters —
  pick an earlier/later priority deliberately.
- **`fw4 reload` procd-restarts nodogsplash** and wipes its runtime
  trust/auth state (bench-verified on 25.12). Any change that adds or
  widens a reload trigger must state what NDS state dies, what restores
  it (keepalive contract), and how long customers are interrupted.
- **"lan" is three namespaces, not one.** (The declared bridge
  vocabulary is the "Network and interface names" rule in `AGENTS.md`;
  this bullet is about the rename hazards.) The bridge DEVICE
  (`br-lan`),
  the network SECTION (`lan`), and the firewall ZONE (`lan`) — plus
  `dhcp.lan` and dnsmasq's `local=/lan/` — are independent uci objects
  that share a string by convention only. Renaming one does not rename
  the others: fw4 skips a renamed zone ref with **exit 0** and no syslog
  (#755), setup used to rebind APs to a deleted `network.lan` (#756),
  and netifd regenerates `/etc/config/wireless` bound to `lan` after
  the file is deleted (caught by the topology self-check, 2026-10-09).
  Topology-touching code resolves names from the router's own config
  (the `resolve_*` helpers in `99-tollgate-setup`), warns loudly on
  every non-stock decision, and never silently skips.
- **Go-side interface literals are the same bug class outside the .nft
  files.** Find them by grep, not by list — an enumeration here rots the
  day a new site lands (the glob rule in `AGENTS.md`'s doctrine). Grep
  `grep -rn '"br-lan"\|"br-private"\|"br-mgmt"' src/` before touching
  interface naming; as of 2026-10-09 that reaches the admin-scope
  generator (`src/cli/operator_settings.go`), `src/identity/identity.go`
  (`StandardInterfaces`), the `ignore_interfaces` default in
  `config_manager`, and `src/upstream_detector`'s bridge-exclusion lists
  — verify against the live grep, not this sentence. The unfenced ones
  are the #796 fence backlog's F1 class.
