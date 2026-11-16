# The captive-portal daemon: keep nodogsplash, pin it, contribute upstream, do not migrate yet

## Status: Decided (2026-10-27)

Applies to `packaging/Makefile` (the runtime dependency), `packaging/build-inputs.json`
(the pin — filed as its own card, `t_4bd613b1`), the two writers of the NoDogSplash
configuration (`packaging/files/etc/uci-defaults/99-tollgate-setup` and
`src/cmd/tollgate-cli/ssl.go`), the convergence step that reads the running `ndsRTR`
chain, and `src/valve` (the whole `ndsctl` surface).

It answers a question the other NoDogSplash records assume rather than decide. They
decide what the module does *around* the daemon —
[`zombie-session-close-reconciliation-decision.md`](zombie-session-close-reconciliation-decision.md)
(the close contract), [`startup-nds-reconciliation-decision.md`](startup-nds-reconciliation-decision.md)
(the client list as evidence) and [`ndsctl-invocation-outcomes-decision.md`](ndsctl-invocation-outcomes-decision.md)
(who ended an invocation) — and none of them asks whether the daemon should be
nodogsplash at all. It does not overrule them; it fixes the component they are written
against.

The analysis this records is `~/reports/nodogsplash-build-vs-adopt-2026-10-27.md`
(four-consultant gap analysis + manager verification, 2026-10-27 — explicitly **not** a
decision record). The interrupted worker recorded source checks on 2026-10-27. Those dated
observations are not installed-version evidence. Recovery qualifies the conclusions
below; estimates remain estimates and deployment claims require fresh evidence.

## Background: the question, and what the audit found

Three questions were on the table, and they are usually asked as one:

1. should this project write its own captive-portal daemon?
2. should it migrate to openNDS (the maintained fork)?
3. should it fork nodogsplash to carry its own fixes?

The premise behind all three is that nodogsplash is the source of the failures we keep
hitting. The audit refutes that premise, and the refutation is the load-bearing evidence
for every decision below: **the majority of the NoDogSplash-related failures this repo
encodes are ours.** Each row cites what it is and where our own record of it lives.

| Our failure (as this repo encodes it) | Owner | Where |
|---|---|---|
| Immortal "zombie" clients, endless deauth retries — rc=1 with `Client <mac> not found.` read as an *unconfirmed* close and retried forever | **Ours** | #595 / `d5722a43`; `zombie-session-close-reconciliation-decision.md` |
| The retry storm that wedged the control socket | **Ours** — the storm is ours; the wedge's C-level cause is unproven | #595, #596 |
| A `not found` answer naming a *different* MAC completing our close (generic text matcher) | **Ours** | #617 / `ae83e280`; `ndsctlUnknownClient` requires the MAC, `src/valve/valve.go:594-600` |
| Our own timeout kills reported as ndsctl failures | **Ours** — `exec.CommandContext` killed the child; `Stop()` was neither called nor idempotent | #596; `ndsctl-invocation-outcomes-decision.md` |
| A module restart leaves NoDogSplash's authorised clients unmetered | **Ours** — our maps are process-local; the daemon retaining valid state is not a defect | #596; `startup-nds-reconciliation-decision.md` |
| An `ndsctl auth` this module never made leaves a client with unmetered access | **Ours** — needed the periodic pass | #619; the 2026-09-28 amendment |
| UCI says a port is allowed, the running `ndsRTR` does not: we wrote UCI and restarted only ourselves | **Ours** | #579 (reload on allow-list change); `converge_nodogsplash_runtime`, `packaging/files/etc/uci-defaults/99-tollgate-setup` |
| Pre-auth DNS allowed on tcp/53 only, so every guest's hostname lookup failed | **Ours** — the daemon enforced exactly the allowlist we gave it | #749 (closed) / #769 |
| apk upgrades never re-ran the setup: our version sentinel was corrupted by placeholder substitution | **Ours** | #459 (closed) |
| WAN/portal interface names hardcoded to the MT3000 | **Ours** | #297 (merged 2026-07-26) |
| Counters read as KiB when the daemon reports kB | **Ours** — `ndsctl json` emits bytes/1000 (`src/util.c:646`, upstream) and the module multiplies by 1024 (`src/valve/valve.go:1311-1312`), a 2.4 % inflation of the reported decimal-kB quantity | TODO(unverified): whether any allotment has been sized against the inflated unit — needs its own card |
| The fw4/nftables enforcement seam | **Integration risk, causal attribution unresolved** — the priority −1 bridge is ours; rule ordering alone does not prove the failure mechanism | `packaging/files/etc/nftables.d/20-nds-enforce.nft:1-33`; the causal header is under review in `t_799f237b` |

Genuine upstream defects exist and are narrower than the folklore. Verified against
`nodogsplash/nodogsplash` on 2026-10-27:

* **#409** (control-socket deadlock under concurrent `ndsctl`), closed 2019-08-10, fixed
  by the merged **#411** the same day;
* **#503** (`ndsctl status/clients/json` unusable from a BinAuth script), closed
  2020-02-08;
* **#633** (daemon will not start — `error initializing firewall rules` on an
  iptables-nft box), **open** since 2023-09-27;
* **#594** ("support of nftables missing"), **open** since 2020-11-11 — the request for
  native nftables, which openNDS answered and nodogsplash has not;
* **#316** (traffic control "officially broken until proven otherwise", `src/tc.c`),
  **open** since 2018-10-29;
* **#38** (IPv6), **open** since 2014-12-26.

27 non-PR issues are open upstream in total. `TODO(unverified)`: the report's claim that
zero of the 27 are memory-leak/crash/deadlock reports is a triage judgement, not a
counted fact — the count (27) is verified, the triage is not something this record can
reproduce from the issue titles alone.

### The numbers

| Question | Number | Source |
|---|---|---|
| Feed recipe observed (not installed-version proof) | nodogsplash **5.0.2-r2** (`PKG_VERSION:=5.0.2`, `PKG_RELEASE:=2`) | `openwrt/packages` `net/nodogsplash/Makefile`, verified 2026-10-27 |
| The tag it comes from | `v5.0.2`, tagged commit dated **2023-10-10** (**41 commits** behind upstream `main`) | `nodogsplash/nodogsplash` |
| Upstream `main` at verification | `7d50f69` (2026-09-25; the merge commit of PR #669) | same |
| Our `ndsctl` surface | **5 call sites, 3 subcommands, no flags** | `src/valve/nds_clients.go:95`, `src/valve/valve.go:520`, `:612`, `:1275`, `:1375` |
| Our NoDogSplash-semantics tests | **32** test functions across 6 files | `src/valve/zombie_session_test.go` (2), `src/valve/zombie_session_contract_test.go` (6), `src/merchant/zombie_session_test.go` (3), `src/merchant/startup_nds_reconciliation_test.go` (9), `src/merchant/nds_client_reconciliation_test.go` (6), `src/valve/nds_clients_test.go` (6) |
| Writing our own daemon | **30–90 engineer-days** | coupling audit, `~/reports/nodogsplash-build-vs-adopt-2026-10-27.md` §3 — TODO(unverified): an estimate, not a measurement |
| Migrating to openNDS | **10–30 engineer-days** | same, §4 — TODO(unverified), an estimate |

## Decision

1. **D1 — this project does not write its own captive-portal daemon.** The deciding
   number is **30–90 engineer-days** (`~/reports/nodogsplash-build-vs-adopt-2026-10-27.md`
   §3) for a component whose observed defects are overwhelmingly our own bookkeeping
   (the table above). An own daemon would not remove the captive-portal subsystem, it
   would remove one process: interception and redirect, per-client firewall rule
   lifecycle (atomic grant/revoke, expiry, firewall-reload reconciliation, conntrack,
   IPv6 bypass), walled garden with DNS-to-address sets, an auth state machine bound to
   the real client, per-client accounting durable enough to bill, persistence and
   fail-closed restart recovery, and startup ordering. Owning these mechanisms transfers the security boundary to us. A rewrite
   might remove particular defects, but neither zero benefit nor faithful repetition
   of our bugs has been demonstrated. The ownership table supports fixing our
   integration first, not a claim that a new daemon could fix nothing.

2. **D2 — do not migrate to openNDS now; queue it as the enforcement-modernisation
   step.** The deciding number is **10–30 engineer-days**, and it is not a package swap
   for three concrete, verified reasons:

   * **the configuration has two writers already.** `packaging/files/etc/uci-defaults/99-tollgate-setup`
     writes it (`assert_nodogsplash_allow_entries` and `setup_nodogsplash`) and so does
     `src/cmd/tollgate-cli/ssl.go` (`allowPort443`, `removePort443Allow` and
     `uciCommitChecked("nodogsplash")`). A third consumer does not inherit an invariant that two
     writers already share only by convention.
   * **the convergence step parses a representation, not a configuration.** It reads the
     *running* chain — `iptables -S ndsRTR` in `99-tollgate-setup` functions
     `nodogsplash_runtime_accept_entries`, `nodogsplash_ruleset_present` and
     `nodogsplash_accept_rule_count`
     — to decide whether the configured allow-list and the enforced one agree. openNDS
     has been natively nftables since **10.1.0** (`opennds/opennds` `ChangeLog`: "This
     version is a major upgrade including full migration to nftables", 2023-07-14), so
     there is no `ndsRTR` to read and the comparison has to be rewritten, not ported.
     (`TODO(unverified)`: whether openNDS's nftables chain layout leaves an equivalent
     invariant at all — it must be established before the migration is scheduled, not
     during it.)
   * **a chain-representation change breaks all three at once** — both writers and the
     reader — which is exactly the shape of failure our own history says we handle
     worst.

   It is a candidate for qualification, not a proven replacement: native nftables
   may let us retire `20-nds-enforce.nft`, but does not automatically remove
   enforcement risks. Command names alone do not establish semantic compatibility.
   The observed feed carries it at **10.3.1-r1**
   (`openwrt/packages` `net/opennds/Makefile`). Two cautions stay attached to the queue
   entry rather than being rediscovered later: the packages **conflict** —
   `CONFLICTS:=nodogsplash` in both — so same-image co-installation is blocked; separate-device staged rollout remains possible;
   and the licence drifted between the two recipes, **GPL-2.0-or-later** in the feed's
   10.3.1 recipe (`net/opennds/Makefile:18`) versus **GPL-2.0-only** in upstream's own
   recipe at tag `v11.0.0` (`linux_openwrt/opennds/Makefile:18`, unchanged on master,
   where the recipe is now 11.1.0), which must be settled before adoption rather
   than after. **Trigger to pull it forward:** the nftables bridge breaking again, or
   the next time enforcement is touched for any other reason — at that point the
   migration is being paid for once instead of twice.

3. **D3 — contribute fixes upstream; do not fork.** The deciding evidence is that
   **nodogsplash merged an external pull request, PR #669 by `db4rne`, on 2026-09-25**
   (`http_microhttpd: check URL size before copying`, base `main`, merge commit
   `7d50f69`), and #663/#665/#666/#668/#670 are external work in the same window. So
   "upstream will not take it" is unsupported for generic correctness work, and a fork
   would fork away from a maintainer who is demonstrably merging. The rule this record
   installs: a fix goes upstream first, with a reproducer, on the *deployed* revision;
   a fork becomes a candidate only when **a specific patch has been rejected** or an
   architectural need has been demonstrated — never from an aggregate impression that
   upstream is unresponsive. This is a rule about process, not about trust: it costs one
   PR per fix and buys the option of not maintaining a C daemon.

4. **D4 — pin the nodogsplash version explicitly.** Today the dependency names no
   version: `packaging/Makefile` contains the unqualified `+nodogsplash`
   dependency; its dependency comment explains why this is a runtime package. `packaging/build-inputs.json`
   has **no** `nodogsplash` key at all — it pins go, gonuts, node, npm, upx, the portal
   and both SDK eras — so there is no home for a version today. Tests can certify a
   version; only a pin names one, and a feed bump would otherwise silently change what
   we ship. The pin, its single declared place and its fence are **their own card**
   (`t_4bd613b1`); this record decides *that* it is required and does not pre-empt that
   card's answer to *where*.

## Qualification gates before any migration

Bind evidence to the installed package version, source revision, downstream patches
and artifact hash. On the real dataplane, verify unauthenticated denial, paid grant,
revoke/expiry, module and daemon restart, firewall reload, IPv4/IPv6 bypass resistance,
counter units, client identity binding, and failure recovery. Compare ndsctl JSON and
exit semantics, reconcile both configuration writers, and prove rollback on a spare
device before rollout. These are required future checks, not results of this ADR.

## Alternatives considered

* **Fork nodogsplash and carry our fixes there.** Rejected by D3: the fork's only
  benefit is speed on patches upstream refuses, and #669 shows refusals are not the
  default. The cost is permanent: a C daemon on a fork with our own security patches,
  which is D1's ownership burden paid in instalments.
* **Migrate to openNDS now, absorbing the 10–30 days.** Rejected by D2's timing, not by
  its merits: the three blockers are all code we would have to change *while* changing
  the daemon, so a failure during the migration would be indistinguishable from a
  failure of it. The queue entry keeps the merits on the table with a trigger.
* **Write our own daemon sized to what we actually use** (3 subcommands, no flags, no
  RADIUS). Rejected by D1: the narrow CLI surface is not the narrow part. The surface we
  use is small because the daemon does the work; the work is the part we would be
  buying.
* **Adopt CoovaChilli.** Not adopted, not rejected for ever: RADIUS interop is the one
  thing it does that we do not need today (selling access for Cashu does not require
  RADIUS), and adopting it would take over DHCP/TUN machinery dnsmasq and our own Go
  process already own. Revisit only against a RADIUS requirement — see below.
* **Vendor nodogsplash into this repo.** Rejected: it converts an upstream maintenance
  relationship into our own release engineering for a package the OpenWrt feed already
  carries, and it would hide feed bumps instead of pinning them (D4).

## What would change these decisions

* **The nftables bridge breaks again** — pull D2 qualification forward, not automatic migration.
  Compare a minimal repair with a tested replacement before choosing.
* **openNDS publishes a stable release with a settled GPL position and a feed bump** —
  D2's timing improves and the migration gets cheaper; the queue entry should be
  re-costed then, not re-litigated.
* **A RADIUS or interop-accounting requirement appears** — revisit CoovaChilli
  seriously, including its TUN/DHCP ownership cost.
* **A reproducible nodogsplash defect is found on the deployed revision** — D3 applies
  immediately: report it upstream with a minimised reproducer, before any fork talk.
* **Upstream stops merging for a sustained period** (a specific patch rejected, or a
  security fix left outstanding) — that is the D3 condition, and it is a per-patch
  judgement, not a switch.

## Consequences

* The daemon stays a dependency with an owner upstream, and our bookkeeping stays the
  thing we fix. The cost is that we cannot "just fix" a daemon defect in a day; the
  benefit is that a fix lands once and everyone upstream carries it.
* The pin (D4, card `t_4bd613b1`) declares intended build input, not installed identity.
  Record package metadata, source revision/patches and artifact hash on the target;
  fake-ndsctl unit tests certify neither the installed daemon nor the dataplane.
* The migration is a scheduled position, not a plan. Nothing in this record estimates
  the migration as work to start; it names the trigger that turns it into work.
* Residual, stated: the decision rests on an engineer-day estimate for D1/D2 that this
  record cannot reproduce, and on the ownership table remaining true — if a genuine
  upstream defect is found to be the *cause* of a failure we currently attribute to
  ourselves, the D1 arithmetic moves and this record should be re-opened rather than
  quietly re-interpreted.
* One fact this record relies on corrects an existing record rather than repeating it:
  `startup-nds-reconciliation-decision.md`'s amendment door (2) describes a client
  "restored by NoDogSplash's own state file", citing `src/main.c:147`/`:304` and
  `src/state_file.c` for **5.0.2**. The upstream v5.0.2 tag has neither — `git ls-tree
  v5.0.2 src/` contains no `state_file.*` and `git show v5.0.2:src/main.c` has no
  state-file reference; the feature arrives with [commit 9b2bc7b](https://github.com/nodogsplash/nodogsplash/commit/9b2bc7b),
  committed 2025-08-27 after the tag (16 commits ahead in the recorded comparison;
  41 is the later main-tip distance, not this commit). Restoration is conditional
  on a build containing that feature, not established for deployed routers. The
  correction of that file is card `t_f53c13c1`; this record only states the verified
  fact and does not edit it.
* TODO(unverified), carried forward from the report and not needed by any decision
  above: installed size and runtime RSS per candidate on the target router; presence of
  either package in the 25.12.6 **stable** binary feed (only feed master and snapshot
  were checked); whether the deployed 5.0.2 package carries downstream OpenWrt patches
  that change restart or persistence behaviour; openNDS's `ndsctl` JSON schema and
  counter-unit equivalence with nodogsplash.
