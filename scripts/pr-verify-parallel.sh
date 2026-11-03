#!/usr/bin/env bash
# pr-verify-parallel.sh — the parallel PR test runner with portal caching
# (#958, implementation lanes 1+2+3).
#
# WHAT IT DOES
#   Takes PR numbers as EXPLICIT dispatch input (opt-in, manual — the #881
#   zero-risk precedent: nothing auto-runs; the runner never fires on the
#   queue by itself), checks each PR head out into its own worktree, and
#   runs the offline per-PR set in parallel lanes:
#
#     hygiene / contract-fences / packaging sequential+concurrent /
#     shell-suites / go-battery            — the scripts/pr-verify.sh set
#     portal lane (conditional)            — assert-portal-bundle-contract
#                                            over the built guest SPA
#
#   Lane isolation: every lane runs under `unshare --mount` with a private
#   tmpfs on /tmp. That is what makes pre-#948 trees safe to run side by
#   side — their suites hardcode /tmp paths (the shared-LOGFILE flake
#   class), and a private /tmp per lane removes the collision domain
#   entirely. The Go build cache stays shared (it lives in $HOME, not
#   /tmp): the 5.3 GB warm cache is the whole point of parallel worktrees.
#
# PORTAL CACHE (#958 lane 1)
#   The portal bundle is a gitignored build product of `packaging/
#   portal-build.sh`, reproducibly keyed by the pin in packaging/
#   build-inputs.json (.portal.commit). The cache stores the staged guest
#   SPA (packaging/files/tollgate-captive-portal-site/) under
#   ${PR_RUNNER_CACHE:-~/.cache/tollgate-portal}/<pin>/. A lane hydrates
#   from the cache instead of the ~90 s npm build when the key exists; on
#   a miss it builds and populates the cache (safe: the output is
#   pin-determined, never PR-determined — the build clones the pinned
#   upstream SHA).
#   Sensitivity (#958 lane 3, module-only default): the portal lane runs
#   ONLY when the PR diff touches the portal surface (packaging/files/,
#   packaging/build-inputs.json, packaging/portal-build.sh) or --portal=all
#   forces it. A PR that retargets the pin gets a different cache key and
#   therefore a fresh build.
#
# TRUST GATE (hard, no override)
#   The runner executes tree-local scripts (that is what an offline suite
#   IS), so it refuses — always — PRs whose diff touches workflow or
#   runner surfaces: .github/workflows/, .github/scripts/, .ngit/,
#   scripts/pr-verify*.sh, ci.d/. This is the "never run it against PRs
#   with untrusted workflow changes" constraint; dispatch inputs are the
#   only thing the runner acts on.
#
# Usage:
#   scripts/pr-verify-parallel.sh [--jobs N] [--portal=auto|all|none] <PR> [<PR> ...]
#
#   --jobs N      parallel lanes (default 4, env PR_RUNNER_JOBS; the disk
#                 rule below caps concurrent worktrees regardless)
#   --portal      auto (default): portal lane only for portal-surface PRs;
#                 all: always; none: never
#
# Resource bounds
#   Worktrees are removed in a per-lane trap (the disk rule: clean after
#   each run) and the run root is pruned on exit; only the logs directory
#   and the portal cache survive. Results print as a table; the exit code
#   is non-zero if any lane failed or was refused.
set -u

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

JOBS="${PR_RUNNER_JOBS:-4}"
PORTAL_MODE=auto
PRS=()

die() { echo "pr-verify-parallel: $*" >&2; exit 2; }

while [ $# -gt 0 ]; do
    case "$1" in
        --jobs)  [ $# -ge 2 ] || die "--jobs needs a value"; JOBS="$2"; shift ;;
        --jobs=*) JOBS="${1#--jobs=}" ;;
        --portal) [ $# -ge 2 ] || die "--portal needs auto|all|none"; PORTAL_MODE="$2"; shift ;;
        --portal=*) PORTAL_MODE="${1#--portal=}" ;;
        -h|--help) sed -n '2,60p' "$0" | sed 's/^# \{0,1\}//' >&2; exit 0 ;;
        -*) die "unknown option: $1" ;;
        *)  [[ "$1" =~ ^[0-9]+$ ]] || die "not a PR number: $1"; PRS+=("$1") ;;
    esac
    shift
done
[ "${#PRS[@]}" -ge 1 ] || die "usage: $0 [--jobs N] [--portal=auto|all|none] <PR>..."
case "$PORTAL_MODE" in auto|all|none) ;; *) die "--portal must be auto|all|none" ;; esac
command -v gh >/dev/null 2>&1 || die "gh is required"
command -v unshare >/dev/null 2>&1 || die "unshare is required (lane /tmp isolation)"

SLUG="$(git -C "$ROOT" remote get-url origin | sed -E 's#.*github\.com[:/]##; s#\.git$##')"
[ -n "$SLUG" ] || die "cannot derive the GitHub slug"

# The run root must live OUTSIDE /tmp: lanes run with a private tmpfs over
# /tmp (the legacy-suite collision isolation), and anything the lane must
# share with the coordinator — logs, verdicts — has to be visible from both
# sides of that mount.
BASE="${XDG_CACHE_HOME:-$HOME/.cache}/tollgate-pr-parallel"
mkdir -p "$BASE"
RUNROOT="$(mktemp -d "$BASE/run.XXXXXX")"
LOGROOT="$RUNROOT/logs"; mkdir -p "$LOGROOT"
PORTAL_CACHE="${PR_RUNNER_CACHE:-$HOME/.cache/tollgate-portal}"
mkdir -p "$PORTAL_CACHE"

cleanup_root() { rm -rf "$RUNROOT"; }
trap cleanup_root EXIT

# Trust gate: file surfaces whose PR-side modification turns a lane into
# untrusted code execution. Refused outright — no override exists.
lane_is_trusted() { # lane_is_trusted <diff-file-list>
    local f
    for f in "$@"; do
        case "$f" in
            .github/workflows/*|.github/scripts/*|.ngit/*|ci.d/*|scripts/pr-verify*)
                return 1 ;;
        esac
    done
    return 0
}

portal_sensitive() { # portal_sensitive <diff-file-list>
    local f
    for f in "$@"; do
        case "$f" in
            packaging/files/*|packaging/build-inputs.json|packaging/portal-build.sh) return 0 ;;
        esac
    done
    return 1
}

# One lane. Everything below runs INSIDE the unshared mount namespace with
# a private /tmp; logs and results escape via $LANE_OUT (outside /tmp).
run_lane() { # run_lane <pr> <head-ref> <head-oid> <portal-mode>
    local pr="$1" _head_ref="$2" head_oid="$3" portal="$4"
    local LANE_OUT="$LOGROOT/$pr" WT="$RUNROOT/wt-$pr"
    mkdir -p "$LANE_OUT"
    unshare --mount --propagation private -- bash -c '
        set -u
        mount -t tmpfs tmpfs /tmp || exit 99
        # Host-visible per-lane TMPDIR: docker bind-mounts (the rootfs suite)
        # need files the host-side dockerd can see, which the private /tmp is
        # not. The path must stay SHORT — the testenv suite binds a Unix
        # socket under it and the 108-char sun_path limit is real (measured:
        # a cache-deep path made every src-module test fail to bind).
        export TMPDIR="/root/.pr-tmp/$1-$$"
        mkdir -p "$TMPDIR"
        pr="$1"; head_oid="$2"; wt="$3"; out="$4"; portal="$5"; root="$6"; cache="$7"
        echo "$portal" > "$out/portal-mode"
        cd "$root" || exit 99
        if ! git worktree add --quiet --detach "$wt" "$head_oid" 2>>"$out/setup.log"; then
            echo "SETUP-FAIL" > "$out/verdict"; exit 1
        fi
        trap "git -C \"$root\" worktree remove --force \"$wt\" >/dev/null 2>&1; true" EXIT
        cd "$wt" || exit 99

        legs=(); statuses=(); details=()
        record() { statuses+=("$2"); details+=("$3"); legs+=("$1"); }
        secs_of() { echo $(( $2 - $1 )); }

        # hygiene
        t=$SECONDS
        if git grep -nE "^(<{7} |>{7} )" -- . >/dev/null 2>&1; then
            record hygiene FAIL "committed conflict markers"
        else
            record hygiene PASS "no conflict markers"
        fi
        t_hygiene=$(secs_of $t $SECONDS)

        # contract fences (per-tree SKIP for fences the branch point lacks)
        t=$SECONDS
        red=""
        for c in "changelog fragments:python3 scripts/changelog-assemble.py check" \
                 "schema lint:node tests/contract/js-schema-lint.mjs" \
                 "entry-UI marker fence:bash tests/contract/check-entry-ui-marker-path.sh" \
                 "doc-facts:python3 tests/contract/check-docs-facts.py" \
                 "build purity:bash tests/contract/build-purity.sh" \
                 "SSID format:bash tests/contract/check-ssid-format.sh" \
                 "SSID naming:bash tests/contract/check-ssid-naming.sh" \
                 "deps sync:python3 tests/contract/check-deps-sync.py" \
                 "gonuts pin:bash tests/contract/gonuts-pin_test.sh" \
                 "gonuts carriers:python3 tests/contract/check-gonuts-pin.py" \
                 "mirror sync:python3 tests/contract/check-mirror-sync.py" \
                 "import paths:python3 tests/contract/check-import-paths.py" \
                 "gosum fence:bash tests/contract/check-gosum-completeness.sh"; do
            name="${c%%:*}"; cmd="${c#*:}"
            script="${cmd#* }"; script="${script%% *}"
            [ -e "$script" ] || continue
            if ! bash -c "$cmd" >>"$out/contract-fences.log" 2>&1; then red="$red $name"; fi
        done
        if [ -n "$red" ]; then record contract-fences FAIL "red:$red"; else record contract-fences PASS "all green"; fi
        t_fences=$(secs_of $t $SECONDS)

        # packaging suites, sequential then concurrent
        t=$SECONDS; seqred=""
        shopt -s nullglob
        pkgs=(tests/packaging/*_test.sh)
        for s in "${pkgs[@]}"; do bash "$s" >>"$out/packaging-seq.log" 2>&1 || seqred="$seqred $(basename "$s")"; done
        if [ -n "$seqred" ]; then record packaging-seq FAIL "red:$seqred"; else record packaging-seq PASS "${#pkgs[@]} suites"; fi
        t_pkgseq=$(secs_of $t $SECONDS)

        t=$SECONDS; concurrent=0; pids=()
        for s in "${pkgs[@]}"; do bash "$s" >"$out/conc-$(basename "$s").log" 2>&1 & pids+=($!); done
        for p in "${pids[@]}"; do wait "$p" || concurrent=1; done
        if [ "$concurrent" = 1 ]; then record packaging-conc FAIL "concurrent red (flake class #948)"; else record packaging-conc PASS "${#pids[@]} at once"; fi
        t_pkgconc=$(secs_of $t $SECONDS)

        # shell suites
        t=$SECONDS; shred=""
        shells=(tests/uci-defaults-*_test.sh)
        for s in "${shells[@]}"; do bash "$s" >>"$out/shell.log" 2>&1 || shred="$shred $(basename "$s")"; done
        if [ -n "$shred" ]; then record shell-suites FAIL "red:$shred"; else record shell-suites PASS "${#shells[@]} suites"; fi
        t_shell=$(secs_of $t $SECONDS)

        # go-battery (shared GOCACHE — it lives in \$HOME, untouched by /tmp isolation)
        t=$SECONDS
        if make go-battery >"$out/go-battery.log" 2>&1; then
            record go-battery PASS "$(find src -name go.mod | wc -l | tr -d " ") modules"
        else
            record go-battery FAIL "see log"
        fi
        t_battery=$(secs_of $t $SECONDS)

        # portal lane (conditional, cached)
        t=$SECONDS; portal_status=SKIP; portal_detail="not portal-sensitive"
        if [ "$portal" = all ] || { [ "$portal" = auto ] && [ -n "$PORTAL_SENSITIVE" ]; }; then
            pin="$(python3 -c "import json;print(json.load(open(\"packaging/build-inputs.json\"))[\"portal\"][\"commit\"])" 2>/dev/null)"
            if [ -z "$pin" ]; then
                portal_status=FAIL; portal_detail="no portal pin in build-inputs.json"
            elif [ -d "$cache/$pin" ]; then
                rm -rf packaging/files/tollgate-captive-portal-site
                cp -a "$cache/$pin" packaging/files/tollgate-captive-portal-site
                if bash tests/packaging/assert-portal-bundle-contract.sh >"$out/portal.log" 2>&1; then
                    portal_status=PASS; portal_detail="cache hit ($pin)"
                else portal_status=FAIL; portal_detail="cache hit, contract red"; fi
            else
                if bash packaging/portal-build.sh >"$out/portal-build.log" 2>&1; then
                    rm -rf "$cache/$pin"; cp -a packaging/files/tollgate-captive-portal-site "$cache/$pin"
                    if bash tests/packaging/assert-portal-bundle-contract.sh >"$out/portal.log" 2>&1; then
                        portal_status=PASS; portal_detail="built fresh ($pin), cache populated"
                    else portal_status=FAIL; portal_detail="built, contract red"; fi
                else portal_status=FAIL; portal_detail="portal-build failed"; fi
            fi
        fi
        record portal "$portal_status" "$portal_detail"
        t_portal=$(secs_of $t $SECONDS)

        # verdict + timings (the timing row rides as a detail on the verdict line)
        worst=0
        for s in "${statuses[@]}"; do [ "$s" = FAIL ] && worst=1; done
        if [ "$worst" = 0 ]; then echo "PASS" > "$out/verdict"; else echo "FAIL" > "$out/verdict"; fi
        {
            for i in "${!legs[@]}"; do
                printf "%s|%s|%s\n" "${legs[$i]}" "${statuses[$i]}" "${details[$i]}"
            done
        } > "$out/legs.tsv"
        printf "hygiene=%ss fences=%ss pkg-seq=%ss pkg-conc=%ss shell=%ss battery=%ss portal=%ss\n" \
            "$t_hygiene" "$t_fences" "$t_pkgseq" "$t_pkgconc" "$t_shell" "$t_battery" "$t_portal" > "$out/timings"
        exit "$worst"
    ' _ "$pr" "$head_oid" "$WT" "$LANE_OUT" "$portal" "$ROOT" "$PORTAL_CACHE"
}

# ---- upfront, serialized: metadata + fetch (avoids lock contention) ----
declare -A HEAD_REF HEAD_OID DIFF_FILES VERDICT
for pr in "${PRS[@]}"; do
    meta="$(gh pr view "$pr" -R "$SLUG" --json state,headRefName,headRefOid,files \
        --jq='[.state,.headRefName,.headRefOid,([.files[].path]|join(" "))]|@tsv' 2>/dev/null)" \
        || die "gh pr view $pr failed"
    IFS=$'\t' read -r state ref oid files <<<"$meta"
    [ "$state" = OPEN ] || die "PR #$pr is $state — the runner takes open PRs"
    HEAD_REF[$pr]="$ref"; HEAD_OID[$pr]="$oid"
    DIFF_FILES[$pr]="$files"
    git -C "$ROOT" fetch --quiet origin "$ref" || die "cannot fetch $ref"
    git -C "$ROOT" rev-parse --verify --quiet "${oid}^{commit}" >/dev/null || die "head $oid not local"
done

# ---- trust gate (serialized, before any lane spawns) ----
REFUSED=0
for pr in "${PRS[@]}"; do
    read -r -a files <<<"${DIFF_FILES[$pr]}"
    if ! lane_is_trusted "${files[@]}"; then
        echo "REFUSED #$pr — diff touches workflow/runner surfaces (untrusted-code-execution class); dispatch a human review instead"
        VERDICT[$pr]="REFUSED"
        REFUSED=1
    fi
done

# ---- lanes with a slot cap ----
START=$SECONDS
declare -A LANE_PID
running=0
for pr in "${PRS[@]}"; do
    [ "${VERDICT[$pr]:-}" = REFUSED ] && continue
    read -r -a files <<<"${DIFF_FILES[$pr]}"
    # portal sensitivity is resolved per-lane into the mode argument
    pmode="$PORTAL_MODE"
    if [ "$PORTAL_MODE" = auto ]; then
        if portal_sensitive "${files[@]}"; then pmode=all; else pmode=none; fi
    fi
    while [ "$running" -ge "$JOBS" ]; do
        wait -n || true
        running=$((running - 1))
    done
    run_lane "$pr" "${HEAD_REF[$pr]}" "${HEAD_OID[$pr]}" "$pmode" &
    LANE_PID[$pr]=$!
    running=$((running + 1))
done
# drain remaining lanes; verdicts come from the lanes' verdict FILES, not
# wait statuses — the slot-cap `wait -n` above may already have reaped a
# pid, and `wait <pid>` would then report 127 and fake a failure.
for pid in "${LANE_PID[@]:-}"; do wait "$pid" 2>/dev/null || true; done
FAILS=0
for pr in "${PRS[@]}"; do
    [ "${VERDICT[$pr]:-}" = REFUSED ] && continue
    case "$(cat "$LOGROOT/$pr/verdict" 2>/dev/null)" in
        PASS) VERDICT[$pr]=PASS ;;
        *)    VERDICT[$pr]=FAIL; FAILS=$((FAILS+1)) ;;
    esac
done
WALL=$((SECONDS - START))

# ---- summary ----
echo
echo "=============================================="
printf '%-8s %-9s %s\n' "PR" "VERDICT" "LEGS"
for pr in "${PRS[@]}"; do
    if [ "${VERDICT[$pr]}" = REFUSED ]; then
        printf '%-8s %-9s %s\n' "#$pr" REFUSED "workflow/runner surface in diff"
        continue
    fi
    legs="(none)"
    [ -f "$LOGROOT/$pr/legs.tsv" ] && legs="$(cut -d'|' -f2 "$LOGROOT/$pr/legs.tsv" | tr '\n' ',' )"
    printf '%-8s %-9s %s\n' "#$pr" "${VERDICT[$pr]}" "$legs"
    [ -f "$LOGROOT/$pr/timings" ] && sed 's/^/         /' "$LOGROOT/$pr/timings"
    [ -f "$LOGROOT/$pr/legs.tsv" ] && while IFS='|' read -r l s d; do printf '         %-16s %-5s %s\n' "$l" "$s" "$d"; done < "$LOGROOT/$pr/legs.tsv"
done
echo "=============================================="
echo "wall: ${WALL}s for ${#PRS[@]} PRs at --jobs $JOBS"
echo "per-PR detail: $LOGROOT (removed on exit — rerun with PR_RUNNER_KEEP=1 to retain)"
if [ "${PR_RUNNER_KEEP:-0}" = 1 ]; then trap - EXIT; echo "kept: $RUNROOT"; fi
[ "$FAILS" = 0 ] && [ "$REFUSED" = 0 ] && exit 0 || exit 1
