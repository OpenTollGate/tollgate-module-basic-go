#!/usr/bin/env python3
"""Count the rows of the CI package matrix in build-package.yml.

The FINDINGS file claims a number of CI matrix rows and how many of them are
mips/mipsel. The raw excerpt committed as `raw-2026-09-19/16-ci-cgo-and-arches.txt`
is TRUNCATED at the middle of the heredoc, so that claim cannot be re-derived from
it. This script derives it from the workflow file itself and prints the file's
sha256 + the revision it was read at, so the number has a traceable source.

Usage: python3 count-ci-matrix.py <path-to-build-package.yml> [label]
"""
import re
import sys
import hashlib
from collections import Counter

path = sys.argv[1] if len(sys.argv) > 1 else "tmbg/.github/workflows/build-package.yml"
label = sys.argv[2] if len(sys.argv) > 2 else path

with open(path, "rb") as fh:
    raw = fh.read()
s = raw.decode("utf-8", "replace")
sha = hashlib.sha256(raw).hexdigest()

m = re.search(r"cat > matrix\.json << 'EOF'(.*?)\n\s*EOF", s, re.S)
if not m:
    sys.exit(f"FATAL: no 'cat > matrix.json << EOF' heredoc found in {path}")
blob = m.group(1)

rows = re.findall(r'\{[^{}]*"architecture"[^{}]*\}', blob)


def field(row, key):
    mm = re.search(r'"%s":\s*"([^"]+)"' % key, row)
    return mm.group(1) if mm else "?"


archs = Counter(field(r, "architecture") for r in rows)
keys = Counter(field(r, "compile_key") for r in rows)
mips_rows = [r for r in rows if "mips" in field(r, "architecture")]
mipsel_rows = [r for r in rows if field(r, "architecture") == "mipsel_24kc"]
armv7_rows = [r for r in rows if field(r, "compile_key") == "armv7"]

print("=== CI package matrix, derived from the workflow file ===")
print("file            : %s" % label)
print("sha256          : %s" % sha)
print("matrix rows     : %d" % len(rows))
print("  per arch      : %s" % ", ".join("%s=%d" % kv for kv in sorted(archs.items())))
print("  per compile_key: %s" % ", ".join("%s=%d" % kv for kv in sorted(keys.items())))
print("  ipk:true rows : %d" % sum(1 for r in rows if '"ipk": true' in r))
print("  apk:true rows : %d" % sum(1 for r in rows if '"apk": true' in r))
print("mips rows       : %d  (mipsel_24kc=%d, mips_24kc=%d)"
      % (len(mips_rows), len(mipsel_rows), len(mips_rows) - len(mipsel_rows)))
print("armv7 rows      : %d" % len(armv7_rows))
print("archs with NO cdk-go artifact (mips/mipsel/armv7): %d of %d rows"
      % (len(mips_rows) + len(armv7_rows), len(rows)))
