#!/usr/bin/env python3
"""lib/rewrite-token-mint.py — point a Cashu V4 token at a different mint URL.

A V4 (`cashuB…`) token is **base64url of CBOR**, not JSON — NUT-00 specifies
CBOR for the V4 token body. The first version of this script assumed JSON and
died with `UnicodeDecodeError: 'utf-8' codec can't decode byte 0xa3` on a real
token; the minimal CBOR codec below is the fix. It exists so this experiment
does not need a third-party CBOR dependency.

Only the `m` (mint URL) text string is changed; every proof, keyset id and
signature byte string is round-tripped untouched, so the token remains
well-formed and the wallet still parses it — it just tries to talk to a mint
of our choosing, which is the whole point of the fault-injection harness.

  rewrite-token-mint.py <cashuB-token> <new-mint-url>   -> prints the new token
  rewrite-token-mint.py --dump <cashuB-token>           -> prints the decoded body
"""
import base64
import json
import sys

# --------------------------------------------------------------- CBOR codec
# Minimal RFC 8949 decoder/encoder covering the major types a Cashu token
# uses: 0/1 ints, 2 byte strings, 3 text strings, 4 arrays, 5 maps, 7 simple
# (true/false/null). Indefinite-length strings/arrays/maps are decoded (and
# re-encoded as definite-length, which is canonical).


def _head(b, i):
    ib = b[i]
    i += 1
    mt, ai = ib >> 5, ib & 0x1F
    if ai < 24:
        return mt, ai, i
    if ai == 24:
        return mt, b[i], i + 1
    if ai == 25:
        return mt, int.from_bytes(b[i:i + 2], "big"), i + 2
    if ai == 26:
        return mt, int.from_bytes(b[i:i + 4], "big"), i + 4
    if ai == 27:
        return mt, int.from_bytes(b[i:i + 8], "big"), i + 8
    if ai == 31:
        return mt, None, i  # indefinite
    raise ValueError("bad additional info %d" % ai)


def _dec(b, i):
    mt, val, i = _head(b, i)
    if mt == 0:
        return val, i
    if mt == 1:
        return -1 - val, i
    if mt == 2:
        if val is None:
            chunks = []
            while b[i] != 0xFF:
                c, i = _dec(b, i)
                chunks.append(c)
            return b"".join(chunks), i + 1
        return bytes(b[i:i + val]), i + val
    if mt == 3:
        if val is None:
            chunks = []
            while b[i] != 0xFF:
                c, i = _dec(b, i)
                chunks.append(c)
            return "".join(chunks), i + 1
        return b[i:i + val].decode("utf-8"), i + val
    if mt == 4:
        out = []
        if val is None:
            while b[i] != 0xFF:
                c, i = _dec(b, i)
                out.append(c)
            return out, i + 1
        for _ in range(val):
            c, i = _dec(b, i)
            out.append(c)
        return out, i
    if mt == 5:
        out = {}
        if val is None:
            while b[i] != 0xFF:
                k, i = _dec(b, i)
                v, i = _dec(b, i)
                out[k] = v
            return out, i + 1
        for _ in range(val):
            k, i = _dec(b, i)
            v, i = _dec(b, i)
            out[k] = v
        return out, i
    if mt == 7:
        if val == 20:
            return False, i
        if val == 21:
            return True, i
        if val == 22:
            return None, i
        raise ValueError("unsupported simple value %d" % val)
    raise ValueError("unsupported major type %d" % mt)


def _enc_head(mt, val):
    if val < 24:
        return bytes([(mt << 5) | val])
    if val < 256:
        return bytes([(mt << 5) | 24, val])
    if val < 65536:
        return bytes([(mt << 5) | 25]) + val.to_bytes(2, "big")
    if val < 2 ** 32:
        return bytes([(mt << 5) | 26]) + val.to_bytes(4, "big")
    return bytes([(mt << 5) | 27]) + val.to_bytes(8, "big")


def enc(o):
    if o is True:
        return b"\xf5"
    if o is False:
        return b"\xf4"
    if o is None:
        return b"\xf6"
    if isinstance(o, int):
        return _enc_head(0, o) if o >= 0 else _enc_head(1, -1 - o)
    if isinstance(o, bytes):
        return _enc_head(2, len(o)) + o
    if isinstance(o, str):
        e = o.encode("utf-8")
        return _enc_head(3, len(e)) + e
    if isinstance(o, list):
        return _enc_head(4, len(o)) + b"".join(enc(x) for x in o)
    if isinstance(o, dict):  # insertion order is preserved by Python
        return _enc_head(5, len(o)) + b"".join(enc(k) + enc(v) for k, v in o.items())
    raise TypeError("cannot encode %r" % type(o))


# ------------------------------------------------------------------ helpers
def decode_token(tok):
    if not tok.startswith("cashuB"):
        raise ValueError("not a V4 (cashuB) token")
    raw = tok[len("cashuB"):]
    raw += "=" * (-len(raw) % 4)
    body, consumed = _dec(base64.urlsafe_b64decode(raw), 0)
    return body, consumed


def encode_token(body):
    return "cashuB" + base64.urlsafe_b64encode(enc(body)).decode().rstrip("=")


def main(argv):
    if len(argv) == 3 and argv[1] == "--dump":
        body, _ = decode_token(argv[2])
        json.dump(_jsonable(body), sys.stdout, indent=2, default=str)
        sys.stdout.write("\n")
        return 0
    if len(argv) != 3:
        sys.stderr.write(__doc__)
        return 2
    tok, newmint = argv[1], argv[2]
    body, _ = decode_token(tok)
    before = body.get("m")
    body["m"] = newmint
    sys.stderr.write("mint: %s -> %s\n" % (before, newmint))
    sys.stdout.write(encode_token(body) + "\n")
    return 0


def _jsonable(o):
    """Render bytes as hex so --dump output is readable and JSON-safe."""
    if isinstance(o, bytes):
        return o.hex()
    if isinstance(o, dict):
        return {(_jsonable(k) if not isinstance(k, str) else k): _jsonable(v)
                for k, v in o.items()}
    if isinstance(o, list):
        return [_jsonable(x) for x in o]
    return o


if __name__ == "__main__":
    sys.exit(main(sys.argv))
