#!/usr/bin/env python3
"""Offline stub mint for the happy-path suite.

WHY THIS EXISTS
---------------
The pre-payment happy path that the suite asserts needs no mint at all. One
assertion does need one: proving the module actually moves the gate (calls
`ndsctl`) when a payment is recognised. Reaching that code path normally means
redeeming real ecash against a real mint, which the suite must never do (no
tokens, no spend, no network).

The module's Lightning path gives a money-free route in:

    POST /ln-invoice          -> module asks the mint for a NUT-04 quote
    GET  /ln-invoice?quote=X  -> module polls the quote; on PAID it mints the
                                 ecash, on ISSUED it grants access directly
                                 (merchant/lightning.go: ensureLightningAccessGranted)

So a mint that (a) answers NUT-01/NUT-04 correctly enough for the wallet to
initialise and (b) reports the quote as ISSUED drives the module all the way to
`valve.OpenGateUntil` -> `ndsctl auth` with no money involved. That is what the
suite asserts: the module called the gate with the right MAC.

What this does NOT prove: that a real payment grants access. No ecash is ever
minted or redeemed here; the issuance is asserted by the stub, not earned from
a mint. See README "What this does NOT cover".

PROTOCOL NOTES (learned the hard way)
-------------------------------------
* NUT-01 keyset IDs are DERIVED from the keys, not free-form: the wallet
  recomputes `00 || sha256(concat(pubkeys sorted by amount))[:14]`. A made-up
  ID is rejected with "mint returned no keysets for id ...".
* The keys must be REAL secp256k1 points; the wallet validates them and
  rejects arbitrary bytes with "x coordinate ... is not on the secp256k1 curve".
  We generate them by repeated doubling of the generator, which is fast and
  deterministic.
* `GET /v1/keys/{id}` must answer the same wrapped `{"keysets":[...]}` shape as
  `GET /v1/keys`; answering the bare keyset object also yields
  "mint returned no keysets for id ...".
"""
import hashlib
import json
import os
import re
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

# --- secp256k1 (minimal, affine) -------------------------------------------

FIELD_P = 2**256 - 2**32 - 977
CURVE_N = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141
GEN = (
    0x79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798,
    0x483ADA7726A3C4655DA4FBFC0E1108A8FD17B448A68554199C47D08FFB10D4B8,
)


def _add(p, q):
    if p is None:
        return q
    if q is None:
        return p
    if p[0] == q[0] and (p[1] + q[1]) % FIELD_P == 0:
        return None
    if p == q:
        lam = (3 * p[0] * p[0]) * pow(2 * p[1], FIELD_P - 2, FIELD_P) % FIELD_P
    else:
        lam = (q[1] - p[1]) * pow(q[0] - p[0], FIELD_P - 2, FIELD_P) % FIELD_P
    x = (lam * lam - p[0] - q[0]) % FIELD_P
    y = (lam * (p[0] - x) - p[1]) % FIELD_P
    return (x, y)


def _compressed(pt):
    return ("02" if pt[1] % 2 == 0 else "03") + format(pt[0], "064x")


def _keyset():
    """64 real secp256k1 points: 2^i * G for amount 2^i, ascending.

    The keyset id is DERIVED, per NUT-01 v0: "00" + sha256 of the concatenated
    *raw compressed key bytes* in ascending-amount order, first 14 hex chars.
    Hashing the hex text instead yields a different id and the wallet rejects
    the keyset with "Derived id: X but got Y from mint".
    """
    keys = {}
    pt = GEN
    for i in range(64):
        keys[str(1 << i)] = _compressed(pt)
        pt = _add(pt, pt)
    ordered = b"".join(bytes.fromhex(keys[str(1 << i)]) for i in range(64))
    kid = "00" + hashlib.sha256(ordered).hexdigest()[:14]
    return kid, keys


KEYSET_ID, KEYS = _keyset()
KEYSETS = {"keysets": [{"id": KEYSET_ID, "unit": "sat", "active": True, "input_fee_ppk": 0}]}
KEYSET_KEYS = {"keysets": [{"id": KEYSET_ID, "unit": "sat", "keys": KEYS}]}

PORT = int(os.environ.get("HP_STUB_MINT_PORT", "0"))
LOG = os.environ.get("HP_STUB_MINT_LOG", "")
# UNPAID is the honest default: a quote nobody paid. ISSUED is what drives the
# access-grant path without money -- the suite sets it explicitly and the README
# names what that does and does not prove.
QUOTE_STATE = os.environ.get("HP_STUB_MINT_QUOTE_STATE", "UNPAID")
QUOTE_ID = os.environ.get("HP_STUB_MINT_QUOTE_ID", "hp-stub-quote-0001")
BOLT11 = "lnbcstub1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"


def log(line):
    if not LOG:
        return
    with open(LOG, "a") as fh:
        fh.write(line + "\n")


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _send(self, code, obj):
        body = json.dumps(obj).encode()
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        log("GET  " + self.path)
        path, _, query = self.path.partition("?")
        if path == "/__hp/quote-state":
            self._send(200, {"quote_state": QUOTE_STATE, "keyset_id": KEYSET_ID})
        elif path == "/v1/keysets":
            self._send(200, KEYSETS)
        elif path == "/v1/keys":
            self._send(200, KEYSET_KEYS)
        elif re.match(r"^/v1/keys/[0-9a-fA-F]+$", path):
            self._send(200, KEYSET_KEYS)
        elif re.match(r"^/v1/mint/quote/bolt11/[^/]+$", path):
            self._send(200, {
                "quote": path.rsplit("/", 1)[1],
                "request": BOLT11,
                "state": QUOTE_STATE,
                "expiry": 9999999999,
            })
        elif path in ("/", "/v1/info"):
            self._send(200, {"name": "happy-path stub mint", "version": "stub", "nuts": ["1", "2", "3", "4"]})
        else:
            self._send(404, {"detail": "stub mint: no such route " + path})

    def do_POST(self):
        n = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(n)
        log("POST " + self.path + " body=" + body.decode(errors="replace")[:200])
        path, _, query = self.path.partition("?")
        if path == "/__hp/quote-state":
            # Flip the state WITHOUT restarting the process: the module's wallet
            # holds a live registration for this mint, and bouncing the stub on
            # the same port made the wallet answer "mint does not exist".
            global QUOTE_STATE
            new_state = ""
            for part in query.split("&"):
                if part.startswith("value="):
                    new_state = part.split("=", 1)[1].upper()
            if new_state not in ("UNPAID", "PAID", "ISSUED"):
                self._send(400, {"error": "value must be UNPAID, PAID or ISSUED"})
                return
            QUOTE_STATE = new_state
            log("quote_state -> " + QUOTE_STATE)
            self._send(200, {"quote_state": QUOTE_STATE})
        elif path == "/v1/mint/quote/bolt11":
            self._send(200, {"quote": QUOTE_ID, "request": BOLT11,
                             "state": "UNPAID", "expiry": 9999999999})
        elif path == "/v1/mint/bolt11":
            # Never reached in the money-free flow; answering honestly empty
            # rather than pretending a signature exists.
            self._send(200, {"signatures": []})
        else:
            self._send(404, {"detail": "stub mint: no such route " + path})

    def log_message(self, *args):
        pass


def main():
    port = PORT
    server = ThreadingHTTPServer(("127.0.0.1", port), Handler)
    actual = server.server_address[1]
    print("stub-mint port=%d keyset_id=%s quote_state=%s" % (actual, KEYSET_ID, QUOTE_STATE),
          flush=True)
    if LOG:
        with open(LOG, "a") as fh:
            fh.write("stub-mint port=%d keyset_id=%s quote_state=%s\n"
                     % (actual, KEYSET_ID, QUOTE_STATE))
    server.serve_forever()


if __name__ == "__main__":
    sys.exit(main())
