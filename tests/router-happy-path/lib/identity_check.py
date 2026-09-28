#!/usr/bin/env python3
"""Build-identity checks for a live TollGate router against a supplied .apk.

This is the non-fakeable half of the router happy-path harness: it fetches the
assets the router actually serves and compares each one, by leading sha256,
against the *same path* inside an extracted package. Nothing here is inferred
from a version string, a config file or a log line.

Why it matters (both findings are real, 2026-09-23):
  * a shipped bundle that did not contain the fix its pin claimed -- the pin was
    right, the bytes on the router were not the bytes in the package;
  * a portal served on a different port than assumed -- the surface the operator
    looked at was not the surface the package feeds.

Both are invisible to an "is something listening" smoke test and to any check
that reads a version number. They are visible to a byte comparison.

Emits one line per check:  RHPCHECK <id> <PASS|FAIL|SKIP> <detail>
and writes <out>/identity.json with the full per-asset table.

stdlib only (urllib + hashlib).
"""

import argparse
import hashlib
import json
import os
import re
import sys
import urllib.error
import urllib.request

UA = "router-happy-path-harness/1"

# surface -> (apk docroot, entry document, default port)
SURFACES = {
    "portal": ("etc/tollgate/tollgate-captive-portal-site", "splash.html", 2051),
    "admin": ("www/tollgate", "index.html", 8090),
}

HASHED_REF = re.compile(r"/assets/[^/\"']+-[A-Za-z0-9_-]{8}\.(?:js|css)$")

# Why the admin surface is skipped from a guest vantage. Kept in one place so the
# four ids it covers cannot drift apart, and phrased as an absence that is NAMED
# rather than one that is quietly dropped.
GUEST_ADMIN_REASON = (
    "guest vantage: the admin board is not reachable from br-lan "
    "(packaging/files/etc/nftables.d/31-admin-board-not-guest-reachable.nft), so its "
    "build identity is not asserted here -- the mgmt/on-box lane asserts it "
    "(--vantage mgmt from the private network, or --ssh on-box)")


def sha256(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


class Emitter:
    """Collects check lines; run.sh owns the tallying so there is one summary."""

    def __init__(self):
        self.rows = []

    def chk(self, cid, status, detail=""):
        print("RHPCHECK %s %s %s" % (cid, status, detail), flush=True)

    def note(self, text):
        print("RHPNOTE %s" % text, flush=True)


def fetch(url, timeout=10):
    """-> (status, body). Never raises for an HTTP error status."""
    req = urllib.request.Request(url, headers={"User-Agent": UA})
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return resp.status, resp.read()
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read()
    except Exception as exc:  # connection refused / timeout / DNS
        return None, ("%r" % (exc,)).encode()


def entry_asset(refs):
    """The SPA entry chunk: the first content-hashed /assets/*.js reference."""
    for ref in refs:
        if HASHED_REF.match(ref.split("?")[0]) and ref.endswith(".js"):
            return ref
    return ""


def check_surface(em, name, base_url, docroot, entry, artifact, strict, table, vantage="mgmt"):
    cid = "identity:%s" % name
    if name == "admin" and vantage == "guest":
        # The admin board is not reachable from br-lan at all (by design:
        # 31-admin-board-not-guest-reachable.nft), so there is nothing here to
        # compare. Say that on every id this surface would have emitted, name the
        # lane that DOES assert it, and never let it read as covered.
        for suffix in ("entry", "assets", "refs-in-package", "content-hashed-entry"):
            em.chk("%s:%s" % (cid, suffix), "SKIP", GUEST_ADMIN_REASON)
        em.note("%s: skipped as a NAMED absence, not a silent one -- the admin "
                "board's build identity is asserted by the mgmt/on-box lane "
                "(run.sh --vantage mgmt from the private network, or --ssh on-box)"
                % cid)
        return ""
    root = os.path.join(artifact, docroot)
    if not os.path.isdir(root):
        em.chk("%s:docroot" % cid, "FAIL",
               "artifact has no %s (wrong package for this surface?)" % docroot)
        return

    status, body = fetch("%s/%s" % (base_url, entry))
    apk_entry = os.path.join(root, entry)
    if status is None:
        em.chk("%s:entry" % cid, "FAIL",
               "cannot fetch %s/%s (%s)" % (base_url, entry, body.decode("utf-8", "replace")[:200]))
        return
    if not os.path.exists(apk_entry):
        em.chk("%s:entry" % cid, "FAIL",
               "artifact has no %s/%s to compare against" % (docroot, entry))
        return
    apk_bytes = open(apk_entry, "rb").read()
    live_sha, apk_sha = sha256(body), sha256(apk_bytes)
    if status != 200:
        em.chk("%s:entry" % cid, "FAIL", "GET %s/%s -> HTTP %s" % (base_url, entry, status))
    elif live_sha != apk_sha:
        em.chk("%s:entry" % cid, "FAIL",
               "live %s is %d B sha256 %s but the package ships %d B sha256 %s "
               "(the router is NOT running the package under test)"
               % (entry, len(body), live_sha[:16], len(apk_bytes), apk_sha[:16]))
    else:
        em.chk("%s:entry" % cid, "PASS",
               "%s %d B sha256 %s == package copy" % (entry, len(body), live_sha[:16]))
    table.append({"surface": name, "path": entry, "live_status": status,
                  "live_bytes": len(body), "live_sha256": live_sha,
                  "apk_bytes": len(apk_bytes), "apk_sha256": apk_sha,
                  "match": live_sha == apk_sha})

    # ---- every asset the package ships for this surface, by the same path ----
    n_pass = n_fail = 0
    mismatch = []
    for dirpath, _dirnames, filenames in sorted(os.walk(root)):
        for fname in sorted(filenames):
            full = os.path.join(dirpath, fname)
            rel = os.path.relpath(full, root)
            if rel == entry:
                continue
            want = open(full, "rb").read()
            st, got = fetch("%s/%s" % (base_url, rel))
            row = {"surface": name, "path": rel, "live_status": st,
                   "live_bytes": len(got), "live_sha256": sha256(got),
                   "apk_bytes": len(want), "apk_sha256": sha256(want),
                   "match": sha256(got) == sha256(want)}
            table.append(row)
            if st != 200:
                n_fail += 1
                mismatch.append(rel)
                em.chk("%s:asset:%s" % (cid, rel), "FAIL",
                       "shipped in the package (%d B) but the router answered HTTP %s"
                       % (len(want), st))
            elif row["match"]:
                n_pass += 1
            else:
                n_fail += 1
                mismatch.append(rel)
                em.chk("%s:asset:%s" % (cid, rel), "FAIL",
                       "live %d B sha256 %s != package %d B sha256 %s"
                       % (len(got), row["live_sha256"][:16], len(want), row["apk_sha256"][:16]))
    em.chk("%s:assets" % cid, "PASS" if n_fail == 0 else "FAIL",
           "%d/%d shipped assets byte-identical on %s%s"
           % (n_pass, n_pass + n_fail, base_url,
              "" if not mismatch else "; first mismatch: %s" % mismatch[0]))
    if n_pass:
        em.note("%s: %d asset hashes matched (see identity.json)" % (cid, n_pass))

    # ---- the reverse direction: what the live entry document references ----
    text = body.decode("utf-8", "replace")
    refs = sorted(set(re.findall(r'(?:src|href)\s*=\s*["\']([^"\']+)["\']', text)))
    missing = []
    for ref in refs:
        if ref.startswith("http") or ref.startswith("//") or ref.startswith("#"):
            continue
        rel = ref.split("?")[0].split("#")[0].lstrip("/")
        if not rel:
            continue
        if not os.path.exists(os.path.join(root, rel)):
            missing.append(ref)
    em.chk("%s:refs-in-package" % cid, "PASS" if not missing else "FAIL",
           "%d/%d refs in the live %s resolve inside %s%s"
           % (len(refs) - len(missing), len(refs), entry, docroot,
              "" if not missing else "; not in the package: %s" % ", ".join(missing[:4])))

    ha = entry_asset(refs)
    em.chk("%s:content-hashed-entry" % cid, "PASS" if ha else ("FAIL" if strict else "FAIL"),
           "entry chunk %s" % (ha if ha else
                               "NOT content-hashed in %s (refs: %s)" % (entry, ", ".join(refs[:4]))))
    return ha


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--router-ip", required=True)
    ap.add_argument("--artifact-dir", default="")
    ap.add_argument("--portal-port", type=int, default=SURFACES["portal"][2])
    ap.add_argument("--admin-port", type=int, default=SURFACES["admin"][2])
    ap.add_argument("--only-surface", default="")
    ap.add_argument("--vantage", choices=("guest", "mgmt"), default="mgmt",
                    help="which lane to assert. Default mgmt, deliberately: this script "
                         "cannot detect the lane itself (that derivation needs the :8090 "
                         "probe run.sh does), so run.sh always resolves the lane and passes "
                         "it in explicitly. A direct invocation without --vantage asserts "
                         "the admin surface, which is the stricter of the two. With guest, "
                         "the admin surface is not reachable from br-lan at all, so its "
                         "checks are reported as named SKIPs instead of failed fetches")
    ap.add_argument("--expect-entry", default="",
                    help="NAME:SIZE:SHA256 pin for the portal entry chunk")
    ap.add_argument("--print-entry", default="",
                    help="print the live entry chunk ref for this surface and exit "
                         "(used by run.sh so it does not have to parse HTML in bash)")
    ap.add_argument("--out", default="")
    args = ap.parse_args()

    if args.print_entry:
        docroot, entry, _default = SURFACES[args.print_entry]
        port = args.portal_port if args.print_entry == "portal" else args.admin_port
        status, body = fetch("http://%s:%d/%s" % (args.router_ip, port, entry))
        if status != 200:
            print("")
            return 1
        text = body.decode("utf-8", "replace")
        refs = sorted(set(re.findall(r'(?:src|href)\s*=\s*["\']([^"\']+)["\']', text)))
        print(entry_asset(refs))
        return 0

    if not args.artifact_dir:
        ap.error("--artifact-dir is required (unless --print-entry is used)")

    em = Emitter()
    table = []
    ports = {"portal": args.portal_port, "admin": args.admin_port}
    found = {}

    for name in ("portal", "admin"):
        if args.only_surface and args.only_surface != name:
            continue
        docroot, entry, _default = SURFACES[name]
        base = "http://%s:%d" % (args.router_ip, ports[name])
        ha = check_surface(em, name, base, docroot, entry, args.artifact_dir,
                           False, table, args.vantage)
        if ha:
            found[name] = ha

    # ---- optional pin: the artifact itself must match the pin it claims ----
    if args.expect_entry:
        try:
            want_name, want_size, want_sha = args.expect_entry.split(":")
            want_size = int(want_size)
        except ValueError:
            em.chk("identity:expected-entry", "FAIL",
                   "--expect-entry must be NAME:SIZE:SHA256 (got %r)" % args.expect_entry)
        else:
            got_name = found.get("portal", "")
            got_path = os.path.join(args.artifact_dir, SURFACES["portal"][0],
                                    got_name.lstrip("/")) if got_name else ""
            if not got_path or not os.path.exists(got_path):
                em.chk("identity:expected-entry", "FAIL",
                       "the portal entry chunk is %s but the pin names %s" % (got_name or "?", want_name))
            else:
                data = open(got_path, "rb").read()
                got_sha = sha256(data)
                if os.path.basename(got_path) == want_name and len(data) == want_size and got_sha == want_sha:
                    em.chk("identity:expected-entry", "PASS",
                           "package entry %s %d B sha256 %s matches the pin" % (want_name, want_size, want_sha[:16]))
                else:
                    em.chk("identity:expected-entry", "FAIL",
                           "package entry is %s %d B sha256 %s but the pin claims %s %d B sha256 %s"
                           % (os.path.basename(got_path), len(data), got_sha[:16],
                              want_name, want_size, want_sha[:16]))
    else:
        em.chk("identity:expected-entry", "SKIP",
               "no --expect-entry pin supplied (set RHP_EXPECT_ENTRY to make the "
               "artifact's own content hash part of the gate)")

    if args.out:
        try:
            os.makedirs(args.out, exist_ok=True)
            with open(os.path.join(args.out, "identity.json"), "w") as fh:
                json.dump({"artifact_dir": args.artifact_dir, "router_ip": args.router_ip,
                           "assets": table}, fh, indent=2)
        except OSError as exc:
            em.note("could not write identity.json: %r" % (exc,))
    return 0


if __name__ == "__main__":
    sys.exit(main())
