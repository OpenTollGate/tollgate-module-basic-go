#!/usr/bin/env python3
"""Minimal Cashu token inspector used ONLY by the opt-in paid lane.

The harness must not spend the operator's ecash by accident, so before it will
POST anything that carries value it insists on knowing what it is about to
spend. That is what this file is for.

NUT-00 has two serialisations:
  * `cashuA...`  -- v3, base64url(JSON)   -> fully parseable here.
  * `cashuB...`  -- v4, base64url(CBOR)   -> CBOR text strings appear verbatim in
    the byte stream, so the mint URL is recoverable with a regex over the decoded
    bytes; integer amounts are NOT reliably recoverable without a CBOR decoder,
    so the caller must declare the amount it is willing to spend
    (RHP_SPEND_MAX_SATS) and this module reports that it could not verify it.

Nothing here talks to a mint. Nothing here redeems anything.

stdlib only.
"""

import base64
import json
import re

URL_RE = re.compile(rb"https?://[^\s\"'\\\x00-\x1f]+")


def _b64url_decode(text):
    pad = "=" * ((4 - len(text) % 4) % 4)
    return base64.urlsafe_b64decode(text + pad)


def inspect(token):
    """-> dict(kind, version, mint_urls, total_sats|None, parseable, note)"""
    token = (token or "").strip()
    out = {"kind": "cashu-token", "version": "", "mint_urls": [],
           "total_sats": None, "parseable": False, "note": ""}
    if not token.lower().startswith("cashu"):
        out["note"] = "does not start with the NUT-00 'cashu' prefix"
        return out
    if len(token) < 7:
        out["note"] = "too short to carry a payload"
        return out

    version = token[6]
    out["version"] = version
    payload = token[7:]

    if version == "A":  # v3: base64url(JSON)
        try:
            data = json.loads(_b64url_decode(payload).decode("utf-8"))
        except Exception as exc:
            out["note"] = "v3 payload did not decode as base64url JSON (%r)" % (exc,)
            return out
        proofs = []
        for entry in data.get("token", []):
            mint = entry.get("mint") or ""
            if mint and mint not in out["mint_urls"]:
                out["mint_urls"].append(mint)
            proofs.extend(entry.get("proofs", []))
        total = 0
        for proof in proofs:
            try:
                total += int(proof.get("amount", 0))
            except (TypeError, ValueError):
                out["total_sats"] = None
                break
        else:
            out["total_sats"] = total
        out["parseable"] = True
        out["proof_count"] = len(proofs)
        # The units caveat the v4 branch states explicitly applies here too: a
        # proof's `amount` is the MINT's unit amount (2^n splits, and a mint may
        # denominate in msat), not a verified sat figure. total_sats is therefore
        # the token's self-declared total, good enough to compare against
        # RHP_SPEND_MAX_SATS as a sanity gate, NOT a settlement-grade amount.
        out["note"] = ("v3/JSON: amount summed from the token's own proofs -- the mint's "
                       "unit amounts, not independently verified sats")
        return out

    if version == "B":  # v4: base64url(CBOR)
        try:
            raw = _b64url_decode(payload)
        except Exception as exc:
            out["note"] = "v4 payload did not decode as base64url (%r)" % (exc,)
            return out
        for hit in URL_RE.findall(raw):
            mint = hit.decode("utf-8", "replace")
            if mint not in out["mint_urls"]:
                out["mint_urls"].append(mint)
        out["parseable"] = bool(out["mint_urls"])
        out["note"] = ("v4/CBOR: mint URL recovered, amount NOT verifiable by this "
                       "inspector -- the caller's declared RHP_SPEND_MAX_SATS is the "
                       "only spend cap")
        return out

    out["note"] = "unknown Cashu token version character %r (expected A or B)" % version
    return out
