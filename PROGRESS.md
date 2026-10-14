# PROGRESS — tollgate-cvm

Branch `pr/cvm-control-plane` · fork `felixfelix-bot/tollgate-module-basic-go`
**Upstream PR: https://github.com/OpenTollGate/tollgate-module-basic-go/pull/938** (OPEN, MERGEABLE)
**Fork `main` merged: efab028** (`cfbfff5..efab028`) — verified, all work present.
Plan: ~/reports/contextvm-tollgate/PLAN.md · ADR: docs/architecture/cvm-control-plane-decision.md
Urgency: SOON (operator-approved; write scope read+config+private+start/stop/restart; money tools EXCLUDED)

## Cluster log (finding → status → files)

- maintainer_npub field did NOT exist upstream → CONFIRMED → recon
- Go toolchain absent on host → FIXED → ~/.local/toolchain/go (1.24.6)
- P1 RED 31c5971, GREEN bb599e0 — field + ValidateMaintainerNpub + SetDotPath rule + schema + assertFieldValue + changelog.d
- P2 RED b34f374 → GREEN d9294d0: SocketBridge, ToolSurface (12 read/7 write, drain+fund+ssl ABSENT), Authorizer, LoadServerConfig/ValidateKeyPair
- Transport RED eb9024d → GREEN 1852eb3: gift wrap 25910 + NIP-44/59. TRAP: decrypt closure must take otherPub from GiftUnwrap's ARG (outer=nonce key, inner=seal key); capturing either breaks the other with 'invalid hmac'
- Dispatcher 7fb70fc: authorize BEFORE socket traffic (pinned by bridge-never-called assertion)
- Serve+main bbf9ac4: broad 1059 subscribe + client-side p filter, 5s relay backoff
- Claim gate 8295930: unclaimed router refuses to serve (reads would leak status/balance)
- Announcements d5f1151: 11316 with ["p", maintainer_npub] = THE discovery key; 11317 from ToolSurface
- E2E fix 6a875b1: post-sign e-tag append invalidated signature → relays drop SILENTLY. WrapResponse takes extraTags, applied pre-sign. Live relay2 round trip 1.66s
- Packaging b61a4e8: local-build-ipk + Makefile PREBUILT_CVM + init.d/tollgate-cvm DISABLED BY DEFAULT (uci tollgate.cvm.enabled), key via env not argv. mipsle cross-compile 12MB verified
- gofmt be6524b (battery caught it)
- 9b50fb0: untracked accidental 11MB binary + .gitignore rule
- Fork main merge efab028 + pushed; PR #938 opened

## Gates

- go test ./... in src/cmd/tollgate-cvm: GREEN
- config_manager + cli suites: GREEN; js-schema-lint PASS (70 entries)
- go-battery (all modules): see /tmp battery run
- E2E: TOLLGATE_CVM_E2E=1 -tags e2e → PASS 1.66s over relay2.contextvm.org

## Remaining

1. P4 dashboard: delegated → ~/worktrees/tollgate-dashboard (child building); needs repo cvm-services/tollgate-dashboard + nsite deploy
2. P5 cold cross-family review PUBLISHED on PR #938 (child audit → ~/reports/contextvm-tollgate/AUDIT-cvm-control-plane.md)
3. Upstream merge = OPERATOR (repo policy: never merge upstream main; ruleset needs c03rad0r approval, bot cannot self-approve)

## Gotchas bank

- secret pre-commit hook rejects nsec-like literals even in tests → craft bech32 negatives
- fleet cred-gate DISABLED warning every commit (CRED-H3, ~/.git-hooks/cred-gate.conf) — flagged, not ours
- go test must run per nested module dir; root ./config_manager/... fails
- post-sign tag append = silent relay drop. ALWAYS add tags via GiftWrap's modify hook
- cross-compile writes the binary next to sources → gitignore + check before `git add -A`
- heredocs trip a gateway guard in this session → use write_file/patch instead
