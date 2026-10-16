"""A tapscript (BIP-342) interpreter -- enough to *execute* a leaf and get an
honest pass/fail, so the CHECKMULTISIG-disabled claim is a test result rather
than a reading of the BIP.

NOT consensus. There is no libbitcoinkernel / bitcoind on this host (checked:
`which bitcoind` is empty, no libbitcoinkernel*.so anywhere). This VM implements
the BIP-342 validation steps that are relevant to a single taproot input:
scan for OP_SUCCESSx, decode, run, require one true element. It is a
re-implementation of the BIP text, not the reference interpreter, so a PASS
here is evidence about the BIP text and the byte-level script, not about a
consensus library. See ../README.md LIMITS.

Cited: BIP-342 "Specification" items 1-5, "Script execution", ext_flag=1
signature validation; BIP-341 "Script validation rules"; BIP-65; BIP-112.
"""

from .bctx import (
    LOCKTIME_THRESHOLD,
    SEQUENCE_LOCKTIME_DISABLE_FLAG,
    SEQUENCE_LOCKTIME_MASK,
    SEQUENCE_LOCKTIME_TYPE_FLAG,
    taproot_sighash,
)
from .secp import schnorr_verify


class ScriptError(Exception):
    pass


# opcodes
OP_0 = 0x00
OP_1NEGATE = 0x4F
OP_1 = 0x51
OP_16 = 0x60
OP_IF = 0x63
OP_NOTIF = 0x64
OP_ELSE = 0x67
OP_ENDIF = 0x68
OP_RETURN = 0x6A
OP_DROP = 0x75
OP_EQUAL = 0x87
OP_EQUALVERIFY = 0x88
OP_1ADD = 0x8B
OP_ADD = 0x93
OP_NUMEQUAL = 0x9C
OP_NUMEQUALVERIFY = 0x9D
OP_SHA256 = 0xA8
OP_CHECKSIG = 0xAC
OP_CHECKSIGVERIFY = 0xAD
OP_CHECKMULTISIG = 0xAE
OP_CHECKMULTISIGVERIFY = 0xAF
OP_CHECKLOCKTIMEVERIFY = 0xB1
OP_CHECKSEQUENCEVERIFY = 0xB2
OP_CHECKSIGADD = 0xBA

DISABLED_IN_TAPSCRIPT = {OP_CHECKMULTISIG, OP_CHECKMULTISIGVERIFY}

OP_SUCCESSX = set(range(80, 81)) | {98} | set(range(126, 130)) | set(range(131, 135)) \
    | {137, 138} | {141, 142} | set(range(149, 154)) | set(range(187, 255))

NUM_PUSH_OPS = {OP_1 + i: i + 1 for i in range(16)}


def _num(b: bytes) -> int:
    """CScriptNum decode (little-endian, sign-magnitude)."""
    if len(b) == 0:
        return 0
    if len(b) > 4:
        raise ScriptError("script number overflow")
    v = int.from_bytes(b, "little")
    if b[-1] & 0x80:
        v &= ~(0x80 << (8 * (len(b) - 1)))
        return -v
    return v


def _ser_num(v: int) -> bytes:
    if v == 0:
        return b""
    neg = v < 0
    a = abs(v)
    out = bytearray()
    while a:
        out.append(a & 0xFF)
        a >>= 8
    if out[-1] & 0x80:
        out.append(0x80 if neg else 0x00)
    elif neg:
        out[-1] |= 0x80
    return bytes(out)


def cast_to_bool(b: bytes) -> bool:
    for i, c in enumerate(b):
        if c != 0:
            return not (i == len(b) - 1 and c == 0x80)
    return False


def decode_script(script: bytes):
    """Yield (opcode, data, is_push). Raises on truncated pushes (BIP-342 #2b)."""
    i = 0
    out = []
    n = len(script)
    while i < n:
        op = script[i]
        i += 1
        if 1 <= op <= 0x4B:
            if i + op > n:
                raise ScriptError("push extends past end of script")
            out.append((op, script[i:i + op], True))
            i += op
        elif op == 0x4C:
            if i + 1 > n:
                raise ScriptError("PUSHDATA1 truncated")
            ln = script[i]
            i += 1
            if i + ln > n:
                raise ScriptError("push extends past end")
            out.append((op, script[i:i + ln], True))
            i += ln
        elif op == 0x4D:
            if i + 2 > n:
                raise ScriptError("PUSHDATA2 truncated")
            ln = int.from_bytes(script[i:i + 2], "little")
            i += 2
            if i + ln > n:
                raise ScriptError("push extends past end")
            out.append((op, script[i:i + ln], True))
            i += ln
        elif op == 0x4E:
            if i + 4 > n:
                raise ScriptError("PUSHDATA4 truncated")
            ln = int.from_bytes(script[i:i + 4], "little")
            i += 4
            if i + ln > n:
                raise ScriptError("push extends past end")
            out.append((op, script[i:i + ln], True))
            i += ln
        else:
            out.append((op, None, False))
    return out


def scan_op_success(script: bytes):
    """BIP-342 #2a: an OP_SUCCESSx ANYWHERE (even in an unexecuted branch)
    makes the script unconditionally valid. LUD-25 requires SERVICE to REJECT
    such a leaf anyway, because consensus accepting it unconditionally is not
    the same as it being a sound note condition."""
    hits = []
    for op, _, is_push in decode_script(script):
        if not is_push and op in OP_SUCCESSX:
            hits.append(op)
    return hits


def execute_leaf(script, witness_items, tx, input_index, prevouts,
                 allow_disabled_opcodes=False):
    """Execute `script` against `witness_items` with the BIP-342 sighash bound.

    Returns (stack, reason). Raises ScriptError on failure.
    """
    import hashlib

    from .secp import tapleaf_hash

    tlh = tapleaf_hash(script)

    def check(sig, pubkey):
        if pubkey == b"" or len(pubkey) != 32:
            return True  # BIP-342 upgradable key types (LUD-25 forbids)
        if sig == b"":
            return False
        if len(sig) not in (64, 65):
            return False
        ht = sig[64] if len(sig) == 65 else 0x00
        if len(sig) == 65 and ht == 0x00:
            return False
        msg = taproot_sighash(
            tx, input_index, prevouts, hash_type=ht,
            ext_flag=1, tapleaf_hash=tlh, key_version=0, codesep_pos=0xFFFFFFFF,
        )
        return schnorr_verify(msg, pubkey, sig[:64])

    ops = decode_script(script)
    for op, _, is_push in ops:
        if not is_push and op in OP_SUCCESSX:
            return [b"\x01"], "OP_SUCCESSx (unconditionally valid per BIP-342 #2a)"

    stack = [bytes(x) for x in witness_items]
    conds = []
    executed = True
    i = 0
    sigops = 0

    def top(k=1):
        if len(stack) < k:
            raise ScriptError("stack underflow")
        return stack[-k]

    while i < len(ops):
        op, data, is_push = ops[i]
        i += 1
        if op in DISABLED_IN_TAPSCRIPT and executed and not allow_disabled_opcodes:
            raise ScriptError("OP_CHECKMULTISIG (0xae) is DISABLED in tapscript (BIP-342)")
        if op in (OP_IF, OP_NOTIF):
            if not executed:
                conds.append(False)
                continue
            v = top(); stack.pop()
            if v not in (b"", b"\x01"):
                raise ScriptError("MINIMALIF violated")
            want = cast_to_bool(v)
            if op == OP_NOTIF:
                want = not want
            conds.append(want); executed = all(conds); continue
        if op == OP_ELSE:
            if not conds:
                raise ScriptError("OP_ELSE without OP_IF")
            if len(conds) > 1 and not all(conds[:-1]):
                continue
            conds[-1] = not conds[-1]; executed = all(conds); continue
        if op == OP_ENDIF:
            if not conds:
                raise ScriptError("OP_ENDIF without OP_IF")
            conds.pop(); executed = all(conds) if conds else True; continue
        if not executed:
            continue
        if is_push:
            stack.append(data); continue
        if op == OP_0:
            stack.append(b""); continue
        if op == OP_1NEGATE:
            stack.append(_ser_num(-1)); continue
        if op in NUM_PUSH_OPS:
            stack.append(_ser_num(NUM_PUSH_OPS[op])); continue
        if op == OP_DROP:
            stack.pop()
        elif op == OP_EQUAL:
            a, b = top(2), top(1); stack.pop(); stack.pop()
            stack.append(b"\x01" if a == b else b"")
        elif op == OP_EQUALVERIFY:
            a, b = top(2), top(1); stack.pop(); stack.pop()
            if a != b:
                raise ScriptError("OP_EQUALVERIFY failed")
        elif op == OP_SHA256:
            v = top(); stack.pop(); stack.append(hashlib.sha256(v).digest())
        elif op == OP_ADD:
            b_, a_ = _num(top(1)), _num(top(2)); stack.pop(); stack.pop()
            stack.append(_ser_num(a_ + b_))
        elif op in (OP_NUMEQUAL, OP_NUMEQUALVERIFY):
            b_, a_ = _num(top(1)), _num(top(2)); stack.pop(); stack.pop()
            ok = (a_ == b_)
            if op == OP_NUMEQUALVERIFY:
                if not ok:
                    raise ScriptError("OP_NUMEQUALVERIFY failed")
            else:
                stack.append(b"\x01" if ok else b"")
        elif op in (OP_CHECKSIG, OP_CHECKSIGVERIFY):
            pub = top(1); sig = top(2); stack.pop(); stack.pop()
            if sig:
                sigops += 1
            if op == OP_CHECKSIGVERIFY:
                if not check(sig, pub):
                    raise ScriptError("OP_CHECKSIGVERIFY failed")
            else:
                stack.append(b"\x01" if check(sig, pub) else b"")
        elif op == OP_CHECKSIGADD:
            pub = top(1); n_ = _num(top(2)); sig = top(3)
            stack.pop(); stack.pop(); stack.pop()
            if sig:
                sigops += 1
            ok = check(sig, pub) if sig else False
            stack.append(_ser_num(n_ + (1 if ok else 0)))
        elif op == OP_CHECKMULTISIG:
            # Only reachable with allow_disabled_opcodes=True. BIP-342 disables
            # this opcode outright; the implementation exists purely so the
            # demo can show that the leaf would have been valid BEFORE BIP-342.
            n_ = _num(top(1)); stack.pop()
            if n_ < 0:
                raise ScriptError("negative pubkey count")
            keys = [stack.pop() for _ in range(n_)]      # top first == last key
            m_ = _num(top(1)); stack.pop()
            if m_ < 0 or m_ > n_:
                raise ScriptError("CHECKMULTISIG sig/pubkey count mismatch")
            sigs = [stack.pop() for _ in range(m_)]      # top first == last sig
            dummy = stack.pop()
            if dummy != b"":
                raise ScriptError("NULLDUMMY: the extra item must be empty")
            ks, ss, ok = 0, 0, True
            while ok and ss < len(sigs):
                if len(keys) - ks < len(sigs) - ss:
                    ok = False
                    break
                sigops += 1
                if check(sigs[ss], keys[ks]):
                    ss += 1
                ks += 1
                if ks > len(keys):
                    ok = False
                    break
            stack.append(b"\x01" if (ok and ss == len(sigs)) else b"")
        elif op in (OP_CHECKLOCKTIMEVERIFY, OP_CHECKSEQUENCEVERIFY):
            n_ = _num(top(1))
            if n_ < 0:
                raise ScriptError("negative locktime")
            if op == OP_CHECKLOCKTIMEVERIFY:
                if (tx.locktime < LOCKTIME_THRESHOLD) != (n_ < LOCKTIME_THRESHOLD):
                    raise ScriptError("locktime type mismatch")
                if n_ > tx.locktime:
                    raise ScriptError("locktime not satisfied")
                if tx.ins[input_index].sequence == 0xFFFFFFFF:
                    raise ScriptError("input is final")
            else:
                seq = tx.ins[input_index].sequence
                if seq & SEQUENCE_LOCKTIME_DISABLE_FLAG:
                    raise ScriptError("sequence lock disabled")
                mask = SEQUENCE_LOCKTIME_TYPE_FLAG | SEQUENCE_LOCKTIME_MASK
                if (n_ & SEQUENCE_LOCKTIME_TYPE_FLAG) != (seq & SEQUENCE_LOCKTIME_TYPE_FLAG):
                    raise ScriptError("sequence lock type mismatch (BIP-112)")
                if (n_ & mask) > (seq & mask):
                    raise ScriptError("sequence lock not satisfied")
        elif op == OP_RETURN:
            raise ScriptError("OP_RETURN")
        else:
            raise ScriptError("unimplemented opcode 0x%02x" % op)

    if conds:
        raise ScriptError("unbalanced conditionals")
    return stack, "ok"


def verify_leaf(script, witness_items, tx, input_index, prevouts,
                allow_disabled_opcodes=False):
    """BIP-342 step 4b: exactly one element, and it is true."""
    try:
        stack, reason = execute_leaf(script, witness_items, tx, input_index, prevouts,
                                     allow_disabled_opcodes=allow_disabled_opcodes)
    except ScriptError as e:
        return False, str(e)
    if len(stack) != 1:
        return False, "final stack has %d elements (must be exactly 1)" % len(stack)
    if not cast_to_bool(stack[0]):
        return False, "final stack element is false"
    return True, reason
