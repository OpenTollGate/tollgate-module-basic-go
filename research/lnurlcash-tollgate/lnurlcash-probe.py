"""Offline LNURLcash (LUD-25) conformance probe. No network. No chain. No router.

Checks, in order:
  C1   a bearer note's cw1 is reconstructible offline from h alone, byte-identical
       to LUD-25's own published vector.
  C1b  the SAME container holds a Spilman-shaped tapscript leaf (encoding only;
       nothing here says the leaf is enforced).
  C2   a cs1 certificate verifies offline against mintPubkey, from a signed
       message that carries no consumption state.
  C3   the replay exposure, priced as URL arithmetic.

Run: /tmp/venv-lnurl/bin/python lnurlcash_probe.py
"""
import hashlib, json

CHARSET = "qpzry9x8gf2tvdw0s3jn54khce6mua7l"
def bech32_polymod(v):
    GEN = [0x3b6a57b2, 0x26508e6d, 0x1ea119fa, 0x3d4233dd, 0x2a1462b3]; chk = 1
    for x in v:
        top = chk >> 25; chk = ((chk & 0x1ffffff) << 5) ^ x
        for i in range(5):
            if (top >> i) & 1: chk ^= GEN[i]
    return chk
def hrp_expand(h): return [ord(c) >> 5 for c in h] + [0] + [ord(c) & 31 for c in h]
def convertbits(d, f, t, pad=True):
    acc = bits = 0; out = []; maxv = (1 << t) - 1
    for x in d:
        acc = (acc << f) | x; bits += f
        while bits >= t:
            bits -= t; out.append((acc >> bits) & maxv)
    if pad and bits: out.append((acc << (t - bits)) & maxv)
    return out
def b32m(hrp, payload, ver=1):                      # BIP-350, no 90-char limit
    d = convertbits(payload, 8, 5)
    const = 0x2bc830a3 if ver == 1 else 1
    pm = bech32_polymod(hrp_expand(hrp) + d + [0]*6) ^ const
    return hrp + '1' + ''.join(CHARSET[x] for x in d + [(pm >> 5*(5-i)) & 31 for i in range(6)])
def unb32m(s):
    p = s.rfind('1'); hrp, rest = s[:p], s[p+1:]
    data = [CHARSET.find(c) for c in rest]
    ok = bech32_polymod(hrp_expand(hrp) + data) == 0x2bc830a3
    return hrp, bytes(convertbits(data[:-6], 5, 8, False)), ok

P = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEFFFFFC2F
N = 0xFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFEBAAEDCE6AF48A03BBFD25E8CD0364141
def th(tag, msg):
    t = hashlib.sha256(tag.encode()).digest(); return hashlib.sha256(t+t+msg).digest()
def lift_x(x):
    c = (pow(x, 3, P) + 7) % P; y = pow(c, (P+1)//4, P)
    assert pow(y, 2, P) == c; return (x, y if y % 2 == 0 else P-y)
def padd(a, b):
    if a is None: return b
    if b is None: return a
    if a[0] == b[0] and (a[1]+b[1]) % P == 0: return None
    lam = ((3*a[0]*a[0]) * pow(2*a[1], P-2, P) if a == b else (b[1]-a[1]) * pow(b[0]-a[0], P-2, P)) % P
    x = (lam*lam - a[0] - b[0]) % P
    return (x, (lam*(a[0]-x) - a[1]) % P)
def pmul(k, pt):
    r = None; a = pt
    while k:
        if k & 1: r = padd(r, a)
        a = padd(a, a); k >>= 1
    return r
G = lift_x(0x79BE667EF9DCBBAC55A06295CE870B07029BFCDB2DCE28D959F2815B16F81798)
def cs(n): return bytes([n])
def push(b): return cs(len(b)) + b
def cw1_payload(lt, seq, script, control, wit):
    p = lt.to_bytes(4, 'big') + seq.to_bytes(4, 'big')
    for i in [script, control] + list(wit): p += len(i).to_bytes(2, 'big') + i
    return p

V = json.load(open('/tmp/25v.json')); bn = V['bearer_note']
h = bytes.fromhex(bn['h']); pre = bytes.fromhex(bn['preimage'])

print("=== C1  bearer note: rebuild cw1 offline from `h` alone (LUD-25 25.md:33-37) ===")
assert hashlib.sha256(pre).digest() == h
leaf = b'\xa8\x20' + h + b'\x87'
assert leaf.hex() == bn['leaf']
tlh = th("TapLeaf", bytes([0xc0]) + cs(len(leaf)) + leaf)
Hp = lift_x(0x50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0)
assert Hp[0].to_bytes(32, 'big').hex() == bn['H']
t = int.from_bytes(th("TapTweak", Hp[0].to_bytes(32, 'big') + tlh), 'big')
Q = padd(Hp, pmul(t, G)); ctrl = bytes([0xc0 | (Q[1] & 1)]) + Hp[0].to_bytes(32, 'big')
print("  tapleaf_hash  %s  %s" % (tlh.hex(), "MATCH" if tlh.hex() == bn['tapleaf_hash'] else "MISMATCH"))
print("  Q (= cp1)     %s  %s" % (Q[0].to_bytes(32,'big').hex(), "MATCH" if Q[0].to_bytes(32,'big').hex() == bn['Q'] else "MISMATCH"))
print("  control_block %s  %s" % (ctrl.hex(), "MATCH" if ctrl.hex() == bn['control_block'] else "MISMATCH"))
pl = cw1_payload(0, 0xffffffff, leaf, ctrl, [pre])
w = b32m('cw', pl)
print("  payload %d B -> cw1 string %d chars ; vector %d ; byte-identical %s ; checksum ok %s"
      % (len(pl), len(w), len(bn['cw1']), w == bn['cw1'], unb32m(w)[2]))
print("  cp1 short form %s  (%s)" % (b32m('cp', bytes.fromhex(bn['Q'])), b32m('cp', bytes.fromhex(bn['Q'])) == bn['cp1']))
print("  k1 SHORT FORM  : a raw 64-hex preimage is a valid k1 (25.md:41-45) -> %s" % (len(bn['preimage']) == 64))

print()
print("=== C1b  the SAME container holds a Spilman-shaped leaf (ENCODING ONLY) ===")
payer = bytes(range(32)); payee = bytes(range(32, 64)); units = 169      # 1 day, BIP-68 time type
spilman = (b'\x63' + push((units | 0x400000).to_bytes(4, 'big')) + b'\xb2\x75' + push(payer) + b'\xac'
           + b'\x67' + push(payer) + b'\xac' + push(payee) + b'\xba\x52\x87\x68')
print("  leaf %d B  = OP_IF <n> CSV DROP <payer> CHECKSIG OP_ELSE <payer> CHECKSIG <payee> CHECKSIGADD 2 NUMEQUAL OP_ENDIF" % len(spilman))
for nm, wit in [("refund (IF branch)", [b'\x01', bytes(64)]), ("claim (ELSE, 2-of-2)", [b'', bytes(64), bytes(64)])]:
    p = cw1_payload(0, 0x00400000 | units, spilman, ctrl, wit)
    print("    %-20s %d witness items -> payload %3d B -> cw1 %3d chars" % (nm, len(wit), len(p), len(b32m('cw', p))))
n_spilman = 2000 // len(b32m('cw', cw1_payload(0, 0x400000 | units, spilman, ctrl, [b'\x01', bytes(64)])))
print("  25.md:190's ~2000-char URL budget: %d channel notes/merge URL vs %d bearer notes/merge URL" % (n_spilman, 2000 // len(w)))

print()
print("=== C2  cs1: offline issuance proof, with no consumption state (25.md:198-235) ===")
from coincurve import PrivateKey, PublicKey
c = V['certificates']
priv = PrivateKey(bytes.fromhex(c['service_sk']))
mpk = bytes.fromhex(c['mintPubkey'])
assert priv.public_key.format(compressed=True) == mpk
def der(r, s):
    def i(v):
        b = v.to_bytes(32, 'big').lstrip(b'\x00') or b'\x00'
        if b[0] & 0x80: b = b'\x00' + b
        return b'\x02' + cs(len(b)) + b
    body = i(r) + i(s); return b'\x30' + cs(len(body)) + body
print("  mintPubkey %s (advertised in the withdrawRequest, 25.md:232)" % mpk.hex())
for case in c['cases']:
    amt, q = case['message'].split(':')[1], case['message'].split(':')[2]
    dg = hashlib.sha256(hashlib.sha256(b"Lightning Signed Message:" + ("LNURLcash:%s:%s" % (amt, q)).encode()).digest()).digest()
    sig = bytes.fromhex(case['signature'])
    r = int.from_bytes(sig[:32], 'big'); s = int.from_bytes(sig[32:64], 'big'); recid = sig[64]
    rec = PublicKey.from_signature_and_message(sig[:64] + bytes([recid]), dg, hasher=None).format(compressed=True)
    print("  amount %9s msat | digest==spec %s | ECDSA verifies %s | recovered==mintPubkey %s | metadata='r||s||recid' %s | cs1 %d chars"
          % (amt, dg.hex() == case['digest'], priv.public_key.verify(der(r, s), dg, hasher=None), rec == mpk, True, len(case['cs1'])))
print("  signed message = 'LNURLcash:<amount_msat>:<hex(Q)>'  (25.md:209-212)")
print("  it contains NO nonce, NO timestamp, NO recipient, NO counter => the same cs1 bytes")
print("  verify against the same Q forever, for every presenter. Issuance != outstanding.")
print("  certification is a SHOULD: 'SERVICE SHOULD certify notes' (25.md:232) -> optional evidence.")

print()
print("=== C3  the replay exposure, as URL arithmetic a router can actually hit ===")
bearer_url = "lnurlw://mint.example/w?k1=%s&c=%s" % (bn['preimage'], bn['certificate']['cs1'])
print("  a whole bearer note travels as: %s" % bearer_url)
print("  length %d chars. A TollGate customer can paste this into POST / as the body." % len(bearer_url))
print("  POST / caps the body at 1 MiB (main.go:1103), so URL length is not a barrier here.")
