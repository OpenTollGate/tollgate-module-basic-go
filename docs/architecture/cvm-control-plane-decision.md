# CVM Control Plane — Architecture Decision

> **Status: Proposed (2026-10-14).** This record proposes that a TollGate router
> exposes its own CLI over ContextVM (CVM) on Nostr, addressed by a maintainer
> npub, and that a static nsite dashboard lets a maintainer manage every router
> they own in one place. Acceptance is a maintainer action; the drafting account
> may not accept its own proposal.

## Context

A TollGate is installed behind NAT as often as not. Its operator has one tool for
it: `tollgate` on the box, over the Unix socket `/var/run/tollgate.sock`. There is
no remote management path, and the obvious one (expose an HTTP port) is ruled out
by the deployment shape — the router usually has no reachable address, and adding
an inbound listener to a payment gateway is a cost, not a feature.

The maintainer identity is also unexpressed. A tollgate does not publish *who
owns it*: `config.json` has no maintainer field, and the only pubkey field
anywhere — `identities.json → public_identities[].pubkey` — ships the literal
string `"not currently used"` and is read by no code path. So a fleet of routers
under one operator is a set of anonymous, unrelated devices.

Prior art exists and was read: `OpenTollGate/tollgate-nostr-daemon` (branch
`main`, 2 commits) specifies `tollgate-nostrd`, a Python asyncio daemon bridging
signed **kind 31998/31999** events to the same CLI socket over an
`TOLLGATE_ALLOWED_NPUBS` allowlist. It is **documentation only** — the repo
contains no `.py` file — and its kinds are private to it, so no existing client
can discover or call a router. It is the right *shape* with the wrong *protocol*,
and its allowlist is hand-maintained per device rather than published by the
router.

## Decision

### D1 — The wrapper is a second Go binary in this module, not a new daemon

`src/cmd/tollgate-cvm` is a nested Go module built and shipped in the same
package as `tollgate-wrt` and `tollgate` (`packaging/Makefile` `Package/install`
already installs two binaries into `$(1)/usr/bin`; this adds a third).

It speaks the **existing** socket contract by importing the CLI's own message
types, so the command surface cannot drift from the CLI's:

```go
type CLIMessage struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args,omitempty"`
	Flags     map[string]string `json:"flags,omitempty"`
	Timestamp time.Time         `json:"timestamp"`
}
type CLIResponse struct {
	Success   bool        `json:"success"`
	Message   string      `json:"message,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Error     string      `json:"error,omitempty"`
	Progress  string     `json:"progress,omitempty"`
	Timestamp time.Time   `json:"timestamp"`
}
```

Socket path is `/var/run/tollgate.sock`, overridden by `TOLLGATE_TEST_CONFIG_DIR`
(the shipped honour — tests point it at a temp dir).

Rejected alternatives:
- **Extend `tollgate-nostr-daemon`** — adds a Python runtime to a router image
  that does not otherwise need one, and keeps non-standard kinds.
- **Bridge off-router** (a VPS or laptop holding the socket) — requires the
  router to be reachable, which is the problem being solved.

### D2 — The wire protocol is ContextVM, not a private kind

Kind **25910** carries JSON-RPC 2.0 (`initialize`, `tools/list`, `tools/call`),
NIP-44 encrypted inside a NIP-59 gift wrap (**kind 1059**). The router announces
itself with CEP-6 kinds **11316** (server) and **11317** (tools).

Consequences that are requirements, not advice:
- **Client and server keys MUST differ.** A shared key makes the server receive
  its own gift-wrapped requests and the response correlation breaks.
- **`#p` tag filtering does not work for kind 1059 on some relays.** Subscribe
  broadly and filter the `p` tag client-side. (The `p` tag on kind **11316** —
  a *regular replaceable* kind — does filter; see D5.)
- Kind 25910 is ephemeral; discovery rides on 11316/11317 only.

### D3 — The maintainer npub is the authorization boundary

`config.json` gains `maintainer_npub` (string, `omitempty`). It is:
- the CVM allowlist seed (who may call write tools),
- the discovery key (which routers belong to one maintainer, D5).

Authorization is by **event signature**, then by npub membership. Every state-
changing tool is advertised with CEP-8 `explicit_gating`.

**Money-adjacent tools are not exposed over Nostr.** `drain cashu` returns
spendable ecash in its response; publishing that to a relay is a money-printing
bug. `wallet fund`, `drain *` and `ssl *` are excluded from the tool surface
unless an explicit opt-in config flag is set, and that flag is documented as
carrying that risk. The default tool surface is read-only plus `config set`,
the `private` network toggles, and `start`/`stop`/`restart`.

### D4 — Discovery: the router publishes its maintainer in an indexed tag

The kind-11316 announcement carries `["p", "<maintainer npub>"]`. Because 11316
is replaceable and regular (not a gift wrap), `#p` **is** honoured by relays, so
one subscription returns every router a maintainer owns — with no registry, no
curator and no directory to trust. This is the whole discovery mechanism.

### D5 — The dashboard is a static nsite; the browser holds no key

A separate dashboard repo (currently `felixfelix-bot/tollgate-dashboard`, intended for a future transfer to the OpenTollGate organization) builds a static site deployed with
`nsyte` (never localhost — standing rule):

1. **Login** via a NIP-07 browser extension. No private key ever enters the page.
2. **List** by subscribing 11316 with `#p = <logged-in npub>`; dedupe replaceable
   events per `(kind, pubkey, d)`.
3. **Per router**: gift-wrapped CVM tool calls from an ephemeral client key for
   live status, wallet balance, private-network state, upstream list, config.
4. **Writes** are confirm-gated and only offered where the announcement says the
   caller is authorized.
5. **Real data only.** No synthetic routers, no placeholder balances, no
   simulated telemetry. A panel that cannot be filled from live CVM data is
   removed, not faked.

### D6 — Configuration surface

`maintainer_npub` is a `config.json` field, so it needs all four of the things
this repo requires of a config field, in the same commit:
1. the struct field in `config_manager_config.go`,
2. an entry in `config_schema.go` (JSONKey `maintainer_npub`),
3. an `assertFieldValue` branch in `config_schema_drift_test.go`,
4. a `changelog.d/` fragment.

Rationale for the first three being non-optional: `TestSchemaCoversAllConfigStructFields`
and `TestSchemaHasNoOrphanEntries` fail on a struct/schema mismatch, and the
dot-path round-trip test is **vacuous** for a key with no `assertFieldValue`
branch (the switch has no default), so the third is what makes the test real.

## Assertions this decision must be held to

- A tool call from a non-maintainer npub against a write tool is **refused**, and
  the refusal is visible (silence is the failure mode).
- A paid/privileged tool is `explicit_gating`; a `cap` tag, where present, equals
  the invoice.
- `drain cashu` does not appear in the announced tool list.
- The announced 11316 carries both the service `d` slug and the maintainer `p`
  tag, read back from a **live relay** and diffed against a committed fixture.
- The dashboard shows a real router's real `wallet balance` end to end, with the
  number on screen asserted — not the API JSON re-read.
