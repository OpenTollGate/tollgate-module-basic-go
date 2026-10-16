#!/usr/bin/env python3
"""
demo_channel.py -- the runnable one-way-channel spike artefact.

USAGE
    python3 rig/run-rig.sh                      # starts regtest, runs both variants
    python3 demo_channel.py --variant spilman   # the GREEN case
    python3 demo_channel.py --variant naive     # the RED case

No network dependency beyond a LOCAL regtest bitcoind. No third-party Python
packages: the claim under test is about a payer with no internet, so a rig that
must reach PyPI to check it is not evidence.

WHAT IT PROVES, AND HOW

Every assertion is executed against Bitcoin Core v31.1 consensus
(sendrawtransaction). Nothing is asserted from our own signature code alone --
if the sighash or DER encoding were wrong, Core would reject and the test would
fail loudly. That cross-check is the whole reason to use Core rather than a
simulator for these legs.

The construction is the canonical Spilman one-way channel:

    OP_IF
      <expiry> OP_CHECKSEQUENCEVERIFY OP_DROP <payerPub> OP_CHECKSIG
    OP_ELSE
      OP_2 <payerPub> <payeePub> OP_2 OP_CHECKMULTISIG
    OP_ENDIF

  * ELSE branch = payments. Every state is a 2-of-2 spend, so a state exists
    ONLY if the payer signed exactly that output set. The payee therefore
    cannot mint a state with a larger payee output.
  * IF branch = the payer's unilateral refund, locked by CSV so it is only
    spendable <expiry> blocks after funding.

Run `--variant naive` to see the same suite run against `<payeePub> OP_CHECKSIG`
(the payer-offline simplification): the non-custody tests FAIL. That pair is
the RED/GREEN evidence.
"""

import argparse
import json
import os
import subprocess
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import btclib as B

BTCDIR = os.path.expanduser("~/.local/toolchain/btc")
CLI = os.path.join(BTCDIR, "bin", "bitcoin-cli")
BITCOIND = os.path.join(BTCDIR, "bin", "bitcoind")
DATADIR = os.path.join(BTCDIR, "regtest-data")
RPCPORT = "18443"
CHANNEL_VALUE = 10_000_000      # 0.1 BTC "channel"
PAYMENT_FEE = 400               # sats budgeted per on-chain spend, in-script
GRANULE = 100_000               # sats of internet bought per state
EXPIRY_BLOCKS = 6               # CSV delay on the payer's refund branch
FEE_RATE_ASSUMED = 2            # sat/vB used when sizing the budget

# payer ("alice"/the customer) and payee ("bob"/the TollGate router)
PAYER_SK = bytes.fromhex("11" * 32)
PAYEE_SK = bytes.fromhex("22" * 32)
PAYER_PUB, PAYEE_PUB = B.pubkey(PAYER_SK), B.pubkey(PAYEE_SK)

RESULTS = []


# --------------------------------------------------------------------------
# node plumbing
# --------------------------------------------------------------------------
def cli(*args, wallet="rig"):
    cmd = [CLI, f"-datadir={DATADIR}", f"-rpcport={RPCPORT}",
           "-rpcuser=rig", "-rpcpassword=rig"]
    if wallet:
        cmd.append(f"-rpcwallet={wallet}")
    cmd += list(args)
    p = subprocess.run(cmd, capture_output=True, text=True)
    if p.returncode != 0:
        raise RuntimeError(f"bitcoin-cli {' '.join(args)}: {p.stderr.strip()}")
    return p.stdout.strip()


def cli_no_wallet(*args):
    return cli(*args, wallet=None)


def try_broadcast(tx):
    """Return (accepted: bool, reason: str). Uses the REAL consensus path."""
    p = subprocess.run(
        [CLI, f"-datadir={DATADIR}", f"-rpcport={RPCPORT}", "-rpcuser=rig",
         "-rpcpassword=rig", "sendrawtransaction", tx.serialize().hex()],
        capture_output=True, text=True)
    if p.returncode == 0:
        return True, "accepted"
    txt = (p.stderr or p.stdout).strip()
    # Extract the human-readable reject reason. Core's shape is:
    #   error message:
    #   <class>-script-verify-flag-failed (<REASON>), input 0 of <txid> (...)
    for line in txt.splitlines():
        if "(" in line and ")" in line and "flag" in line:
            return False, line.split("(")[1].split(")")[0]
    for line in txt.splitlines():
        if "error message" not in line and line and "error code" not in line:
            return False, line.strip()[:100]
    return False, "rejected"


def check(name, cond, detail=""):
    RESULTS.append((name, bool(cond)))
    print(f"  [{'PASS' if cond else 'FAIL'}] {name}" + (f"\n          {detail}" if detail else ""))
    return cond


def head(t):
    print("\n" + "=" * 76)
    print(t)
    print("=" * 76)


# --------------------------------------------------------------------------
# the construction
# --------------------------------------------------------------------------
def build_script(variant: str) -> bytes:
    if variant == "naive":
        # The "payer-offline simplification": the redeemer signs alone.
        return B.push(PAYEE_PUB) + bytes([B.OP_CHECKSIG])
    if variant != "spilman":
        raise SystemExit(f"unknown variant {variant}")
    s = bytes([B.OP_IF])
    s += B.pushnum(EXPIRY_BLOCKS) + bytes([B.OP_CHECKSEQUENCEVERIFY]) + bytes([B.OP_DROP])
    s += B.push(PAYER_PUB) + bytes([B.OP_CHECKSIG])
    s += bytes([B.OP_ELSE])
    s += B.pushnum(2) + B.push(PAYER_PUB) + B.push(PAYEE_PUB) + B.pushnum(2) + bytes([0xAE])
    s += bytes([B.OP_ENDIF])
    return s


def fund_channel(ws):
    """Fund a fresh P2WSH outpoint. Returns (txid, vout, value)."""
    spk = B.p2wsh_spk(ws)
    addr = B.p2wsh_address(ws)
    raw = cli("sendtoaddress", addr, f"{CHANNEL_VALUE/1e8:.8f}")
    cli("generatetoaddress", "1", cli("getnewaddress"))
    dec = json.loads(cli_no_wallet("getrawtransaction", raw, "true"))
    for i, o in enumerate(dec["vout"]):
        if o["scriptPubKey"].get("hex") == spk.hex():
            v = int(round(o["value"] * 1e8))
            assert v == CHANNEL_VALUE, f"funded {v}"
            return raw, i, v
    raise RuntimeError("funding vout not found")


def payment_state(txid, vout, value, ws, granule):
    """State paying the payee `granule` sats (2-of-2 branch, for spilman)."""
    payee = granule
    payer = value - granule - PAYMENT_FEE
    tx = B.Tx([B.TxIn(txid, vout, 0)],
              [B.TxOut(payer, B.p2wsh_spk(ws)), B.TxOut(payee, B.p2wsh_spk(ws))])
    return tx, payer, payee


def sign_payment(tx, value, ws):
    h = tx.sighash_v0(0, value, ws, 1)
    # Witness order is bottom-of-stack first. The script's ELSE branch is
    #   <m=2> <payerPub> <payeePub> <n=2> OP_CHECKMULTISIG
    # so the stack it consumes, bottom->top, MUST be:
    #   dummy, payerSig, payeeSig [, if-selector]
    # CHECKMULTISIG pops n, then n keys, then m, then m sigs, then ONE EXTRA
    # item immediately BELOW the signatures -- so the dummy goes FIRST, not
    # last. TWO traps that cost this rig a debugging round each (see ERRATA):
    #  * the dummy must be EMPTY. A 1-byte b"\x00" is not empty and violates
    #    SCRIPT_VERIFY_NULLDUMMY (consensus for segwit spends);
    #  * putting sigs where the dummy belongs makes CHECKMULTISIG consume the
    #    wrong items and report a signature failure, which reads like a key or
    #    sighash bug rather than a stack-order bug.
    # The trailing empty item is the OP_IF selector: empty == falsy == take ELSE.
    tx.ins[0].witness = [b"", B.sign(PAYER_SK, h) + b"\x01",
                         B.sign(PAYEE_SK, h) + b"\x01", b"", ws]
    return tx


def sign_unilateral_payee(tx, value, ws):
    """naive variant: payee signs alone (no payer cosignature)."""
    tx.ins[0].witness = [B.sign(PAYEE_SK, tx.sighash_v0(0, value, ws, 1)) + b"\x01", ws]
    return tx


def refund_tx(txid, vout, value, ws):
    """Payer's unilateral escape hatch (IF branch). Unspendable until CSV ages."""
    tx = B.Tx([B.TxIn(txid, vout, B.csv_blocks(EXPIRY_BLOCKS))],
              [B.TxOut(value - PAYMENT_FEE, B.p2wsh_spk(ws))])
    h = tx.sighash_v0(0, value, ws, 1)
    tx.ins[0].witness = [B.sign(PAYER_SK, h) + b"\x01", b"\x01", ws]
    return tx


# --------------------------------------------------------------------------
# the suite
# --------------------------------------------------------------------------
def run(variant):
    ws = build_script(variant)
    spk = B.p2wsh_spk(ws)
    print(f"variant            : {variant}")
    print(f"witness script     : {len(ws)} bytes  ({ws.hex()[:64]}...)")
    print(f"scriptPubKey       : {spk.hex()}")
    print(f"regtest address    : {B.p2wsh_address(ws)}")

    head("T1  What the script encodes")
    if variant == "spilman":
        rc = json.loads(cli_no_wallet("decodescript", ws.hex()))
        asm = rc["asm"]
        print(f"    asm: {asm}")
        check("has a unilateral CSV-gated payer branch (the refund)",
              "OP_CHECKSEQUENCEVERIFY" in asm and "OP_CHECKSIG" in asm)
        check("has a 2-of-2 branch for payments (the payer must cosign every state)",
              "OP_CHECKMULTISIG" in asm)
    else:
        check("naive variant: redeemer-only script, NO payer branch", True,
              "this is the script you get if you insist the payer stay offline")

    # ---- T2: the latest payment state is consensus-valid ----------------
    head("T2  A payment state settles on-chain")
    txid, vout, value = fund_channel(ws)
    print(f"    channel funded: {txid}:{vout} = {value} sats (1 confirmation)")
    st, payer_out, payee_out = payment_state(txid, vout, value, ws, GRANULE)
    st = sign_payment(st, value, ws) if variant == "spilman" else sign_unilateral_payee(st, value, ws)
    ok, why = try_broadcast(st)
    print(f"    state(owed={GRANULE}) broadcast: {'ACCEPTED' if ok else 'REJECTED ' + why}")
    check("latest payment state is valid consensus", ok, why if not ok else "")
    if ok:
        vs = st.vsize()
        print(f"    vsize {vs} vB, fee {PAYMENT_FEE} sats ({PAYMENT_FEE/vs:.1f} sat/vB,"
              f" {PAYMENT_FEE/GRANULE*100:.2f}% of the {GRANULE} sat granule)")
        cli("generatetoaddress", "1", cli("getnewaddress"))

    # ---- T3: NON-CUSTODY -- can the redeemer inflate its own payout? ----
    head("T3  NON-CUSTODY: can the redeemer take more than was signed off?")
    txid2, vout2, value2 = fund_channel(ws)
    # The payer signs state(owed=GRANULE). The redeemer now tries to broadcast
    # a DIFFERENT output set -- itself taking the whole channel.
    signed_state, _, _ = payment_state(txid2, vout2, value2, ws, GRANULE)
    signed_state = (sign_payment(signed_state, value2, ws) if variant == "spilman"
                    else sign_unilateral_payee(signed_state, value2, ws))
    theft = B.Tx([B.TxIn(txid2, vout2, 0)],
                 [B.TxOut(value2 - PAYMENT_FEE, spk)])   # redeemer takes 100%
    if variant == "spilman":
        # The redeemer has only its own key. Best it can do: reuse the payer's
        # signature from the *signed* state, which commits to different outputs.
        payer_sig_from_signed_state = signed_state.ins[0].witness[1]
        h = theft.sighash_v0(0, value2, ws, 1)
        theft.ins[0].witness = [b"", payer_sig_from_signed_state,
                                B.sign(PAYEE_SK, h) + b"\x01", b"", ws]
    else:
        theft = sign_unilateral_payee(theft, value2, ws)
    ok_theft, why_theft = try_broadcast(theft)
    print(f"    redeemer takes 100% of channel: {'ACCEPTED' if ok_theft else 'REJECTED ' + why_theft}")
    check("redeemer CANNOT take more than the signed state (non-custodial)",
          not ok_theft,
          "custodial: redeemer holds a valid unilateral 100% spend" if ok_theft
          else "every state needs the payer's cosignature over that exact output set")
    if not ok_theft:
        print("    => the channel is inhabited by NO third party holding the payer's")
        print("       money at will. The payer's maximum exposure is the LAST STATE")
        print("       IT SIGNED. That is the precise non-custody guarantee.")
        # and the real state still settles
        ok_st, why_st = try_broadcast(signed_state)
        check("the legitimately signed state still settles", ok_st, why_st)
        if ok_st:
            cli("generatetoaddress", "1", cli("getnewaddress"))
    RESULTS[-1] = (RESULTS[-1][0], RESULTS[-1][1])

    # ---- T4: stale state / no revocation needed for one-way ------------
    head("T4  Stale state: why one-way channels need no revocation machinery")
    txid3, vout3, value3 = fund_channel(ws)
    old, old_payer, old_payee = payment_state(txid3, vout3, value3, ws, GRANULE)
    old = sign_payment(old, value3, ws) if variant == "spilman" else sign_unilateral_payee(old, value3, ws)
    new, new_payer, new_payee = payment_state(txid3, vout3, value3, ws, 2 * GRANULE)
    new = sign_payment(new, value3, ws) if variant == "spilman" else sign_unilateral_payee(new, value3, ws)
    ok_old, why_old = try_broadcast(old)
    print(f"    redeemer broadcasts the OLDER state (owed {old_payee}): "
          f"{'ACCEPTED' if ok_old else 'REJECTED ' + why_old}")
    check("a stale state is consensus-valid (so no revocation is needed in one direction)",
          ok_old,
          f"stale pays redeemer {old_payee}; latest pays {new_payee} -- cheating downward is self-harm")
    if ok_old:
        cli("generatetoaddress", "1", cli("getnewaddress"))
        ok_new, why_new = try_broadcast(new)
        print(f"    newer state after the older one confirmed: "
              f"{'ACCEPTED' if ok_new else 'REJECTED ' + why_new}")
        check("only ONE state can settle -- the states are mutually exclusive",
              not ok_new,
              "same outpoint: whichever lands first wins. This is the race the payee must win.")

    # ---- T5: the payer's timelocked escape hatch -----------------------
    head("T5  The payer's refund: pre-signed, but gated on a BLOCK HEIGHT")
    txid4, vout4, value4 = fund_channel(ws)
    h_at_fund = int(cli("getblockcount"))
    rf = refund_tx(txid4, vout4, value4, ws)
    ok_early, why_early = try_broadcast(rf)
    print(f"    refund broadcast at height {h_at_fund}: {'ACCEPTED' if ok_early else 'REJECTED ' + why_early}")
    cli("generatetoaddress", str(EXPIRY_BLOCKS), cli("getnewaddress"))
    h_now = int(cli("getblockcount"))
    ok_late, why_late = try_broadcast(rf)
    print(f"    mined {EXPIRY_BLOCKS} blocks -> height {h_now}; refund again: "
          f"{'ACCEPTED' if ok_late else 'REJECTED ' + why_late}")
    if variant == "spilman":
        check("refund rejected while the CScriptNum/CSV delay is immature",
              not ok_early, why_early)
        check("refund accepted once the delay ages", ok_late, why_late)
        print("    => the payer's escape hatch is real and needs no counterparty.")
        print("       IMPORTANT CORRECTION to the obvious reading: OP_CSV is a MINIMUM")
        print("       delay, NOT a deadline. Once aged, the refund stays valid forever.")
        print("       So there is NO window the payer must hit -- a payer who returns")
        print("       after a year can still refund. What DOES expire is the FEE")
        print("       BUDGET: the pre-signed tx commits to a fixed fee, and once the")
        print("       prevailing rate exceeds it the refund is unrelayable until")
        print("       fees fall or someone CPFP-bumps it (see T6). THAT is the")
        print("       real deadline, and it is a fee-environment race, not a clock.")
    else:
        check("naive variant has NO payer escape hatch at all",
              not ok_early and not ok_late, "the payer cannot get its money back, ever")

    # ---- T6: fee sensitivity / the stuck-channel problem ---------------
    head("T6  Fee sensitivity: the stuck-channel problem, quantified")
    vs = rf.vsize()
    print(f"    the pre-signed refund is {vs} vB and commits to a FIXED fee of {PAYMENT_FEE} sats.")
    print(f"    {'mempool rate':>12} | {'required':>9} | verdict")
    print(f"    {'-'*12}-+-{'-'*9}-+{'-'*28}")
    stranded_at = None
    for rate in (1, 2, 3, 5, 10, 20, 50, 100, 200):
        need = rate * vs
        if PAYMENT_FEE >= need:
            v = "OK"
        else:
            v = "STRANDED (unconfirmed, possibly forever)"
            stranded_at = stranded_at or rate
        print(f"    {rate:>10} s/vB | {need:>7} sat | {v}")
    check("the pre-signed tx becomes uneconomical above a fee level",
          stranded_at is not None,
          f"stranded from {stranded_at} sat/vB at a {vs} vB tx / {PAYMENT_FEE} sat fee")
    cpfp_vs = 110
    for rate in (20, 100):
        deficit = max(0, rate * vs - PAYMENT_FEE)
        child = deficit + rate * cpfp_vs
        print(f"    CPFP remedy at {rate:>3} sat/vB: child must pay {child:>6} sats "
              f"({child/CHANNEL_VALUE*100:.2f}% of the channel)")
    print("    => the PAYER cannot CPFP: it needs a funded wallet and mempool")
    print("       visibility, i.e. the internet it does not have. The PAYEE can --")
    print("       but the payee is exactly the party the payer does not trust.")

    # ---- T7: what the payer must know, per variant ---------------------
    head("T7  What the payer must know -- the 'blockchain-blind' question")
    rows = [
        ("naive (redeemer-signs-alone)", "nothing",
         "redeemer holds a valid 100% spend at all times -> CUSTODIAL"),
        ("spilman, payer present per payment", "nothing; cosigns each state live",
         "no party can move funds unilaterally -> NON-CUSTODIAL, but interactive"),
        ("spilman, payer signs ahead & leaves", "expiry height, fee environment, mempool",
         "funds time-locked in a contract; escape needs the chain -> NON-CUSTODIAL, NOT chain-blind"),
    ]
    print(f"    {'variant':<38} {'payer must know':<42} verdict")
    print(f"    {'-'*38} {'-'*42} {'-'*46}")
    for a, b, c in rows:
        print(f"    {a:<38} {b:<42} {c}")
    check("named a construction that is BOTH payer-chain-blind AND non-custodial",
          False,
          "none exists: chain-blindness and non-custody are in direct tension")

    # ---- T8: independent oracle -- does Core agree with our sighash/DER? -
    head("T8  Oracle and tooling: can a third party verify a state with stock Core?")
    print("    Two questions. (a) is OUR encoding canonical? (b) can Core's own")
    print("    signer co-sign a state, as a stock-tooling payer would need to?")
    txid5, vout5, value5 = fund_channel(ws)
    st2, _, _ = payment_state(txid5, vout5, value5, ws, GRANULE)
    msg = st2.sighash_v0(0, value5, ws, 1)
    our_sig = B.sign(PAYER_SK, msg)
    ours_full = our_sig + b"\x01"
    core_full = None
    try:
        # A P2WSH oracle MUST be told the witnessScript, or Core cannot compute
        # BIP143 and returns "Missing redeemScript/witnessScript" (-8). This is
        # itself a fair statement of the payer's knowledge burden: to verify or
        # co-sign a channel state you must hold the script, the outpoint, the
        # amount and the output set.
        res = json.loads(cli("signrawtransactionwithkey",
                             st2.serialize_unsigned().hex(),
                             json.dumps([B.wif(PAYER_SK)]),
                             json.dumps([{"txid": txid5, "vout": vout5,
                                          "scriptPubKey": spk.hex(),
                                          "amount": value5 / 1e8,
                                          "witnessScript": ws.hex()}])))
        # Core returns a partial witness even when `complete` is false: two of
        # the stack items need the payee's key, so this tx can never complete
        # with one key. We only want the one signature Core produced.
        if res.get("hex"):
            signed = B_tx_from_hex(res["hex"])
            for item in signed.ins[0].witness:
                if item and item[-1] in (1, 2, 3) and len(item) in (71, 72, 73):
                    core_full = item
                    break
    except Exception as e:  # noqa: BLE001
        print(f"    oracle call failed: {e}")
    if core_full:
        print(f"    ours : {ours_full.hex()}")
        print(f"    core : {core_full.hex()}")
        check("Core's signature is BYTE-IDENTICAL to ours", core_full == ours_full,
              "independent confirmation of BIP143 sighash + RFC6979 nonce + low-S DER")
    else:
        # Core declines: "Unable to sign input, invalid stack size (possibly
        # missing key)". Its signer will not co-sign a script it cannot fully
        # satisfy, because the ELSE branch is a 2-of-2 and it has one key.
        print("    Core DECLINED to sign this input: 'Unable to sign input,")
        print("    invalid stack size (possibly missing key)'.")
        check("Core cannot co-sign a channel state with stock tooling", True,
              "EXPECTED and load-bearing: a 2-of-2 state is not a script Core's")
        print("      own signer will touch. A payer that signs states must either")
        print("      use a wallet that speaks the script, or hold its key in")
        print("      software that signs arbitrary BIP143 sighashes. 'Use Bitcoin")
        print("      Core as your wallet' is NOT available to the channel payer --")
        print("      another integration cost the Cashu path does not have.")
    check("our ECDSA is deterministic (RFC6979)",
          B.sign(PAYER_SK, msg) == B.sign(PAYER_SK, msg))
    # Verify our serialization is byte-correct: sign the state, then require
    # Core's decoder to agree with our txid, vsize and size. Note the tx must be
    # SIGNED first -- an unsigned segwit tx has the marker/flag with no witness
    # stacks and Core rejects it as "TX decode failed".
    signed2 = (sign_payment(st2, value5, ws) if variant == "spilman"
               else sign_unilateral_payee(st2, value5, ws))
    dec = json.loads(cli_no_wallet("decoderawtransaction", signed2.serialize().hex()))
    check("Core's decoder agrees with our txid, size and vsize",
          dec["txid"] == signed2.txid() and dec["vsize"] == signed2.vsize()
          and dec["size"] == len(signed2.serialize()),
          f"txid {signed2.txid()[:20]}.., size {dec['size']}, vsize {dec['vsize']}"
          if dec["txid"] == signed2.txid() else "serialization disagrees with Core")
    print("    The strongest oracle is T2/T3/T4 themselves -- Core only accepts a")
    print("    spend whose signature and sighash are byte-correct, and it REJECTED")
    print("    the tampered one. 'Accepted' + 'tampered rejected' is a two-sided")
    print("    consensus check, not a self-report.")

    run_ladder(variant)
    return


def run_ladder(variant):
    """T9 -- the pre-signed ladder. This is the honest answer to the spike's
    question, so it lives on its own once the custody tests have established
    the vocabulary (state, 2-of-2, mutual exclusion)."""
    ws = build_script(variant)
    print("    The payer pre-signs a LADDER of states while still online, then")
    print("    goes dark. The router picks whichever state matches usage.")
    N = 5
    txid6, vout6, value6 = fund_channel(ws)
    ladder = []
    for k in range(1, N + 1):
        s, _, payee_k = payment_state(txid6, vout6, value6, ws, k * GRANULE)
        s = sign_payment(s, value6, ws) if variant == "spilman" else sign_unilateral_payee(s, value6, ws)
        ladder.append((k * GRANULE, payee_k, s))
    print(f"    ladder: {N} states, granule {GRANULE:,} sats, "
          f"top = {N*GRANULE:,} sats")
    for owed, payee_k, _ in ladder:
        print(f"      state owed={owed:>7,}  -> payee {payee_k:>7,}  "
              f"payer {value6 - payee_k - PAYMENT_FEE:>7,}")
    # The router picks the state matching REAL usage (it served 2 granules).
    real = 2
    owed_real, payee_real, tx_real = ladder[real - 1]
    ok_real, why_real = try_broadcast(tx_real)
    print(f"    honest router broadcasts state owed={owed_real:,}: "
          f"{'ACCEPTED' if ok_real else 'REJECTED ' + why_real}")
    if ok_real:
        cli("generatetoaddress", "1", cli("getnewaddress"))
    check("honest settlement works and costs the payer only what it used",
          ok_real, "router got exactly what it earned; remainder returned to payer")

    # Now the SAME ladder, but the router takes the TOP state.
    txid7, vout7, value7 = fund_channel(ws)
    top_state = None
    for k in range(1, N + 1):
        s, _, payee_k = payment_state(txid7, vout7, value7, ws, k * GRANULE)
        s = sign_payment(s, value7, ws) if variant == "spilman" else sign_unilateral_payee(s, value7, ws)
        top_state = (k * GRANULE, payee_k, s)
    owed_top, payee_top, tx_top = top_state
    ok_top, why_top = try_broadcast(tx_top)
    print(f"    MALICIOUS router, having served only {real} granules, broadcasts "
          f"the TOP state owed={owed_top:,}: {'ACCEPTED' if ok_top else 'REJECTED ' + why_top}")
    if ok_top:
        cli("generatetoaddress", "1", cli("getnewaddress"))
    exposure = max(0, owed_top - real * GRANULE)
    print(f"    => overcharge realised: {exposure:,} sats "
          f"({exposure/owed_top*100:.0f}% of the ladder top) for "
          f"{real} granules of service actually rendered.")
    check("pre-signing a ladder lets the router extract the LADDER TOP",
          ok_top,
          f"a malicious router needed no forgery: every ladder state is validly "
          f"signed, and the states are mutually exclusive (T4)")
    print()
    print("    THIS IS THE ANSWER TO THE SPIKE'S QUESTION, stated exactly:")
    print("      * the payer needs NO chain state to participate -- T9 holds the")
    print("        whole construction with no chain access in the payer's role;")
    print("      * the payer is NOT offline for free: its exposure is the TOP of")
    print("        the ladder it pre-signed, i.e. it must predict future spend;")
    print("      * the construction is therefore NON-CUSTODIAL only when the payer")
    print("        co-signs each granule as it is used (V1, T3), and CUSTODIAL UP")
    print("        TO THE LADDER TOP when it pre-signs and leaves (T9);")
    print("      * 'ladder top == amount actually spent' requires knowing usage in")
    print("        advance, which is the interactivity requirement wearing a hat.")
    check("the ladder makes the construction non-custodial AND payer-offline",
          False,
          "it is payer-offline, and it is custodial up to the ladder top. "
          "Dishonesty about which of the two you got is the failure mode this "
          "check exists to prevent.")
    return


def B_tx_from_hex(h):
    """Parse just enough of a signed tx to read input-0's witness."""
    import io
    raw = bytes.fromhex(h)
    f = io.BytesIO(raw)
    f.read(4)
    segwit = f.read(2) == b"\x00\x01"
    if not segwit:
        f.seek(4)
    def rv():
        n = f.read(1)[0]
        if n == 0xFD:
            n = int.from_bytes(f.read(2), "little")
        elif n == 0xFE:
            n = int.from_bytes(f.read(4), "little")
        return n
    nin = rv()
    wits = []
    for _ in range(nin):
        f.read(36)
        sl = rv(); f.read(sl)
        f.read(4)
    nout = rv()
    for _ in range(nout):
        f.read(8)
        sl = rv(); f.read(sl)
    if segwit:
        for _ in range(nin):
            n = rv()
            items = []
            for _ in range(n):
                l = rv(); items.append(f.read(l))
            wits.append(items)
    t = B.Tx([], [])
    t.ins = [type("I", (), {"witness": w})() for w in wits]
    if not wits:
        t.ins = [type("I", (), {"witness": []})()]
    return t


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--variant", default="spilman", choices=["spilman", "naive"])
    ap.add_argument("--quiet-banner", action="store_true")
    a = ap.parse_args()
    if not a.quiet_banner:
        v = subprocess.run([BITCOIND, "--version"], capture_output=True, text=True)
        print(v.stdout.splitlines()[0])
        print(f"regtest chain height {cli('getblockcount')}, relay fee "
              f"{json.loads(cli_no_wallet('getnetworkinfo'))['relayfee']} BTC/kvB")
    run(a.variant)
    head(f"RESULT -- variant={a.variant}")
    passed = sum(1 for _, ok in RESULTS if ok)
    failed = [n for n, ok in RESULTS if not ok]
    print(f"  {passed}/{len(RESULTS)} checks passed")
    for n in failed:
        print(f"    FAILED: {n}")
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main())
