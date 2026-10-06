#!/usr/bin/env bash
# lab.sh — preemption-safe, collision-free runner for the cloud-lab.
#
# One lab = one run = one compose project with its own name, container
# names, subnet and images. Nothing is shared with another run on the
# same host except the docker layer cache, so concurrent sessions
# (humans, agents, CI shards) cannot collide, and a run that dies any
# way — including SIGKILL — is fully reclaimable by `lab.sh reap`.
#
# The shape follows three established patterns:
#   - labgrid's acquire/use/release: the run id IS the lease; every
#     resource it creates is labeled with it.
#   - ephemeral per-CI-run compose stacks: per-run COMPOSE_PROJECT_NAME,
#     `up --wait`, teardown with `down -v --remove-orphans`.
#   - preemption-safe CI runners: label every resource, reap by label,
#     never let a cleanup wedge (bounded), distinct exit codes.
#
# Usage:
#   tests/cloud-lab/lab.sh up [service...]        # bring the lab up (default topology)
#   tests/cloud-lab/lab.sh run <service> <cmd...> # run a command in a service (like compose run)
#   tests/cloud-lab/lab.sh ps                     # status of this run
#   tests/cloud-lab/lab.sh logs [service]         # follow logs
#   tests/cloud-lab/lab.sh down                   # tear this run down (volumes + orphans)
#   tests/cloud-lab/lab.sh exec <svc> <cmd...>    # docker exec into a container
#   CLOUD_LAB_RUN_ID=... lab.sh ...               # resume/operate an existing run
#   lab.sh reap [--all]                           # sweep dead runs' leftovers (this run's by default)
#   lab.sh tap <local-port> <target-service>:<port> [logfile]  # managed TCP tap (loopback-only) with pidfile
#
# Environment:
#   CLOUD_LAB_RUN_ID   default: <user>-<branch-slug>-<path-hash> (stable per checkout — same worktree resumes its run)
#   CLOUD_LAB_STATE    default: /tmp/cloud-lab-runs/<run-id>
#   COMPOSE_PROFILES   activates profile-gated lanes (compose reads it directly):
#                      COMPOSE_PROFILES=external-mints lab.sh up — `--profile` is
#                      not a lab.sh flag (compose only accepts it before the subcommand)
#
# Exit codes: 0 ok · 1 command failed · 2 usage · 130 interrupted · 143 preempted (SIGTERM)
set -euo pipefail

# Absolutize $0 BEFORE any cd: a self-locating cd that leaves $0
# relative breaks every later "$0"-based use (usage, re-invocation).
SELF="$(cd "$(dirname -- "$0")" && pwd)/$(basename -- "$0")"
SCRIPT_DIR="$(dirname "$SELF")"
cd "$SCRIPT_DIR"

LABEL="cl.lab.run"
# Stable per checkout: the same worktree resumes its run on every
# invocation; two worktrees of the same branch get different ids via the
# path hash. A timestamp or $$ here breaks the up/run/tap/down command
# model — each invocation would mint a fresh, unreachable run.
RUN_ID="${CLOUD_LAB_RUN_ID:-$(id -un)-$(git branch --show-current 2>/dev/null | tr -c 'a-zA-Z0-9' '-' | tr -d '\n-' | cut -c1-20)-$(git rev-parse --show-toplevel 2>/dev/null | md5sum | cut -c1-6)}"
# Compose lowercases project names; match it here, or an uppercase
# branch/user name makes reap's ${PROJECT}_ volume grep diverge from the
# real (lowercased) volume names on such checkouts.
RUN_ID="$(printf '%s' "$RUN_ID" | tr '[:upper:]' '[:lower:]')"
PROJECT="cl-$RUN_ID"
STATE="${CLOUD_LAB_STATE:-/tmp/cloud-lab-runs/$RUN_ID}"
OVERRIDE="$STATE/override.yml"
META="$STATE/meta"

usage() { sed -n '2,36p' "$SELF" | sed 's/^# \{0,1\}//' >&2; exit 2; }
[ $# -ge 1 ] || usage
cmd="$1"; shift

die() { echo "lab.sh: $*" >&2; exit 1; }

# --- state bootstrap --------------------------------------------------------
init_state() {
    mkdir -p "$STATE"
    # Lease record for humans: who, where, when — written once per run.
    # Must run BEFORE generate_override: that function's subnet append
    # creates $META, so a file-existence guard placed after it never
    # fires and the record is silently lost (found in review).
    [ -f "$META" ] || { echo "run_id=$RUN_ID"; echo "project=$PROJECT"; echo "cwd=$SCRIPT_DIR";
      echo "started=$(date -Is)"; echo "user=$(id -un)"; } > "$META"
    [ -f "$OVERRIDE" ] || generate_override
}

# The per-run override does the three things a shared host demands:
# strip ALL host port bindings (tests talk over the compose network),
# rename the hardcoded container names (they are global), and move the
# lab off the pinned subnet onto an allocated one.
generate_override() {
    SUBNET="$(allocate_subnet)"
    SUBNET_BASE="${SUBNET%.0/24}"   # 172.31.N — per-service octets keep the base layout
    # One source of truth for the service set: the base compose file,
    # resolved by compose itself with every profile expanded, so a new
    # lane service is covered without editing this script (both review
    # rounds asked for the derivation). COMPOSE_PROFILES still gates what
    # actually runs — this list only drives the rename/remap override.
    local BASE_CONFIG SVC_IPS svc ip
    BASE_CONFIG="$(docker compose -f docker-compose.yml --profile '*' config 2>/dev/null)" \
        || die "docker compose config failed — cannot resolve the service set"
    SVC_IPS="$(printf '%s\n' "$BASE_CONFIG" | awk '
        /^services:/{f=1; next}
        f && /^[^ ]/{exit}
        f && /^  [-A-Za-z0-9_.]+:$/ {
            if (cur != "") print cur, ip
            cur = $1; sub(/:$/, "", cur); ip = ""
        }
        f && /^ +ipv4_address:/ { ip = $NF }
        END { if (cur != "") print cur, ip }
    ')"
    [ -n "$SVC_IPS" ] || die "no services parsed from docker-compose.yml — refusing an empty override"
    {
        echo "# generated by lab.sh for run $RUN_ID — safe to delete with the run"
        echo "services:"
        while read -r svc ip; do
            echo "  $svc:"
            echo "    container_name: $PROJECT-$svc"
            echo "    labels: [\"$LABEL=$RUN_ID\"]"
            echo "    ports: !reset []"
            echo "    networks:"
            echo "      tollgate-lab:"
            # A service the base file gives a static IP keeps that layout
            # (same last octet, the run's own /24); one the base file
            # leaves to compose keeps that freedom too.
            if [ -n "$ip" ]; then
                echo "        ipv4_address: ${SUBNET_BASE}.${ip##*.}"
            fi
        done <<<"$SVC_IPS"
        echo "networks:"
        echo "  tollgate-lab:"
        echo "    driver: bridge"
        echo "    labels: [\"$LABEL=$RUN_ID\"]"
        echo "    ipam:"
        echo "      driver: default"
        echo "      config:"
        echo "        - subnet: $SUBNET"
    } > "$OVERRIDE"
    echo "subnet=$SUBNET" >> "$META" 2>/dev/null || true
}

# Pick a free /24 in 172.31.64.0–172.31.250.0 by inspecting live docker
# networks — the same trick our 172.31.99.0/24 remap did by hand, minus
# the hand. Ranges below .64 and the base file's 172.28/16 stay reserved.
# Overlap-safe: a live network wider than the /24 granularity blocks
# every third octet it COVERS, not just the one its base address names —
# a third-octet grep would hand out 172.31.65.0/24 inside a live
# 172.31.64.0/23 (found in review). Alignment is computed from the
# prefix: the true base of 172.31.65.0/23 is 172.31.64.0.
allocate_subnet() {
    local used cidr rest third prefix base span n o octet
    used=""
    for cidr in $(docker network ls -q | xargs -r docker network inspect \
        --format '{{range .IPAM.Config}}{{.Subnet}}{{"\n"}}{{end}}' 2>/dev/null \
        | grep -E '^172\.31\.[0-9]+\.[0-9]+/[0-9]+$' || true); do
        rest="${cidr#*.}"       # 31.65.0/23
        third="${rest#*.}"      # 65.0/23
        third="${third%%.*}"    # 65
        prefix="${cidr##*/}"    # 23
        if [ "$prefix" -le 16 ]; then
            base=0; span=256       # covers all of 172.31
        elif [ "$prefix" -le 23 ]; then
            n=$((24 - prefix))
            base=$(( third - (third % (1 << n)) ))
            span=$((1 << n))
        else
            base=$third; span=1    # /24.. /32: one octet (conservative)
        fi
        for ((o = base; o < base + span; o++)); do
            used="$used $o"
        done
    done
    SUBNET_BASE=""
    for octet in $(seq 64 250); do
        # space-delimited membership so "25" cannot match "250"
        case "$used " in
            *" $octet "*) ;;
            *) echo "172.31.$octet.0/24"; return ;;
        esac
    done
    die "no free 172.31.64-250 /24 on this host"
}

compose() { docker compose -p "$PROJECT" -f docker-compose.yml -f "$OVERRIDE" "$@"; }

# Taps deliberately OUTLIVE a lab.sh invocation: start one, then drive
# probes from later invocations. They die via down/reap, by recorded pid
# only — never by pattern match (which has killed innocent neighboring
# socats more than once).
kill_taps() {
    local p
    for p in "$STATE"/tap-*.pid; do
        [ -f "$p" ] || continue
        kill "$(cat "$p")" 2>/dev/null || true
        rm -f "$p"
    done
}

# --- commands ----------------------------------------------------------------
case "$cmd" in
up)
    init_state
    # --wait kills the boot race: no service is addressed before its
    # healthcheck passes. Timeout bounded so a wedged boot can't hang a
    # caller that will be preempted anyway.
    # Manifest-driven toolchain for the shortcut build path: every image that
# compiles Go MUST take its toolchain + canonical flags from here (the
# same packaging/build-inputs.json the SDK path pins to). Drift is gated
# by tests/contract/check-toolchain-parity.py.
REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
export TG_GO_VERSION="$(jq -r '.go.version' "$REPO_ROOT/packaging/build-inputs.json")"
export TG_PACKAGE_VERSION="$( [ -f "$REPO_ROOT/VERSION" ] && tr -d '[:space:]' < "$REPO_ROOT/VERSION" || echo 0.0.0-r0 )-r0"
export TG_LDFLAGS="-s -w -X main.version=$TG_PACKAGE_VERSION"
compose up -d --build --wait --wait-timeout 300 "$@"
    echo "lab up: run-id $RUN_ID  project $PROJECT  subnet $(grep ^subnet "$META" | cut -d= -f2)"
    echo "state dir: $STATE"
    ;;
run)
    [ $# -ge 2 ] || usage
    init_state
    compose run --rm "$@"
    ;;
exec)
    [ $# -ge 2 ] || usage
    init_state
    compose exec "$@"
    ;;
ps) init_state; compose ps "$@" ;;
logs) init_state; compose logs "$@" ;;
down)
    init_state
    kill_taps
    compose down -v --remove-orphans
    rm -rf "$STATE"
    echo "lab down and state removed: $RUN_ID"
    ;;
tap)
    # Managed TCP tap: pidfile + log live in the run state, so `reap`
    # can clean it and nobody ever pkill-greps by pattern (which has
    # killed innocent neighboring socats more than once).
    [ $# -ge 2 ] || usage
    init_state
    local_port="$1"; target="$2"; log="${3:-$STATE/tap-$local_port.log}"
    svc="${target%%:*}"; port="${target##*:}"
    ip="$(compose ps -q "$svc" | head -1 | xargs -r docker inspect --format "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}")"
    [ -n "$ip" ] || die "service $svc not running"
    # Loopback-only: a tap fronts an unauthenticated mint or portal, so
    # it must not answer on every host interface.
    setsid socat -v "TCP-LISTEN:$local_port,bind=127.0.0.1,fork,reuseaddr" "TCP:$ip:$port" \
        < /dev/null > "$log" 2>&1 &
    tap_pid=$!
    echo "$tap_pid" > "$STATE/tap-$local_port.pid"
    # A socat that dies instantly — the port is already taken — must be
    # reported loudly, not as success with a pidfile at a dead pid while
    # the next probe silently reaches whatever neighbour owns that port.
    # Confirm OUR listener exists, owned by our pid: a bare port grep
    # would be satisfied by the neighbour that caused the bind failure.
    # Bounded to ~1s.
    tap_ok=""
    for _ in $(seq 1 20); do
        kill -0 "$tap_pid" 2>/dev/null || break
        if ss -ltnp "sport = :$local_port" 2>/dev/null | grep -q "pid=$tap_pid,"; then
            tap_ok=1
            break
        fi
        sleep 0.05
    done
    if [ -z "$tap_ok" ]; then
        kill "$tap_pid" 2>/dev/null || true
        rm -f "$STATE/tap-$local_port.pid"
        die "tap failed: no listener on 127.0.0.1:$local_port owned by pid $tap_pid (port taken or socat died — see $log)"
    fi
    echo "tap: localhost:$local_port -> $ip:$port (pid $tap_pid, log $log)"
    ;;
reap)
    # Bounded, idempotent sweep of a dead run's leftovers. Default: this
    # run. --all sweeps every cl.lab.run-labeled resource older than
    # REAP_MINUTES (a janitor can call this from cron).
    scope="${1:-}"
    if [ "${scope:-}" = "--all" ]; then
        mins="${REAP_MINUTES:-60}"
        cutoff="$(date -d "-$mins min" +%s)"
        for id in $(docker ps -a --filter "label=$LABEL" --format '{{.Label "'$LABEL'"}}' | sort -u); do
            created="$(docker ps -a --filter "label=$LABEL=$id" --format '{{.CreatedAt}}' | head -1 | cut -d' ' -f1-2)"
            [ -n "$created" ] || continue
            # Unparseable CreatedAt must fail safe: skip the run, never
            # let the janitor force-remove on a timestamp it can't read.
            ts="$(date -d "$created" +%s 2>/dev/null || true)"
            [ -n "$ts" ] || continue
            if [ "$ts" -lt "$cutoff" ]; then
                CLOUD_LAB_RUN_ID="$id" CLOUD_LAB_STATE="/tmp/cloud-lab-runs/$id" "$SELF" _reap_one
            fi
        done
    else
        init_state; "$SELF" _reap_one
    fi
    ;;
_reap_one)
    # internal: force-clean everything this run created, no matter how it died
    kill_taps
    docker ps -aq --filter "label=$LABEL=$RUN_ID" | xargs -r docker rm -f >/dev/null 2>&1 || true
    docker network ls --filter "label=$LABEL=$RUN_ID" -q | xargs -r docker network rm >/dev/null 2>&1 || true
    docker volume ls --filter "label=$LABEL=$RUN_ID" -q | xargs -r docker volume rm >/dev/null 2>&1 || true
    # compose-created volumes carry only the project prefix, not the label
    docker volume ls --format '{{.Name}}' | grep "^${PROJECT}_" | xargs -r docker volume rm >/dev/null 2>&1 || true
    rm -rf "$STATE"
    echo "reaped run $RUN_ID"
    ;;
*)
    usage
    ;;
esac

