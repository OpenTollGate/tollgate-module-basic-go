#!/usr/bin/env python3
"""Check that the cloud-lab's tracked install.json is the pristine template."""

import difflib
import json
import sys
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent.parent
TARGET = REPO / "tests" / "cloud-lab" / "configs" / "install.json"

# The canonical template. install_time/download_time are 0 because the lab's
# containers stamp them at runtime (EnsureDefaultInstall) through the compose
# bind mount — a run must leave the tracked file byte-identical (#550).
CANONICAL = """{
  "config_version": "v0.0.2",
  "package_path": "false",
  "ip_address_randomized": false,
  "install_time": 0,
  "download_time": 0,
  "release_channel": "stable",
  "ensure_default_timestamp": 1719000000,
  "installed_version": "0.0.0"
}
"""

def fingerprints(actual):
    """Distinguish runtime write-through from a deliberate template edit."""
    notes = []
    try:
        data = json.loads(actual)
    except json.JSONDecodeError:
        return ["the file no longer parses as JSON"]
    if data.get("install_time", 0) != 0:
        notes.append(f"install_time={data['install_time']} (runtime stamp; template is 0)")
    if data.get("download_time", 0) != 0:
        notes.append(f"download_time={data['download_time']} (runtime stamp; template is 0)")
    if not actual.endswith("\n"):
        notes.append("trailing newline missing (SaveInstallConfig writes without one)")
    return notes

def main():
    actual = TARGET.read_text()
    if actual == CANONICAL:
        print("✅ tests/cloud-lab/configs/install.json is the pristine template.")
        return 0

    print("=" * 60)
    print("  CLOUD-LAB install.json IS NOT PRISTINE")
    print("=" * 60)
    for note in fingerprints(actual):
        print(f"🔴 {note}")
    print("\nDiff vs the canonical template:")
    for line in difflib.unified_diff(
        CANONICAL.splitlines(keepends=True), actual.splitlines(keepends=True),
        fromfile="template", tofile=str(TARGET.relative_to(REPO)),
    ):
        print(f"  {line.rstrip()}")
    print(
        "\nA lab run stamps install_time through the compose bind mount — runtime"
        "\nstate, not template content. It rode careless git add/stash twice (#550)."
        "\nFix: git restore tests/cloud-lab/configs/install.json"
        "\n      (or update CANONICAL here if the template change is deliberate)."
    )
    return 1

if __name__ == "__main__":
    sys.exit(main())
