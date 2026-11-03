#!/usr/bin/env bash
# Router lane: run the REAL sidecar binary ON a labgrid router place and drive it
# over the wire, proving the deployable artifact behaves like the host build.
#
# Why this lane exists: every other test in this repo proves the code works on
# the machine that compiled it. That is the class of evidence that says nothing
# about the router — AGENTS.md records the 81-kicad-headless role, where a green
# suite and a passing syntax check shipped a role that installed the wrong major
# version and did it in a way that could never upgrade. We are cross-compiling a
# statically linked musl binary to a device with a different libc, a different
# CPU and 480 MB of RAM; "cargo test passed on x86_64" is not evidence for it.
#
# HONEST SCOPE: this proves the SIDECAR SEGMENT on the router — the binary
# starts, binds its socket, parses the nested params, derives the golden channel
# id and refuses each adversarial registration. It does not prove a purchase, a
# swap, a mint call or settlement; none of those exist in this milestone, and the
# artifact is checked to carry no settlement capability string at all.
#
# Method: frames are built on the host (where python3 exists), shipped as files,
# sent with `socat` on the router, and the replies are shipped back and asserted
# here. Stock OpenWrt has neither python3 nor jq, so nothing is parsed on the
# device and no assertion is computed by the machine that built the binary.
#
# Usage:
#   bash tests/contract/spilman-router-lane.sh [--host HOST] [--skip-build]
#
# Exit: 0 pass, 1 fail, 77 skip (no target resolved) — a skip is NOT a pass.
set -uo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
SIDECAR_DIR="$REPO_ROOT/sidecar/spilman"
FIXTURE="$SIDECAR_DIR/tests/fixtures/registration_golden.json"
TARGET=${SPILMAN_TARGET:-aarch64-unknown-linux-musl}
BIN="$SIDECAR_DIR/target/$TARGET/release/tollgate-spilman-sidecar"
REMOTE_DIR=${SPILMAN_REMOTE_DIR:-/tmp/spilman-lane}
SKIP_BUILD=0
HOST=${SPILMAN_ROUTER_HOST:-}
WORK=$(mktemp -d "${TMPDIR:-/tmp}/spilman-lane.XXXXXX")

while [ $# -gt 0 ]; do
    case "$1" in
        --host) HOST=$2; shift 2 ;;
        --skip-build) SKIP_BUILD=1; shift ;;
        -h|--help) sed -n '2,32p' "$0"; exit 0 ;;
        *) echo "unknown argument: $1" >&2; exit 2 ;;
    esac
done

fail=0
step() { printf '\n=== %s ===\n' "$*"; }
ok()   { printf '  ok   %s\n' "$*"; }
bad()  { printf '  FAIL %s\n' "$*"; fail=$((fail + 1)); }
skip() { printf '  SKIP %s\n' "$*"; }

# The place's address is a device fact owned by the labgrid registry. Reading it
# here keeps one declaration; a literal in this script would be exactly the drift
# AGENTS.md fences.
resolve_host() {
    [ -n "$HOST" ] && { echo "$HOST"; return 0; }
    local registry=${LABGRID_REGISTRY:-$HOME/hermes-orchestration/state/fleet/labgrid-places.json}
    local place=${LABGRID_PLACE:-gl-mt3000}
    [ -f "$registry" ] || return 1
    python3 - "$registry" "$place" <<'PY'
import json, sys
registry, place = sys.argv[1], sys.argv[2]
for router in json.load(open(registry)).get("routers", []):
    if router.get("name") == place:
        print(router.get("lan_ip", ""))
        break
PY
}

step "resolve the labgrid place"
if ! HOST=$(resolve_host) || [ -z "$HOST" ]; then
    skip "no place resolved (set SPILMAN_ROUTER_HOST, or check LABGRID_REGISTRY/LABGRID_PLACE)"
    echo "  This lane is TARGET evidence; with no target it reports nothing" >&2
    echo "  rather than passing. Exit 77." >&2
    exit 77
fi
ok "place ${LABGRID_PLACE:-gl-mt3000} -> $HOST"

SSH=(ssh -o BatchMode=yes -o ConnectTimeout=10 -o StrictHostKeyChecking=yes "root@$HOST")

step "identity of the target"
KARCH=$("${SSH[@]}" 'uname -m' 2>/dev/null) || { bad "cannot ssh to $HOST"; exit 1; }
[ "$KARCH" = "aarch64" ] && ok "uname -m = aarch64" || bad "unexpected arch: '$KARCH'"
ERA=$("${SSH[@]}" 'command -v apk >/dev/null 2>&1 && echo apk || echo not-apk' 2>/dev/null)
[ "$ERA" = "apk" ] && ok "apk era (25.x, the only feature target)" || bad "not the apk era: $ERA"
REL=$("${SSH[@]}" '. /etc/openwrt_release 2>/dev/null; printf %s "${DISTRIB_RELEASE:-unknown}"' 2>/dev/null)
ok "OpenWrt release $REL"
"${SSH[@]}" 'command -v socat >/dev/null' 2>/dev/null && ok "socat present (the only tool this lane needs)" \
    || { skip "router has no socat; cannot drive an AF_UNIX socket"; exit 77; }

step "build for the target"
if [ "$SKIP_BUILD" -eq 1 ]; then
    ok "--skip-build: reusing $BIN"
else
    if cargo build --release --target "$TARGET" --manifest-path "$SIDECAR_DIR/Cargo.toml" \
            >"$WORK/build.log" 2>&1; then
        ok "cargo build --release --target $TARGET"
    else
        bad "cargo build failed (tail of $WORK/build.log follows)"
        tail -5 "$WORK/build.log" | sed 's/^/       /'
        exit 1
    fi
fi
[ -f "$BIN" ] || { bad "no artifact at $BIN"; exit 1; }
BIN_SHA=$(sha256sum "$BIN" | cut -d' ' -f1)
ok "artifact $(basename "$BIN") sha256 $BIN_SHA"

step "deploy and verify the artifact byte-for-byte"
"${SSH[@]}" "mkdir -p $REMOTE_DIR && chmod 700 $REMOTE_DIR" || bad "could not create $REMOTE_DIR"
# Stop any instance from a previous run BEFORE overwriting. A running binary
# holds its text image, so scp fails with "Text file busy", and a leftover
# process turns a deploy step into a misleading transfer error.
#
# BusyBox ships `pgrep` but NOT `pkill`, so the obvious `pkill -f ...` exits
# "not found" and, with its output suppressed, leaves the old process running —
# instances then accumulate until the overwrite fails. Kill by pid instead.
stop_remote_sidecar() {
    # Match the BASENAME, not "$REMOTE_DIR/tollgate-spilman-sidecar". The binary
    # is launched as `./tollgate-spilman-sidecar --socket ...` (relative), so an
    # absolute-path pattern matches nothing, the kill never fires, and the next
    # scp dies with "Text file busy" — which reads like a transfer fault and is
    # really a leftover process. The `[t]` keeps pgrep's own ash command line
    # from matching itself.
    "${SSH[@]}" "for p in \$(pgrep -f '[t]ollgate-spilman-sidecar' 2>/dev/null); do kill \$p 2>/dev/null; done
        for i in 1 2 3 4 5 6 7 8 9 10; do
            pgrep -f '[t]ollgate-spilman-sidecar' >/dev/null 2>&1 || exit 0
            sleep 0.3
        done
        for p in \$(pgrep -f '[t]ollgate-spilman-sidecar' 2>/dev/null); do kill -9 \$p 2>/dev/null; done
        sleep 0.3; true" 2>/dev/null
}
stop_remote_sidecar
if "${SSH[@]}" "pgrep -f '[t]ollgate-spilman-sidecar' >/dev/null 2>&1"; then
    bad "a sidecar from a previous run is still alive on $HOST; refusing to overwrite"
    exit 1
fi
ok "no previous instance holding the binary"
# Dropbear has no sftp-server, so modern scp (which speaks SFTP) fails with
# "/usr/libexec/sftp-server: not found". Force the legacy protocol it does speak,
# and retry: a single dropped connection over a lab link must not read as a
# product failure. The sha256 check below is what proves the transfer, so a
# retry cannot mask a corrupted copy.
copy_to_router() { # $1 = source glob
    local src=$1 attempt
    for attempt in 1 2 3; do
        # shellcheck disable=SC2086 # the glob must expand
        scp -O -q -o BatchMode=yes -o ConnectTimeout=10 $src "root@$HOST:$REMOTE_DIR/" && return 0
        sleep 2
    done
    return 1
}
copy_to_router "$BIN" && ok "scp -> $HOST:$REMOTE_DIR" || { bad "scp failed after 3 attempts"; exit 1; }
REMOTE_SHA=$("${SSH[@]}" "sha256sum $REMOTE_DIR/tollgate-spilman-sidecar" | cut -d' ' -f1)
[ "$REMOTE_SHA" = "$BIN_SHA" ] && ok "remote sha256 matches the built artifact" \
    || bad "sha256 mismatch: local $BIN_SHA remote $REMOTE_SHA"

step "build the request frames"
python3 - "$FIXTURE" "$WORK" <<'PY' || { bad "frame builder failed"; exit 1; }
import json, sys, pathlib
fixture, outdir = sys.argv[1], pathlib.Path(sys.argv[2])
g = json.load(open(fixture))
reg = g["registration"]
nested = json.loads(g["nested_params_json"])
now = 1700000000   # well before the fixture's expiry, so the happy path is clean

def params(**over):
    p = {
        "receiver_secret_hex": g["receiver_secret_hex"],
        "trusted_mint_url": g["trusted_mint_url"],
        "trusted_keyset_info": g["trusted_keyset_info"],
        "registration": json.loads(json.dumps(reg)),
        "params_json": json.dumps(nested, sort_keys=True, separators=(",", ":")),
        "now_unix": now,
    }
    for path, value in over.items():
        # `"now_unix" in p` first: rpartition("__") would split it into
        # ("", "", "now_unix") and index p[""], which is the bug this line
        # exists to avoid.
        if path in p:
            p[path] = value
            continue
        target, _, field = path.partition("__")
        if not field or target not in p:
            raise SystemExit(f"unknown override {path!r}")
        p[target][field] = value
    return p

cases = {
    "golden": params(),
    # (a) the receiver pubkey is not the one derived from the payee's secret
    "receiver_mismatch": params(registration__receiver_pubkey="03" + reg["receiver_pubkey"][2:]),
    # (b) the NESTED params name another mint while the outer record and the
    # trusted URL both stay correct: only parsing the nested blob catches it
    "nested_mint": params(params_json=json.dumps({**nested, "mint": "https://evil.example"},
                                                 sort_keys=True, separators=(",", ":"))),
    # (b) nested capacity disagrees with the outer capacity
    "nested_capacity": params(params_json=json.dumps({**nested, "capacity": 1},
                                                     sort_keys=True, separators=(",", ":"))),
    # (b) the outer record names a mint this daemon does not trust
    "untrusted_outer_mint": params(trusted_mint_url="https://evil.example"),
    # (c) expired: the clock is SUPPLIED, so expiry is judgeable offline
    "expired": params(now_unix=4102444801),
    # (c) zero capacity is not a channel
    "zero_capacity": params(registration__capacity=0),
    # (c) an unsupported unit
    "unsupported_unit": params(registration__unit="usd"),
    # (c) a channel id that cannot be derived
    "bad_sender": params(registration__sender_pubkey="not-a-pubkey"),
}
for name, p in cases.items():
    frame = {"version": 1, "request_id": f"lane-{name}", "operation_id": f"lane-{name}",
             "method": "validate_registration", "params": p}
    (outdir / f"{name}.json").write_text(json.dumps(frame) + "\n")
(outdir / "expected.json").write_text(json.dumps({
    "channel_id": g["expected"]["channel_id"],
    "canonical_params_json": g["expected"]["canonical_params_json"],
    "cases": {
        "golden": None,
        "receiver_mismatch": "receiver_pubkey_mismatch",
        "nested_mint": "mint_not_trusted",
        "nested_capacity": "nested_params_mismatch",
        "untrusted_outer_mint": "mint_not_trusted",
        "expired": "registration_expired",
        "zero_capacity": "zero_capacity",
        "unsupported_unit": "unsupported_unit",
        "bad_sender": "channel_id_not_derivable",
    },
}, indent=1))
print("built", len(cases), "frames")
PY
ok "frames built for $(python3 -c 'import json,sys;print(len(json.load(open(sys.argv[1]))["cases"]))' "$WORK/expected.json") cases"

step "send the frames from the router"
copy_to_router "$WORK/*.json" && ok "frames shipped" || bad "could not ship frames"

# Start detached: `setsid` so it survives the ssh session ending.
stop_remote_sidecar
"${SSH[@]}" "cd $REMOTE_DIR && setsid ./tollgate-spilman-sidecar --socket $REMOTE_DIR/s.sock \
        >$REMOTE_DIR/sidecar.log 2>&1 &
    for i in 1 2 3 4 5 6 7 8 9 10; do [ -S $REMOTE_DIR/s.sock ] && break; sleep 0.2; done
    [ -S $REMOTE_DIR/s.sock ] && echo BOUND || { echo NOBOUND; cat $REMOTE_DIR/sidecar.log; }" >"$WORK/start.log" 2>&1
if grep -q BOUND "$WORK/start.log"; then
    ok "sidecar bound its socket on the router"
else
    bad "sidecar did not bind $REMOTE_DIR/s.sock"
    sed 's/^/       /' "$WORK/start.log" | head -6
    exit 1
fi

# One socat invocation per frame, each writing its reply to a file. Each frame is
# a separate connection because the sidecar closes after certain refusals.
for f in "$WORK"/*.json; do
    name=$(basename "$f" .json)
    [ "$name" = "expected" ] && continue
    "${SSH[@]}" "socat -t 10 - UNIX-CONNECT:$REMOTE_DIR/s.sock < $REMOTE_DIR/$name.json > $REMOTE_DIR/$name.reply 2>/dev/null" \
        >/dev/null 2>&1
    scp -O -q -o BatchMode=yes "root@$HOST:$REMOTE_DIR/$name.reply" "$WORK/$name.reply" 2>/dev/null
done
ok "replies collected"

step "assert against the golden vector"
python3 - "$WORK" <<'PY'
import json, sys, pathlib
work = pathlib.Path(sys.argv[1])
expected = json.loads((work / "expected.json").read_text())
failures = []

def reply(name):
    path = work / f"{name}.reply"
    if not path.exists() or not path.read_text().strip():
        return None
    try:
        return json.loads(path.read_text().strip().splitlines()[0])
    except json.JSONDecodeError:
        return {"__unparseable__": path.read_text()[:120]}

for name, want in expected["cases"].items():
    r = reply(name)
    if r is None:
        failures.append(f"{name}: no reply over the socket")
        continue
    got = r.get("error", "")
    if want is None:
        # Happy path: the derived values must equal the crate-generated vector.
        ch = r.get("validated", {}).get("channel_id")
        cp = r.get("validated", {}).get("canonical_params_json")
        if ch != expected["channel_id"]:
            failures.append(f"{name}: channel_id {ch!r} != {expected['channel_id']!r}")
        elif cp != expected["canonical_params_json"]:
            failures.append(f"{name}: canonical_params_json differs from the pinned crate's own output")
        else:
            print(f"  ok   {name}: channel_id and canonical_params_json match the golden vector")
    else:
        if got == want:
            print(f"  ok   {name}: refused with {want}")
        else:
            failures.append(f"{name}: expected {want!r}, got {got!r}")

if failures:
    for f in failures:
        print(f"  FAIL {f}")
    sys.exit(1)
PY
[ $? -eq 0 ] || fail=$((fail + 1))

step "the artifact cannot move value"
FOUND=$("${SSH[@]}" "grep -qaE 'settlement\\.v1|prepare_close|verify_prepared' $REMOTE_DIR/tollgate-spilman-sidecar && echo YES || echo NO" 2>/dev/null)
if [ "$FOUND" = "NO" ]; then
    ok "no settlement or signing capability string in the on-router artifact"
else
    bad "the on-router artifact advertises a settlement/signing surface"
fi
CAPS=$("${SSH[@]}" "printf '{\"version\":1,\"request_id\":\"lane-caps\",\"operation_id\":\"lane-caps\",\"method\":\"capabilities\"}\\n' | socat -t 10 - UNIX-CONNECT:$REMOTE_DIR/s.sock" 2>/dev/null)
if printf '%s' "$CAPS" | grep -q 'validate_registration.v1'; then
    ok "capabilities advertise validate_registration.v1"
else
    bad "capabilities do not advertise validate_registration.v1: $(printf '%s' "$CAPS" | cut -c1-120)"
fi
if printf '%s' "$CAPS" | grep -qE 'settlement|swap|mint\\.'; then
    bad "capabilities advertise a money-moving surface"
else
    ok "capabilities advertise no money-moving surface"
fi

stop_remote_sidecar

step "result"
if [ "$fail" -eq 0 ]; then
    echo "  PASS: sidecar segment verified on $HOST ($REL, $KARCH), artifact sha256 $BIN_SHA"
else
    echo "  $fail check group(s) failed"
fi
rm -rf "$WORK"
exit $([ "$fail" -eq 0 ] && echo 0 || echo 1)
