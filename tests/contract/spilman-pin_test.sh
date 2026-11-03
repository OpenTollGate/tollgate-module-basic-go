#!/usr/bin/env bash
# Contract test: tests/contract/check-spilman-sidecar-surface.py verdicts.
#
# The Spilman pin fence exists because the sidecar compiles a pinned crate that
# DERIVES CHANNEL IDENTITY: a different revision derives a different channel id
# for the same registration, so drift is not a style problem. A fence that
# cannot fail is decoration, so every drift shape is planted in a fixture tree
# and the checker must refuse it BY NAME, then a clean tree must pass.
#
# The last two cases matter most:
#
#   * a NO-OP mutation must not count — the keyset-substitution case in the
#     Rust suite initially swapped a key for the value already at that amount,
#     which "passed" while proving nothing;
#   * the transport-only SHAPE is checked, because the pin can be perfect while
#     the dependency has quietly become mandatory. The manifest would still be
#     correct and the Go build would no longer be provably protocol-free.
#
# Offline only: no cargo, no network. Run from anywhere:
#   bash tests/contract/spilman-pin_test.sh
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
CHECK="$ROOT/tests/contract/check-spilman-sidecar-surface.py"

PIN="c2083defc2015ad0bae2a8a086a2876bc775ff79"
CASHU="a056e0f0f69e94f431b1aeb90d883f18c61ea4c6"
SPILMAN_REPO="https://github.com/SatsAndSports/cashu_spilman_channels.git"
CDK_REPO="https://github.com/cashubtc/cdk.git"

cases=0
fails=0
pass() { cases=$((cases + 1)); printf 'ok   %s\n' "$1"; }
fail() { cases=$((cases + 1)); fails=$((fails + 1)); printf 'FAIL %s\n' "$1" >&2; }

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# --- fixture writers ---------------------------------------------------------

seed_manifest() { # seed_manifest <dir> [commit] [cashu_commit]
    local d="$1" commit="${2:-$PIN}" cashu="${3:-$CASHU}"
    mkdir -p "$d/packaging"
    cat >"$d/packaging/build-inputs.json" <<JSON
{
  "schema": 1,
  "go": {"version": "1.26.10"},
  "spilman_protocol": {
    "repo": "$SPILMAN_REPO",
    "crate": "cdk-spilman",
    "version": "0.2.0",
    "commit": "$commit",
    "cashu_commit": "$cashu"
  }
}
JSON
}

seed_cargo_toml() { # seed_cargo_toml <dir> [spilman-pin-style] [cdk-style]
    local d="$1" spilman="${2:-rev}" cdk="${3:-tag}"
    mkdir -p "$d/sidecar/spilman/src"
    local spilman_line cdk_line
    if [ "$spilman" = rev ]; then
        spilman_line="cdk-spilman = { git = \"$SPILMAN_REPO\", rev = \"$PIN\", default-features = false, optional = true }"
    elif [ "$spilman" = tag ]; then
        spilman_line="cdk-spilman = { git = \"$SPILMAN_REPO\", tag = \"v0.2.0\", optional = true }"
    elif [ "$spilman" = branch ]; then
        spilman_line="cdk-spilman = { git = \"$SPILMAN_REPO\", branch = \"main\", optional = true }"
    elif [ "$spilman" = nopin ]; then
        spilman_line="cdk-spilman = { git = \"$SPILMAN_REPO\", optional = true }"
    else # wrongrev
        spilman_line="cdk-spilman = { git = \"$SPILMAN_REPO\", rev = \"1111111111111111111111111111111111111111\", optional = true }"
    fi
    if [ "$cdk" = tag ]; then
        cdk_line="cashu = { git = \"$CDK_REPO\", tag = \"v0.18.1\", package = \"cashu\", default-features = false, optional = true }"
    else
        cdk_line="cashu = { git = \"$CDK_REPO\", branch = \"main\", optional = true }"
    fi
    cat >"$d/sidecar/spilman/Cargo.toml" <<TOML
[package]
name = "tollgate-spilman-sidecar"
version = "0.1.0"
edition = "2021"

[features]
default = ["protocol"]
protocol = ["dep:cdk-spilman", "dep:cashu"]

[dependencies]
libc = "0.2"
$spilman_line
$cdk_line
TOML
    # The source must carry the transport-only branch, or leg 4 refuses for a
    # different reason and the planted drift would be misattributed.
    cat >"$d/sidecar/spilman/src/protocol.rs" <<'RS'
pub const CAPABILITY_VALIDATE_REGISTRATION: &str = "validate_registration.v1";
#[cfg(feature = "protocol")]
pub const CAPABILITIES: &[&str] = &["transport.v1", CAPABILITY_VALIDATE_REGISTRATION];
#[cfg(not(feature = "protocol"))]
pub const CAPABILITIES: &[&str] = &["transport.v1"];
RS
}

seed_lock() { # seed_lock <dir> [commit] [cashu]
    local d="$1" commit="${2:-$PIN}" cashu="${3:-$CASHU}"
    mkdir -p "$d/sidecar/spilman"
    cat >"$d/sidecar/spilman/Cargo.lock" <<LOCK
[[package]]
name = "cdk-spilman"
version = "0.2.0"
source = "git+$SPILMAN_REPO?rev=$commit#$commit"

[[package]]
name = "cashu"
version = "0.18.1"
source = "git+$CDK_REPO?tag=v0.18.1#$cashu"
LOCK
}

seed_aligned() { # seed_aligned <dir> — a tree the fence must accept
    seed_manifest "$1"
    seed_cargo_toml "$1"
    seed_lock "$1"
}

require_verdict() { # require_verdict <dir> <accept|refuse> <label> [needle]
    local d="$1" want="$2" label="$3" needle="${4:-}" out rc
    out="$(python3 "$CHECK" --root "$d" 2>&1)"; rc=$?
    if [ "$want" = accept ] && [ "$rc" -eq 0 ]; then pass "$label"; return; fi
    if [ "$want" = refuse ] && [ "$rc" -ne 0 ]; then
        if [ -z "$needle" ] || printf '%s' "$out" | grep -qF -- "$needle"; then
            pass "$label"
        else
            fail "$label (refused, but the report does not name '$needle')"
        fi
        return
    fi
    fail "$label (rc=$rc, want $want; output: $(printf '%s' "$out" | tail -3 | tr '\n' ' '))"
}

# --- the real tree passes ----------------------------------------------------

require_verdict "$ROOT" accept "the real tree's manifest, Cargo.toml and Cargo.lock agree"

# --- aligned fixture also passes (proves the fixture is not why it works) ----

d="$TMP/aligned"; seed_aligned "$d"
require_verdict "$d" accept "an aligned fixture tree passes"

# --- leg 1: manifest shape ---------------------------------------------------

d="$TMP/no-section"; mkdir -p "$d/packaging"
printf '{"go": {"version": "1.26.10"}}\n' >"$d/packaging/build-inputs.json"
seed_cargo_toml "$d"; seed_lock "$d"
require_verdict "$d" refuse "a manifest with no .spilman_protocol section" "no .spilman_protocol section"

d="$TMP/tag-pin"; seed_manifest "$d" "v0.2.0" "$CASHU"
seed_cargo_toml "$d"; seed_lock "$d"
require_verdict "$d" refuse "a manifest pinning a TAG instead of a commit" "is not a 40-hex commit"

d="$TMP/short-pin"; seed_manifest "$d" "c2083de" "$CASHU"
seed_cargo_toml "$d"; seed_lock "$d"
require_verdict "$d" refuse "a manifest pinning an abbreviated commit" "is not a 40-hex commit"

# --- leg 2: Cargo requirement shapes -----------------------------------------

d="$TMP/rev-drift"; seed_manifest "$d"
seed_cargo_toml "$d" wrongrev; seed_lock "$d"
require_verdict "$d" refuse "a Cargo rev that disagrees with the manifest" "pins rev 1111111111111111111111111111111111111111"

d="$TMP/tag-requirement"; seed_manifest "$d"
seed_cargo_toml "$d" tag; seed_lock "$d"
require_verdict "$d" refuse "a Cargo TAG requirement for the pinned crate" "must pin \`rev\`, not \`tag\`"

d="$TMP/branch-requirement"; seed_manifest "$d"
seed_cargo_toml "$d" branch; seed_lock "$d"
require_verdict "$d" refuse "a Cargo BRANCH requirement" "pins a BRANCH"

d="$TMP/no-pin"; seed_manifest "$d"
seed_cargo_toml "$d" nopin; seed_lock "$d"
require_verdict "$d" refuse "a git dependency with neither rev nor tag" "pins neither rev nor tag"

d="$TMP/carrierless"; seed_manifest "$d"
mkdir -p "$d/sidecar/spilman/src"
printf '[package]\nname = "x"\nversion = "0.1.0"\n' >"$d/sidecar/spilman/Cargo.toml"
printf '#[cfg(not(feature = "protocol"))]\nconst X: &str = "settlement.";\n' \
    >"$d/sidecar/spilman/src/protocol.rs"
seed_lock "$d"
require_verdict "$d" refuse "a tree whose manifest pins a crate no requirement names" "no Cargo requirement references"

# --- leg 3: lock resolution --------------------------------------------------

d="$TMP/lock-drift"; seed_manifest "$d"; seed_cargo_toml "$d"
seed_lock "$d" "2222222222222222222222222222222222222222"
require_verdict "$d" refuse "a Cargo.lock resolving a different commit (half-update)" "resolves to 2222222222222222222222222222222222222222"

d="$TMP/lock-tag-query"; seed_manifest "$d"; seed_cargo_toml "$d"
mkdir -p "$d/sidecar/spilman"
cat >"$d/sidecar/spilman/Cargo.lock" <<LOCK
[[package]]
name = "cdk-spilman"
source = "git+$SPILMAN_REPO?tag=v0.2.0#$PIN"

[[package]]
name = "cashu"
source = "git+$CDK_REPO?tag=v0.18.1#$CASHU"
LOCK
require_verdict "$d" refuse "a lock resolving the pinned crate by tag, not rev" "is not resolved by \`rev\`"

d="$TMP/no-lock"; seed_manifest "$d"; seed_cargo_toml "$d"
require_verdict "$d" refuse "a tree with no Cargo.lock" "no Cargo.lock in the tree"

# --- leg 4: the shipped build's shape ---------------------------------------

d="$TMP/not-optional"; seed_manifest "$d"; seed_lock "$d"
seed_cargo_toml "$d"
sed_i() { python3 - "$@" <<'PY'
import sys, pathlib
path, old, new = sys.argv[1], sys.argv[2], sys.argv[3]
p = pathlib.Path(path)
p.write_text(p.read_text().replace(old, new))
PY
}
sed_i "$d/sidecar/spilman/Cargo.toml" ", optional = true }" " }"
require_verdict "$d" refuse "a protocol dependency that is not optional" "must be optional"

d="$TMP/second-default"; seed_manifest "$d"; seed_lock "$d"
seed_cargo_toml "$d"
sed_i "$d/sidecar/spilman/Cargo.toml" 'default = ["protocol"]' 'default = ["protocol", "telemetry"]'
require_verdict "$d" refuse "a second default feature changing the shipped surface" "expected exactly ['protocol']"

d="$TMP/feature-spelling"; seed_manifest "$d"; seed_lock "$d"
seed_cargo_toml "$d"
sed_i "$d/sidecar/spilman/Cargo.toml" 'protocol = ["dep:cdk-spilman", "dep:cashu"]' 'protocol = ["dep:cashu"]'
require_verdict "$d" refuse "a protocol feature that does not enable the pinned crate" "does not enable \`dep:cdk-spilman\`"

d="$TMP/no-transport-branch"; seed_manifest "$d"; seed_lock "$d"
seed_cargo_toml "$d"
cat >"$d/sidecar/spilman/src/protocol.rs" <<'RS'
pub const CAPABILITIES: &[&str] = &["transport.v1", "validate_registration.v1"];
RS
require_verdict "$d" refuse "a source with no transport-only branch (would over-advertise)" "no \`cfg(not(feature ="

d="$TMP/advertised-settlement"; seed_manifest "$d"; seed_lock "$d"
seed_cargo_toml "$d"
cat >>"$d/sidecar/spilman/src/protocol.rs" <<'RS'
pub const CAPABILITIES_EXTRA: &[&str] = &["settlement.v1"];
RS
require_verdict "$d" refuse "a source advertising a settlement capability" "settlement capability is advertised"

# --- the no-op trap: a mutation that changes nothing must not be counted -----

d="$TMP/noop-mutation"; seed_aligned "$d"
# "Mutate" the lock by writing back the SAME commit. This must still PASS: a
# harness that reports drift here is reporting its own edit, not real drift.
seed_lock "$d"
require_verdict "$d" accept "an identical rewrite of the lock is NOT drift (no-op mutations prove nothing)"

# --- verdict ------------------------------------------------------------------

printf '\n%s cases, %s failed\n' "$cases" "$fails"
[ "$fails" = 0 ] || exit 1
exit 0
