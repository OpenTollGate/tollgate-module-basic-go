#!/usr/bin/env python3
"""Unit self-test for lib/cashtoken.py -- the paid lane's spend gate.

WHY THIS EXISTS: on the paid lane's FIRST hardware run (2026-09-25) the lane was
dead before it could spend anything. `inspect()` read the NUT-00 version
character at `token[6]` -- the first PAYLOAD character -- and sliced the payload
at `token[7:]`. A real `cashuB` token therefore reported

    unknown Cashu token version character 'o' (expected A or B)

and `paid:token-inspected` FAILed, so `paid:purchase-accepted` was never
reached. Every token, v3 or v4, failed: the lane had been dead since it merged.
The default harness run SKIPs the opt-in paid lane, which is exactly why the
harness self-test never saw it.

Emits one line per case, which the harness self-test folds into its own counters:

    SELFTEST cashtoken-<case> OK|BAD <detail>

and exits non-zero if any case is BAD.

`--emit-v3 <sats>` prints a parseable, NON-REDEEMABLE v3 token for a case that
needs something token-shaped to POST at a stub (never used against a real mint).

stdlib only.
"""

import base64
import json
import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "lib"))
import cashtoken  # noqa: E402

MINT_V3 = "http://mint.stub.invalid"
MINT_V4 = "https://mint.stub.invalid"


def v3_token(sats, mint=MINT_V3):
    # `secret` here is a NUT-00 proof field, not a credential: the token is a
    # fixture the stub accepts, it is never sent to a mint, and nothing signs it.
    doc = {"token": [{"mint": mint, "proofs": [
        {"amount": sats, "id": "00" * 8, "secret": "stub-secret",  # pragma: allowlist secret
         "C": "02" + "00" * 32}]}]}
    return "cashuA" + base64.urlsafe_b64encode(json.dumps(doc).encode()).decode().rstrip("=")


def _cbor_text(value):
    raw = value.encode()
    if len(raw) < 24:
        return bytes([0x60 | len(raw)]) + raw
    return bytes([0x78, len(raw)]) + raw


def v4_token(mint=MINT_V4):
    """A hand-framed CBOR token, not a redeemable one.

    The inspector's v4 branch decodes base64url and recovers the mint URL with a
    regex over the bytes, so the framing only has to be honest CBOR: map(1)
    { "m": [ text(mint) ] }.
    """
    payload = b"\xa1" + _cbor_text("m") + b"\x81" + _cbor_text(mint)
    return "cashuB" + base64.urlsafe_b64encode(payload).decode().rstrip("=")


def _fail(name, detail):
    print("SELFTEST cashtoken-%s BAD %s" % (name, detail))
    return 1


def _ok(name, detail):
    print("SELFTEST cashtoken-%s OK %s" % (name, detail))
    return 0


def main(argv):
    if len(argv) >= 2 and argv[0] == "--emit-v3":
        print(v3_token(int(argv[1])))
        return 0

    bad = 0
    token = v3_token(210)
    info = cashtoken.inspect(token)
    if info["parseable"] and info["version"] == "A" and info["total_sats"] == 210 \
            and MINT_V3 in info["mint_urls"]:
        bad += _ok("v3", "cashuA token parses: version=A total_sats=210 mint=%s" % info["mint_urls"])
    else:
        bad += _fail("v3", "v3 token did not parse: version=%r total=%r mints=%r note=%r"
                     % (info["version"], info["total_sats"], info["mint_urls"], info["note"]))

    token = v4_token()
    info = cashtoken.inspect(token)
    if info["parseable"] and info["version"] == "B" and info["total_sats"] is None \
            and MINT_V4 in info["mint_urls"] and "declared" in info["note"]:
        bad += _ok("v4", "cashuB token parses: version=B mint recovered=%s total_sats=None "
                         "(the declared ceiling is the only cap)" % info["mint_urls"])
    else:
        bad += _fail("v4", "v4 token did not parse: version=%r total=%r mints=%r note=%r"
                     % (info["version"], info["total_sats"], info["mint_urls"], info["note"]))

    # Pin the OFFSET, not just "some message": a token whose version character
    # (index 5) is not A/B must be reported by that character. Reading index 6
    # instead -- the bug above -- would name 'Q' here.
    info = cashtoken.inspect("cashuZ" + "QVJ" + v3_token(1)[6:])
    if (not info["parseable"]) and "version character 'Z'" in info["note"]:
        bad += _ok("version-offset", "a token with version char 'Z' at index 5 is rejected by "
                                     "that character: %s" % info["note"])
    else:
        bad += _fail("version-offset", "expected the version at token[5] to be reported as 'Z': %r"
                     % info["note"])

    info = cashtoken.inspect("notatoken")
    if (not info["parseable"]) and "cashu" in info["note"]:
        bad += _ok("prefix", "a token with no NUT-00 prefix is refused: %s" % info["note"])
    else:
        bad += _fail("prefix", "expected the NUT-00 prefix check to refuse %r: %r"
                     % ("notatoken", info["note"]))

    return 1 if bad else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
