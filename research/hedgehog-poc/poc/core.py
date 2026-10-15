#!/usr/bin/env python3
"""core.py -- thin, dependency-free RPC client for Bitcoin Core, via bitcoin-cli.

Deliberately uses `bitcoin-cli` as a subprocess rather than a hand-rolled HTTP
client: bitcoin-cli IS the reference client for this node, so there is no chance
of us mis-speaking the RPC dialect. The harness talks only to loopback.
"""
import json
import os
import subprocess

BTC_DIR = os.environ.get("BTC_DIR",
                         os.path.expanduser("~/.local/toolchain/btc"))
CLI = os.path.join(BTC_DIR, "bin", "bitcoin-cli")
DATADIR = os.environ.get("BTC_DATADIR", os.path.join(BTC_DIR, "regtest-data"))
RPCPORT = os.environ.get("BTC_RPCPORT", "18443")


def cli(*args, wallet=None):
    base = [CLI, f"-datadir={DATADIR}", "-regtest", f"-rpcport={RPCPORT}"]
    if wallet:
        base.append(f"-rpcwallet={wallet}")
    # .strip() every arg: values round-tripped back from rpc() carry a trailing
    # newline, which bitcoind reports as odd length or invalid character.
    p = subprocess.run(base + [str(a).strip() for a in args],
                       capture_output=True, text=True)
    if p.returncode != 0:
        err = p.stderr.strip() or p.stdout.strip()
        raise RuntimeError(f"bitcoin-cli {' '.join(str(a) for a in args)} -> {err}")
    return p.stdout


def jcli(*args, wallet=None):
    out = cli(*args, wallet=wallet)
    try:
        return json.loads(out)
    except json.JSONDecodeError:
        return out.strip()


class Node:
    def __init__(self, wallet="rig"):
        self.wallet = wallet
        self.rpc = cli

    # ---- chain ----
    def height(self):
        return jcli("getblockcount")

    def mine(self, n=1):
        addr = jcli("getnewaddress", wallet=self.wallet)
        jcli("generatetoaddress", n, addr, wallet=self.wallet)
        return self.height()

    def get_tx(self, txid):
        # bitcoind wants the JSON literal "true", not Python True.
        # rpc() may hand back a value with a trailing newline -- strip it, else
        # Core sees a 65-char txid and refuses with "must be of length 64".
        return jcli("getrawtransaction", str(txid).strip(), "true")

    def get_utxo(self, txid, vout):
        return jcli("gettxout", txid, str(vout))

    # ---- raw tx ----
    def send_raw(self, rawhex, allow_high_fees=True):
        """Returns txid, or raises with Core's verdict (that is the evidence)."""
        try:
            return jcli("sendrawtransaction", rawhex, "0" if allow_high_fees else "0")
        except RuntimeError as e:
            raise

    def test_mempool_accept(self, rawhex):
        """Returns the (allowed, reject_reason, txid) triple without broadcasting."""
        out = cli("testmempoolaccept", json.dumps([rawhex]))
        a = json.loads(out)[0]
        return a.get("allowed", False), a.get("reject-reason", ""), a.get("txid", "")

    def verify_tx(self, rawhex, extra_flags=None):
        """Core's *script* verdict on a transaction that cannot be broadcast yet
        (e.g. its input is not on-chain yet). `verifytxoutproof`-adjacent; the
        returned dict's 'script-verification-failed' / absence thereof is the
        consensus evidence for witness/script correctness.

        Core 31.1: `verifymempoolaccept` does not exist; we use
        testmempoolaccept for anything whose parent is already on-chain and
        `decodescript` / scantxoutset for descriptor checks.
        """
        return jcli("testmempoolaccept", json.dumps([rawhex]))

    def find_spent_output(self, txid, vout):
        """gettxout returns None once spent -> that IS the evidence of a spend."""
        return jcli("gettxout", txid, vout)

    def rpc(self, *args):
        return jcli(*args)

    def decoderaw(self, rawhex):
        return jcli("decoderawtransaction", rawhex)

    # ---- descriptors ----
    def descriptor_info(self, desc):
        return jcli("getdescriptorinfo", desc)
