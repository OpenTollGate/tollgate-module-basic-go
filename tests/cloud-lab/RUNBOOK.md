# Cloud-lab runbook — running labs on a shared host without stepping on anyone

This documents the operating patterns for `tests/cloud-lab` on a host
that multiple sessions (humans, agents, CI shards) use at the same time,
and the lessons that produced them. Every rule below exists because its
violation cost real time on this repository.

## Prerequisites (host-side, beyond docker itself)

- docker with compose v2.24+ — the per-run override relies on
  `ports: !reset []`;
- `socat` — every `lab.sh tap` is a socat under pidfile management;
- `ss` (iproute2) — taps verify their listener actually bound;
- a `date` implementing `-d` (GNU or uutils coreutils) — the
  `reap --all` janitor parses container `CreatedAt` stamps.

## The tool: `lab.sh`

`lab.sh` wraps the entire lab lifecycle in one run-scoped lease (in the
spirit of labgrid's acquire/use/release model, adapted to a single docker
host):

```bash
tests/cloud-lab/lab.sh up                 # bring the lab up, isolated to this checkout
tests/cloud-lab/lab.sh run --rm client -q test_smoke_payment.py
tests/cloud-lab/lab.sh tap 18885 mint:8085   # managed TCP tap (pidfile + log)
tests/cloud-lab/lab.sh down              # teardown: containers, network, volumes, taps
tests/cloud-lab/lab.sh reap              # force-clean after a crash (by label, bounded)
tests/cloud-lab/lab.sh reap --all        # janitor: reap any run older than REAP_MINUTES
```

What one `up` creates — and nothing else on the host shares:

- a compose **project** named `cl-<run-id>` (run id is stable per
  checkout: user + branch slug + worktree-path hash);
- **container names** `$PROJECT-<service>` (the base file's `tg-*`
  names are global and collide across labs);
- a **subnet** allocated from `172.31.64–250/24` by inspecting live
  networks — overlap-checked against live networks *wider* than /24 too,
  so a neighbouring `172.31.64.0/23` blocks both of its octets (the
  base file pins `172.28.0.0/16`, which allows exactly one lab per
  host);
- **no host port bindings at all** — tests reach services over the
  compose network by service name; host access goes through a managed
  `tap` or `docker exec`.

Profile-gated lanes run through the same lease:
`COMPOSE_PROFILES=external-mints tests/cloud-lab/lab.sh up` (compose
reads the variable directly; `--profile` is not a `lab.sh` argument).
The override covers every service in the base file, gated or not, so a
lane's containers are renamed and remapped exactly like the default
topology's.

Prefer `lab.sh` over hand-rolled `docker compose -p … -f …` invocations.
The run state (override, lease record, tap pidfiles) lives in
`/tmp/cloud-lab-runs/<run-id>` and is fully removed by `down`/`reap`.

One deliberate divergence to know about: `lab.sh` passes an explicit
`-f docker-compose.yml -f <run override>` pair, so a dropped-in
`docker-compose.override.yml` is **not** part of a `lab.sh` run — while
the lane scripts (`run-keyset-rotation.sh`, `renewal_e2e.sh`, …) do
re-append it when present. If you rely on an override file, `lab.sh`
and the lane scripts will differ for you; pick one path per
investigation instead of mixing them.

## Patterns and why (the research-backed version)

| Pattern | Source | What it prevents here |
|---|---|---|
| Run = lease; label everything (`cl.lab.run=<id>`) | labgrid coordinator/places | Anonymous resources nobody dares delete |
| Per-run compose project name | ephemeral-CI compose stacks | Two checkouts silently sharing containers/volumes/images |
| `up --wait --wait-timeout` | compose CI practice | Racing a half-booted lab |
| Teardown `down -v --remove-orphans` + label-based `reap` | preemption-safe CI runners (`if: always()`) | Leaks after timeouts/SIGKILL |
| Taps as managed resources (pidfile in run state) | labgrid exporters as first-class entities | Orphaned socats, pattern-matched `pkill` killing the neighbor's taps |
| Build inside the run's own project | per-run image tags | A stale shared `:latest` image being probed as if it were new code |

## Lessons learned (each cost real time — do not relearn them)

1. **A run id must be stable across invocations, not unique.** `up`,
   `run`, `tap`, `down` are separate process invocations; a `$$`/timestamp
   in the id mints a new unreachable run per command. (This bug survived
   two reviews of the very script written to prevent it — test the
   harness.)
2. **Never reuse a compose project across checkouts.** Two worktrees
   building into one project name means one worktree probes the other's
   binary — a stale-image bug that cost two full debug loops here. Per-run
   projects give per-run images for free.
3. **The base compose file's `container_name:` values are global.** Any
   second lab on the host loses the name lottery. Always override them.
4. **The pinned `172.28.0.0/16` allows exactly one lab.** Any second lab
   must remap the subnet (and per-service static IPs with it).
5. **Host port bindings are collision surface.** The tests never need
   them — everything speaks service DNS on the compose network. Strip
   them; use taps when a host process must reach in.
6. **Never kill background helpers by pattern.** `pkill -f socat` once
   reaped a neighboring session's lightning-rpc taps. Kill by recorded
   pid from your run state, or by run label — nothing else.
7. **A self-locating `cd "$SCRIPT_DIR"` breaks relative `$0`.** Absolutize
   `$0` before any `cd`, or every later `"$0"` use (usage text,
   re-invocation) dies.
8. **Long jobs outlive tool timeouts.** A `nohup make go-battery &` from
   a command that gets killed at its 120 s timeout takes the battery with
   it (SIGTERM propagates to the process group). Run long jobs under a
   real supervisor — `tmux` — not `nohup` from a doomed shell.
9. **Keep scratch trees out of the module tree.** A vendored fork under
   `src/third_party/` makes `gofmt -l .` (and thus `make go-battery`)
   fail on the vendored code's pre-existing formatting. If a replace
   needs a local copy, keep it outside the repo or expect to gofmt it.
10. **Set the working directory explicitly on every command** from an
    automation context. "Ran from the wrong worktree" has produced
    wrong-binary builds, files landing in other sessions' checkouts, and
    pushes to the wrong repo — the single most repeated mistake in this
    catalog.
11. **Never pass prose through shell interpolation to `gh`.** Backticks
    in a double-quoted `--body` get command-substituted (it has posted
    `('s single production  caller)` to a PR). Use `--body-file` for any
    body containing markup.
12. **Bounded, idempotent teardown only.** A cleanup step that can hang
    (`compose down` against a wedged docker) wedges the caller. Wrap
    teardown in a timeout and make it safe to run twice.

## Concurrent-session etiquette

- Your run's resources all carry your run id. If you did not create it,
  do not touch it — that includes containers that look like "the" lab
  (`tg-*` names may belong to someone else's default-project run).
- Before allocating host resources (ports, subnets), inspect what is
  live (`ss -ltn`, `docker network inspect`); the allocator does this
  for subnets, you must do it for ports.
- A janitor cron may call `lab.sh reap --all` with a max age; anything
  you need for a long investigation should hold a `tmux`-supervised
  session or be prepared to be reaped.
