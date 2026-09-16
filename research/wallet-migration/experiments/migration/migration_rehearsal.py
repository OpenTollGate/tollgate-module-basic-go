#!/usr/bin/env python3
"""T6 — migration rehearsal: move a router's funds gonuts -> CDK, and roll back.

Mechanism (implementation-agnostic, proven interchangeable in T2e):
    drain/send the gonuts wallet to a Cashu token, receive it into CDK.
Rollback reverses it (CDK -> token -> gonuts), so the switch is reversible
without touching the losing wallet's DB.

The alternative — same-seed NUT-09 restore (point CDK at gonuts' BIP-39 mnemonic)
— is documented in 03-baseline/migration-path.md but is *not* exercised here:
extracting gonuts' seed requires a dedicated export (its bbolt store is locked
while the service runs), and NUT-13 derivation compatibility is unverified.

Assertions: value is conserved across migrate AND rollback; each wallet is empty
after handing off. Uses the local fakewallet mint (fee 0).
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
    "gonutsinterop": ROOT / "tg-research/research/wallet-migration/experiments/parity/gonutsinterop/gonutsinterop",
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


class Gonuts:
    def __init__(self, r, name):
        self.r = r
        self.dir = r.root / name
        self.dir.mkdir(parents=True, exist_ok=True)

    def call(self, *a):
        return run([str(self.r.args.gonutsinterop), str(self.dir), self.r.mint_url,
                    *[str(x) for x in a]])

    def balance(self):
        return (self.call("balance") or {}).get("balance")


class Cdk:
    def __init__(self, r, name):
        self.r = r
        self.dir = r.root / name
        self.dir.mkdir(parents=True, exist_ok=True)
        self.sock = str(self.dir / "w.sock")
        self.proc = None

    def start(self):
        if os.path.exists(self.sock):
            os.remove(self.sock)
        f = open(self.dir / "daemon.log", "a")
        self.proc = subprocess.Popen(
            [str(self.r.args.daemon), "--socket", self.sock, "--work-dir", str(self.dir),
             "--mint", self.r.mint_url], stdout=f, stderr=f)
        for _ in range(150):
            if os.path.exists(self.sock):
                return
            time.sleep(0.1)
        raise RuntimeError("cdk socket never appeared")

    def call(self, *a):
        return run([str(self.r.args.cdkclient), self.sock, *[str(x) for x in a]])

    def balance(self):
        return (self.call("balance") or {}).get("balance")

    def kill(self):
        if self.proc and self.proc.poll() is None:
            self.proc.terminate()
            try:
                self.proc.wait(timeout=8)
            except subprocess.TimeoutExpired:
                self.proc.kill()


class Runner:
    def __init__(self, args):
        self.args = args
        self.root = Path(args.workdir)
        self.root.mkdir(parents=True, exist_ok=True)
        self.mint_dir = self.root / "mint"
        self.mint_url = f"http://127.0.0.1:{args.mint_port}"
        self.mint_proc = None
        self.lines = []

    def log(self, m):
        line = f"{time.strftime('%H:%M:%S')} {m}"
        print(line, flush=True)
        self.lines.append(line)

    def start_mint(self):
        self.mint_dir.mkdir(parents=True, exist_ok=True)
        cfg = self.mint_dir / "config.toml"
        cfg.write_text(f"""[info]
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
""")
        env = dict(os.environ, CDK_MINTD_MNEMONIC=MINT_MNEMONIC)
        for step in (["config", "validate"], ["config", "init", "--new-mint"]):
            r = subprocess.run([str(self.args.mintd), "--work-dir", str(self.mint_dir),
                                *step, "--file", str(cfg)], env=env,
                               capture_output=True, text=True, timeout=180)
            if r.returncode != 0:
                raise RuntimeError(f"mint {' '.join(step)}: {r.stderr[-300:]}")
        log = open(self.mint_dir / "mint.log", "w")
        self.mint_proc = subprocess.Popen([str(self.args.mintd), "--work-dir",
                                           str(self.mint_dir)], env=env, stdout=log, stderr=log)
        for _ in range(120):
            try:
                urllib.request.urlopen(self.mint_url + "/v1/info", timeout=2).read()
                return
            except Exception:
                time.sleep(0.25)
        raise RuntimeError("mint never healthy")

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


def main():
    print("rehearsal: start", flush=True)
    ap = argparse.ArgumentParser(description=__doc__)
    for k in ("mintd", "daemon", "cdkclient", "gonutsinterop"):
        ap.add_argument(f"--{k}", default=str(DEFAULTS[k]))
    ap.add_argument("--mint-port", type=int, default=8085)
    ap.add_argument("--amount", type=int, default=100)
    ap.add_argument("--workdir", default=str(ROOT / f"migrate-{int(time.time())}"))
    args = ap.parse_args()

    r = Runner(args)
    r.wallets = []
    verdict = {"task": "T6 migration + rollback", "ok": False, "workdir": args.workdir}
    try:
        r.log(f"workdir {args.workdir}")
        r.start_mint()
        r.log("mint up")
        g = Gonuts(r, "gonuts"); r.wallets.append(g)
        c = Cdk(r, "cdk"); r.wallets.append(c); c.start()
        r.log("mint + gonuts + cdk up")

        # fund gonuts
        q = g.call("mintquote", args.amount)
        qid = q.get("quote_id")
        for _ in range(40):
            if g.call("mqstate", qid).get("state") in ("PAID", "ISSUED"):
                break
            time.sleep(1)
        g.call("mint", qid)
        g0 = g.balance()
        r.log(f"gonuts funded: balance={g0}")

        # MIGRATE: gonuts -> token -> cdk
        mig = g.call("send", args.amount)
        mig_tok = mig.get("token", "")
        rec = c.call("receive", mig_tok)
        g1, c1 = g.balance(), c.balance()
        r.log(f"MIGRATE: gonuts send ok={mig.get('ok')} -> cdk receive={rec.get('received')} "
              f"(gonuts={g1}, cdk={c1})")

        # ROLLBACK: cdk -> token -> gonuts
        rb = c.call("send", c1)
        rb_tok = rb.get("token", "")
        rec2 = g.call("receive", rb_tok)
        c2, g2 = c.balance(), g.balance()
        r.log(f"ROLLBACK: cdk send ok={rb.get('ok')} -> gonuts receive={rec2.get('received')} "
              f"(cdk={c2}, gonuts={g2})")

        checks = {
            "funded": g0 == args.amount,
            "migrate_transferred": rec.get("received") == args.amount and c1 == args.amount and g1 == 0,
            "rollback_transferred": rec2.get("received") == args.amount and g2 == args.amount and c2 == 0,
            "no_value_lost": g0 == g2 and c2 == 0,
        }
        verdict = {
            "task": "T6 migration + rollback",
            "ok": all(checks.values()),
            "amount": args.amount,
            "migrate": {"gonuts_send_ok": bool(mig.get("ok")), "cdk_received": rec.get("received"),
                        "gonuts_after": g1, "cdk_after": c1},
            "rollback": {"cdk_send_ok": bool(rb.get("ok")), "gonuts_received": rec2.get("received"),
                         "cdk_after": c2, "gonuts_after": g2},
            "checks": checks,
            "workdir": args.workdir,
        }
    except Exception as exc:
        verdict = {"task": "T6 migration + rollback", "ok": False, "error": str(exc),
                   "workdir": args.workdir}
    finally:
        (Path(args.workdir) / "transcript.txt").write_text("\n".join(r.lines) + "\n")
        r.stop()

    print(json.dumps(verdict, indent=2))
    return 0 if verdict.get("ok") else 1


if __name__ == "__main__":
    sys.exit(main())
