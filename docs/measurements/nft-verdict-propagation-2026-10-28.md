# nft verdict propagation across base chains — 2026-10-28

**Status: the mechanism asserted in `20-nds-enforce.nft`'s header from #283 is
REFUTED. The symptom it was invented to explain is not explained by this
record, and remains unexplained.**

Rig: `nft-verdict-propagation.sh` (this directory). Host, not a router:
`nftables 1.0.9`, kernel `7.0.0-38-generic`. Three network namespaces — a
client, a forwarding router, a server — so every echo request *and* every echo
reply crosses the router's `forward` hook: 3 pings = 6 forwarded packets.
Counters read from one `nft list ruleset` dump per trial, after the flow.

## What was being tested

The header of `packaging/files/etc/nftables.d/20-nds-enforce.nft` claimed, from
commit `f97435bc` (#283) until this change:

> On OpenWrt 24.10, fw4's inet fw4 forward chain (priority 0) accepts forwarded
> traffic before the ip filter FORWARD chain (same priority) ever sees it —
> leaving NDS's enforcement chain dead (0 packets).

#283's body made the mechanism explicit: *"the enforcement chain in ip filter
FORWARD (priority 0) is shadowed by fw4's inet fw4 forward (same priority, inet
family processed first)"*. That is a testable claim about verdict propagation,
and it is false.

## Results

| # | Shape | Counter behind the verdict | Verdict on the claim |
|---|---|---|---|
| A | `accept` @prio 0 (inet table) registered **first**; counting chain @prio 0 (ip table) behind | **6/6** — both chains counted every packet | accept not final |
| B | Same two chains, registration order **reversed** | **6/6** again | accept not final; order did not matter here |
| C | `accept` @prio **−1**; counting chain @prio 0 behind | **6/6** | accept not final across priorities too |
| D | `drop` @prio **−1**; counting chain @prio 0 behind | **3**, then **0** | only a terminal verdict starves a later chain |
| E | One chain: `accept` rule, then a counting rule | first rule 6, second rule **0** | accept ends **that chain** |
| F | One chain: `reject` rule, then a counting rule | first rule 3, second rule **0** | reject ends that chain as well |
| G | Shipped shape: `reject` @−1 ahead of a counter @0, unmarked flow | reject **3**, later chain **0** | the zero is the terminal reject, not an accept |
| H | Shipped shape, mark-accept cannot match (nothing marks the flow) | mark-accept **0**, reject **3**, later chain **0** | same |
| I | Full NDS analogue: mark set @−150, `accept` @−1, counter @0 behind | mark-accept **3**, reject **3**, later chain **3** | an accepted flow still traverses the later chain |

Trials A and B put the two base chains at the *same* priority in the *same*
hook and swapped which table was created first: 6/6 both ways. Whatever order
the kernel chose, an `accept` in one base chain did not stop the packet
reaching the other. Trials C and D are the control pair — identical shape,
verdict as the only variable.

**One rig artifact, stated so it is not over-read:** in trial I the priority −1
chain's reject also reads 3, because the rig's mark rule matches on
`saddr 10.9.0.1`, so replies from the server are unmarked and fall through to
the reject. A real router does not behave that way — the shipped rules are all
scoped `iifname $tg_portal_if`, and a reply arriving from WAN never matches
them. The number that matters in trial I is the priority-0 chain's **3**: the
requests that the −1 chain accepted still traversed it.

## Why this refutes the header rather than merely doubting it

The header's mechanism has three parts: (1) fw4's chain runs first, (2) it
accepts the forwarded packet, (3) a preceding `accept` therefore hides the
packet from the same-priority `ip filter FORWARD` chain. Part (3) is what A, B
and C measure directly and it does not hold. Part (2) is also inert: in trial I
a flow that a priority −1 chain *accepted* still arrived at the priority 0
chain. Even granting parts (1) and (2), the conclusion does not follow.

## Independent authority

The nftables wiki, ["Configuring chains"](https://wiki.nftables.org/wiki-nftables/index.php/Configuring_chains):

> NOTE: If a packet is accepted and there is another chain, bearing the same
> hook type and with a later priority, then the packet will subsequently
> traverse this other chain. Hence, an accept verdict - be it by way of a rule
> or the default chain policy - isn't necessarily final. However, the same is
> not true of packets that are subjected to a drop verdict.

and, on the point the rig's A/B pair cannot settle:

> It's possible to give two base chains the same priority, but there is no
> guaranteed evaluation order of base chains with identical priority that are
> attached to the same hook location.

So the ordering part of the header was never even a sound premise: NDS's
`ip filter FORWARD` and fw4's `inet fw4 forward` both sit at priority `filter`,
and *neither* order may be assumed on any box. The header presented an
unspecified ordering as a measured fact.

## What this does NOT settle

**The original symptom is still unexplained.** #283 reported that authenticated
clients were not actually gated and that the NDS chains read 0 packets, and no
counter dump, `fw4 print`, or per-chain capture from that bench box was ever
archived with the report — the reading is remembered, not recorded. Nothing
here reproduces it or proposes a cause for it.

Two candidates are live and this rig does not choose between them:

- **`$tg_portal_if` did not resolve to the traffic's ingress interface.** Every
  rule in the shipped chain is scoped `iifname $tg_portal_if`. #289 is the
  measured precedent for exactly this failure mode: the same fragment's
  `oifname { "eth0", "phy0-sta0" }` matched no interface on a non-GL-MT3000
  arch, and #289 reports *"The reject rule counter showed 0 packets despite
  active pre-auth traffic"* on x86_64 QEMU — an iifname/oifname mismatch
  producing zero counters on live traffic, with no accept and no fw4 involved.
  `$tg_portal_if` comes from a renderer (#757) that did not exist in #283.
- **fw4's own policy accepted before the fragment saw the packet** — not
  because `accept` is terminal (it is not), but because a *later* base chain
  still cannot rescue a flow, and fw4's rendered zone rules are not the only
  thing in that hook. This is the mechanism the fragment was *supposed* to
  address; it is not established for the #283 observation either.

Neither is asserted. Both are written down as hypotheses because a confident
wrong story is what this document exists to retire.

## The trap this rig did find, which matters operationally

Trials G and H: the shipped chain carries a **terminal `reject` at priority
−1** — ahead of NDS's priority 0 `ip filter FORWARD`, precisely because the
fragment moved itself earlier to stop being shadowed. So on any build carrying
this fragment, a flow the fragment rejects never reaches NDS's chain and NDS's
counters read **zero**. That zero is *consistent with the fragment working*.
It has been read as evidence of a fault in the past; it is not evidence of one.
Diagnose this gate from `nft list chain inet fw4 nds_enforce_forward` — its own
counters — and never from NDS's.

## The experiment that would settle the original question

Not run here: it needs the router, not a host. Minimum to be answerable:

1. **One known flow.** One client MAC, one protocol, one destination port. A
   flapping multi-flow capture cannot attribute counters.
2. **One ruleset snapshot** taken *after* the flow: `nft list ruleset`, complete,
   plus `fw4 print` so the chain set and priorities fw4 actually rendered are
   on the record.
3. **Counter-only probe chains** — `counter` with no verdict, so they cannot
   perturb the path — at priorities **−150, −1, 0 and +1**, in **both** the
   `ip` and `inet` families, on the `forward` hook. A probe that reads 0 while
   its neighbours read non-zero localises the decision to the gap between them.
4. **The mark, observed rather than assumed:** the same probes match
   `meta mark & 0x00030000` so "NDS marked it correctly" is a counter, not a
   claim.
5. **Per build lane:** the 24.10/ipk and 25.12/apk lanes do not necessarily
   render the same ruleset, and `$tg_portal_if` is resolved per box.
6. **Both directions:** requests and replies take different verdicts here (trial
   I) — count them separately or the numbers mislead.

Archive the dumps with the result. The gap this card is fixing is not that the
#283 conclusion was wrong; it is that a conclusion was recorded without the
measurement, and then repeated as fact in a shipped file, a renderer, a
contract test and the doctrine.
