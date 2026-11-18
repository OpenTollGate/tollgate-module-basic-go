# v0.6.0 beta→stable release runbook — evidence ledger

Working document (uncommitted; the durable halves live in
`docs/release-process.md`). Each gate = precondition + exact command +
artifact. The deferred queue (`/tmp/opencode/release-queue.sh`) automates
G0–G1 and G3; everything human is marked.

## G0 — host recovery (auto)

Precondition: bench host MemAvailable ≥ 20% of MemTotal, 3 consecutive
samples 120 s apart (avoids triggering on a transient blip).

```
awk '/MemAvailable/ {printf "%.1f", $2/1048576}' /proc/meminfo   # GiB
```

## G1 — replace-battery for the v0.17.1 pin (auto → posts to #1242)

The module's full 19-module battery against a local `replace` of the
tagged fork tip, BEFORE the bump merges:

```
git -C /root/src/gonuts-fix worktree add /tmp/opencode/gonuts-v0.17.1 v0.17.1
git -C /root/src/tollgate-module-basic-go worktree add /tmp/opencode/wt-relq origin/main
cd /tmp/opencode/wt-relq
for m in src/go.mod src/merchant/go.mod src/tollwallet/go.mod src/cli/go.mod scripts/token-recovery/go.mod; do
  go mod edit -replace github.com/OpenTollGate/gonuts-tollgate=/tmp/opencode/gonuts-v0.17.1 "$m"
done
for m in src src/merchant src/tollwallet src/cli scripts/token-recovery; do (cd "$m" && go mod tidy); done
systemd-run --quiet --scope -p MemoryMax=6G -p MemorySwapMax=1G -- make go-battery
# artifact: battery log; verdict comment posted to PR #1242 automatically
```

**Pass:** 19 modules green. Fail → the bump does not merge; triage on the
fork.

## G2 — #1242 review + merge (HUMAN)

Wallet-bump class → one independent approval (or maintainer delegation,
PRTA precedent). The G1 comment is the evidence package.

## G3 — after-picture conformance (auto, after G1 if memory holds)

The full lane on composed main; the arc completes: both new scenarios
GREEN on main.

```
cd /tmp/opencode/wt-relq/tests/cloud-lab   # post-G2 this is main with the pin
env PRTA_CONFORMANCE_DIR=/root/src/physical-router-test-automation/tests/conformance \
    TG_FAULTPROXY_HOST_PORT=39742 PROXY_HOST_URL=http://127.0.0.1:39742 \
    TG_UPSTREAM_HOST_PORT=39743 UPSTREAM_HOST_URL=http://127.0.0.1:39743 \
    bash conformance/run-conformance.sh
```

**Pass:** 7/7 — `issuer-400-keyset-unknown` and `swap-replay-rejected-ladder`
both green (the RED sides are recorded in #1236's differential; #1242's pin
delivers the second green). **Caveat:** G3 needs G2 merged to show the ladder
green; if run pre-G2 it shows 6/7 with the ladder red = expected, rerun after.

## G4 — v0.6.0-beta tag (HUMAN — one keystroke after G2)

```
python3 scripts/changelog-assemble.py check        # fold hygiene (168+ fragments, green)
git tag -a v0.6.0-beta <main-sha> -m "v0.6.0-beta ..."   # version stamps from the tag
git push origin v0.6.0-beta                        # fires the ngit lane; artifact → kind-1063
```

Verify the artifact: `nak req -k 1063 --tag v=v0.6.0-beta …` and sha256 vs
the `x` tag before any consumer uses it.

## G5 — hardware window (HUMAN physical → then automatable)

1. On-site: stock GS1900-8HP-2 — check power and the ERX p1 uplink
   (192.168.12.3 answers again). Precedent: sibling switch CPU-hang, AC-pull.
2. Unit C re-verify at **stock p8** by MAC (`78:c5:7d:13:a6:e4`) before
   anything else — registry rule after the seat retracts.
3. Flash the rc4 artifact (mt7621, sha256 `e04e271c…`) via the registry
   flow; commit the registry state after.
4. On-device: happy-path hygiene, `/wan-health` gating test (PRTA #348,
   now on main), custom-unit MB/min redemption (#1040's declared-unproven
   item), RF evidence for #1201/#1202 (owed before stable claims).
5. Unit B J5 recovery → the two-device RF peering journey (stretch).

## G6 — v0.6.0-stable (after G5 evidence exists)

```
python3 scripts/changelog-assemble.py fold     # maintainer folds fragments
make release-check                             # gonuts pin vs fork tags — v0.17.1 satisfies
scripts/repro-test.sh                          # reproducibility lane
# PRTA: extensive-test tiered campaign on the published artifact
```

## Ledger

| Gate | Artifact | Recorded at |
|---|---|---|
| G1 | battery log | PR #1242 comment (auto) |
| G3 | verdicts-go.json + phase logs | this host `.conformance/` + PR comment (auto) |
| G4 | tag → kind-1063 event | Nostr; sha256 vs `x` tag |
| G5 | flash evidence + on-device results | conwrt-bench registry + PRTA results/ |
| G6 | fold + release-check + campaign | release notes; PRTA reports/ |
