#!/usr/bin/env python3
"""Contract: the two-path build doctrine is structurally enforced.

Asserts the shape of what builds this module, so creep and drift fail CI
instead of shipping:

  1. MANIFEST — both SDK eras pinned (25.12.x apk primary, 24.10.x ipk for
     the installed base), digest-pinned targets, manifest Go + tarball.
  2. SURFACE ALLOWLIST — any script that BUILDS or PACKAGES (name matches
     build/package/compile/sdk, or lives under packaging/) must be on the
     sanctioned two-path list, added deliberately with a path-comment.
     Verification/release tooling (repro-*, ngit-*, release-check, battery)
     is intentionally out of scope: it checks builds, it does not make them.
  3. ERA WIRING — the SDK path is format-selected end to end
     (build-sdk-package.sh flows TG_PACKAGE_FORMAT; build-env.sh resolves
     per-era release+digest).
  4. NO HARDCODED SDK VERSIONS outside the manifest (workflows must take
     image tags from the matrix the manifest builds).
  5. LOCATION AGNOSTICISM — build scripts contain no host-specific
     absolute paths (build anywhere; ai-legion is just the fastest, not a
     requirement).
"""

from __future__ import annotations

import json
import re
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent.parent
MANIFEST = REPO / "packaging" / "build-inputs.json"
EXPECTED_TARGETS = {
    "mediatek-filogic", "bcm27xx-bcm2711", "bcm27xx-bcm2709",
    "ramips-mt7621", "ath79-generic", "x86-64",
}
# The sanctioned build surface. Add entries ONLY with a path-comment.
BUILD_SCRIPTS = {
    "build-sdk-package.sh",     # Path B (SDK): both eras, format-selected
    "local-build-ipk.sh",       # Path A (FAST): canonical local entry
    "build-ipk.sh",             # Path A: ar/tar packager used by the above
    "shc-build-package.sh",     # Path A transport (short-lived VM wrapper)
    "build-env.sh",             # shared: manifest-pinned environment
    "portal-build.sh",          # asset build (portal bundle, not a package path)
    "normalize-apk-version.sh", # apk packaging helper
    "normalize-ipk-version.sh", # ipk packaging helper (#738: Debian pre-release ordering)
    "update-build-inputs.sh",   # manifest refresher (not a build path)
    "sdk-go-version.sh",        # manifest audit (not a build path)
}
failures: list[str] = []


def check_manifest() -> None:
    d = json.loads(MANIFEST.read_text())
    sdk = d.get("openwrt_sdk", {})
    releases = sdk.get("releases", {})
    if "apk" not in releases or "ipk" not in releases:
        failures.append("manifest: openwrt_sdk.releases must carry BOTH eras (apk + ipk)")
        return
    if not re.fullmatch(r"25\.\d+(\.\d+)?", str(releases["apk"].get("release", ""))):
        failures.append(f"manifest: apk era release should be a 25.x line, got {releases['apk'].get('release')}")
    if not re.fullmatch(r"24\.\d+(\.\d+)?", str(releases["ipk"].get("release", ""))):
        failures.append(f"manifest: ipk era release should be a 24.x line, got {releases['ipk'].get('release')}")
    for era in ("apk", "ipk"):
        targets = releases[era].get("targets", {})
        missing = EXPECTED_TARGETS - set(targets)
        if missing:
            failures.append(f"manifest: {era} era missing pinned targets: {sorted(missing)}")
        for t, entry in targets.items():
            dg = entry.get("digest", "")
            if not re.fullmatch(r"sha256:[0-9a-f]{64}", dg):
                failures.append(f"manifest: {era}/{t} digest not sha256-pinned: {dg[:30]}")
    go = d.get("go", {})
    if not go.get("version") or not go.get("tarball_linux_amd64", {}).get("sha256"):
        failures.append("manifest: .go.version / tarball sha256 must be pinned (single toolchain truth)")


def _is_build_shaped(path: Path) -> bool:
    n = path.name
    return bool(re.search(r"build|package|compile|sdk", n)) or path.parent.name == "packaging"


def check_surface() -> None:
    for d in (REPO / "scripts", REPO / "packaging"):
        for f in d.glob("*.sh"):
            if f.name in BUILD_SCRIPTS or not _is_build_shaped(f):
                continue
            failures.append(
                f"surface: {f.relative_to(REPO)} looks like a build/packaging script but is "
                "not on the sanctioned two-path list — add it to "
                "tests/contract/check-build-surface.py WITH a path-comment, or fold it into "
                "an existing path (this gate exists so a third build path cannot appear "
                "by accident)"
            )


def check_era_wiring() -> None:
    bsp = (REPO / "scripts" / "build-sdk-package.sh").read_text()
    if "TG_PACKAGE_FORMAT" not in bsp or "sdk_image_ref" not in bsp:
        failures.append("era-wiring: build-sdk-package.sh must flow TG_PACKAGE_FORMAT into the era-aware sdk_image_ref")
    be = (REPO / "packaging" / "build-env.sh").read_text()
    if "sdk_release_for" not in be or "releases[$f]" not in be:
        failures.append("era-wiring: build-env.sh must resolve per-era release via sdk_release_for")


def check_no_hardcoded_sdk_versions() -> None:
    pat = re.compile(r"openwrt/sdk:\$\{\{[^}]*\}\}-\d+\.\d+")
    for wf in (REPO / ".github" / "workflows").glob("*.yml"):
        for m in pat.finditer(wf.read_text()):
            failures.append(
                f"{wf.name}: hardcoded SDK version in image ref ({m.group(0)}) — "
                "take it from the matrix the manifest builds"
            )


def check_location_agnostic() -> None:
    pat = re.compile(r'(/home/[^/"\s]+|/root(?![/.a-z-]*openwrt)|/Users/)')
    for f in [REPO / "scripts" / "build-sdk-package.sh", REPO / "packaging" / "local-build-ipk.sh",
              REPO / "packaging" / "build-env.sh", REPO / "packaging" / "build-ipk.sh"]:
        for m in pat.finditer(f.read_text()):
            failures.append(f"{f.name}: host-specific absolute path ({m.group(0)}) — builds must be location-agnostic")


def main() -> int:
    check_manifest()
    check_surface()
    check_era_wiring()
    check_no_hardcoded_sdk_versions()
    check_location_agnostic()
    if failures:
        print("❌ BUILD-SURFACE DRIFT — the two-path doctrine is violated:")
        for f in failures:
            print(f"  🔴 {f}")
        return 1
    print("✅ Build surface matches the two-path doctrine (manifest, eras, surface, agnosticism).")
    return 0


if __name__ == "__main__":
    sys.exit(main())
