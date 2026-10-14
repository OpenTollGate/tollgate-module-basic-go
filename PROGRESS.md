# PROGRESS — tollgate-cvm (pr/cvm-control-plane)

Worktree: ~/worktrees/tollgate-cvm · branch pr/cvm-control-plane · fork felixfelix-bot/tollgate-module-basic-go
Plan: ~/reports/contextvm-tollgate/PLAN.md · ADR: docs/architecture/cvm-control-plane-decision.md
Urgency: SOON (operator-approved with recs: write scope=read+config+private+start/stop/restart, money tools EXCLUDED; relays=relay2.contextvm.org/primal/nos.lol; dashboard=new repo cvm-services/tollgate-dashboard)

## Cluster log (finding → status → files)

- maintainer_npub field does NOT exist upstream; only inert identities pubkey field → CONFIRMED → recon
- Go toolchain absent on host → FIXED → ~/.local/toolchain/go (1.24.6, PATH in ~/.bashrc)
- RED tests TestMaintainerNpubRoundTrip/Validation → COMMITTED 31c5971 → src/config_manager/config_schema_drift_test.go
- ADR cvm-control-plane-decision.md (D1-D6) → COMMITTED 31c5971 → docs/architecture/
- GREEN: MaintainerNpub field + ValidateMaintainerNpub + SetDotPath rule + schema entry + assertFieldValue branch + changelog fragment → COMMITTED bb599e0, PUSHED → config_manager_config.go, config_maintainer_npub.go, config_dotpath.go, config_schema.go, changelog.d/cvm-control-plane.added.md
- go.sum gained nip19/bech32 indirect → COMMITTED bb599e0
- config_manager + cli suites green; js-schema-lint PASS (70 entries) → VERIFIED
- Secret hook tripped on fake nsec test literal → fixed test data (truncated npub + wrong-prefix bech32 cases instead) — do NOT --no-verify

## Remaining (P2-P5)

1. P2: src/cmd/tollgate-cvm nested module — CVM wire (25910 in NIP-44/59 gift wrap 1059), socket bridge via cli.CLIMessage/CLIResponse (import from src/cli), allowlist=maintainer_npub, tool surface read+config set+private+start/stop/restart, money tools (drain/fund/ssl) EXCLUDED, keys-differ assertion. TDD: unit tests w/ fake relay + fake socket FIRST.
2. P3: CEP-6 announcement 11316 (+11317) with ["p", maintainer_npub] + d=tollgate-control-plane; live relay read-back vs committed fixture (nak req).
3. P4: cvm-services/tollgate-dashboard nsite — NIP-07 login, #p discovery, ephemeral-key CVM calls, confirm-gated writes, REAL DATA ONLY, nsyte deploy.
4. P5: E2E two real keys over public relay; cold cross-family review published on PR; ONE upstream PR to OpenTollGate/tollgate-module-basic-go (fork-first, never merge own PR).
5. packaging/Makefile: install tollgate-cvm binary + init script (packaging/files/etc/init.d/ pattern: tollgate-wrt exists as reference).

## Gotchas bank

- secret pre-commit hook: never commit nsec-like literals even in tests; craft bech32 negatives instead
- fleet cred-gate DISABLED warning on every commit here (CRED-H3, ~/.git-hooks/cred-gate.conf) — flagged to operator, not ours to re-enable
- go test must run per nested module dir (cd src/config_manager && go test ./...), root pattern ./config_manager/... fails: "main module does not contain package"
- upstream PR target: base=OpenTollGate main, head=felixfelix-bot pr/cvm-control-plane
