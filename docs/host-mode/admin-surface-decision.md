# Admin surface for Linux host mode — decision record

Linux host mode (the deb lane) needs a written answer to "where is the admin
web UI?" so no later session re-litigates it. This record pins the decision,
the options that were considered, the minimal contract any phase-2 admin API
must satisfy, and the consequences of shipping phase 1 without one.

Scope: Linux host mode only. Nothing here changes the OpenWrt package.

## Decision

**DEFERRED to phase 2, not cancelled.**

Phase 1 of Linux host mode ships **no admin surface**: no admin SPA, no
admin HTTP listener, no admin port, no rpcd-style shim. The `tollgate` CLI
is the only operator interface. The release design already reserves
`/usr/share/tollgate/admin` as **PHASE 2 ONLY** (RELEASE-linux-deb.md §2.3,
the deb file list), so deferral is the outcome the design itself encodes:
the directory is a reservation, and nothing serves it in phase 1.

A phase-2 admin surface is deferred behind an explicit operator trigger —
the operator declares phase 2 — and any implementation of it **must**
satisfy the contract in [The phase-2 contract](#the-phase-2-contract)
below.

> RELEASE-linux-deb.md belongs to the packaging lane and is not yet merged
> in-tree; §2.3 is cited from the design cards that carry it. Re-check this
> section against the merged file when the packaging lane lands.

## Options

**Option (a) — deferred (chosen).** Phase 1 ships CLI-only; phase 2 may add
an admin surface under the minimal contract below.

Why it wins:

- Zero new attack surface in phase 1: no second listener, no port, no
  token store to protect, nothing to harden or audit.
- Every phase-1 operator need is already covered by existing seams — the
  CLI over the daemon's Unix socket (config, wallet, private network,
  service control) and the ndsctl shim verbs (session grant/revoke,
  session counters).
- It contradicts nothing: the design's file list already earmarks the
  admin directory for phase 2 rather than omitting it.
- Cost is real but bounded: the headless-box UX described under
  [Consequences](#consequences).

**Option (b) — not planned (rejected).** Cancel the admin surface outright:
CLI is the permanent interface, the reservation is dropped.

Rejected because it buys nothing today — both options ship no admin surface
in phase 1, so the only difference is option (b) additionally forecloses a
path the design deliberately kept open. Cancelling is a strictly stronger
commitment for zero phase-1 gain. If the operator later wants cancellation
after all, this record is the place to amend; until then the answer is
"deferred, contract below", not "no".

## The phase-2 contract

A future admin API is only acceptable if it satisfies all three of these.
Anything broader (more verbs, network exposure, a second service) needs its
own decision record superseding this one.

**1. Verbs — exactly four at introduction, each mapped to a seam that
already exists:**

| Verb | Kind | Parity |
|------|------|--------|
| `status`   | read  | CLI `status` + `wallet balance` / `wallet info`: service health, wallet summary |
| `sessions` | read  | ndsctl shim `json` surface: active client sessions with per-MAC counters |
| `session grant/revoke` | write | ndsctl shim `auth` / `deauth`: gate a MAC on demand |
| `config set` | write | CLI `config set`: set and apply one configuration key |

Money movement (`wallet fund`, drain, Cashu payout) is deliberately **not**
in the initial verb set: irreversible wallet operations stay on the
CLI / AF_UNIX path, behind interactive confirmation.

**2. Auth model — the CLI's trust boundary, never anonymous.** Default is
an AF_UNIX socket with file-permission auth, exactly the existing CLI
server model (`/var/run/tollgate.sock`, mode 0660: the daemon user owns it,
a group grants CLI access). Any TCP exposure is opt-in and additionally
requires a bearer token minted into `/etc/tollgate/` — the directory the
packaging design keeps across both upgrade and purge. No unauthenticated
admin verb, in any configuration.

**3. Where it listens — loopback only at introduction.** An AF_UNIX socket
(preferred) or `127.0.0.1` at most; never a new port bound on a
non-loopback interface. The admin SPA bytes, when they exist, are served
from `/usr/share/tollgate/admin` by the same listener that terminates the
API — one socket, not two. Phase 1's only HTTP listeners stay the
client-facing ones: the money-path API on `:2121` and the captive portal
(`:2051` SPA, `:2050` stub, per the portal lane).

## Consequences

What phase 1 actually ships with, stated plainly:

- **SSH plus the CLI is the whole admin story.** Every administrative
  action on a host-mode box is `ssh` in, then `tollgate ...`: `config get`
  / `config set` (routed through the running daemon over the Unix socket),
  `wallet balance|info|fund`, drain, `private enable|disable|rename|
  set-password`, `upstream` management, and service start/stop/restart.
- **On a headless box this means:** no browser on the box and no remote
  dashboard — a non-technical operator cannot administer the gateway
  without a shell account on it. Granting or revoking a client session
  outside the payment flow is `ndsctl auth|deauth <mac>` on the box (or an
  SSH session running it), not a dashboard button. Diagnostics are
  `journalctl` and CLI output, not a status page.
- **Automation has no admin endpoint to scrape.** Monitoring and alerting
  must consume CLI `--json` output or journald; there is deliberately no
  admin HTTP surface to enumerate in phase 1.
- **The reservation stays empty.** `/usr/share/tollgate/admin` carries no
  bytes and no listener in phase 1; nothing may depend on its existence
  yet, and its absence is not an error condition.

These are accepted costs of the deferral, not gaps to quietly fill.

## Not claimed

- No SPA was authored, no rpcd shim built, no HTTP listener added, no port
  opened — all explicitly out of scope for this record; it changes only
  documentation.
- The phase-2 contract is a constraint set for a future implementer. It is
  not an implementation, not a schedule, and not operator approval to
  build phase 2. The revisit trigger is the operator declaring phase 2.
- No UX research backs the four-verb set beyond mapping them onto seams
  that already exist in the codebase and the host-mode design lanes.
- RELEASE-linux-deb.md §2.3 is cited from the design cards; the file
  itself is not yet in-tree (packaging lane). This record must be
  re-checked against the merged §2.3 when that lane lands.
