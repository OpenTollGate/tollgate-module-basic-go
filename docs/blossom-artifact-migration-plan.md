# Blossom Artifact Migration Plan

Eliminate `actions/upload-artifact` and `actions/download-artifact` from the CI workflow.
Replace cross-job data transfer with Blossom uploads/downloads and Nostr kind 30078
coordination events. Makes the workflow fully compatible with `nektos/act`.

Branch: `fix/blossom-no-artifacts`
Base: `main`

---

## Checklist

### Phase 1 — Scaffolding

- [ ] Create feature branch from `main`
- [ ] Add `COORDINATION_RELAYS` and `BUILD_ID` to workflow env / outputs

### Phase 2 — compile-binaries (single job, Blossom upload)

- [ ] Remove `strategy.matrix` from `compile-binaries`
- [ ] Build all 5 architectures sequentially in one shell step
- [ ] Install `nak`, tar binaries, upload to Blossom
- [ ] Output `binaries_hash` via `GITHUB_OUTPUT`
- [ ] Remove `actions/upload-artifact@v7` step

### Phase 3 — package-ipk (Blossom download + upload + Nostr 30078)

- [ ] Replace `actions/download-artifact` with `curl` from Blossom
- [ ] Keep existing build steps unchanged
- [ ] Install `nak`, upload built `.ipk` to Blossom
- [ ] Publish kind 30078 coordination event to relays
- [ ] Remove `actions/upload-artifact@v7` step

### Phase 4 — package-apk (Blossom download + upload + Nostr 30078)

- [ ] Replace `actions/download-artifact` with `curl` from Blossom
- [ ] Install `nak` + `curl` in SDK container
- [ ] Upload built `.apk` to Blossom
- [ ] Publish kind 30078 coordination event to relays
- [ ] Remove `actions/upload-artifact@v7` step

### Phase 5 — publish-metadata (query 30078, build 1063, cleanup)

- [ ] Replace `actions/download-artifact` with Nostr query
- [ ] Parse kind 30078 events, build NIP-94 kind 1063 events
- [ ] Publish kind 1063 events (batched, as before)
- [ ] Publish kind 5 deletion events for 30078 cleanup
- [ ] Keep summary step

### Phase 6 — Validation

- [x] Validate YAML syntax
- [ ] Run `act` dry-run (if available)
- [x] Push and verify GitHub CI — **all jobs green** (run 26645855908)
- [x] Open / update PR (#155)

---

## Architecture

### Data Flow (Before — Artifact Actions)

```
compile-binaries (matrix x5)
  └─ upload-artifact → binaries-${compile_key}
       │
       ▼
package-ipk (matrix x11)  /  package-apk (matrix x3)
  └─ download-artifact ← binaries-${compile_key}
  └─ upload-artifact → ${PACKAGE_FILENAME}
       │
       ▼
publish-metadata (single job)
  └─ download-artifact ← tollgate-wrt_* (all packages)
  └─ nak blossom upload → Blossom server
  └─ nak event -k 1063 → NIP-94 events → relays
```

### Data Flow (After — Blossom + Nostr)

```
determine-versioning
  └─ outputs: build_id, package_version, release_channel

compile-binaries (single job, no matrix)
  └─ nak blossom upload → Blossom (binaries-all.tar.gz)
  └─ outputs: binaries_hash

package-ipk/apk (matrix)
  └─ curl Blossom ← binaries-all.tar.gz (via hash)
  └─ build .ipk/.apk
  └─ nak blossom upload → Blossom (package file)
  └─ nak event -k 30078 → relays (metadata announcement)

publish-metadata
  └─ nak req -k 30078 ← relays (collect build results)
  └─ nak event -k 1063 → relays (NIP-94, batched)
  └─ nak event -k 5    → relays (cleanup 30078 events)
  └─ summary

trigger-build-os (unchanged)
```

### Run Identifier

`BUILD_ID = ${COMMIT_SHORT}-${EPOCH}` (e.g. `abc1234-1700000000`)

Generated in `determine-versioning`, used as `r` tag in 30078 events for correlation.

### Relay Configuration

| Purpose | Relays |
|---------|--------|
| Build coordination (30078) | damus, nos.lol, nostr.mom, relay.tollgate.me, relay1.orangesync.tech, relay2.orangesync.tech |
| Package publishing (1063) | damus, nos.lol, nostr.mom, relay.tollgate.me (unchanged) |

### Kind 30078 Event Schema

```json
{
  "kind": 30078,
  "tags": [
    ["d", "tollgate-build/${BUILD_ID}/${arch}/${fmt}/${compression}"],
    ["r", "${BUILD_ID}"],
    ["t", "tollgate-build"]
  ],
  "content": "{\"sha256\":\"...\",\"filename\":\"...\",\"url\":\"...\",\"architecture\":\"...\",\"format\":\"...\",\"compression\":\"...\"}"
}
```

---

## Risk / Open Items

- `nak req` is a streaming subscription — needs `--limit` or `timeout` to avoid hanging
- `openwrt/sdk` container needs `curl`/`jq` — verify or install via `apt-get`
- Kind 30078 events are temporarily visible on public relays (deleted after publish)
- GitHub CI will pass since artifact actions are fully removed (no v3 deprecation issue)
- `act` must support `GITHUB_OUTPUT` env file (it does since act v0.2.70+)
