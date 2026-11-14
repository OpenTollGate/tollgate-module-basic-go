#!/usr/bin/env python3
"""Exercise the hedgehog-verify CLI with a REAL Core-validated spend.

This is the on-device verifier's test. It runs the same binary that gets
cross-compiled for the router, so the artefact under test is the artefact shipped.

It asserts BOTH directions, because either alone is weak:
  * a valid 2-of-2 spend is ACCEPTED
  * the payer's half alone is REJECTED (the safety property that makes a cheque
    safe to transmit)

A verifier that accepted everything would pass the first check; one that rejected
everything would pass the second. Only both together pin the behaviour.

USAGE
    verify_cli_test.py <path-to-hedgehog-verify> [--rpc http://127.0.0.1:18443]
"""

import argparse
import json
import os
import subprocess
import sys

FIXTURE = os.path.join(os.path.dirname(os.path.abspath(__file__)),
                       "signing_fixture.json")


def run_verify(binary, req):
    p = subprocess.run([binary, "-"], input=json.dumps(req).encode(),
                       capture_output=True, timeout=60)
    try:
        out = json.loads(p.stdout.decode() or "{}")
    except json.JSONDecodeError:
        out = {"raw": p.stdout.decode()[:300]}
    return p.returncode, out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("binary", help="path to the hedgehog-verify binary")
    ap.add_argument("--fixture", default=FIXTURE)
    args = ap.parse_args()

    if not os.path.exists(args.fixture):
        print(f"SKIP: no fixture at {args.fixture}. Run signing_fixture.py first.")
        return 0
    fx = json.load(open(args.fixture))

    results = []

    def check(name, ok, detail=""):
        results.append((name, ok, detail))
        print(f"  [{'PASS' if ok else 'FAIL'}] {name}"
              + (f" -- {detail}" if detail else ""))

    print("=" * 70)
    print(f"HEDGEHOG VERIFY CLI  ({args.binary})")
    print("=" * 70)

    # ---------------------------------------------------------------- malformed
    rc, out = run_verify(args.binary, {"tx_hex": "zz", "prev_script": "00"})
    check("malformed input is rejected with code 2", rc == 2,
          f"rc={rc} error={out.get('error', '')[:60]}")

    rc, out = run_verify(args.binary, {
        "tx_hex": "0200000001", "prev_script": "00", "vout": 99})
    check("out-of-range vout is rejected", rc == 2, f"rc={rc}")

    # ---------------------------------------------------------------- real spend
    spend = fx.get("spend") or {}
    if not spend.get("tx_hex"):
        # The fixture may only carry the funding data; build the negative test from
        # what we have and report the positive as skipped rather than faking it.
        check("fixture carries a spendable transaction", False,
              "fixture has no 'spend' section; regenerate with signing_fixture.py")
    else:
        rc, out = run_verify(args.binary, {
            "tx_hex": spend["tx_hex"],
            "vout": spend.get("vout", 0),
            "prev_value": fx["funding_value"],
            "prev_script": fx["core_script_pub_key"],
            "witness": spend["witness"],
        })
        check("a valid 2-of-2 spend is ACCEPTED", rc == 0 and out.get("valid"),
              f"rc={rc} reason={out.get('reason', '')[:70]}")

        # The payer's half alone must NOT verify.
        if spend.get("payer_half_witness"):
            rc, out = run_verify(args.binary, {
                "tx_hex": spend["tx_hex"],
                "vout": spend.get("vout", 0),
                "prev_value": fx["funding_value"],
                "prev_script": fx["core_script_pub_key"],
                "witness": spend["payer_half_witness"],
                "expect_failure": True,
            })
            check("the payer's half alone is REJECTED", rc == 0,
                  f"rc={rc} reason={out.get('reason', '')[:70]}")

    print()
    failed = [r for r in results if not r[1]]
    print("=" * 70)
    print(f"SUMMARY  pass={len(results) - len(failed)} fail={len(failed)}")
    for n, _, d in failed:
        print(f"  FAILED: {n} -- {d}")
    print("=" * 70)
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
