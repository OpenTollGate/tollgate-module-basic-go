#!/usr/bin/env bash
# hygiene.sh — strict process/service hygiene for the happy-path suite (#947).
#
# Two co-residency failure classes on shared hosts (ai-legion runs concurrent
# lanes, including a tollgate-virtual-lab):
#   1. a stale process from a PREVIOUS suite run still holds :2121;
#   2. a foreign lane's service (e.g. the vlab mockgate) holds :2121.
# Only class 1 may be cleaned automatically. A candidate is OURS — and thus
# killable — only when process identity is proven by BOTH:
#   port evidence:  it listens on the module port, or it is a recognized
#                   module process name that no longer listens (a leaked run),
#   tree evidence:  /proc/<pid>/exe resolves under one of the given roots,
#                   or a cmdline token is a recognized module binary path
#                   under one of the roots.
# Anything else holding the port is reported with full evidence and left
# alone — the wrapper never kills a process it cannot identify, and it logs
# every decision it makes (HPHYG lines on stderr).
#
# Usage:
#   hygiene_teardown [--dry-run] [--port N] --root DIR [--root DIR ...]
#   hygiene_scan     [--port N]     --root DIR [--root DIR ...]
#   hygiene_teardown [--dry-run] [--port N] --root DIR [--root DIR ...]

DryRun=0
ModulePort=2121
Roots=()

hygiene_log() { printf 'HPHYG %s\n' "$*" >&2; }

hygiene_args() {
    while [ $# -gt 0 ]; do
        case "$1" in
            --dry-run) DryRun=1 ;;
            --port) ModulePort="$2"; shift ;;
            --root)
                [ -d "$2" ] || { hygiene_log "root $2 is not a directory: skipping"; shift; continue; }
                Roots+=("$(cd "$2" && pwd)")
                shift
                ;;
            *) printf 'hygiene: unknown argument %s\n' "$1" >&2; return 2 ;;
        esac
        shift
    done
    [ ${#Roots[@]} -gt 0 ] || { hygiene_log "no usable --root given: refusing to identify anything"; return 2; }
}

port_listener_pids() {
    ss -ltnp 2>/dev/null | awk -v port=":$ModulePort" '
        $4 ~ port"$" {
            line=$0
            while (match(line, /pid=[0-9]+/)) {
                print substr(line, RSTART+4, RLENGTH-4)
                line=substr(line, RSTART+RLENGTH)
            }
        }' | sort -u
}

is_recognized_module_name() {
    case "${1##*/}" in
        tollgate-wrt|tollgate) return 0 ;;
        *) return 1 ;;
    esac
}

under_roots() {
    local p="$1"
    local r
    for r in "${Roots[@]}"; do
        [ -n "$r" ] && case "$p" in
            "$r"/*|"$r") return 0 ;;
        esac
    done
    return 1
}

# Identity proof for one pid: echoes "ours" or "foreign:<reason>".
classify_pid() {
    local pid="$1"
    local exe="" arg0="" token
    [ -d "/proc/$pid" ] || { echo "foreign:no-proc"; return; }
    exe="$(readlink -f "/proc/$pid/exe" 2>/dev/null || true)"
    if [ -n "$exe" ] && under_roots "$exe"; then
        echo "ours:exe=$exe"
        return
    fi
    for token in $(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null); do
        if is_recognized_module_name "$token" && under_roots "$token"; then
            echo "ours:cmdline=$token"
            return
        fi
    done
    arg0="$(tr '\0' '\n' < "/proc/$pid/cmdline" 2>/dev/null | head -1)"
    echo "foreign:exe=${exe:-?} arg0=${arg0:-?}"
}

# Docker containers this suite created (exact name prefix; never a wildcard
# over unrelated containers).
suite_containers() {
    command -v docker >/dev/null 2>&1 || return 0
    docker ps -a --format '{{.Names}}' 2>/dev/null | grep -E '^tg-happy-path-' || true
}

hygiene_scan() {
    hygiene_args "$@" || return 2
    local pid verdict
    hygiene_log "scan: port=$ModulePort roots=${Roots[*]}"
    for pid in $(port_listener_pids); do
        verdict="$(classify_pid "$pid")"
        hygiene_log "port holder pid=$pid -> $verdict"
    done
    for pid in $(pgrep -x 'tollgate-wrt|tollgate' 2>/dev/null || true); do
        verdict="$(classify_pid "$pid")"
        case "$verdict" in ours:*) hygiene_log "module-name stray pid=$pid -> $verdict" ;; esac
    done
    local c
    for c in $(suite_containers); do
        hygiene_log "suite container $c (docker)"
    done
}

hygiene_teardown() {
    hygiene_args "$@" || return 2
    local pid verdict name
    [ "$DryRun" = "1" ] && hygiene_log "DRY-RUN: nothing will be signaled"
    for pid in $(port_listener_pids); do
        verdict="$(classify_pid "$pid")"
        case "$verdict" in
            ours:*)
                hygiene_log "teardown: killing OUR leaked pid=$pid ($verdict) holding :$ModulePort"
                [ "$DryRun" = "1" ] || kill "$pid" 2>/dev/null || hygiene_log "teardown: kill $pid failed"
                ;;
            *)
                hygiene_log "REFUSING to kill pid=$pid holding :$ModulePort — $verdict (not under our roots; likely another lane's service)"
                ;;
        esac
    done
    for pid in $(pgrep -x 'tollgate-wrt|tollgate' 2>/dev/null || true); do
        verdict="$(classify_pid "$pid")"
        case "$verdict" in
            ours:*)
                hygiene_log "teardown: killing OUR module-name stray pid=$pid ($verdict)"
                [ "$DryRun" = "1" ] || kill "$pid" 2>/dev/null || hygiene_log "teardown: kill $pid failed"
                ;;
            *) hygiene_log "leaving pid=$pid alone — recognized name but $verdict" ;;
        esac
    done
    for name in $(suite_containers); do
        hygiene_log "teardown: removing suite container $name"
        [ "$DryRun" = "1" ] || docker rm -f "$name" >/dev/null 2>&1 || true
    done
    hygiene_log "teardown complete (dry-run=$DryRun)"
}
