#!/usr/bin/env python3
"""T15 ``swap_proof_loss`` -- conclusive, deterministic, host-local.

Property under test
-------------------
The gonuts fork carries a funds-safety fix upstream will never absorb
(``7dc430b``, "proof loss in swap"). Any replacement wallet must reproduce it:
**an interrupted swap must not lose the user's value.**

Why the earlier scaffold was inconclusive
-----------------------------------------
A local fakewallet mint settles a swap in single-digit milliseconds, so
SIGKILLing the daemon on a timer almost never lands inside the only window that
matters (mint accepted the swap, wallet not yet told). This script widens that
window deterministically at the network layer instead of racing it.

Method
------
1. Fresh local ``cdk-mintd`` (fakewallet, **plain HTTP**) behind
   ``drop_swap_proxy.py``.
2. Wallet **A** mints, then ``send``s a token (an ordinary send; the proxy is
   not armed).

   Wallet **B** receives that token -- a receive always performs an online
   NUT-03 swap, so it is a deterministic swap trigger.
3. Arm the proxy; B's ``/v1/swap`` is **delivered to the mint** (inputs really
   spent, outputs really issued) and the **response is discarded**. SIGKILL B
   the instant the drop is observed.
4. Restart B on the same wallet DB + seed. Startup reconciliation
   (``recover_incomplete_sagas`` + ``check_all_pending_proofs`` plus CDK's
   NUT-13/NUT-09 deterministic-output restore) runs.
5. Assert no value was lost: B's post-restart balance >= the received amount.

Verdict is printed as JSON; a transcript and raw logs are kept in the work dir.
Pass => the candidate reproduces the fork's funds-safety property.
"""

import argparse
import json
import os
import re
import signal
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

ROOT = Path("/home/c03rad0r/r2-work")
HERE = Path(__file__).resolve().parent

DEFAULTS = {
    "daemon": ROOT / "cdk-upstream/target-daemon/release/cdk-walletd",
    "client": ROOT / "cdkinterop/cdkinterop.host",
    "mintd": ROOT / "cdk-upstream/target-mintd/release/cdk-mintd",
    "proxy": HERE / "drop_swap_proxy.py",
}

MINT_MNEMONIC = (
    "abandon abandon abandon abandon abandon abandon "
    "abandon abandon abandon abandon abandon about"
)


class Wallet:
    """One cdk-walletd instance (socket + sqlite work dir)."""

    def __init__(self, runner, name):
        self.r = runner
        self.name = name
        self.dir = runner.root / name
        self.dir.mkdir(parents=True, exist_ok=True)
        self.sock = str(self.dir / "w.sock")
        self.log = self.dir / "daemon.log"
        self.proc = None

    def start(self, mnemonic=None):
        if os.path.exists(self.sock):
            os.remove(self.sock)
        f = open(self.log, "a")
        cmd = [str(self.r.args.daemon), "--socket", self.sock,
               "--work-dir", str(self.dir),
               "--mint", f"http://127.0.0.1:{self.r.port_proxy}"]
        if mnemonic:
            cmd += ["--mnemonic", mnemonic]
        self.proc = subprocess.Popen(cmd, stdout=f, stderr=f)
        for _ in range(150):
            if os.path.exists(self.sock):
                return
            if self.proc.poll() is not None:
                raise RuntimeError(f"wallet {self.name} exited during startup")
            time.sleep(0.1)
        raise RuntimeError(f"wallet {self.name} socket never appeared")

    def kill(self):
        if self.proc and self.proc.poll() is None:
            self.proc.send_signal(signal.SIGKILL)
            self.proc.wait(timeout=10)

    def mnemonic(self):
        m = re.findall(r"generated mnemonic: (.+)", self.log.read_text(errors="ignore"))
        return m[-1].strip() if m else ""

    def reconcile_lines(self):
        return [ln.strip() for ln in self.log.read_text(errors="ignore").splitlines()
                if "reconcil" in ln or "saga" in ln]

    def call(self, *a, timeout=60):
        try:
            out = subprocess.run(
                [str(self.r.args.client), self.sock, *[str(x) for x in a]],
                capture_output=True, text=True, timeout=timeout,
            ).stdout
        except subprocess.TimeoutExpired:
            return {"ok": False, "error": "timeout"}
        for line in reversed(out.strip().splitlines()):
            s = line.strip()
            if s.startswith("{"):
                try:
                    return json.loads(s)
                except json.JSONDecodeError:
                    continue
        return {"ok": False, "error": "no json"}

    def balance(self):
        return (self.call("balance") or {}).get("balance")


class Runner:
    def __init__(self, args):
        self.args = args
        self.port_mint = args.mint_port
        self.port_proxy = args.proxy_port
        self.root = Path(args.workdir)
        self.root.mkdir(parents=True, exist_ok=True)
        self.mint_dir = self.root / "mint"
        self.armed = self.root / "ARM"
        self.transcript = []
        self.mint_proc = None
        self.proxy_proc = None

    def log(self, msg):
        line = f"{time.strftime('%H:%M:%S')} {msg}"
        print(line, flush=True)
        self.transcript.append(line)

    # -- infra -------------------------------------------------------------
    def start_mint(self):
        self.mint_dir.mkdir(parents=True, exist_ok=True)
        cfg = self.mint_dir / "config.toml"
        cfg.write_text(
            f"""[info]
url = "http://127.0.0.1:{self.port_proxy}/"
listen_host = "127.0.0.1"
listen_port = {self.port_mint}
mnemonic = "env:CDK_MINTD_MNEMONIC"

[database]
engine = "sqlite"

[payment_backend]
backend = "fakewallet"

[fake_wallet]
fee_percent = 0
reserve_fee_min = 0
min_delay_time = 0
max_delay_time = 0
"""
        )
        env = dict(os.environ, CDK_MINTD_MNEMONIC=MINT_MNEMONIC)
        for step in (["config", "validate"], ["config", "init", "--new-mint"]):
            r = subprocess.run(
                [str(self.args.mintd), "--work-dir", str(self.mint_dir), *step,
                 "--file", str(cfg)],
                env=env, capture_output=True, text=True, timeout=180,
            )
            if r.returncode != 0:
                raise RuntimeError(f"mint {' '.join(step)}: {r.stderr[-300:]}")
        log = open(self.mint_dir / "mint.log", "w")
        self.mint_proc = subprocess.Popen(
            [str(self.args.mintd), "--work-dir", str(self.mint_dir)],
            env=env, stdout=log, stderr=log,
        )
        self._wait_url(f"http://127.0.0.1:{self.port_mint}/v1/info", "mint")

    def start_proxy(self):
        log = open(self.root / "proxy.log", "w")
        self.proxy_proc = subprocess.Popen(
            [sys.executable, str(self.args.proxy),
             "--listen-port", str(self.port_proxy),
             "--target-port", str(self.port_mint),
             "--armed-file", str(self.armed)],
            stdout=log, stderr=log,
        )
        self._wait_url(f"http://127.0.0.1:{self.port_proxy}/v1/info", "proxy")

    def _wait_url(self, url, what, tries=120):
        for _ in range(tries):
            try:
                urllib.request.urlopen(url, timeout=2).read()
                return
            except Exception:
                time.sleep(0.25)
        raise RuntimeError(f"{what} never healthy at {url}")

    def proxy_dropped(self):
        p = self.root / "proxy.log"
        return p.exists() and "DROPPED" in p.read_text(errors="ignore")

    def stop_all(self):
        for w in getattr(self, "wallets", []):
            w.kill()
        for p in (self.proxy_proc, self.mint_proc):
            if p and p.poll() is None:
                try:
                    p.terminate(); p.wait(timeout=5)
                except Exception:
                    p.kill()


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--daemon", default=str(DEFAULTS["daemon"]))
    ap.add_argument("--client", default=str(DEFAULTS["client"]))
    ap.add_argument("--mintd", default=str(DEFAULTS["mintd"]))
    ap.add_argument("--proxy", default=str(DEFAULTS["proxy"]))
    ap.add_argument("--mint-port", type=int, default=8085)
    ap.add_argument("--proxy-port", type=int, default=8086)
    ap.add_argument("--amount", type=int, default=100,
                    help="sats A mints and sends to B (the swap under fault)")
    ap.add_argument("--negative-control", action="store_true", default=True,
                    help="also prove a fresh-seed wallet cannot recover (default: on)")
    ap.add_argument("--no-negative-control", dest="negative_control",
                    action="store_false")
    ap.add_argument("--workdir", default=str(ROOT / f"parity-run-{int(time.time())}"))
    args = ap.parse_args()

    for name in ("daemon", "client", "mintd"):
        if not Path(getattr(args, name)).exists():
            print(json.dumps({"ok": False, "error": f"missing {name}: {getattr(args, name)}"}))
            return 2

    r = Runner(args)
    r.wallets = []
    verdict = {"case": "swap_proof_loss", "ok": False, "workdir": args.workdir}
    try:
        r.log(f"workdir {args.workdir}")
        r.start_mint()
        r.start_proxy()

        A = Wallet(r, "walletA"); r.wallets.append(A)
        B = Wallet(r, "walletB"); r.wallets.append(B)
        A.start(); B.start()
        mnA, mnB = A.mnemonic(), B.mnemonic()
        r.log(f"A mnemonic {mnA[:20]}... | B mnemonic {mnB[:20]}...")

        # Fund A.
        q = A.call("mintquote", args.amount)
        if not q.get("ok"):
            raise RuntimeError(f"A mintquote: {q}")
        for _ in range(40):
            if A.call("mqstate", q["quote_id"]).get("state") in ("PAID", "ISSUED"):
                break
            time.sleep(1)
        A.call("mint", q["quote_id"])
        balA = A.balance()
        r.log(f"A minted -> balance {balA}")
        if balA != args.amount:
            raise RuntimeError(f"A expected {args.amount}, got {balA}")

        # A sends a token (proxy not armed -> ordinary path).
        sent = A.call("send", args.amount)
        r.log(f"A send -> ok={sent.get('ok')} err={sent.get('error')} "
              f"token_len={sent.get('token_len')}")
        if not sent.get("ok"):
            raise RuntimeError(f"A send failed: {sent}")
        token = sent["token"]

        # Arm the proxy and drive B's receive: the NUT-03 swap whose response we drop.
        r.armed.touch()
        r.log("ARMED; B receiving token (swap response will be dropped)")
        client = subprocess.Popen(
            [str(args.client), B.sock, "receive", token],
            stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True,
        )
        dropped = False
        for _ in range(200):
            if r.proxy_dropped():
                dropped = True
                break
            time.sleep(0.1)
        B.kill()  # crash the instant the response was dropped
        r.log(f"swap response dropped={dropped}; B SIGKILLed")
        try:
            cout = client.communicate(timeout=10)[0]
            r.log(f"B receive client: {cout.strip()[-200:]}")
        except subprocess.TimeoutExpired:
            client.kill()
        if not dropped:
            raise RuntimeError("proxy never saw a /v1/swap to drop")

        # Negative control: a *fresh-seed* wallet must NOT be able to recover the
        # token. If it could, the inputs were never really spent and the fault
        # injection would be vacuous (the mint never processed the dropped swap).
        neg = None
        if args.negative_control:
            C = Wallet(r, "walletC"); r.wallets.append(C)
            C.start()
            rec = C.call("receive", token)
            neg = {"fresh_wallet_receive_ok": bool(rec.get("ok")),
                   "fresh_wallet_error": rec.get("error"),
                   "fresh_wallet_balance": C.balance()}
            r.log(f"negative control (fresh seed) receive -> ok={neg['fresh_wallet_receive_ok']} "
                  f"balance={neg['fresh_wallet_balance']} err={neg['fresh_wallet_error']}")
            C.kill()

        # Restart B on the same wallet + seed.
        r.log("restarting B (same work-dir + seed) ...")
        B.start(mnemonic=mnB)
        for ln in B.reconcile_lines()[-8:]:
            r.log("B: " + ln)
        post = B.balance()
        r.log(f"B post-restart balance={post} (expected >= {args.amount})")

        verdict = {
            "case": "swap_proof_loss",
            "ok": post is not None and post >= args.amount,
            "amount": args.amount,
            "A_balance_after_send": A.balance(),
            "B_balance_after_reconcile": post,
            "expected_min": args.amount,
            "response_dropped": dropped,
            "negative_control": neg,
            "B_reconcile_log": B.reconcile_lines()[-8:],
            "workdir": args.workdir,
        }
    except Exception as exc:
        verdict = {"case": "swap_proof_loss", "ok": False, "error": str(exc),
                   "workdir": args.workdir}
    finally:
        (Path(args.workdir) / "transcript.txt").write_text("\n".join(r.transcript) + "\n")
        r.stop_all()

    print(json.dumps(verdict, indent=2))
    return 0 if verdict.get("ok") else 1


if __name__ == "__main__":
    sys.exit(main())
