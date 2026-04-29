# Router Mutex: Avoiding Collisions Between LLM Sessions

## Problem

Multiple LLM sessions (or humans) may need to run tests on the same physical
routers simultaneously. Without coordination, two sessions could deploy
different binaries, modify configs, or restart services on the same router at
the same time, causing test failures and confusion.

## Solution: File-Based Lock

A `routers.lock` file in the repo root acts as an advisory mutex. Any session
that needs to modify router state **must** acquire the lock first.

### The Lock File

**Location**: `routers.lock` (gitignored, never committed)

**Format**:
```
locked: true
branch: 94-temporarily-remove-unreachable-mints-from-list-of-supported-mints
session: LLM session — degraded merchant testing
timestamp: 2026-04-29T16:45:00Z
phase: Part B — cold boot testing
```

### How to Use

#### Before starting router tests

```sh
make -f Makefile.test r-lock PHASE="Part B: cold boot testing"
```

This checks if the lock already exists. If it does, it shows who holds it and
exits with an error. If not, it creates the lock with your branch name,
timestamp, and phase description.

#### While running tests

Update the phase as you progress:

```sh
make -f Makefile.test r-lock PHASE="Part D: multi-mint scenarios"
```

This updates the phase description without changing the branch/session info.

#### Check if routers are available

```sh
make -f Makefile.test r-status-lock
```

Shows either "unlocked" or the current lock holder's details.

#### After finishing tests

```sh
make -f Makefile.test r-unlock
```

Removes the lock file so other sessions can proceed.

#### If a session crashes without unlocking

```sh
make -f Makefile.test force-unlock
```

Force-removes the lock with a warning. Use with caution — always check if
someone is actually still running tests first.

### Convention for LLM Sessions

1. **Always check the lock before deploying or modifying router state.**
   All `r-*` Makefile targets enforce this automatically.

2. **Set a meaningful phase description.** This helps other sessions
   understand what you're doing and estimate when you'll be done.

3. **Include your branch name.** The lock automatically captures the current
   git branch so others know which PR is using the routers.

4. **Unlock promptly when done.** Don't hold the lock longer than necessary.

5. **Stale locks**: If a lock is older than 2 hours and you can't reach the
   session that created it, it's reasonable to `force-unlock`.

### For New Branches / PRs

If you're setting up router testing for a new PR:

1. Copy this pattern into your `Makefile.test` (or reference this file)
2. Use the same `routers.lock` file — it's shared across branches
3. The lock is advisory — it prevents accidental collisions, not malicious use

### The `r-lock` and `r-unlock` Targets

These targets are defined in `Makefile.test`:

- `r-lock` — Acquire or update the lock. Fails if someone else holds it.
- `r-unlock` — Release the lock.
- `force-unlock` — Force-release the lock (use with caution).
- `r-status-lock` — Show current lock status.

All `r-*` targets that modify router state check the lock before proceeding.
Local targets (run on the router itself) do not check the lock — they assume
you're already on the router and know what you're doing.
