# The Wired LAN Port's Role — `tollgate.lan_ports.role`

> **Status: Proposed (2026-09-27).** This record documents what a cable into a
> LAN port is *for*, as a setting, and what the setting deliberately cannot do.
> It implements the field
> [`lan-port-management-bridge-decision.md`](lan-port-management-bridge-decision.md)
> left open ("the later, separately carded feature … is out of scope here; D1's
> writer is written as one place that computes *which bridge the wired ports are
> on*, so that field becomes a parameter rather than a second implementation"),
> and it inherits that record's one-gate constraint in full. Acceptance is a
> maintainer action: the drafting account may not accept its own proposal, and
> nothing here is in force until a router has been measured with it.

## Context

### What the card asked for

The wired LAN ports' placement is currently hard-coded: `setup_mgmt_bridge` in
`packaging/files/etc/uci-defaults/99-tollgate-setup` MOVES the ports the base
image wrote on the captive bridge onto `br-mgmt`, always, so a cabled operator
reaches the administration surfaces before paying and stops sharing a layer-2
domain with the strangers on the open SSID (PR #601, D1-D8 of the decision
record above). That is the right default for the operator's own cable, and it is
not the only thing a cable can be:

* an **administration** port (the default, what #601 built);
* a port on the **operator's own trusted network** — the `br-private` the private
  SSID already uses, with internet and without a payment step;
* a **customer** port again — back on the captive bridge with the guests, paying
  through the portal like every other client.

The card: *"uci field to choose the LAN port bridge: br-mgmt (admin reachable,
still pays) / br-private (trusted, free) / guest bridge (captive). Ships AFTER
br-mgmt. Touches uci-defaults, the guards' iifname sets, nodogsplash interface
config, docs + rc-tester-guide assertions."*

### The `mgmt` role's "still pays" is not deliverable here, and the record says so

The card's parenthetical for `br-mgmt` — *admin reachable, still pays* — is the
operator's original ask, verbatim. #601 delivers one half of it (administration
reachable, on a bridge of its own) and states plainly that the paywall half is
**not satisfiable by configuration on the shipped stack**: nodogsplash 5.0.2
gates exactly one interface (`src/conf.h:146`), its `nds*` chains have fixed
shared names in one namespace (`src/fw_iptables.h:35-44`), its teardown deletes
them **by name** (`src/fw_iptables.c:689-747`), and this module drives one
control socket (`src/valve/valve.go:96-102`, an `ndsctl` exec with no `-s`). A
second instance is startable and unsafe, with free internet as its failure mode.

So the `mgmt` role is what #601 shipped: administration reachable, **no internet
and no payment surface**. This record does not rename that, does not widen it,
and does not pretend the cable pays. A paywalled wired network is the F1/F2
follow-up (upstream per-instance isolation, or a module-owned gate), tracked and
decided separately.

## Decision

**D1 — The placement is one uci field, in the module's own namespace:
`tollgate.lan_ports.role`.** Three values, and nothing else:

| `tollgate.lan_ports.role` | the wired ports land on | administration surfaces | internet | can transact |
|---|---|---|---|---|
| `mgmt` *(default)* | `br-mgmt`, its own zone, `forward REJECT` | **yes**, pre-auth | **no** (no forwarding, ICMP dropped) | no — `:2050`/`:2051`/`:2121` dropped |
| `private` | `br-private`, `firewall.private_zone` | **yes** (both guards are captive-bridge-scoped) | **yes** (`private -> wan`) | no — `:2121` is `br-lan`/loopback only, and it does not need to |
| `guest` | the **captive** bridge (`network.lan.device`) | no — dropped on the captive bridge | after paying | **yes**, through the portal |

The field lives in a new `/etc/config/tollgate`, installed by both packaging
recipes (`packaging/Makefile`, `packaging/local-build-ipk.sh`). It is a **uci**
file and not the module's `/etc/tollgate/config.json` because the reader is a
shell script (`99-tollgate-setup`) that runs before any Go process exists, and
because `/etc/config` is what `sysupgrade` preserves and what `uci`/LuCI already
know how to edit. It is one named section, `config lan_ports 'lan_ports'`, with
`option role 'mgmt'` as the shipped default.

**D2 — The default is `mgmt`, and an unset or empty value means the default.**
An option added after a router was installed must not change that router's
network: a box that predates this field converges to exactly the placement #601
gives it. An **unrecognised** value (a typo, the wrong case, a bridge name
instead of a role) is an ERROR and the writer changes **nothing** — it never
guesses a bridge. Moving the operator's only cable access to a network he did not
ask for is the failure this rule exists to prevent.

**D3 — It is a MOVE, in every direction.** The writer reads the union of the
ports listed on the candidate bridges (the captive bridge, `br-mgmt`,
`br-private`), places each port on the target exactly once, and **clears** the
candidates that are not the target. A port on two bridges is a port on two
layer-2 domains, and a role change is a transition between placements, not a
"set the new one and hope": switching `mgmt -> private -> guest -> mgmt` carries
the ports correctly in every direction, and a converged router is a no-op.

**D4 — It is the same writer on both setup paths.** `setup_mgmt_bridge` — the
name both call sites and their tests already pin — becomes the single place that
validates and dispatches the role; the full-setup path and the
verify/repair (same-version/reinstall) path both reach it, because the port list
is image-owned and a `sysupgrade -n` or factory reset returns the port to the
captive bridge with nothing else in this repository noticing.

**D5 — The management bridge is removed when the role moves away from it.**
`network.mgmt`, `network.mgmt_bridge`, `dhcp.mgmt` and `firewall.mgmt_zone` are
this module's own scaffolding and exist only while the ports are on `br-mgmt`. A
portless `br-mgmt` left behind is exactly the state the `mgmt` writer refuses to
create ("a management bridge the operator has no way into"), and a `mgmt` zone
with no traffic can only be misread. The removal is idempotent.

**D6 — No role gives a bridge a captive gate, and no role changes the guards.**
This is the constraint inherited from the record above, and the card's other two
"touches" are discharged as *invariants with tests*, not as edits:

* `nodogsplash.@nodogsplash[0].gatewayinterface` stays `br-lan` for every role —
  one section, one gate, the bridge the guest SSIDs are bound to. `mgmt` and
  `private` cannot sell, and `guest` is the one network that has the gate.
* `20-nds-enforce.nft`, `30-backend-firewall.nft`,
  `31-admin-board-not-guest-reachable.nft` and
  `32-luci-not-guest-reachable.nft` keep matching `iifname "br-lan"`. They are
  about a stranger on an open SSID; a cable in a LAN port means walking to the
  router and plugging in, which is a different trust boundary, so extending any
  of them to a role's bridge would be the defect, not the fix. `guest` needs no
  change at all: the port is back on the bridge those rules already cover.
* `33-mgmt-bridge-scope.nft` is untouched and stays scoped to `br-mgmt`
  (`iifname` never matches a bridge that does not exist, so it is inert while the
  ports are elsewhere — pinned by a test).
* No shipped fragment reads the field: `grep -rl lan_ports packaging/files/etc/nftables.d/`
  is empty. A *conditional* ruleset is what a role-parameterised fragment would
  be, and it would put the guard policy in two places.
* `30-backend-firewall.nft`'s `:2121` lock stays `br-lan`/loopback only, so
  neither `mgmt` nor `private` can reach the payment API. For `private` that is
  deliberate and honest: a trusted port has free internet already, so letting it
  reach the API would take money for nothing.

## Invariants

1. **One gated bridge.** Exactly one `config nodogsplash` section exists on every
   setup path, and its `gatewayinterface` is the captive bridge — for every value
   of the field.
2. **One writer.** The wired ports' bridge is computed in exactly one place
   (`lan_ports_role` is read once, in the dispatcher), and both setup paths reach
   it.
3. **A move, never a copy.** After any run, each wired port is listed on exactly
   one bridge.
4. **Nothing on a refusal.** An unknown role, a role whose target bridge does not
   exist, or no port listed anywhere: an ERROR is logged, the writer returns
   non-zero, and **no** `uci` write happens.
5. **`mgmt` and `private` cannot transact**, `guest` pays: the payment surfaces
   stay off the two ungated bridges, and the gate stays on the one the guests
   share.
6. **`guest` is the pre-module status quo**, reachable as an explicit choice but
   not as a default: it reintroduces the layer-2 sharing with the open SSID.

## Consequences

### Positive

- A cable can be what the operator needs it to be, without a second
  implementation of "which bridge the ports are on": #601's writer became the
  parameterised one the earlier record predicted.
- The change is small and reversible: one uci file, one shell writer, two
  packaging lines, tests and docs. No Go file, no portal file, no nft fragment.
- `guest` is available for a bench that wants the pre-#601 behaviour back
  without downgrading, which also makes the #601 change measurable A/B on the
  same build.

### Costs and what the operator gives up

- **`mgmt` still has no internet.** Choosing the administration port means the
  cable cannot browse; that is the honest half of "one gate", not an oversight.
- **`private` trusts the cable the way it trusts the private SSID.** A device
  plugged into a LAN port is on the operator's own network: it can see the
  private Wi-Fi clients and they can see it.
- **`guest` re-opens the exposure #601 closed.** The port shares a broadcast
  domain with the strangers on the open guest SSID — ARP, mDNS and all — which is
  precisely what the management bridge stopped. It is offered because it is the
  picture the pre18 tester guide describes and because a bench needs it; it is
  not the recommended placement.
- **A role change is a moment of network change.** The ports are re-placed and
  the addresses change with them (`mgmt` derives its own `/24`; the captive and
  private bridges keep theirs). On a running router the change lands at the next
  install/upgrade, or immediately via the setup script (see the tester guide);
  either way a client holding a lease from the old bridge needs a new one.

## Alternatives rejected, and why

**A1 — A fourth role: `mgmt` *and* paywalled.** Rejected: it is the F1/F2
follow-up. One nodogsplash gates one bridge; a second instance is startable and
unsafe (shared `nds*` chain names, teardown by name, one control socket), and its
failure mode is a bridge with free internet. Until F1 or F2 lands, a bridge this
module cannot gate must not be sold — the paid purchase would fail in the single
instance's `ndsctl` (`Client <mac> not found.`) with the money taken.

**A2 — The field names a bridge (`br-mgmt`/`br-private`/`br-lan`), not a role.**
Rejected: the bridge names are the module's business, and the nft fragments
already pin `br-mgmt`/`br-lan` by name with a documented caveat for an operator
who renames one. A field whose values are bridge names invites a fourth value
that names a bridge the stack has never heard of, and makes a rename silently
change what the operator's setting means. The role is what is being chosen; the
bridge it lands on is documented in the file, printed in the setup log, and
visible in `uci show network`.

**A3 — Keep the placement hard-coded and ship a "customer port" module flag.**
Rejected: the port list is image-owned, so the placement has to be re-asserted by
this repository's own writer anyway; a flag that did not move ports would be a
setting that claims to do something it does not.

**A4 — Make `guest` mean a *dedicated* guest bridge for the wired port.**
Rejected: it needs a second gate (A1's problem) or it leaves a wired port with no
captive network at all. `guest` means the captive bridge because that is what the
captive network *is*.

**A5 — Delete the `mgmt` scaffolding only when it is empty, and leave it
otherwise.** Rejected: "otherwise" is a portless `br-mgmt` — the state D5 exists
to remove, and the state the `mgmt` writer refuses to create. Partial cleanup
would be indistinguishable from the portless case at a glance, which is the worst
property a convergence step can have.

## What must be true before this ships

**Offline (fake `uci`, no router, no network — the repo's existing tier,
`tests/uci-defaults-lan-port-role_test.sh`, 123 assertions, 4 negative controls)**

1. The field ships: `/etc/config/tollgate` exists, declares
   `config lan_ports 'lan_ports'` with `option role 'mgmt'`, its default matches
   the writer's (`LAN_PORTS_ROLE_DEFAULT`), and **both** recipes install it.
2. Each role places the ports on its bridge and **clears** the others; `guest`
   leaves them where they are (and does not duplicate them); multi-port devices
   move every port.
3. Every transition converges: `mgmt -> private`, `private -> guest`,
   `private -> mgmt`, and a factory reset (port list back on the captive bridge)
   under a non-default role. A second run changes nothing.
4. `mgmt`'s scaffolding is removed for `private` and `guest`; the private
   network's own zone, forwarding, address and DHCP are left alone.
5. Refusals: an unknown role (`br-lan`, `MGMT`, `mgmt,private`), `private`
   without a private bridge, `guest` without a device section for the captive
   bridge, and no port listed anywhere — each logs an ERROR, returns non-zero and
   writes nothing.
6. The one-gate and guard invariants of D6, pinned as assertions on the shipped
   fragments and the shipped writer.
7. The same-version (reinstall/upgrade) path places the ports per the role.
8. `make go-battery` unaffected (no Go file is touched).

**Bench, after the install (GL-MT3000, one owner, artifact identity read back)** —
the #601 rows 9-17 all still apply, and this field adds the transitions:

18. **Each role lands where it says.** For `mgmt`, `private` and `guest` in turn
    (set, `uci commit tollgate`, re-run the setup): `ip -d link show <port>` shows
    `master` the expected bridge; `uci show network | grep ports` shows it on
    exactly one bridge; `ip -d link show` the other two do not list it.
19. **The behaviour matches the table.** Role `mgmt`: `:8090` answers pre-auth
    from the cable, `ping 9.9.9.9` fails, `:2121`/`:2050`/`:2051` are refused.
    Role `private`: `:8090` answers, a WAN ping succeeds, `:2121` is refused.
    Role `guest`: `:8090`/`:8443`/`:8080`/`:443` are dropped before payment, the
    portal completes a purchase, and the gate opens.
20. **One gate, unchanged by the role.** `uci show nodogsplash` has one section
    with `gatewayinterface='br-lan'`; `pgrep -fc nodogsplash` is 1; one
    `/tmp/ndsctl*` socket — after each role, including `guest`.
21. **`guest` and `mgmt` are distinguishable the way the guest path is**: a
    client on the open guest SSID is unaffected by whatever the cable's role is
    (guards still drop, portal still sells).
22. **Recovery.** With the role set to a value the writer refuses, the cable
    still has the placement it had before, `logread -e tollgate` names the value,
    and the private SSID and module CLI still reach the box.

`docs/rc-tester-guide.md` §7 is updated in the same change: it now tells the
tester to read `uci get tollgate.lan_ports.role` first and gives the per-role
expectations, because "wired into a LAN port" no longer implies one behaviour.

## Rollout / PR sequence

1. **The bridge and its scope** (PR #601, open): `setup_mgmt_bridge` +
   `33-mgmt-bridge-scope.nft`, `mgmt` hard-coded. This record depends on it and
   its branch is this change's base.
2. **This record and the field** (one PR, stacked on #601's branch): the uci
   file, the role dispatch, the tests, the tester-guide section and the
   CHANGELOG entry.
3. **Bench measurement, then the release cut** — rows 9-17 of the record above
   plus rows 18-22 here, on the build that carries both.
4. **Then, separately, F1 or F2** if a paywalled wired network is still wanted.

## Notes

- The `mgmt` role is not a regression of #601 and not a duplicate of it: #601 is
  the *placement* this field parameterises. Both records describe the same
  writer; this one documents the field, the transitions, and the half of the
  operator's ask that neither can deliver.
- The two tests that matter most are the **negative controls**: a guard that has
  never failed is decoration. `tests/uci-defaults-lan-port-role_test.sh` carries
  four (the dispatch arm, the clearing of the non-target bridge, the removal of
  the management bridge, and the union that carries ports across a role change),
  and it refuses a control whose strip string matches no line or whose mutant
  does not parse — a control that removed nothing always "passes".
