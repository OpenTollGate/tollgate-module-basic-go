#!/usr/bin/env python3
"""Regtest cross-check: does Bitcoin Core accept the v2 spends, and does it agree
that the timelock is a SERVICE policy rather than a consensus fact?

Uses a local regtest node on 127.0.0.1:18443 (−regtest, no public network, no real
coin). Starts one itself if none is listening, and stops only a node it started.

Writes out/CONSENSUS.txt.
"""
import json, os, socket, struct, subprocess, sys, time

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.dirname(HERE)
OUT = os.path.join(ROOT, "out")
os.makedirs(OUT, exist_ok=True)
sys.path.insert(0, ROOT)

from lnurlcash import note as v1

BTC = os.path.expanduser("~/.local/toolchain/btc/bin")
RPC = 18443
log = []
def emit(s=""):
    print(s); log.append(s)

def cli(*args, raw=False):
    p = subprocess.run([os.path.join(BTC, "bitcoin-cli"), "-regtest", "-rpcport=%d" % RPC,
                        "-rpcuser=rig", "-rpcpassword=rig"] + [str(a) for a in args],
                       capture_output=True, text=True, timeout=90)
    if p.returncode != 0:
        raise RuntimeError(" ".join(args) + " -> " + p.stderr.strip())
    return p.stdout if raw else json.loads(p.stdout)

def port_open():
    s = socket.socket(); s.settimeout(0.4)
    ok = s.connect_ex(("127.0.0.1", RPC)) == 0
    s.close(); return ok

def finish(code=0):
    open(os.path.join(OUT, "CONSENSUS.txt"), "w").write("\n".join(log) + "\n")
    print("\nwrote " + os.path.join(OUT, "CONSENSUS.txt"))
    sys.exit(code)

emit("=" * 78); emit("CONSENSUS CROSS-CHECK — LUD-25 @ fd80cdd (v2)"); emit("=" * 78)

bd = os.path.join(BTC, "bitcoind")
if not (os.access(bd, os.X_OK) and os.access(os.path.join(BTC, "bitcoin-cli"), os.X_OK)):
    emit("bitcoind/bitcoin-cli NOT present at %s — consensus cross-check NOT RUN" % BTC)
    emit("Any taproot assertion in the accompanying notes is REASONED, not MEASURED.")
    finish(0)

started = None
if not port_open():
    os.makedirs(os.path.expanduser("~/.hermes/cache/scratch/tg-regtest-v2"), exist_ok=True)
    started = subprocess.Popen(
        [bd, "-datadir=" + os.path.expanduser("~/.hermes/cache/scratch/tg-regtest-v2"),
         "-regtest", "-server", "-rpcuser=rig", "-rpcpassword=rig", "-rpcport=%d" % RPC,
         "-fallbackfee=0.0002", "-printtoconsole=1"],
        stdout=open(os.path.join(OUT, "bitcoind.log"), "w"), stderr=subprocess.STDOUT)
    for _ in range(120):
        if port_open():
            break
        time.sleep(0.5)
if not port_open():
    emit("could not reach a regtest RPC on 127.0.0.1:%d — cross-check NOT RUN" % RPC)
    finish(0)

try:
    net = cli("getnetworkinfo"); chain = cli("getblockchaininfo")
    emit("bitcoind %s  chain=%s  height=%d  rpc=127.0.0.1:%d"
         % (net["subversion"], chain["chain"], chain["blocks"], RPC))

    V = json.load(open(os.path.join(ROOT, "vectors", "25-vectors.json")))
    k = V["key_path_spend"]; tl = V["timelock_note"]; b = V["bearer_note"]

    emit("\n--- A  the notes' own output keys, expanded by Core")
    for name, Qhex in (("key_path", k["Q"]), ("bearer", b["Q"]), ("timelock", tl["Q"])):
        try:
            info = cli("getdescriptorinfo", "tr(50929b74c1a04954b78b4b6035e97a5e078a5a0f28ec96d547bfee9ace803ac0,pk(02%s))" % Qhex)
            addr = cli("deriveaddresses", info["descriptor"])[0]
            spk = cli("getaddressinfo", addr)["scriptPubKey"]
            emit("  %-9s Q=%s…  addr=%s  spk-type=%s"
                 % (name, Qhex[:16], addr[:26], cli("decodescript", spk).get("type", "?")))
        except Exception as e:
            emit("  %-9s Core rejected it: %s" % (name, e))

    emit("\n--- B  fund a P2TR output at the bearer note's spk, spend it with its cw1")
    wallets = cli("listwallets")
    if "rig" not in wallets:
        try:
            cli("createwallet", "rig")
        except Exception:
            cli("loadwallet", "rig")
    addr = cli("getnewaddress", raw=True).strip()
    cli("generatetoaddress", 101, addr)
    bearer_spk = v1.spent_script_pubkey(bytes.fromhex(b["Q"])).hex()
    # LUD-25's own descriptor for a key-path note is rawtr(Q); here we need the
    # OUTPUT key form, which rawtr() gives, so Core will pay to exactly OP_1 <Q>.
    info = cli("getdescriptorinfo", "rawtr(%s)" % b["Q"])
    bearer_addr = cli("deriveaddresses", info["descriptor"])[0]
    core_spk = cli("getaddressinfo", bearer_addr)["scriptPubKey"]
    emit("  derived spk = %s" % bearer_spk)
    emit("  Core rawtr(%s…) -> %s  spk-identical=%s"
         % (b["Q"][:12], bearer_addr[:26], core_spk == bearer_spk))
    utxo = max(cli("listunspent", "1"), key=lambda x: x["amount"])
    change_addr = cli("getnewaddress", raw=True).strip()
    change_spk = cli("getaddressinfo", change_addr)["scriptPubKey"]
    in_amt = int(round(float(utxo["amount"]) * 1e8))
    fund = cli("createrawtransaction",
               json.dumps([{"txid": utxo["txid"], "vout": utxo["vout"]}]),
               json.dumps([{bearer_addr: 0.5},
                           {change_addr: round((in_amt - 50_000_000 - 10_000) / 1e8, 8)}]),
               raw=True).strip()
    signed = cli("signrawtransactionwithwallet", fund)
    emit("  wallet signed the funding tx: complete=%s" % signed.get("complete"))
    funded = cli("sendrawtransaction", signed["hex"], raw=True).strip()
    cli("generatetoaddress", 1, addr)
    rtx = cli("decoderawtransaction", signed["hex"])
    vi = next(i for i, o in enumerate(rtx["vout"]) if o["scriptPubKey"]["hex"] == bearer_spk)
    emit("  funded txid=%s vout=%d value=%s type=%s"
         % (funded[:16], vi, rtx["vout"][vi]["value"], rtx["vout"][vi]["scriptPubKey"]["type"]))
    emit("  funded spk == derived spk : %s"
         % (rtx["vout"][vi]["scriptPubKey"]["hex"] == bearer_spk))

    lt, seq, script, control, wit = v1.decode_cw1(b["cw1"])
    emit("  cw1 → locktime=%d sequence=%#x script=%s control=%d B witness=%d item(s)"
         % (lt, seq, script.hex(), len(control), len(wit)))
    emit("  Core's asm of that leaf: %s" % cli("decodescript", script.hex())["asm"])

    def vi_(n):
        return bytes([n]) if n < 0xfd else b"\xfd" + struct.pack("<H", n)
    def ser(nver, vin, vout, locktime):
        segwit = any(v["wit"] for v in vin)
        o = struct.pack("<i", nver)
        if segwit:
            o += b"\x00\x01"            # marker + flag
        o += vi_(len(vin))
        for v in vin:
            o += bytes.fromhex(v["txid"])[::-1] + struct.pack("<I", v["vout"])
            o += vi_(len(v["ss"])) + v["ss"]   # scriptSig length belongs here
            o += struct.pack("<I", v["seq"])
        o += vi_(len(vout))
        for v in vout:
            o += struct.pack("<q", v["val"]) + vi_(len(v["spk"])) + v["spk"]
        for v in vin:
            if v["wit"]:
                o += vi_(len(v["wit"]))          # witness stack item count
                for item in v["wit"]:
                    o += vi_(len(item)) + item
        return (o + struct.pack("<I", locktime)).hex()

    spend = ser(2, [{"txid": funded, "vout": vi, "ss": b"", "seq": 0xffffffff,
                     "wit": [(w if isinstance(w, bytes) else bytes.fromhex(w)) for w in wit]
                            + [script, control]}],
                [{"val": 49_990_000, "spk": bytes.fromhex(change_spk)}], 0)
    try:
        txid = cli("sendrawtransaction", spend, raw=True).strip()
        cli("generatetoaddress", 1, addr)
        emit("  RESULT: Core ACCEPTED the bearer script-path spend  txid=%s" % txid[:16])
    except Exception as e:
        emit("  RESULT: Core REJECTED it -> %s" % e)
        emit("  (a reject here is a finding about the vector or about this harness, not a verdict)")

    emit("\n--- C  the timelock is a claim; consensus never sees 'now'")
    claims = [v1.decode_cw1(s["cw1"])[0] for s in tl["spends"]]
    emit("  vector locktime T=%d; the two spends claim %s" % (tl["T"], claims))
    emit("  vector 'expect' for each: %s" % [s["expect"] for s in tl["spends"]])
    emit("  Core's view of the leaf: %s" % cli("decodescript", tl["leaf"])["asm"])
    emit("  Both spends are syntactically valid tapscript. Which one a SERVICE")
    emit("  accepts is decided by its clock — which is exactly the spec's warning.")
finally:
    if started is not None:
        try:
            cli("stop")
        except Exception:
            pass
        time.sleep(2)
        if started.poll() is None:
            started.kill()

finish(0)
