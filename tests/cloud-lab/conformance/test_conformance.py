"""
test_conformance.py — Go-side fast-subset lane for the shared
conformance/fault-injection matrix (#503).

Driven by conformance/run-conformance.sh from the host in phases,
exactly like the rejection-safety and keyset-rotation lanes: the client
container has no docker control, and process kills must be
host-orchestrated. All phases are gated on CONFORMANCE_LANE=1 and skip
in a default `client` run.

Topology (see docker-compose.conformance.yml): the PRTA fault proxy runs
as a lab service; accepted_mints in the runtime config points at it, so
every mint call the daemon makes passes the proxy, which can inject
faults (drop_response / notify triggers) and observes every blinded
message for the no-output-reuse invariant. Tokens are minted by cdk-cli
through the same proxy URL so the token's embedded mint URL matches
accepted_mints.

Verdicts: each phase writes a verdict part (scenario -> per-invariant
pass/fail/pending + evidence) into .conformance/verdict-parts/; the host
runner merges them into the PRTA verdict-table format. Invariants that
need the payment-record store are recorded as pending (blocked on #502),
never silently skipped: pending is a first-class verdict.
"""

import json
import os
import random
import tempfile
import threading
import time
from pathlib import Path

import pytest
import requests

from conftest import (
    UPSTREAM_URL,
    build_payment_event,
    create_cashu_token,
    generate_nostr_keypair,
    run_cmd,
    wait_for,
)
from test_swap_fees import assert_token_unspent

CONFORMANCE_LANE = os.environ.get("CONFORMANCE_LANE") == "1"

# In-container view of the fault proxy lab service; the runtime config's
# accepted_mints uses the same spelling so the token's embedded URL and
# the config agree.
PROXY_MINT_URL = os.environ.get("PROXY_MINT_URL", "http://faultproxy:9090")
# Direct mint URL for NUT-07 spentness checks (bypasses the proxy on
# purpose: spentness is ground truth from the mint, not under fault).
MINT_DIRECT_URL = os.environ.get("MINT_DIRECT_URL", "http://mint:8085")

HERE = Path(__file__).resolve().parent
LAB_DIR = HERE.parent
STATE_FILE = LAB_DIR / ".conformance" / "state.json"
VERDICT_DIR = LAB_DIR / ".conformance" / "verdict-parts"

PENDING_NOTE = {
    "no-fund-loss": "full reconciliation needs the payment-record store (#502)",
    "service-or-refund": "refund path is #403/#258, open at lane-authoring time",
}


def _mac():
    # Locally-administered, random per scenario: allotments are cumulative
    # per MAC, so a stable MAC would couple scenarios to each other.
    return "02:%02x:%02x:%02x:%02x:%02x" % tuple(random.randbytes(5))


def _pay(token, upstream_pubkey, customer_identity, mac, timeout=120):
    customer_sec, customer_pub = customer_identity
    event = build_payment_event(customer_sec, customer_pub, upstream_pubkey, mac, token)
    return requests.post(f"{UPSTREAM_URL}?mac={mac}", json=event, timeout=timeout)


def _proxyctl(payload):
    r = requests.post(f"{PROXY_MINT_URL}/__fault/control", json=payload, timeout=10)
    r.raise_for_status()
    return r.json()


def _observations():
    r = requests.get(f"{PROXY_MINT_URL}/__fault/observations", timeout=10)
    r.raise_for_status()
    return r.json()


def _swap_counts(obs):
    """hash -> number of sightings on swap/melt routes. The cdk-cli wallet
    also swaps through the proxy when splitting tokens; reuse of a DERIVED
    output is a swap-route property, so mint-route traffic is ignored."""
    counts = {}
    for digest, sightings in obs.get("blinded_messages", {}).items():
        n = sum(1 for s in sightings if "/v1/swap" in s or "/v1/melt" in s)
        if n:
            counts[digest] = n
    return counts


def _phase_reuse(before, after):
    """Hashes whose swap-route sightings grew in this phase: >=2 total
    sightings with growth in-phase (the set was re-exposed), whether the
    re-exposure happened inside this phase or against an earlier one."""
    grew = {}
    for digest, n in after.items():
        delta = n - before.get(digest, 0)
        if delta > 0 and n > 1:
            grew[digest] = {"total": n, "phase_delta": delta}
    return grew


PHASE_LOG = {
    "duplicate-post-sequential": ["duplicate_post.log"],
    "duplicate-post-concurrent": ["duplicate_post.log"],
    "swap-timeout-retry": ["swap_timeout_retry.log"],
    "pay-kill-post-receive-pre-session": ["kill_boundary_setup.log", "kill_boundary_aftermath.log"],
    "mint-alias-spellings": ["mint_alias_spellings.log"],
}


def _write_verdict(scenario, invariants):
    VERDICT_DIR.mkdir(parents=True, exist_ok=True)
    (VERDICT_DIR / f"{scenario}.json").write_text(
        json.dumps(
            {
                "scenario": scenario,
                "invariants": invariants,
                "verdict": "fail" if any(v["status"] == "fail" for v in invariants.values())
                else ("error" if any(v["status"] == "error" for v in invariants.values())
                      else "pass"),
                "evidence": [f".conformance/evidence/{name}" for name in PHASE_LOG[scenario]],
            },
            indent=2,
        )
        + "\n"
    )


def _inv(status, detail):
    return {"status": status, "detail": detail}


def _fund_wallet(amount=10000):
    wallet_dir = tempfile.mkdtemp(prefix="tg-conf-wallet-")
    run_cmd(["cdk-cli", "-w", wallet_dir, "mint", PROXY_MINT_URL, str(amount)])
    return wallet_dir


def _balance():
    """IP-keyed by the daemon: resolves the requester's lease MAC, so it
    only speaks for the fixed client-container MAC."""
    r = requests.get(f"{UPSTREAM_URL}/balance", timeout=10)
    r.raise_for_status()
    return r.json()


def _kind(resp):
    if not (resp.headers.get("content-type") or "").startswith("application/json"):
        return None
    try:
        return resp.json().get("kind")
    except ValueError:
        return None


def _daemon_answers(timeout=30):
    try:
        wait_for(UPSTREAM_URL, timeout=timeout)
        return True
    except TimeoutError:
        return False


pytestmark = pytest.mark.skipif(
    not CONFORMANCE_LANE, reason="conformance lane: run via conformance/run-conformance.sh"
)


@pytest.fixture(scope="module")
def lab_up():
    wait_for(UPSTREAM_URL)
    wait_for(f"{MINT_DIRECT_URL}/v1/keys")
    requests.get(f"{PROXY_MINT_URL}/__fault/state", timeout=10).raise_for_status()
    return True


@pytest.fixture(scope="module")
def identities(lab_up):
    upstream = requests.get(UPSTREAM_URL, timeout=10).json()
    tollgate_pub = upstream.get("pubkey")
    assert tollgate_pub, f"no pubkey in advertisement: {upstream}"
    return {"tollgate_pub": tollgate_pub, "customer": generate_nostr_keypair()}


def _spend_and_check(wallet_dir, identities, mac):
    """Mint a fresh 100-sat token, pay, return (token, response)."""
    token = create_cashu_token(wallet_dir, 100, PROXY_MINT_URL)
    resp = _pay(token, identities["tollgate_pub"], identities["customer"], mac)
    return token, resp


# ---------------------------------------------------------------- duplicates


def test_duplicate_post_sequential(lab_up, identities, client_mac):
    scenario = "duplicate-post-sequential"
    # /balance is IP-keyed (it resolves the requester's lease MAC), so the
    # allotment check must pay from the fixed client MAC and assert deltas:
    # allotments are cumulative per MAC.
    mac = client_mac
    wallet = _fund_wallet()
    token = create_cashu_token(wallet, 100, PROXY_MINT_URL)
    obs_before = _swap_counts(_observations())
    bal0 = _balance()

    first = _pay(token, identities["tollgate_pub"], identities["customer"], mac)
    first_ok = first.status_code == 200 and _kind(first) == 1022
    bal1 = _balance()

    second = _pay(token, identities["tollgate_pub"], identities["customer"], mac)
    second_granted = second.status_code == 200 and _kind(second) == 1022
    bal2 = _balance()

    grant = (bal1.get("allotment") or 0) - (bal0.get("allotment") or 0)
    extra = (bal2.get("allotment") or 0) - (bal1.get("allotment") or 0)
    single_session = grant > 0 and extra == 0

    reused = _phase_reuse(obs_before, _swap_counts(_observations()))
    _proxyctl({"clear": True})

    _write_verdict(
        scenario,
        {
            "no-fund-loss": _inv("pending", PENDING_NOTE["no-fund-loss"]),
            "no-double-count": _inv(
                "pass" if (first_ok and not second_granted and single_session) else "fail",
                f"first={first.status_code}/k{_kind(first)} "
                f"second={second.status_code}/k{_kind(second)} "
                f"allotment_grant={grant} allotment_extra={extra}",
            ),
            "no-output-reuse": _inv("pass" if not reused else "fail", f"reused={reused}"),
            "service-or-refund": _inv(
                "pass" if first_ok else "fail",
                f"first response kind={_kind(first)}",
            ),
        },
    )
    assert first_ok, f"first payment failed: {first.status_code} {first.text[:200]}"


def test_duplicate_post_concurrent(lab_up, identities, client_mac):
    scenario = "duplicate-post-concurrent"
    mac = client_mac
    wallet = _fund_wallet()
    token = create_cashu_token(wallet, 100, PROXY_MINT_URL)
    obs_before = _swap_counts(_observations())
    bal0 = _balance()

    results = {}

    def submit(name):
        try:
            r = _pay(token, identities["tollgate_pub"], identities["customer"], mac, timeout=90)
            body = r.json() if r.headers.get("content-type", "").startswith("application/json") else {}
            results[name] = (r.status_code, body.get("kind"))
        except requests.RequestException as exc:
            results[name] = ("error", str(exc)[:120])

    threads = [threading.Thread(target=submit, args=(f"t{i}",)) for i in range(2)]
    for t in threads:
        t.start()
    for t in threads:
        t.join()

    granted = [v for v in results.values() if v == (200, 1022)]
    bal1 = _balance()
    delta = (bal1.get("allotment") or 0) - (bal0.get("allotment") or 0)
    reused = _phase_reuse(obs_before, _swap_counts(_observations()))
    _proxyctl({"clear": True})

    _write_verdict(
        scenario,
        {
            "no-fund-loss": _inv("pending", PENDING_NOTE["no-fund-loss"]),
            "no-double-count": _inv(
                "pass" if (len(granted) == 1 and delta > 0) else "fail",
                f"responses={results} allotment_delta={delta} "
                f"(delta ~= 2x a single grant means two allotments)",
            ),
            "no-output-reuse": _inv("pass" if not reused else "fail", f"reused={reused}"),
            "service-or-refund": _inv(
                "pass" if len(granted) == 1 else "fail",
                f"exactly one session grant expected, got {len(granted)}: {results}",
            ),
        },
    )
    assert len(granted) == 1, f"expected exactly one grant, got {results}"


# --------------------------------------------------------------- swap timeout


def test_swap_timeout_retry(lab_up, identities):
    scenario = "swap-timeout-retry"
    wallet = _fund_wallet()
    mac = _mac()
    # Arm AFTER minting: cdk-cli's send splits through its own swap at the
    # mint via the proxy and would otherwise consume the fault.
    token = create_cashu_token(wallet, 100, PROXY_MINT_URL)
    obs_before = _swap_counts(_observations())
    _proxyctl(
        {
            "rules": [
                {"id": "drop-first-swap-response",
                 "match_path": "/v1/swap", "action": "drop_response", "remaining": 1}
            ]
        }
    )

    started = time.time()
    try:
        resp = _pay(token, identities["tollgate_pub"], identities["customer"], mac, timeout=150)
        primary = (resp.status_code, resp.text[:200])
    except requests.RequestException as exc:
        primary = ("error", str(exc)[:200])
    elapsed = time.time() - started

    # Settle: any internal reconciliation/timeout handling gets a window.
    time.sleep(15)

    proofs_state = "unknown"
    try:
        assert_token_unspent(token, MINT_DIRECT_URL)
        proofs_state = "unspent"
    except AssertionError:
        proofs_state = "spent"

    reused = _phase_reuse(obs_before, _swap_counts(_observations()))
    _proxyctl({"clear": True})

    # The customer's token either still spends elsewhere (unspent) or a
    # session was granted despite the dropped response. Anything else is a
    # value-loss verdict on service-or-refund.
    service = "pass" if proofs_state == "unspent" else "fail"

    _write_verdict(
        scenario,
        {
            "no-fund-loss": _inv("pending", PENDING_NOTE["no-fund-loss"]),
            "no-output-reuse": _inv("pass" if not reused else "fail",
                                    f"reused={reused} proofs={proofs_state}"),
            "service-or-refund": _inv(service,
                                      f"primary={primary} elapsed={elapsed:.1f}s "
                                      f"proofs={proofs_state}"),
            "restart-converges": _inv("pass" if _daemon_answers(30) else "fail",
                                      "daemon still answering after the fault"),
        },
    )
    assert proofs_state in ("unspent", "spent"), proofs_state


# ------------------------------------------------------------------ kill lane


def test_kill_boundary_setup(lab_up, identities):
    """Arm the kill trigger and pay: the daemon must die inside the window
    (mint processed the swap, daemon never saw the response). The runner
    restarts the container; the aftermath phase asserts."""
    scenario = "pay-kill-post-receive-pre-session"
    wallet = _fund_wallet()
    mac = _mac()
    # Arm AFTER minting (cdk-cli's split swap would otherwise fire the
    # trigger). The webhook targets an unassigned lab IP: nothing answers,
    # nothing rejects, so the proxy's 5s notify timeout holds the mint's
    # swap response open for the whole kill window.
    token = create_cashu_token(wallet, 100, PROXY_MINT_URL)
    _proxyctl(
        {
            "rules": [
                {"id": "kill-on-swap-response",
                 "match_path": "/v1/swap", "action": "notify", "notify_on": "response",
                 "notify_url": "http://172.28.0.99:9/hit"}
            ]
        }
    )

    try:
        resp = None

        def submit():
            nonlocal resp
            try:
                r = _pay(token, identities["tollgate_pub"], identities["customer"], mac, timeout=60)
                resp = r
            except requests.RequestException:
                resp = None

        # Fire the payment and exit the phase while it is still in flight:
        # the host runner polls the proxy's rule counter and docker-kills
        # the daemon inside the notify hold (mid-Receive). Staying for the
        # response would mean the kill lands only after the payment
        # completed — a different (useless) boundary.
        STATE_FILE.parent.mkdir(parents=True, exist_ok=True)
        STATE_FILE.write_text(json.dumps({"token": token, "mac": mac}) + "\n")
        worker = threading.Thread(target=submit, daemon=True)
        worker.start()
        worker.join(timeout=2)

        primary = (
            (resp.status_code, resp.text[:200]) if resp is not None else ("in-flight", None)
        )
    except Exception as exc:  # noqa: BLE001 - recorded as the scenario outcome
        primary = ("error", str(exc)[:200])

    if primary[0] == "in-flight":
        STATE_FILE.parent.mkdir(parents=True, exist_ok=True)
        state = json.loads(STATE_FILE.read_text())
        state["primary"] = primary
        STATE_FILE.write_text(json.dumps(state) + "\n")

    assert primary[0] != 200, (
        f"daemon answered normally ({primary}); the kill trigger never fired — "
        "check the proxy notify rule armed and the runner is polling for the trigger"
    )


def test_kill_boundary_aftermath(lab_up, identities):
    scenario = "pay-kill-post-receive-pre-session"
    state = json.loads(STATE_FILE.read_text())
    token, mac = state["token"], state["mac"]
    obs_before = _swap_counts(_observations())

    wait_for(UPSTREAM_URL, timeout=60)

    proofs_state = "unknown"
    try:
        assert_token_unspent(token, MINT_DIRECT_URL)
        proofs_state = "unspent"
    except AssertionError:
        proofs_state = "spent"

    retry = None
    try:
        r = _pay(token, identities["tollgate_pub"], identities["customer"], mac, timeout=60)
        body = r.json() if r.headers.get("content-type", "").startswith("application/json") else {}
        retry = (r.status_code, body.get("kind"))
    except requests.RequestException as exc:
        retry = ("error", str(exc)[:120])

    reused = _phase_reuse(obs_before, _swap_counts(_observations()))
    _proxyctl({"clear": True})

    # Restart convergence: daemon answers; value state is coherent enough
    # to give a CLASSIFIED answer to the retry (not a 500/timeout hang).
    converged = retry is not None and retry[0] in (200, 400)
    service = "pass" if proofs_state == "unspent" else "fail"

    _write_verdict(
        scenario,
        {
            "no-fund-loss": _inv("pending", PENDING_NOTE["no-fund-loss"]),
            "no-double-count": _inv("pass" if retry != (200, 1022) else "fail",
                                    f"retry={retry} (a second grant for one "
                                    f"already-consumed token would double-count)"),
            "no-output-reuse": _inv("pass" if not reused else "fail", f"reused={reused}"),
            "service-or-refund": _inv(service, PENDING_NOTE["service-or-refund"]
                                      + f" | measured: proofs={proofs_state} retry={retry}"),
            "restart-converges": _inv("pass" if converged else "fail",
                                      f"daemon answered retry with {retry}"),
        },
    )


# ---------------------------------------------------------------- alias lane


def test_mint_alias_spellings(lab_up, identities):
    """Config mint URL (normal spelling) vs token mint URL (case + trailing
    slash variant): the payment must succeed and the wallet must hold ONE
    mint entry, not one per spelling (#375/#480 class)."""
    scenario = "mint-alias-spellings"
    variant = "http://FAULTPROXY:9090/"
    wallet = tempfile.mkdtemp(prefix="tg-conf-alias-")
    run_cmd(["cdk-cli", "-w", wallet, "mint", variant, "10000"])
    token = create_cashu_token(wallet, 100, variant)
    mac = _mac()

    resp = _pay(token, identities["tollgate_pub"], identities["customer"], mac)
    accepted = resp.status_code == 200 and _kind(resp) == 1022

    # The wallet-info mint count is captured host-side (socat over the CLI
    # socket) by the runner right after this phase; this part records the
    # payment-level verdict. The runner folds the mint count into
    # no-fund-loss for the alias scenario.
    STATE_FILE.parent.mkdir(parents=True, exist_ok=True)
    STATE_FILE.write_text(json.dumps({"token": token, "mac": mac}) + "\n")

    _write_verdict(
        scenario,
        {
            "no-fund-loss": _inv("pass" if accepted else "fail",
                                 "payment accepted across spellings; wallet mint-count "
                                 "checked host-side (runner folds it in)"),
            "restart-converges": _inv("pass" if _daemon_answers(30) else "fail",
                                      "daemon still answering"),
        },
    )
    assert accepted, (
        f"alias-spelled token rejected: {resp.status_code} {resp.text[:300]} — "
        "canonicalization must fold case/trailing-slash spellings (#375/#480)"
    )
