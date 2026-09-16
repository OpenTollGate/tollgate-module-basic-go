#!/usr/bin/env python3
"""T2e — behavioural parity: gonuts vs CDK on identical fixed inputs.

Both wallets run against the same local fakewallet mint and execute the same
fixed sequence; the observable results must match, and each wallet must be able
to *receive the other's token* (interchangeability across implementations and
token versions).

Sequence (per implementation), amount A = 100, send S = 40:

    mintquote(A) -> state ... PAID -> mint -> balance
    send(S) -> token -> balance
    decode(token) -> amount
    <other implementation's receiver> receive(token) -> received -> balance

Pass => the two backends are observably interchangeable for these flows.
"""

import argparse
import json
import os
import subprocess
import sys
import time
import urllib.request
from pathlib import Path

ROOT = Path("/home/c03rad0r/r2-work")
HERE = Path(__file__).resolve().parent

DEFAULTS = {
    "mintd": ROOT / "cdk-upstream/target-mintd/release/cdk-mintd",
    "daemon": ROOT / "cdk-upstream/target-daemon/release/cdk-walletd",
    "cdkclient": ROOT / "cdkinterop/cdkinterop.host",
    "gonutsinterop": HERE / "gonutsinterop/gonutsinterop",
}

MINT_MNEMONIC = (
    "abandon abandon abandon abandon abandon abandon "
    "abandon abandon abandon abandon abandon about"
)


def run(cmd, timeout=90):
    try:
        out = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout).stdout
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


def mint_invoice(mint_url, amount=10, timeout=10):
    """Ask the mint for a bolt11 payment request (NUT-04), for melt-quote parity."""
    req = urllib.request.Request(
        mint_url + "/v1/mint/quote/bolt11",
        data=json.dumps({"amount": amount, "unit": "sat"}).encode(),
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            return json.loads(resp.read()).get("request", "")
    except Exception:
        return ""


class GonutsWallet:
    def __init__(self, runner, name):
        self.r = runner
        self.name = name
        self.dir = runner.root / name
        self.dir.mkdir(parents=True, exist_ok=True)

    def call(self, *a, timeout=90):
        return run([str(self.r.args.gonutsinterop), str(self.dir),
                    self.r.mint_url, *[str(x) for x in a]], timeout=timeout)

    def balance(self):
        return (self.call("balance") or {}).get("balance")


class CdkWallet:
    def __init__(self, runner, name):
        self.r = runner
        self.name = name
        self.dir = runner.root / name
        self.dir.mkdir(parents=True, exist_ok=True)
        self.sock = str(self.dir / "w.sock")
        self.log = self.dir / "daemon.log"
        self.proc = None

    def start(self):
        if os.path.exists(self.sock):
            os.remove(self.sock)
        f = open(self.log, "a")
        self.proc = subprocess.Popen(
            [str(self.r.args.daemon), "--socket", self.sock,
             "--work-dir", str(self.dir), "--mint", self.r.mint_url],
            stdout=f, stderr=f,
        )
        for _ in range(150):
            if os.path.exists(self.sock):
                return
            if self.proc.poll() is not None:
                raise RuntimeError(f"cdk {self.name} exited at startup")
            time.sleep(0.1)
        raise RuntimeError(f"cdk {self.name} socket never appeared")

    def kill(self):
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=8)
            except subprocess.TimeoutExpired:
                self.proc.kill()

    def call(self, *a, timeout=90):
        return run([str(self.r.args.cdkclient), self.sock, *[str(x) for x in a]],
                   timeout=timeout)

    def balance(self):
        return (self.call("balance") or {}).get("balance")


class Runner:
    def __init__(self, args):
        self.args = args
        self.root = Path(args.workdir)
        self.root.mkdir(parents=True, exist_ok=True)
        self.mint_dir = self.root / "mint"
        self.mint_url = f"http://127.0.0.1:{args.mint_port}"
        self.mint_proc = None
        self.log_lines = []

    def log(self, msg):
        line = f"{time.strftime('%H:%M:%S')} {msg}"
        print(line, flush=True)
        self.log_lines.append(line)

    def start_mint(self):
        self.mint_dir.mkdir(parents=True, exist_ok=True)
        cfg = self.mint_dir / "config.toml"
        cfg.write_text(
            f"""[info]
url = "{self.mint_url}"
listen_host = "127.0.0.1"
listen_port = {self.args.mint_port}
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
            r = subprocess.run([str(self.args.mintd), "--work-dir", str(self.mint_dir),
                                *step, "--file", str(cfg)],
                               env=env, capture_output=True, text=True, timeout=180)
            if r.returncode != 0:
                raise RuntimeError(f"mint {' '.join(step)}: {r.stderr[-300:]}")
        log = open(self.mint_dir / "mint.log", "w")
        self.mint_proc = subprocess.Popen(
            [str(self.args.mintd), "--work-dir", str(self.mint_dir)],
            env=env, stdout=log, stderr=log)
        for _ in range(120):
            try:
                urllib.request.urlopen(self.mint_url + "/v1/info", timeout=2).read()
                return
            except Exception:
                time.sleep(0.25)
        raise RuntimeError("mint never became healthy")

    def stop(self):
        for w in getattr(self, "wallets", []):
            try:
                w.kill()
            except Exception:
                pass
        if self.mint_proc and self.mint_proc.poll() is None:
            self.mint_proc.terminate()
            try:
                self.mint_proc.wait(timeout=5)
            except Exception:
                self.mint_proc.kill()


def sequence(r, w, amount, send_amt):
    """The fixed flow, returning the observable result dict."""
    out = {}
    q = w.call("mintquote", amount)
    out["mintquote_ok"] = bool(q.get("ok"))
    qid = q.get("quote_id")
    state = None
    for _ in range(40):
        st = w.call("mqstate", qid)
        state = st.get("state")
        if state in ("PAID", "ISSUED"):
            break
        time.sleep(1)
    out["quote_state_paid"] = state in ("PAID", "ISSUED")
    m = w.call("mint", qid)
    out["minted"] = m.get("minted")
    out["balance_after_mint"] = w.balance()
    s = w.call("send", send_amt)
    out["send_ok"] = bool(s.get("ok"))
    out["send_error"] = s.get("error")
    out["balance_after_send"] = w.balance()
    token = s.get("token", "")
    out["token_prefix"] = token[:6]
    d = w.call("decode", token)
    out["decode_amount"] = d.get("amount")
    out["_token"] = token
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--mintd", default=str(DEFAULTS["mintd"]))
    ap.add_argument("--daemon", default=str(DEFAULTS["daemon"]))
    ap.add_argument("--cdkclient", default=str(DEFAULTS["cdkclient"]))
    ap.add_argument("--gonutsinterop", default=str(DEFAULTS["gonutsinterop"]))
    ap.add_argument("--mint-port", type=int, default=8085)
    ap.add_argument("--amount", type=int, default=100)
    ap.add_argument("--send", type=int, default=40)
    ap.add_argument("--workdir", default=str(ROOT / f"parity-t2e-{int(time.time())}"))
    args = ap.parse_args()

    for name in ("mintd", "daemon", "cdkclient", "gonutsinterop"):
        if not Path(getattr(args, name)).exists():
            print(json.dumps({"ok": False, "error": f"missing {name}: {getattr(args, name)}"}))
            return 2

    r = Runner(args)
    r.wallets = []
    verdict = {"task": "T2e behavioural parity", "ok": False, "workdir": args.workdir}
    try:
        r.start_mint()
        r.log(f"mint up at {r.mint_url}")

        g = GonutsWallet(r, "gonuts"); r.wallets.append(g)
        c = CdkWallet(r, "cdk"); r.wallets.append(c); c.start()
        r.log("gonuts wallet + CDK daemon up")

        gres = sequence(r, g, args.amount, args.send)
        r.log(f"gonuts: minted={gres['minted']} bal_mint={gres['balance_after_mint']} "
              f"bal_send={gres['balance_after_send']} prefix={gres['token_prefix']}")
        cres = sequence(r, c, args.amount, args.send)
        r.log(f"cdk:    minted={cres['minted']} bal_mint={cres['balance_after_mint']} "
              f"bal_send={cres['balance_after_send']} prefix={cres['token_prefix']}")

        # Cross-receive: each implementation receives the other's token.
        g2 = GonutsWallet(r, "gonuts_recv"); r.wallets.append(g2)
        c2 = CdkWallet(r, "cdk_recv"); r.wallets.append(c2); c2.start()
        recv_c_by_g = g2.call("receive", cres["_token"])
        recv_g_by_c = c2.call("receive", gres["_token"])
        g2bal = g2.balance()
        c2bal = c2.balance()
        r.log(f"cross-receive: cdk token into gonuts -> {recv_c_by_g.get('received')} "
              f"(bal {g2bal}); gonuts token into cdk -> {recv_g_by_c.get('received')} "
              f"(bal {c2bal})")

        # Melt-quote is NOT comparable through WalletPort today:
        #   - gonuts' adapter returns "RequestMeltQuote: not yet wired; TollWallet
        #     uses MeltToLightning at a higher level" (no raw-invoice melt in the port)
        #   - cdkinterop does not expose a melt subcommand
        # Recorded as a finding rather than asserted. (The CDK daemon *does* support
        # it; the gonuts port simply does not, so port-level melt parity is N/A.)
        inv = mint_invoice(r.mint_url, 10)
        gmq = g.call("meltquote", inv)
        meltquote_note = {
            "comparable": False,
            "reason": "gonuts WalletPort.RequestMeltQuote is not wired (uses MeltToLightning); "
                      "cdkinterop exposes no melt subcommand",
            "gonuts_response": gmq.get("error") or gmq,
        }
        r.log(f"meltquote: not comparable through the port ({meltquote_note['reason']})")

        checks = {
            "gonuts_minted_100": gres["minted"] == args.amount,
            "cdk_minted_100": cres["minted"] == args.amount,
            "gonuts_balance_after_mint": gres["balance_after_mint"] == args.amount,
            "cdk_balance_after_mint": cres["balance_after_mint"] == args.amount,
            "gonuts_balance_after_send": gres["balance_after_send"] == args.amount - args.send,
            "cdk_balance_after_send": cres["balance_after_send"] == args.amount - args.send,
            "minted_equal": gres["minted"] == cres["minted"],
            "balance_after_send_equal": gres["balance_after_send"] == cres["balance_after_send"],
            "decode_amount_equal": gres["decode_amount"] == cres["decode_amount"] == args.send,
            "cross_receive_cdk_into_gonuts": recv_c_by_g.get("received") == args.send and g2bal == args.send,
            "cross_receive_gonuts_into_cdk": recv_g_by_c.get("received") == args.send and c2bal == args.send,
        }
        verdict = {
            "task": "T2e behavioural parity",
            "ok": all(checks.values()),
            "amount": args.amount, "send": args.send,
            "gonuts": {k: v for k, v in gres.items() if k != "_token"},
            "cdk": {k: v for k, v in cres.items() if k != "_token"},
            "cross_receive": {
                "cdk_token_into_gonuts": {"received": recv_c_by_g.get("received"), "balance": g2bal},
                "gonuts_token_into_cdk": {"received": recv_g_by_c.get("received"), "balance": c2bal},
            },
            "meltquote": meltquote_note,
            "checks": checks,
            "workdir": args.workdir,
        }
    except Exception as exc:
        verdict = {"task": "T2e behavioural parity", "ok": False, "error": str(exc),
                   "workdir": args.workdir}
    finally:
        (Path(args.workdir) / "transcript.txt").write_text("\n".join(r.log_lines) + "\n")
        r.stop()

    print(json.dumps(verdict, indent=2))
    return 0 if verdict.get("ok") else 1


if __name__ == "__main__":
    sys.exit(main())
