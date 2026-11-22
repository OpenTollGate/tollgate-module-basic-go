"""bech32m (BIP-350) with the 90-character limit REMOVED.

LUD-25 "Encoding": "Except for `mintPubkey` and the short forms, every value is
BIP-350 bech32m, without BIP-173's 90-character limit (as BOLT-11 invoices
already do)."

BIP-350 fixes the checksum constant at 0x2bc830a3 and keeps the 5-bit data
alphabet / checksum algorithm of BIP-173. The length cap in BIP-173 is a
*policy* check ("Encoders MUST NOT output more than 90 chars"), not part of the
checksum, so dropping it is exactly what BOLT-11 does and what LUD-25 says.
"""

CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
BECH32M_CONST = 0x2BC830A3
_SEP = "1"


class Bech32Error(ValueError):
    pass


def _polymod(values):
    gen = [0x3B6A57B2, 0x26508E6D, 0x1EA119FA, 0x3D4233DD, 0x2A1462B3]
    chk = 1
    for v in values:
        top = chk >> 25
        chk = ((chk & 0x1FFFFFF) << 5) ^ v
        for i in range(5):
            if (top >> i) & 1:
                chk ^= gen[i]
    return chk


def _hrp_expand(hrp: str):
    return [ord(c) >> 5 for c in hrp] + [0] + [ord(c) & 31 for c in hrp]


def _checksum(hrp: str, data):
    values = _hrp_expand(hrp) + list(data) + [0, 0, 0, 0, 0, 0]
    pm = _polymod(values) ^ BECH32M_CONST
    return [(pm >> (5 * (5 - i))) & 31 for i in range(6)]


def convertbits(data, frombits: int, tobits: int, pad: bool = True):
    """General power-of-2 base conversion (BIP-173 reference `convertbits`)."""
    acc = 0
    bits = 0
    ret = []
    maxv = (1 << tobits) - 1
    for value in data:
        if value < 0 or (value >> frombits):
            raise Bech32Error("invalid value in convertbits")
        acc = (acc << frombits) | value
        bits += frombits
        while bits >= tobits:
            bits -= tobits
            ret.append((acc >> bits) & maxv)
    if pad:
        if bits:
            ret.append((acc << (tobits - bits)) & maxv)
    elif bits >= frombits or ((acc << (tobits - bits)) & maxv):
        raise Bech32Error("convertbits: invalid padding")
    return ret


def encode(hrp: str, data5) -> str:
    """bech32m encode. NO length limit (LUD-25 Encoding)."""
    if any(ord(c) < 33 or ord(c) > 126 for c in hrp):
        raise Bech32Error("hrp has invalid characters")
    hrp = hrp.lower()
    combined = list(data5) + _checksum(hrp, data5)
    return hrp + _SEP + "".join(CHARSET[d] for d in combined)


def decode(s: str):
    """bech32m decode -> (hrp, [5-bit values]). No length limit."""
    if any(ord(c) < 33 or ord(c) > 126 for c in s):
        raise Bech32Error("invalid character")
    if s.lower() != s and s.upper() != s:
        raise Bech32Error("mixed case")
    s = s.lower()
    pos = s.rfind(_SEP)
    if pos < 1 or pos + 7 > len(s):
        raise Bech32Error("separator missing or too little data")
    hrp, rest = s[:pos], s[pos + 1:]
    try:
        data = [CHARSET.index(c) for c in rest]
    except ValueError:
        raise Bech32Error("invalid data character")
    if _polymod(_hrp_expand(hrp) + data) != BECH32M_CONST:
        raise Bech32Error("checksum is not bech32m")
    return hrp, data[:-6]


def encode_bytes(hrp: str, payload: bytes) -> str:
    return encode(hrp, convertbits(list(payload), 8, 5, True))


def decode_bytes(s: str) -> (str, bytes):
    hrp, data = decode(s)
    return hrp, bytes(convertbits(data, 5, 8, False))
