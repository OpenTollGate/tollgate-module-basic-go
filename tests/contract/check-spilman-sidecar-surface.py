#!/usr/bin/env python3
"""check-spilman-sidecar-surface.py — the Spilman protocol pin is ONE revision.

`packaging/build-inputs.json` is the build authority for external inputs
(AGENTS.md: "Manifest truth for externals"). The Spilman sidecar adds two
declarations that a naive build would let drift apart silently:

  * the Cargo requirement's `rev`, which decides which crate version compiles
    channel-id derivation into the shipped binary;
  * `Cargo.lock`'s resolved source, which is what a `--locked` build actually
    uses.

A difference between those and the manifest is not a style problem. The crate
derives channel identity from its own parameters, so a different revision can
derive a DIFFERENT channel id for the SAME registration — the daemon would
record a channel the sidecar never validated. That is why the pin is a commit
and never a branch or tag: a tag can be moved to point at new code.

Four legs, all offline and fast enough for a pre-commit hook:

  1. manifest well-formedness (.spilman_protocol with a 40-hex commit);
  2. every Cargo requirement for the pinned repos carries exactly that rev/tag;
  3. Cargo.lock's resolved source for each pinned crate matches the manifest;
  4. the sidecar's advertised surface is the SHAPE this contract assumes: the
     protocol dependency is optional and feature-gated, so a transport-only
     build (`--no-default-features`) has no protocol crate in its graph.

Leg 4 exists because legs 1-3 can all pass while the dependency has quietly
become unconditional — the manifest would still be correct, and the shipped
transport-only build would no longer be provable.

Carriers are DISCOVERED by glob (`**/Cargo.toml`, `**/Cargo.lock`), never by a
hand-maintained list: a list is how the gonuts pin drifted three ways (#791).

Usage:
  python3 tests/contract/check-spilman-sidecar-surface.py [--root DIR]

Exit codes: 0 = one pin, everywhere, correct shape; 1 = drift, itemized.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
from pathlib import Path

DEFAULT_ROOT = Path(__file__).resolve().parent.parent.parent
MANIFEST_REL = "packaging/build-inputs.json"
SPILMAN_KEY = "spilman_protocol"

# Both pinned git repos, and which manifest field holds their expected commit.
REPO_PINS = {
    "https://github.com/SatsAndSports/cashu_spilman_channels.git": "commit",
    "https://github.com/cashubtc/cdk.git": "cashu_commit",
}

HEX40 = re.compile(r"^[0-9a-f]{40}$")

# A dependency line in a Cargo.toml: the crate name, then a table body that may
# wrap onto following lines. Matching per-line and then scanning forward is more
# robust than one giant regex, because Cargo tables are commonly multi-line.
DEP_START = re.compile(r"^\s*(?P<name>[A-Za-z0-9_-]+)\s*=\s*\{(?P<rest>.*)$")
REV = re.compile(r'\brev\s*=\s*"(?P<value>[^"]+)"')
TAG = re.compile(r'\btag\s*=\s*"(?P<value>[^"]+)"')
BRANCH = re.compile(r'\bbranch\s*=\s*"(?P<value>[^"]+)"')
GIT = re.compile(r'\bgit\s*=\s*"(?P<value>[^"]+)"')

failures: list[str] = []
notes: list[str] = []


def fail(message: str) -> None:
    failures.append(message)


def load_manifest(root: Path) -> dict:
    path = root / MANIFEST_REL
    if not path.is_file():
        fail(f"{MANIFEST_REL}: missing")
        return {}
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError as error:
        fail(f"{MANIFEST_REL}: not valid JSON: {error}")
        return {}


def manifest_pins(manifest: dict) -> dict[str, str]:
    """The expected commit per repo, from the manifest."""
    section = manifest.get(SPILMAN_KEY)
    if not isinstance(section, dict):
        fail(f"no .{SPILMAN_KEY} section in {MANIFEST_REL}")
        return {}
    pin = section.get("commit")
    if not isinstance(pin, str) or not HEX40.match(pin):
        fail(
            f".{SPILMAN_KEY}.commit in {MANIFEST_REL} is not a 40-hex commit "
            f"(got {pin!r}); a branch or tag pin can move"
        )
        return {}
    cashu_pin = section.get("cashu_commit")
    if not isinstance(cashu_pin, str) or not HEX40.match(cashu_pin):
        fail(
            f".{SPILMAN_KEY}.cashu_commit in {MANIFEST_REL} is not a 40-hex commit "
            f"(got {cashu_pin!r})"
        )
        return {}
    return {
        "https://github.com/SatsAndSports/cashu_spilman_channels.git": pin,
        "https://github.com/cashubtc/cdk.git": cashu_pin,
    }


def discover_cargo_manifests(root: Path) -> list[Path]:
    """Every Cargo.toml in the tree, by glob. Never a hand list."""
    return sorted(
        path
        for path in root.rglob("Cargo.toml")
        if "target" not in path.parts and "node_modules" not in path.parts
    )


def dependency_tables(path: Path) -> list[tuple[str, int, str]]:
    """(crate name, line number, table body) for every git dependency."""
    try:
        lines = path.read_text(encoding="utf-8").splitlines()
    except (OSError, UnicodeDecodeError) as error:
        fail(f"{path}: unreadable: {error}")
        return []

    tables: list[tuple[str, int, str]] = []
    index = 0
    while index < len(lines):
        line = lines[index]
        match = DEP_START.match(line)
        if not match:
            index += 1
            continue
        body = match.group("rest")
        start = index
        # A Cargo table may continue on indented lines until the braces balance.
        depth = body.count("{") - body.count("}") + 1
        while depth > 0 and index + 1 < len(lines):
            index += 1
            body += "\n" + lines[index]
            depth += lines[index].count("{") - lines[index].count("}")
        tables.append((match.group("name"), start + 1, body))
        index += 1
    return tables


def check_cargo_requirements(root: Path, expected: dict[str, str]) -> None:
    """Leg 2: every Cargo requirement for a pinned repo names the manifest pin."""
    seen: dict[str, list[str]] = {repo: [] for repo in expected}

    for path in discover_cargo_manifests(root):
        for name, line, body in dependency_tables(path):
            git = GIT.search(body)
            if not git:
                continue
            repo = git.group("value")
            # Normalize the spelling `...git` vs `...git/` so a trailing slash is
            # not reported as a different repository.
            repo_key = repo if repo in expected else repo.rstrip("/")
            if repo_key not in expected:
                continue
            rel = path.relative_to(root)
            seen[repo_key].append(f"{rel}:{line} ({name})")

            branch = BRANCH.search(body)
            if branch:
                fail(
                    f"{rel}:{line}: {name} pins a BRANCH ({branch.group('value')}); "
                    f"use the commit from {MANIFEST_REL}"
                )
                continue
            rev = REV.search(body)
            tag = TAG.search(body)
            if not rev and not tag:
                fail(
                    f"{rel}:{line}: {name} names {repo_key} but pins neither rev nor tag"
                )
                continue
            # The Spilman crate must be a commit. cdk is a tag in the ecosystem,
            # and its resolved commit is what the lock records, so a tag is
            # accepted there only when the lock agrees with the manifest.
            if repo_key.endswith("cashu_spilman_channels.git"):
                if not rev:
                    fail(
                        f"{rel}:{line}: {name} must pin `rev`, not `tag` "
                        f"(a tag can move; {MANIFEST_REL} pins a commit)"
                    )
                elif rev.group("value") != expected[repo_key]:
                    fail(
                        f"{rel}:{line}: {name} pins rev {rev.group('value')}, "
                        f"{MANIFEST_REL} says {expected[repo_key]}"
                    )
            # cdk is consumed by tag throughout this ecosystem; its resolved
            # commit is what leg 3 checks against the lock, which is the value a
            # `--locked` build actually uses. Nothing to compare to here.
            elif not tag and not rev:
                fail(f"{rel}:{line}: {name} pins neither rev nor tag")

    for repo, locations in seen.items():
        if not locations:
            fail(f"no Cargo requirement references {repo} (manifest pins it)")
        else:
            notes.append(f"{repo} required at {', '.join(locations)}")


def check_cargo_lock(root: Path, expected: dict[str, str]) -> None:
    """Leg 3: Cargo.lock's resolved source carries the manifest commit."""
    locks = sorted(
        path
        for path in root.rglob("Cargo.lock")
        if "target" not in path.parts and "node_modules" not in path.parts
    )
    if not locks:
        fail("no Cargo.lock in the tree (the sidecar build must be lockable)")

    for path in locks:
        try:
            text = path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError) as error:
            fail(f"{path}: unreadable: {error}")
            continue
        rel = path.relative_to(root)
        # A resolved git source looks like:
        #   source = "git+https://github.com/owner/repo.git?rev=<sha>#<sha>"
        #   source = "git+https://github.com/owner/repo.git?tag=v1#<sha>"
        for match in re.finditer(
            r'source\s*=\s*"git\+(?P<repo>[^?"#]+)(?P<query>\?[^"#]*)?#(?P<sha>[0-9a-f]{40,64})"',
            text,
        ):
            repo = match.group("repo").rstrip("/")
            if repo not in expected:
                continue
            query = match.group("query") or ""
            sha = match.group("sha")
            if sha != expected[repo]:
                fail(
                    f"{rel}: {repo} resolves to {sha} but {MANIFEST_REL} pins "
                    f"{expected[repo]} (run `cargo update` after fixing the requirement, "
                    f"or scripts/bump-spilman-protocol.py)"
                )
            if repo.endswith("cashu_spilman_channels.git"):
                rev = re.search(r"[?&]rev=(?P<value>[0-9a-f]{40})", query)
                if not rev:
                    fail(f"{rel}: {repo} is not resolved by `rev` ({query or 'no query'})")
                elif rev.group("value") != expected[repo]:
                    fail(
                        f"{rel}: {repo} resolves a rev of {rev.group('value')}, "
                        f"{MANIFEST_REL} says {expected[repo]}"
                    )


def check_sidecar_surface(root: Path) -> None:
    """Leg 4: the protocol dependency is optional, feature-gated, and advertised
    only in a feature build.

    This is the leg that keeps the SHIPPED Go build free of protocol linkage.
    The manifest can be perfect while the crate has become a hard dependency, and
    then a `--no-default-features` build is no longer a proof of anything.
    """
    cargo = root / "sidecar" / "spilman" / "Cargo.toml"
    if not cargo.is_file():
        fail("sidecar/spilman/Cargo.toml: missing (the sidecar package must exist)")
        return
    text = cargo.read_text(encoding="utf-8")

    # Both pinned crates must be OPTIONAL.
    for crate in ("cdk-spilman", "cashu"):
        found = [body for name, _, body in dependency_tables(cargo) if name == crate]
        if not found:
            fail(f"sidecar/spilman/Cargo.toml: no `{crate}` dependency table")
            continue
        if "optional = true" not in found[0].replace("\n", " "):
            fail(
                f"sidecar/spilman/Cargo.toml: `{crate}` must be optional so a "
                f"transport-only build has no protocol crate in its graph"
            )

    # The feature that turns them on must exist and must be the default's only
    # member (adding another default feature is a decision, not a refactor).
    if not re.search(r"^protocol\s*=\s*\[", text, re.MULTILINE):
        fail("sidecar/spilman/Cargo.toml: no `protocol` feature")
    default = re.search(r"^default\s*=\s*\[(?P<body>[^\]]*)\]", text, re.MULTILINE)
    if not default:
        fail("sidecar/spilman/Cargo.toml: no `default` feature list")
    else:
        members = [item.strip().strip('"') for item in default.group("body").split(",")]
        members = [item for item in members if item]
        if members != ["protocol"]:
            fail(
                f"sidecar/spilman/Cargo.toml: `default` is {members}, expected exactly "
                f"['protocol'] (a second default feature changes the shipped surface)"
            )
    for crate in ("cdk-spilman", "cashu"):
        if not re.search(rf'protocol\s*=\s*\[[^\]]*"dep:{re.escape(crate)}"', text, re.DOTALL):
            fail(
                f"sidecar/spilman/Cargo.toml: the `protocol` feature does not enable "
                f"`dep:{crate}`, so the dependency is reachable without it"
            )

    # The capability advertisement itself must be feature-gated in source.
    protocol_rs = root / "sidecar" / "spilman" / "src" / "protocol.rs"
    if not protocol_rs.is_file():
        fail("sidecar/spilman/src/protocol.rs: missing")
    else:
        source = protocol_rs.read_text(encoding="utf-8")
        if '#[cfg(not(feature = "protocol"))]' not in source:
            fail(
                "sidecar/spilman/src/protocol.rs: no `cfg(not(feature = \"protocol\"))` "
                "branch, so a transport-only build would advertise validation"
            )
        if '"settlement.' in source or "settlement." in "\n".join(
            line for line in source.splitlines() if 'pub const CAPABILIT' in line
        ):
            fail("sidecar/spilman/src/protocol.rs: a settlement capability is advertised")


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=DEFAULT_ROOT)
    args = parser.parse_args()
    root = args.root.resolve()

    manifest = load_manifest(root)
    expected = manifest_pins(manifest) if manifest else {}
    if expected:
        check_cargo_requirements(root, expected)
        check_cargo_lock(root, expected)
    check_sidecar_surface(root)

    for note in notes:
        print(f"note {note}", file=sys.stderr)
    if failures:
        print(f"{len(failures)} Spilman pin/surface failure(s):", file=sys.stderr)
        for failure in failures:
            print(f"  FAIL {failure}", file=sys.stderr)
        return 1
    summary = ", ".join(
        "{}={}".format(repo.rsplit("/", 1)[-1], sha[:12])
        for repo, sha in expected.items()
    )
    print(f"ok: {SPILMAN_KEY} pin and sidecar surface agree ({summary})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
