"""Wallet parity suite — the gate before any gonuts demotion.

Phase 2 of the cdk migration campaign (case table:
tollgate-wallet-migration skill, references/parity-suite-cases.md).
Every case is driven through the module's WalletPort client
(client/cdkinterop via tollwallet.NewSidecarWallet); raw-socket rpc()
is used only for OBSERVABILITY (saga_state), never to move money.

Invariants under test (the fund-safety contract):
  - value is preserved across daemon kills at every saga boundary
    reachable through the mint wire (swap acceptance -> response loss);
  - received value is credited exactly once after recovery;
  - a reused derivation range against a persistent mint (the
    #257/#266/#480 duplicate-outputs class) errors with a stable
    identity and is recovered by advancing the counter — never by
    re-deriving;
  - money operations FAIL CLOSED until post-crash recovery succeeded
    for that mint (mutation-checked: the suite must go red when the
    gate is bypassed).

Verdicts record status + verbatim error strings (error identity IS the
finding); structural NUT-19 code passthrough through the daemon
protocol is a recorded follow-up, not silently string-matched.

Run via run-parity.sh (boots mint + proxy + builds the daemon).
"""
import json
import os
import shutil
import signal
import socket
import subprocess
import urllib.request
import time

import pytest

RUN = os.environ.get("PARITY_RUN", "/tmp/parity-run")
DAEMON = os.environ.get("PARITY_DAEMON", os.path.expanduser("~/.cargo-target/release/cdk-walletd"))
MUTANT = os.environ.get("PARITY_MUTANT", "")
CLI = os.environ.get("PARITY_CLI", os.path.join(RUN, "cdkinterop"))
MINT_URL = "http://127.0.0.1:3340"  # the proxy — the stable wallet identity
MARKER = os.path.join(RUN, "kill.marker")
ARM = os.path.join(RUN, "arm")


def rpc(sock_path, method, params=None, timeout=30):
    s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    s.settimeout(timeout)
    s.connect(sock_path)
    s.sendall((json.dumps({"id": 1, "method": method, "params": params or {}}) + "\n").encode())
    buf = b""
    while not buf.endswith(b"\n"):
        chunk = s.recv(65536)
        if not chunk:
            break
        buf += chunk
    s.close()
    return json.loads(buf.decode())


def cli(sock_path, *args, timeout=60):
    out = subprocess.run(
        [CLI, sock_path, *args],
        capture_output=True, text=True, timeout=timeout,
        env={**os.environ, "MINT_URL": MINT_URL},
    )
    try:
        return json.loads(out.stdout.strip().splitlines()[-1])
    except (ValueError, IndexError):
        return {"ok": False, "error": f"unparseable client output: {out.stdout!r} {out.stderr!r}"}


class Daemon:
    """One cdk-walletd instance. restart(kill=True) is the crash lever."""

    def __init__(self, name, seed_path=None):
        self.name = name
        self.workdir = os.path.join(RUN, f"wd-{name}")
        self.sock = os.path.join(RUN, f"{name}.sock")
        self.log = os.path.join(RUN, f"{name}.log")
        self.seed_path = seed_path or os.path.join(RUN, f"seed-{name}.txt")
        self.proc = None

    def start(self, binary=None):
        os.makedirs(self.workdir, exist_ok=True)
        args = [binary or DAEMON, "--socket", self.sock, "--work-dir", self.workdir,
                "--mint", MINT_URL]
        if os.path.exists(self.seed_path):
            args += ["--mnemonic-file", self.seed_path]
        self.logf = open(self.log, "ab")
        self.proc = subprocess.Popen(args, stdout=self.logf, stderr=subprocess.STDOUT)
        for _ in range(100):
            if os.path.exists(self.sock):
                # socket exists but daemon may still be opening wallets
                try:
                    rpc(self.sock, "info", timeout=5)
                    return
                except OSError:
                    pass
            time.sleep(0.1)
        raise RuntimeError(f"daemon {self.name} did not come up; log:\n{open(self.log).read()}")

    def stop(self, sig=signal.SIGKILL):
        if self.proc:
            self.proc.send_signal(sig)
            self.proc.wait(timeout=10)
            self.proc = None

    def restart_after_crash(self):
        """Restart on the SAME workdir + seed — the crash-recovery path."""
        self.stop(signal.SIGKILL)
        self.start()

    def capture_seed(self):
        """Persist the daemon's generated mnemonic (first-boot mode) so
        restarts resume the same wallet. Backends that persist their own
        seed (micronuts-walletd writes its state file) print nothing —
        tolerate that."""
        if os.path.exists(self.seed_path):
            return
        for _ in range(50):
            if os.path.exists(self.log):
                for line in open(self.log, errors="replace"):
                    if "generated mnemonic:" in line:
                        words = line.split("generated mnemonic:")[1].strip()
                        with open(self.seed_path, "w") as f:
                            f.write(words)
                        os.chmod(self.seed_path, 0o600)
                        return
            time.sleep(0.1)
        # No mnemonic line: backend persists its own seed.

    def log_text(self):
        return open(self.log, errors="replace").read()


def arm_proxy():
    open(ARM, "w").close()


def disarm_proxy():
    for p in (ARM, MARKER, MARKER + ".release"):
        if os.path.exists(p):
            os.remove(p)


def release_proxy():
    open(MARKER + ".release", "w").close()


def wait_marker(timeout=30):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if os.path.exists(MARKER):
            return
        time.sleep(0.05)
    raise RuntimeError("killproxy never swallowed a watched response — "
                       "the operation did not reach the mint wire")


def fund(daemon, amount):
    """Mint `amount` sats through WalletPort (quote -> settle -> mint)."""
    q = cli(daemon.sock, "mintquote", str(amount))
    assert q.get("ok"), f"mintquote failed: {q}"
    qid = q["quote_id"]
    st = None
    for _ in range(60):  # fakewallet: instant settle, allow slack
        st = rpc(daemon.sock, "mint_quote_state", {"quote_id": qid})
        if st.get("ok") and st["result"] == 1:
            break
        time.sleep(0.25)
    else:
        raise RuntimeError(f"quote {qid} never reached PAID: {st}")
    m = rpc(daemon.sock, "mint_tokens", {"quote_id": qid})
    assert m.get("ok") and m["result"] == amount, f"mint_tokens: {m}"
    return qid


def saga_total_pending(sock):
    """Sum of amounts of incomplete sagas (the value not in spendable
    balance but still owned/pending) — the value-preservation term."""
    sg = rpc(sock, "saga_state")
    assert sg.get("ok"), sg
    return sum(s["amount"] for s in sg["result"]), sg["result"]


@pytest.fixture(autouse=True)
def clean_proxy_state():
    disarm_proxy()
    yield
    disarm_proxy()


def test_00_handshake(tmp_path):
    d = Daemon("w00")
    d.start()
    d.capture_seed()
    info = cli(d.sock, "info")
    assert info.get("ok") and info.get("backend") == "cdk", info
    assert oct(os.stat(d.sock).st_mode & 0o777) == "0o600", "socket must be 0600"
    d.stop()


def test_10_happy_roundtrip():
    d = Daemon("w10")
    d.start()
    d.capture_seed()
    fund(d, 1000)
    b = rpc(d.sock, "get_balance")
    assert b["result"] == 1000, b
    s = cli(d.sock, "send", "400")
    assert s.get("ok"), s
    tok = s["token"]
    # 400 is not exactly composable from the mint's power-of-2 split of
    # 1000 — the send forces an online swap, i.e. exercises the swap saga.
    fee = rpc(d.sock, "swap_fee_sats", {"token": tok})
    assert fee["result"] == 0, fee  # zero-fee fakewallet
    cs = cli(d.sock, "checkstate", tok)
    assert cs.get("ok") and cs["spent"] is False, cs  # through WalletPort
    r = cli(d.sock, "receive", tok)
    assert r.get("ok") and r["received"] == 400, r
    cs2 = cli(d.sock, "checkstate", tok)
    assert cs2.get("ok") and cs2["spent"] is True, cs2
    assert rpc(d.sock, "get_balance")["result"] == 1000
    d.stop()


def test_20_kill_melt_between_acceptance_and_proof_save():
    """Kill the daemon after the mint accepted a MELT (it paid the invoice
    and consumed the proofs) but before the response was processed. The
    melt is the irreversible op — value must survive the restart and be
    accounted exactly once (paid) or compensated (unpaid), never lost.

    (Original plan killed the send's online swap — empirically dead:
    cdk 0.18 sends OFFLINE whenever an exact proof subset exists, and
    the wallet's split gives rich denominations, so sends rarely touch
    the wire. Recorded as a parity finding; the receive-side swap is
    covered by test_30.)"""
    d = Daemon("w20")
    d.start()
    d.capture_seed()
    fund(d, 1000)

    # A payable invoice: any mint quote's request field (fakewallet
    # settles instantly, fee 0).
    q = cli(d.sock, "mintquote", "300")
    assert q.get("ok"), q
    invoice = q.get("request")
    assert invoice, f"no invoice in mintquote output: {q}"

    mq = cli(d.sock, "meltquote", invoice)
    assert mq.get("ok"), f"meltquote failed: {mq}"
    assert mq["amount"] == 300, mq

    arm_proxy()
    proc = subprocess.Popen(
        [CLI, d.sock, "melt", mq["quote_id"]],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        env={**os.environ, "MINT_URL": MINT_URL},
    )
    wait_marker()
    d.stop(signal.SIGKILL)  # mint paid + proofs consumed, response lost
    release_proxy()
    proc.wait(timeout=30)

    d.start()
    bal = rpc(d.sock, "get_balance")["result"]
    pending, sagas = saga_total_pending(d.sock)
    # PRE-recovery (deterministic, 5/5 census under the real daemon,
    # 2026-10-29): the melt saga holds the 300 RESERVED — 700 spendable
    # + 300 in the saga = 1000. Nothing lost across the kill.
    print(f"    [shape] pre-recovery: bal={bal} pending={pending}")
    assert (bal, pending) == (700, 300), (
        f"unexpected melt-kill state (census: always 700/300): "
        f"balance={bal} pending={pending} sagas={json.dumps(sagas)}"
    )
    # A money op forces the fail-closed recovery gate. OUTCOME (measured
    # against cdk-mintd fakewallet, 2026-10-28): the melt COMPENSATES —
    # the fakewallet cannot pay an invoice that is one of its own mint
    # quotes ("Request already paid" at settlement), so the MINT's own
    # melt saga rolls back (proofs_recovered=true, quote UNPAID) and the
    # wallet's recovery compensates in agreement. Final: 1000 - 1 (the
    # send) with NO pending melt saga. The kill boundary, the mint's
    # rollback, and the wallet's compensation were all observed in the
    # mint's saga log — two-sided saga coherence, no phantom money.
    s2 = cli(d.sock, "send", "1")
    assert s2.get("ok"), s2
    bal2 = rpc(d.sock, "get_balance")["result"]
    pending2, sagas2 = saga_total_pending(d.sock)
    # 999 spendable + the 1-sat send token (Send sagas park in
    # TokenCreated until the recipient claims — a monitoring record,
    # value preserved) = 1000. The melt saga itself must be RESOLVED.
    assert bal2 + pending2 == 1000, (
        f"post-recovery accounting wrong: balance={bal2} pending={pending2} "
        f"sagas={json.dumps(sagas2)}"
    )
    melt_pending = [s for s in sagas2 if s["kind"] == "Melt"]
    # Post-recovery (deterministic, 5/5 census under the real daemon):
    # the melt saga is RESOLVED — the mint's settlement failure (a
    # fakewallet cannot pay its own invoice) rolled the mint's saga
    # back and the wallet's recovery compensated in agreement;
    # two-sided saga coherence, no phantom money. bal2 999 + the 1-sat
    # send token (a Send saga parks in TokenCreated until claimed — a
    # monitoring record, value preserved) = 1000. A stale or
    # still-reserved melt saga after recovery is a REGRESSION (value
    # locked or observability lied to): Amperstrand/cdk-walletd#1
    # tracks the single-pass-recovery hardening — the shapes that
    # motivated it were observed under a gate-bypassed build during a
    # rig incident (mutation harness left the mutant as the default
    # binary), corrected there.
    print(f"    [shape] post-recovery: bal2={bal2} pending2={pending2} "
          f"melt_pending={[(x['state'], x['amount']) for x in melt_pending]}")
    assert not melt_pending, f"melt saga not resolved by recovery: {melt_pending}"
    d.stop()


def test_30_kill_receive_between_swap_acceptance_and_proof_save():
    """Receive-side boundary: the mint consumed the incoming proofs and
    signed our outputs, the response is lost, daemon killed. After
    restart the value must be credited EXACTLY ONCE (never twice)."""
    payer = Daemon("w30-payer")
    victim = Daemon("w30-victim")
    payer.start(); payer.capture_seed()
    victim.start(); victim.capture_seed()
    fund(payer, 1000)
    fund(victim, 600)
    s = cli(payer.sock, "send", "400")  # payer swap passes (proxy unarmed)
    assert s.get("ok"), s
    tok = s["token"]
    assert cli(victim.sock, "checkstate", tok)["spent"] is False

    arm_proxy()
    proc = subprocess.Popen(
        [CLI, victim.sock, "receive", tok],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        env={**os.environ, "MINT_URL": MINT_URL},
    )
    wait_marker()
    victim.stop(signal.SIGKILL)
    release_proxy()
    proc.wait(timeout=30)

    victim.start()
    bal = rpc(victim.sock, "get_balance")["result"]
    pending, sagas = saga_total_pending(victim.sock)
    # PRE-recovery: 600 own + 400 pending in the interrupted receive
    # saga — value preserved, credited-once not yet applied.
    assert bal + pending == 1000, (
        f"value lost across receive-kill: balance={bal} pending={pending} "
        f"sagas={json.dumps(sagas)}"
    )
    # Money op forces recovery: the interrupted receive completes (the
    # mint signed our outputs before the kill), the 400 is credited
    # EXACTLY ONCE — 1000 spendable, never 1400.
    s3 = cli(victim.sock, "send", "1")
    assert s3.get("ok"), s3
    bal2 = rpc(victim.sock, "get_balance")["result"]
    pending2, sagas2 = saga_total_pending(victim.sock)
    assert bal2 + pending2 == 1000, (
        f"post-recovery receive accounting wrong: balance={bal2} "
        f"pending={pending2} sagas={json.dumps(sagas2)}"
    )
    # The incoming token is spent at the mint — through WalletPort.
    assert cli(victim.sock, "checkstate", tok)["spent"] is True
    assert "recovery complete" in victim.log_text(), "recovery must run before money ops"
    # And a further money op succeeds.
    assert cli(victim.sock, "send", "1").get("ok")
    payer.stop()
    victim.stop()


def test_40_duplicate_outputs_same_seed():
    """The #257/#266/#480 class, formalized: a second wallet reusing the
    first wallet's SEED re-derives blinded messages the mint has already
    signed. Expected: the mint refuses (stable error identity, recorded
    verbatim), and recovery credits the value via a FRESHLY INCREMENTED
    counter — never by re-deriving the burned range."""
    w1 = Daemon("w40-a")
    w1.start()
    w1.capture_seed()
    fund(w1, 1000)

    w2 = Daemon("w40-b", seed_path=w1.seed_path)  # SAME seed, fresh workdir
    w2.start()
    q = cli(w2.sock, "mintquote", "500")
    assert q.get("ok"), q
    qid = q["quote_id"]
    for _ in range(60):
        st = rpc(w2.sock, "mint_quote_state", {"quote_id": qid})
        if st.get("ok") and st["result"] == 1:
            break
        time.sleep(0.25)
    m = rpc(w2.sock, "mint_tokens", {"quote_id": qid})
    # The refusal is the EXPECTED outcome: verbatim identity recorded.
    assert not m.get("ok"), f"mint unexpectedly succeeded on burned range: {m}"
    err = m.get("error", "")
    assert "already paid" in err.lower() or "already signed" in err.lower(), (
        f"unexpected error identity: {err!r}"
    )

    # Recovery: restart. MEASURED cdk 0.18.2 behavior (unpatched mint):
    # recovery retries the SAME outputs, the mint refuses again, the saga
    # is classified FAILED, the derivation counter rolls back — and the
    # wallet is BRICKED against this mint: even a FRESH quote mints from
    # the same burned range and is refused ("Request already paid").
    # Root cause (traced 2026-10-28): cdk-mintd maps the blind-signature
    # UNIQUE violation to InvoiceAlreadyPaid instead of NUT-19 11003
    # (BlindedMessageAlreadySigned), so the wallet never learns to
    # regenerate outputs with a fresh counter. Fork fix:
    # Amperstrand/cdk fix/duplicate-outputs-error-identity.
    # FLIP DETECTOR: once the fixed mint (or a wallet-side 11003 handler)
    # is in play, this assertion goes RED — flip it to assert the
    # auto-heal (mint succeeds with fresh outputs) and record the date.
    w2.restart_after_crash()
    q2 = cli(w2.sock, "mintquote", "500")
    assert q2.get("ok"), q2
    qid2 = q2["quote_id"]
    st = None
    for _ in range(60):
        st = rpc(w2.sock, "mint_quote_state", {"quote_id": qid2})
        if st.get("ok") and st["result"] == 1:
            break
        time.sleep(0.25)
    m2 = rpc(w2.sock, "mint_tokens", {"quote_id": qid2})
    err2 = m2.get("error", "").lower()
    bricked = ("already paid" in err2
               or "recovery" in err2 and "failed saga" in err2)
    assert not m2.get("ok") and bricked, (
        f"unpatched cdk 0.18.2 must brick here (mint refusal or the "
        f"fail-closed gate on the permanently-failed saga); if this mint "
        f"SUCCEEDED the duplicate-outputs heal landed — flip this "
        f"assertion: {m2}"
    )
    w1.stop(); w2.stop()


def test_50_fail_closed_gate():
    """Money ops must FAIL CLOSED while recovery cannot complete (mint
    unreachable at restart). Mutation check: with the recovery gate
    bypassed (PARITY_MUTANT binary built by run-parity.sh), the refusal
    disappears — the suite must observe the difference."""
    d = Daemon("w50")
    d.start()
    d.capture_seed()
    fund(d, 1000)

    # Leave a PENDING saga before the crash — otherwise recovery
    # completes locally with zero sagas (no mint contact) and the gate
    # correctly opens: cdk 0.18 sends OFFLINE whenever an exact proof
    # subset exists (measured 2026-10-28), so a saga-less wallet can
    # build tokens with the mint down. Fail-closed is only observable
    # when there is something to reconcile.
    inv = cli(d.sock, "mintquote", "300")["request"]
    mq = cli(d.sock, "meltquote", inv)
    arm_proxy()
    proc = subprocess.Popen(
        [CLI, d.sock, "melt", mq["quote_id"]],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
        env={**os.environ, "MINT_URL": MINT_URL},
    )
    wait_marker()
    d.stop(signal.SIGKILL)
    release_proxy()
    proc.wait(timeout=30)

    # Mint away, restart: recovery CANNOT complete (pending melt saga,
    # mint unreachable). Money ops must refuse.
    d.stop(signal.SIGKILL)
    subprocess.run(["docker", "stop", os.environ.get("PARITY_MINT_CONTAINER", "parity-mint")],
                   check=True, capture_output=True)
    try:
        d.start()
        s = cli(d.sock, "send", "1", timeout=90)
        if MUTANT:
            # The gate is bypassed: the refusal must be ABSENT. If it
            # still refused, the mutation would be undetectable — the
            # suite would be decoration.
            assert "funds ops refused" not in s.get("error", ""), (
                f"mutant still fails closed — mutation check is blind: {s}"
            )
        else:
            assert not s.get("ok"), s
            assert "funds ops refused" in s.get("error", ""), (
                f"money op did not fail closed with mint down: {s}"
            )
    finally:
        subprocess.run(["docker", "start", os.environ.get("PARITY_MINT_CONTAINER", "parity-mint")],
                       check=True, capture_output=True)
        # Wait for mintd to actually serve again — a blind sleep let the
        # NEXT test's first money op hit a mid-boot mint (listening socket
        # closes during init -> reqwest "unexpected end of file").
        for _ in range(60):
            try:
                urllib.request.urlopen(
                    "http://127.0.0.1:43340/v1/keys", timeout=2)
                break
            except Exception:
                time.sleep(0.5)
        else:
            raise RuntimeError("mint did not come back after test_50")
    d.stop()


def test_60_locked_token_refused():
    """Demotion gate, fork fix #324/#325 through WalletPort: a payment
    whose proofs are locked to a key the gateway does NOT hold must be
    REFUSED with zero credit — the 'free internet with unspendable
    tokens' attack. The gonuts fork enforces this LOCALLY
    (TollWallet.hasLockedProofs -> ErrLockedToken, before any wire
    round-trip); the cdk path is enforced by the MINT (the receive-swap
    requires the witness; without the key the mint rejects and nothing
    is consumed). The case asserts the OUTCOME both layers must share:
    refusal, zero credit, payer debited."""
    payer = Daemon("w60-payer")
    victim = Daemon("w60-victim")
    payer.start(); payer.capture_seed()
    victim.start(); victim.capture_seed()
    fund(payer, 1000)

    # secp256k1 generator G: a valid curve point whose secret the
    # victim certainly does not hold (any random valid key works).
    g = "0279be667ef9dcbbac55a06295ce870b07029bfcdb2dce28d959f2815b16f81798"
    r = cli(payer.sock, "send_p2pk", "100", g)
    assert r.get("ok") and r.get("token"), r
    tok = r["token"]

    # The victim must NOT be able to credit it.
    res = cli(victim.sock, "receive", tok)
    assert not res.get("ok"), (
        f"locked token credited — the #324 attack through the cdk "
        f"backend: {res}"
    )
    # Record the enforcement layer's error shape (mint-side witness
    # refusal on cdk; gonuts would give ErrLockedToken locally).
    print(f"    [layer] receive refusal: {res.get('error', '')[:120]}")
    vbal = rpc(victim.sock, "get_balance")["result"]
    assert vbal == 0, "victim credited for a locked token"
    # And the payer's send was real (value left the payer's wallet).
    pbal = rpc(payer.sock, "get_balance")["result"]
    assert pbal == 900, f"payer balance after locked send: {pbal}"
    payer.stop(); victim.stop()
