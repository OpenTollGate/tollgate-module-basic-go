# Hardware and VM testing (labgrid)

Topic doctrine split out of `AGENTS.md` (#920) so lab and campaign
guidance grows in its own file instead of appending to the shared core.
Indexed in AGENTS.md's "Topic doctrine" section.

All router- and VM-based testing is coordinated through **labgrid**
(coordinator `ai-legion:20408`). Do not drive lab hardware ad hoc: reserve
through places (`labgrid-client -p <place> acquire` … `release`), and treat
a place held by someone else as theirs. Note some hosts carry a stale
`LG_COORDINATOR` pointing at a dead address — use the hostname form:

```bash
export LG_COORDINATOR=ai-legion:20408
labgrid-client places          # inventory + comments say what each seat is
labgrid-client who             # current holders
```

The lab's single source of truth is the private
**`Amperstrand/conwrt-bench`** repo (ADR-0005): `registry/` for devices,
`labgrid/` for place seeds and examples, `docs/decisions/` for the why,
sops for secrets. `conwrt-lab` is retired — do not add data there. Rules
that every hardware-touching change follows:

1. Never hardcode device IPs, MACs or serial paths — resolve from the
   registry or a labgrid place.
2. Access hardware through labgrid places (`ssh|console|power`), with
   acquire/release for exclusivity during a test.
3. Flashing and adoption go through conwrt tooling
   (`dut_recover.py --from-lab`, `bench_net.py`), which updates the
   registry.
4. A state change ends with a registry commit — flashed, moved or
   adopted devices must be reflected before you walk away.
5. When surprised, reconcile first
   (`lab_registry.py reconcile`) before touching anything.

VM lane: `labgrid/qemu-x86-64.yaml.example` in conwrt-bench is the
pattern — the client runs ON ai-legion (QEMUDriver executes where the
client runs), pristine per-acquire boots via `snapshot=on`. The on-target
package harness is `tests/happy-path/run.sh --artifact <extracted pkg>`;
the artifact is the published bytes from a kind-`1063` event
(hash-pinned via its `x` tag) or, for pre-tag candidates, a local
`scripts/build-sdk-package.sh` build whose sha256 is recorded in the
evidence.
