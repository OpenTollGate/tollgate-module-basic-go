#!/usr/bin/env python3
"""T15 swap-proof-loss fault injection (host, local fakewallet mint).

Mints 100, then repeatedly launches a `send` and SIGKILLs the daemon mid-flight,
restarting it (same work dir + same seed) and checking value conservation:
    balance + 10 * (successful tokens)  ==  minted
A shortfall means funds were lost in the interrupted swap.

The daemon prints a freshly generated mnemonic on first start (no --mnemonic);
we capture it and reuse it for every restart so the wallet continues.
"""
import json
import os
import random
import re
import signal
import subprocess
import sys
import tempfile
import time

BIN = "/home/c03rad0r/r2-work/cdk-upstream/target-daemon/release/cdk-walletd"
CLIENT = "/home/c03rad0r/r2-work/cdkinterop/cdkinterop.host"
MINT = os.environ.get("MINT", "https://nofee.testnut.cashu.space")
KILLS = int(os.environ.get("KILLS", "15"))


def start(workdir, mnemonic=None):
    sock = os.path.join(workdir, "w.sock")
    if os.path.exists(sock):
        os.remove(sock)
    log = open(os.path.join(workdir, "d.log"), "a")
    args = [BIN, "--socket", sock, "--work-dir", workdir, "--mint", MINT]
    if mnemonic:
        args += ["--mnemonic", mnemonic]
    p = subprocess.Popen(args, stdout=log, stderr=log)
    for _ in range(50):
        if os.path.exists(sock):
            break
        time.sleep(0.2)
    return p, sock


def read_mnemonic(workdir):
    try:
        for line in reversed(open(os.path.join(workdir, "d.log"), errors="ignore").read().splitlines()):
            mm = re.search(r"generated mnemonic: (.+)", line)
            if mm:
                return mm.group(1).strip()
    except FileNotFoundError:
        pass
    return ""


def cl(sock, *a, timeout=30):
    try:
        out = subprocess.run([CLIENT, sock, *[str(x) for x in a]],
                             capture_output=True, text=True, timeout=timeout).stdout
    except subprocess.TimeoutExpired:
        return {"ok": False, "error": "timeout"}
    for line in reversed(out.strip().splitlines()):
        s = line.strip()
        if s.startswith("{"):
            try:
                return json.loads(s)
            except json.JSONDecodeError:
                continue
    return {"ok": False}


def main():
    workdir = tempfile.mkdtemp(prefix="pl-")
    p, sock = start(workdir)
    mn = read_mnemonic(workdir)
    print(json.dumps({"mnemonic": mn}))

    q = cl(sock, "mintquote", 100)
    qid = q.get("quote_id")
    for _ in range(20):
        if cl(sock, "mqstate", qid).get("state") in ("PAID", "ISSUED"):
            break
        time.sleep(2)
    m = cl(sock, "mint", qid)
    minted = cl(sock, "balance").get("balance") or 0
    print(json.dumps({"mint_ok": m.get("ok"), "mint_error": m.get("error"), "minted": minted}))

    tokens = 0
    random.seed(7)
    for i in range(KILLS):
        sp = subprocess.Popen([CLIENT, sock, "send", "10"], stdout=subprocess.PIPE, text=True)
        time.sleep(random.uniform(0.0, 0.08))
        p.send_signal(signal.SIGKILL)
        try:
            out = sp.communicate(timeout=5)[0]
            for line in reversed(out.strip().splitlines()):
                s = line.strip()
                if s.startswith("{"):
                    r = json.loads(s)
                    if r.get("ok") and r.get("token"):
                        tokens += 1
                    break
        except subprocess.TimeoutExpired:
            sp.kill()
        try:
            p.wait(timeout=5)
        except subprocess.TimeoutExpired:
            p.kill()
        p, sock = start(workdir, mn)
        bal = cl(sock, "balance").get("balance")
        print(f"  kill {i}: balance={bal} tokens={tokens}")

    final = cl(sock, "balance").get("balance", 0) or 0
    p.kill()
    conserved = final + 10 * tokens
    print(json.dumps({"minted": minted, "final_balance": final, "tokens_issued": tokens,
                      "conserved": conserved, "ok": conserved >= minted}))
    return 0


if __name__ == "__main__":
    sys.exit(main())
