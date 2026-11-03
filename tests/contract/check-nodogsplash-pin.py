#!/usr/bin/env python3
"""check-nodogsplash-pin.py — the captive-portal daemon is a NAMED version.

The router image's most load-bearing runtime dependency was, until this
checker, an UNVERSIONED one:

    packaging/Makefile   DEPENDS:=+nodogsplash +jq +ca-bundle

Nothing in the tree named which nodogsplash we ship. The daemon is not a byte
of ours — we ship a DEPENDS on it and the OpenWrt feed resolves the rest — so
the version that lands on an operator's router was decided entirely by
whatever the feed happened to carry at build time. A silent feed bump (the
same upstream version re-released as -r2, exactly what happened on the 25.12
branch) changes what we ship with no diff in this repository.

Tests can certify a version. Only a pin NAMES one. This checker is the fence
around the pin in ``packaging/build-inputs.json`` (.nodogsplash), and it is
deliberately built like check-gonuts-pin.py: carriers are DISCOVERED by glob,
never by an enumerated list, because an enumerated list is how #791's fifth
``go.mod`` hid.

THREE LEGS, on purpose
----------------------

  * ``identity`` (default, offline, fast enough for hooks/pre-commit):
    every carrier in the tree that names a nodogsplash version must name the
    manifest's per-era value — INCLUDING the ``-rN`` packaging release when it
    states one. A carrier that names only ``5.0.2`` matches both eras; a
    carrier that names ``5.0.2-r2`` matches the apk era and is REFUSED by the
    ipk era, itemized, rather than silently accepted.

  * ``--feed`` (needs network; CI/release-check): the manifest must agree with
    the LIVE feed recipe of each era (``openwrt/routing`` branch
    ``openwrt-<series>``), for PKG_VERSION, PKG_RELEASE and PKG_HASH — and the
    released point-release feed must actually publish that package, so a
    branch-head-ahead-of-release gap cannot pass as agreement.

  * ``--artifact <file>`` (needs a built package): the artifact's OWN declared
    dependency must be a nodogsplash the manifest names. This is the leg that
    catches the build resolving something other than the pin, because it reads
    the metadata the package manager reads — not our recipe, not our manifest.

WHY PER-ERA AND NOT ONE STRING
------------------------------

The two OpenWrt eras DO resolve different packaging releases of the same
upstream version. Measured, not assumed:

    openwrt/routing@openwrt-25.12   PKG_VERSION:=5.0.2  PKG_RELEASE:=2
    openwrt/routing@openwrt-24.10   PKG_VERSION:=5.0.2  PKG_RELEASE:=1

and the released feeds publish accordingly (25.12.0 -> nodogsplash-5.0.2-r2.apk,
24.10.8 -> nodogsplash_5.0.2-r1_<arch>.ipk). A single-value pin would be a
FALSE EQUALITY: it would name a package one lane does not publish and pass
anyway. So the manifest pins per era, and this checker reports which era each
carrier matched. Support policy is the repo's (#796): the apk era is the only
feature target, the ipk era is a frozen compatibility lane.

Exit codes: 0 = the pin holds; 1 = drift, itemized.
"""

from __future__ import annotations

import argparse
import json
import re
import sys
import urllib.error
import urllib.request
from pathlib import Path

DEFAULT_ROOT = Path(__file__).resolve().parent.parent.parent
MANIFEST_REL = "packaging/build-inputs.json"

# A carrier naming a nodogsplash version. Two shapes exist in the wild and
# both have to be found: the prose/library form ("nodogsplash 5.0.2",
# "NDS 5.0.2", "nodogsplash `v5.0.2` tag") and the package-filename form
# ("nodogsplash-5.0.2-r2.apk", "nodogsplash_5.0.2-r1_x86_64.ipk"). The
# packaging release is captured when stated — that is the half a single
# version string usually drops.
NAME_RE = re.compile(r"nodogsplash", re.IGNORECASE)
VERSION_RE = re.compile(
    r"(?:"
    r"nodogsplash[`\s_=-]*v?(\d+\.\d+\.\d+)(?:-r(\d+))?"   # nodogsplash 5.0.2[-r2]
    r"|NDS[`\s]*v?(\d+\.\d+\.\d+)(?:-r(\d+))?"             # NDS 5.0.2[-r2]
    r")",
)
# Package filenames: nodogsplash-5.0.2-r2.apk / nodogsplash_5.0.2-r1_arch.ipk
PKG_NAME_RE = re.compile(r"nodogsplash[-_](\d+\.\d+\.\d+)(?:-r(\d+))?[-_.]")

# Glob-discovered carrier surfaces. Directories are checked for existence by
# rglob; nothing here is a hand-maintained list of FILES.
CARRIER_SUFFIXES = {
    ".md", ".sh", ".py", ".nft", ".json", ".yml", ".yaml", ".go", ".js", ".mjs",
}
# Trees that cannot carry a shipped claim about the daemon, and the fence's own
# files (which necessarily name versions the pin refuses — planting drift in a
# harness is how rule 4 is satisfied, and those literals are not claims).
SKIP_DIR_PARTS = {".git", "node_modules", "changelog.d", "vendor"}
SKIP_FILENAMES = {"check-nodogsplash-pin.py", "nodogsplash-pin_test.sh"}

ERA_LANES = {"apk": "25.x", "ipk": "<=24.10"}

failures: list[str] = []
notes: list[str] = []


# --- manifest ---------------------------------------------------------------


def load_manifest(root: Path) -> dict:
    path = root / MANIFEST_REL
    if not path.is_file():
        raise SystemExit(f"❌ {MANIFEST_REL} is missing — the manifest of record is the pin's only home")
    data = json.loads(path.read_text())
    nds = data.get("nodogsplash")
    if not isinstance(nds, dict):
        raise SystemExit(
            f"❌ {MANIFEST_REL} has no .nodogsplash block — nothing names which daemon we ship. "
            "Add it (see tests/contract/check-nodogsplash-pin.py) or this fence is decoration."
        )
    per_era = nds.get("per_era")
    if not isinstance(per_era, dict) or not per_era:
        raise SystemExit(
            f"❌ {MANIFEST_REL} .nodogsplash has no .per_era map — the two eras resolve "
            "different packaging releases, so a single value would be a false equality."
        )
    for era in ERA_LANES:
        entry = per_era.get(era)
        if not isinstance(entry, dict):
            raise SystemExit(
                f"❌ {MANIFEST_REL} .nodogsplash.per_era.{era} is missing — both eras are "
                "installed in the field; neither may be left unnamed."
            )
        if not entry.get("version") or not entry.get("release"):
            raise SystemExit(
                f"❌ {MANIFEST_REL} .nodogsplash.per_era.{era} must pin version AND packaging "
                "release (a bare version drops the -rN half that distinguishes the eras)"
            )
    return nds


def era_entries(nds: dict) -> dict[str, dict]:
    return {era: nds["per_era"][era] for era in ERA_LANES}


def era_label(era: str, entry: dict) -> str:
    return f"{entry['version']}-r{entry['release']} ({era} era, {ERA_LANES[era]})"


# --- carriers (glob discovery) ----------------------------------------------


def discover_carriers(root: Path) -> list[Path]:
    """Every text file in the tree that names a nodogsplash version — by glob."""
    found: list[Path] = []
    for path in sorted(root.rglob("*")):
        if not path.is_file():
            continue
        if path.suffix.lower() not in CARRIER_SUFFIXES:
            continue
        rel_parts = path.relative_to(root).parts
        if any(part in SKIP_DIR_PARTS for part in rel_parts):
            continue
        if path.name in SKIP_FILENAMES:
            continue
        try:
            text = path.read_text(encoding="utf-8", errors="replace")
        except OSError:
            continue
        if NAME_RE.search(text) and (VERSION_RE.search(text) or PKG_NAME_RE.search(text)):
            found.append(path)
    return found


# Which era a claim's surrounding text names, when it names one at all. A
# package FILENAME settles it (`nodogsplash_5.0.2-r2_x86_64.ipk` on the ipk
# lane is a claim about THAT lane, not about the apk one); bare prose settles
# nothing and is judged against both eras.
ERA_WORDS = {
    "ipk": re.compile(r"\.ipk\b|\bipk\b|opkg|24\.10", re.IGNORECASE),
    "apk": re.compile(r"\.apk\b|\bapk\b|apk-tools|25\.12", re.IGNORECASE),
}


def claim_era(line: str, suffix: str | None) -> set[str] | None:
    """The eras a claim speaks for, or None when it speaks for both.

    This is the difference between a pin and a false equality: without it, the
    apk era's `-r2` written on the ipk lane would be attributed to the apk era
    and pass, which is exactly the drift the per-era pin exists to refuse.
    """
    if suffix == ".ipk":
        return {"ipk"}
    if suffix == ".apk":
        return {"apk"}
    named = {era for era, pattern in ERA_WORDS.items() if pattern.search(line)}
    if len(named) == 1:
        return named
    return None


def claims_in(text: str) -> list[tuple[str, str | None, int, set[str] | None]]:
    """(version, packaging-release, line-number, eras-or-None) per claim."""
    out: list[tuple[str, str | None, int, set[str] | None]] = []
    for lineno, line in enumerate(text.splitlines(), start=1):
        seen: set[tuple[str, str | None]] = set()
        for pattern, suffix_for in ((VERSION_RE, None), (PKG_NAME_RE, ".ipk")):
            for match in pattern.finditer(line):
                groups = [g for g in match.groups() if g is not None]
                if not groups:
                    continue
                version = groups[0]
                release = groups[1] if len(groups) > 1 else None
                if (version, release) in seen:
                    continue
                seen.add((version, release))
                # A package filename's own extension is the strongest signal;
                # PKG_NAME_RE matches both, so read it off the match text.
                suffix = suffix_for
                if suffix is None:
                    lowered = match.group(0).lower()
                    if lowered.endswith(".ipk") or "_x86_64.ipk" in lowered:
                        suffix = ".ipk"
                    elif lowered.endswith(".apk"):
                        suffix = ".apk"
                out.append((version, release, lineno, claim_era(line, suffix)))
    return out


def check_identity(root: Path, nds: dict) -> dict[str, str]:
    """Every carrier's version claims are compatible with the per-era pins.

    Returns {era: verdict-line} describing WHICH era each carrier matched, so
    the two eras are reported distinctly instead of collapsed into one pass.
    """
    eras = era_entries(nds)
    matched: dict[str, list[str]] = {era: [] for era in ERA_LANES}

    carriers = discover_carriers(root)
    if not carriers:
        failures.append(
            "no file in the tree names a nodogsplash version — a tree that names no daemon "
            "version has no pin to hold; if that is ever intentional, delete .nodogsplash and "
            "this checker together"
        )
        return {}

    notes.append(f"carriers discovered by glob: {len(carriers)} file(s)")

    for path in carriers:
        rel = path.relative_to(root).as_posix()
        text = path.read_text(encoding="utf-8", errors="replace")
        for version, release, lineno, eras_of_claim in claims_in(text):
            judged = eras_of_claim if eras_of_claim is not None else set(eras)
            hits = [
                era
                for era in judged
                if eras[era]["version"] == version
                and (release is None or eras[era]["release"] == release)
            ]
            if hits:
                for era in hits:
                    matched[era].append(f"{rel}:{lineno}")
                continue
            named = f"{version}-r{release}" if release else version
            want = "; ".join(
                f"{era}={entry['version']}-r{entry['release']}"
                for era, entry in eras.items()
                if eras_of_claim is None or era in eras_of_claim
            )
            if eras_of_claim is None:
                scope = "which neither era pins"
            else:
                scope = f"which the {sorted(eras_of_claim)[0]} era does not pin"
            failures.append(
                f"{rel}:{lineno}: names nodogsplash {named}, {scope} "
                f"(manifest: {want}) — the daemon's version decided by this line "
                "is not the one build-inputs.json names"
            )

    verdicts = {}
    for era in ERA_LANES:
        entry = eras[era]
        count = len(matched[era])
        verdicts[era] = era_label(era, entry)
        if count:
            notes.append(f"{era} era: {count} claim(s) match {entry['version']}-r{entry['release']}")
        else:
            notes.append(
                f"{era} era: no carrier names {entry['version']}-r{entry['release']} "
                "(the pin still holds — it is the manifest's, not a doc's)"
            )
    return verdicts


# --- live-feed leg ----------------------------------------------------------


def fetch(url: str, timeout: int = 30) -> str:
    request = urllib.request.Request(url, headers={"User-Agent": "tollgate-nodogsplash-pin"})
    with urllib.request.urlopen(request, timeout=timeout) as response:
        return response.read().decode("utf-8", errors="replace")


def feed_branch(entry: dict) -> str:
    """The feed branch this era's SDK compiles against.

    Declared in the manifest rather than derived, because the era->series map
    is already the manifest's job (.openwrt_sdk.go_per_release keys); deriving
    it in a second place is the duplication rule 1 forbids.
    """
    branch = entry.get("feed_branch")
    if not branch:
        raise SystemExit(
            "❌ .nodogsplash.per_era.<era>.feed_branch is missing — the live-feed leg cannot "
            "know which branch defines this era's daemon"
        )
    return branch


def feed_recipe(entry: dict) -> str:
    """Fetch the live feed recipe that defines this era's daemon.

    The feed location is read from the manifest (one declaration per fact):
    a checker that also knows the repo/package names is a second copy of a
    fact the manifest already states, and the two drift the day the feed moves.
    """
    missing = [
        key for key in ("feed_repo", "feed_package", "feed_branch")
        if not entry.get(key)
    ]
    if missing:
        raise SystemExit(
            f"❌ .nodogsplash.per_era.<era> is missing {', '.join(missing)} — the live-feed leg "
            "cannot know where this era's daemon is defined"
        )
    return fetch(
        f"https://raw.githubusercontent.com/{entry['feed_repo']}/"
        f"{entry['feed_branch']}/{entry['feed_package']}/Makefile"
    )


def make_var(text: str, name: str) -> str | None:
    match = re.search(rf"^{re.escape(name)}\s*:?=\s*(\S+)\s*$", text, re.MULTILINE)
    return match.group(1) if match else None


def check_feed(nds: dict) -> None:
    for era, entry in era_entries(nds).items():
        where = f"{entry.get('feed_repo')}@{entry.get('feed_branch')}"
        era_failures = len(failures)
        try:
            text = feed_recipe(entry)
        except (urllib.error.URLError, OSError) as error:
            # Transport failure is reported as drift: a fence that passes
            # because it could not look is the decoration rule 4 forbids.
            failures.append(
                f"{era} era: could not read {where} ({error}) — cannot certify "
                f"{entry['version']}-r{entry['release']} against the live feed"
            )
            continue
        live_version = make_var(text, "PKG_VERSION")
        live_release = make_var(text, "PKG_RELEASE")
        live_hash = make_var(text, "PKG_HASH")
        if live_version != entry["version"]:
            failures.append(
                f"{era} era: feed {where} has PKG_VERSION={live_version}, "
                f"manifest pins {entry['version']} — the feed moved; the pin now names a "
                "package this era's SDK will not build"
            )
        if live_release != entry["release"]:
            failures.append(
                f"{era} era: feed {where} has PKG_RELEASE={live_release}, "
                f"manifest pins r{entry['release']} — a packaging release is a different "
                "artifact with the same version string; name the one the feed builds"
            )
        if entry.get("source_hash") and live_hash != entry["source_hash"]:
            failures.append(
                f"{era} era: feed {where} has PKG_HASH={live_hash}, "
                f"manifest pins {entry['source_hash']} — the tarball the SDK unpacks changed "
                "under a pinned version; investigate before touching the pin"
            )
        if len(failures) == era_failures:
            notes.append(f"{era} era: live feed agrees ({live_version}-r{live_release})")


def check_published(root: Path, nds: dict) -> None:
    """The pinned package must actually be PUBLISHED in the era's released feed.

    The feed BRANCH is what the next point release will carry; the RELEASED
    feed is what a router installed today resolves against, and they are not
    the same thing — the released 25.12.0 feed moved golang under a static tag
    once already. A pin that matches the branch head while the released feed
    publishes something else is the "ahead of release" gap: real, stated, and
    not silently equal.
    """
    manifest = json.loads((root / MANIFEST_REL).read_text())
    sdk = manifest.get("openwrt_sdk", {}).get("releases", {})
    for era, entry in era_entries(nds).items():
        release = (sdk.get(era) or {}).get("release")
        arch = entry.get("downloads_arch")
        if not release or not arch:
            failures.append(
                f"{era} era: cannot check the released feed — no "
                f".openwrt_sdk.releases.{era}.release or .nodogsplash.per_era.{era}.downloads_arch"
            )
            continue
        suffix = "apk" if era == "apk" else "ipk"
        # Filename shape differs per era by design: nodogsplash-5.0.2-r2.apk
        # (no arch segment) vs nodogsplash_5.0.2-r1_<arch>.ipk. Both are
        # matched by the version and release, never by a name we reconstructed,
        # so the optional tail segment must stay optional.
        url = f"https://downloads.openwrt.org/releases/{release}/packages/{arch}/routing/"
        try:
            listing = fetch(url)
        except (urllib.error.URLError, OSError) as error:
            failures.append(
                f"{era} era: could not read the released feed {url} ({error}) — cannot prove "
                f"{entry['version']}-r{entry['release']} is published for {release}"
            )
            continue
        pattern = re.compile(
            rf"nodogsplash[-_]{re.escape(entry['version'])}-r{re.escape(entry['release'])}"
            rf"(?:[-_.][^\s\"'<>]*?)?\.{suffix}\b"
        )
        if pattern.search(listing):
            notes.append(
                f"{era} era: released {release} feed publishes "
                f"{entry['version']}-r{entry['release']} ({arch})"
            )
            continue
        # Report what IS published, so the gap is legible rather than a bare fail.
        seen = sorted(set(re.findall(rf"nodogsplash[-_]\d[^\s\"'<>]*\.{suffix}", listing)))
        failures.append(
            f"{era} era: released {release} feed does NOT publish "
            f"{entry['version']}-r{entry['release']}; it carries "
            f"{', '.join(seen) if seen else '<no nodogsplash package at all>'} — the pin names a "
            "package this release line does not ship (branch head ahead of the released feed is "
            "the usual cause; state the gap instead of passing on a branch-only match)"
        )


# --- resolved-artifact leg --------------------------------------------------


def check_artifact(path: Path, nds: dict) -> None:
    """The built package's own dependency metadata must name a pinned daemon."""
    if not path.is_file():
        failures.append(f"--artifact {path} does not exist — the resolved-artifact leg cannot run")
        return
    suffix = path.suffix.lower()
    text = ""
    if suffix == ".apk":
        # apk v3 packages are ar/tar-ish; the dependency metadata is text and
        # greppable, which is all this leg needs. Never trust the file name.
        text = path.read_bytes().decode("utf-8", errors="replace")
    elif suffix == ".ipk":
        import subprocess
        import tempfile

        with tempfile.TemporaryDirectory() as tmp:
            proc = subprocess.run(
                ["tar", "xzf", str(path), "-O", "./control.tar.gz"],
                capture_output=True,
            )
            if proc.returncode != 0:
                failures.append(f"--artifact {path}: cannot read control.tar.gz from the .ipk")
                return
            inner = subprocess.run(
                ["tar", "xzO"], input=proc.stdout, capture_output=True
            )
            if inner.returncode != 0:
                failures.append(f"--artifact {path}: cannot read the .ipk control file")
                return
            text = inner.stdout.decode("utf-8", errors="replace")
    else:
        failures.append(f"--artifact {path}: unknown package suffix {suffix!r} (want .apk or .ipk)")
        return

    depends = [line for line in text.splitlines() if re.search(r"(?i)depends|depend:", line)]
    blob = "\n".join(depends) if depends else text
    names_daemon = re.search(r"nodogsplash", blob, re.IGNORECASE)
    if not names_daemon:
        failures.append(
            f"--artifact {path}: the package declares no nodogsplash dependency at all — the "
            "artifact is the thing opkg/apk reads, so this is a stop-ship, not a doc issue"
        )
        return

    # A version stated in the dependency must be one the manifest names. An
    # unversioned DEPENDS is what the recipes ship today and is accepted: the
    # pin names the version, the resolution is reality-checked by --feed.
    stated = set()
    for match in re.finditer(r"nodogsplash[^,;\n]*?(\d+\.\d+\.\d+)(?:-r(\d+))?", blob, re.IGNORECASE):
        stated.add((match.group(1), match.group(2)))
    if not stated:
        notes.append(
            f"artifact {path}: depends on nodogsplash without a version constraint (the "
            "recipes' shape); the pin, not the artifact, names the version"
        )
        return
    eras = era_entries(nds)
    for version, release in stated:
        if not any(
            entry["version"] == version and (release is None or entry["release"] == release)
            for entry in eras.values()
        ):
            want = "; ".join(f"{e}={v['version']}-r{v['release']}" for e, v in eras.items())
            failures.append(
                f"--artifact {path}: resolved nodogsplash {version}"
                f"{'-r' + release if release else ''}, which the manifest does not pin ({want}) "
                "— the build resolved a daemon the pin does not name"
            )
        else:
            notes.append(f"artifact {path}: resolved nodogsplash {version}"
                         f"{'-r' + release if release else ''} (pinned)")


# --- entry ------------------------------------------------------------------


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(
        description="The captive-portal daemon is a named, fenced version."
    )
    parser.add_argument("--root", default=None, help="repository root (default: this checkout)")
    parser.add_argument("--feed", action="store_true",
                        help="also certify the pin against the live OpenWrt feed recipes (network)")
    parser.add_argument("--artifact", default=None,
                        help="also check a built .apk/.ipk's own dependency metadata")
    args = parser.parse_args(argv)

    root = Path(args.root).resolve() if args.root else DEFAULT_ROOT
    nds = load_manifest(root)

    print(f"  manifest pin ({MANIFEST_REL} .nodogsplash.per_era):")
    eras = era_entries(nds)
    for era in ERA_LANES:
        print(f"    {era:4s} -> {era_label(era, eras[era])}")
    divergence = nds.get("era_divergence")
    if divergence:
        same = len({(e['version'], e['release']) for e in eras.values()}) == 1
        print(f"    eras {'resolved IDENTICALLY' if same else 'DIVERGE (stated in the manifest)'}")

    check_identity(root, nds)
    if args.feed:
        check_feed(nds)
        check_published(root, nds)
    if args.artifact:
        check_artifact(Path(args.artifact).resolve(), nds)

    for note in notes:
        print(f"  {note}")

    if failures:
        print(f"\n❌ NODOGSPLASH PIN DRIFT — {len(failures)} finding(s):")
        for problem in failures:
            print(f"  🔴 {problem}")
        print(
            "\nFix: the pin lives in packaging/build-inputs.json (.nodogsplash.per_era) and is "
            "bumped DELIBERATELY — never by a script. If the feed moved, that is a decision "
            "with a re-verification cost (every gate, session and pre-auth allow-list was "
            "bench-verified against this daemon), not a refresh."
        )
        return 1

    print("✅ nodogsplash is a named, fenced version: the manifest pin holds, per era.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
