"""The LNURLcash opt-in lane: the gate, a real mint, and a fake issuer.

What this lane is FOR — and what it is not.

It is not a unit test of the redeemer (those live in
src/merchant/lnurlcash/redeem_test.go and need no container). It is the check
that the pieces the unit tests stub are wired correctly in a running binary:
the route appears only when the flag is on, the gate talks to a real issuer over
a real socket, and the grant decision is made by a real mint rather than by the
issuer's reply.

The fake issuer cannot make the mint say PAID. That is the property that matters:
in the `ok` mode the issuer accepts the callback and never pays anything, so a
gate that granted on the issuer's word would grant here, and a correct gate will
not.

Prerequisites: the `lnurlcash` compose profile is up (`run-lnurlcash-lane.sh`).
"""

import json
import os
import time

import pytest
import requests

GATE = os.environ.get("LNURLCASH_GATE_URL", "http://upstream-lnurlcash:2121")
ISSUER = os.environ.get("LNURLCASH_ISSUER_URL", "http://fake-issuer:8099")

HEALTH_TIMEOUT = int(os.environ.get("LNURLCASH_HEALTH_TIMEOUT", "90"))


def wait_for(url, timeout=HEALTH_TIMEOUT, interval=2):
    deadline = time.time() + timeout
    last = None
    while time.time() < deadline:
        try:
            requests.get(url, timeout=3)
            return True
        except Exception as exc:  # noqa: BLE001 - any failure means "not up yet"
            last = exc
            time.sleep(interval)
    raise AssertionError(f"{url} did not come up within {timeout}s (last: {last})")


def issuer_mode(mode, reset=True):
    r = requests.post(f"{ISSUER}/_control", json={"mode": mode, "reset": reset}, timeout=5)
    r.raise_for_status()
    return r.json()


@pytest.fixture(scope="session", autouse=True)
def lane_up():
    wait_for(f"{ISSUER}/_state", timeout=30)
    # The gate is up when its Cashu path answers; /lnurlcash/pay is under test.
    wait_for(f"{GATE}/", timeout=HEALTH_TIMEOUT)


def note_for(secret):
    """A note is just the issuer's withdraw URL. A customer would present this
    (bech32-encoded in the wild); the plain URL is the same thing decoded."""
    return f"{ISSUER}/withdraw?k1={secret}"


def pay(note, mac="02:00:00:00:00:01"):
    return requests.post(
        f"{GATE}/lnurlcash/pay",
        data=note,
        headers={"Content-Type": "text/plain"},
        timeout=60,
    )


# --- the capability contract ------------------------------------------------

def test_route_exists_because_the_lane_enabled_it():
    """Sanity: the flag is on for this container, so the route must exist. If
    this fails, every negative assertion below is vacuous."""
    r = requests.post(f"{GATE}/lnurlcash/pay", data="", timeout=10)
    assert r.status_code != 404, (
        "the lnurlcash profile config should enable the method; a 404 here means "
        "the lane is testing a disabled gate and all results below are meaningless"
    )


def test_get_is_not_a_payment():
    r = requests.get(f"{GATE}/lnurlcash/pay", timeout=10)
    assert r.status_code == 405


# --- the safety boundary ----------------------------------------------------

def test_issuer_accepts_but_mint_unpaid_does_not_grant():
    """The central claim. The fake issuer answers OK and pays nothing, so the
    gate's own mint never reports the quote paid. Access must not be granted."""
    issuer_mode("ok")
    r = pay(note_for("boundary-1"))
    assert r.status_code in (200, 202), r.text
    body = r.json()
    assert body.get("granted") is False, f"GRANTED on the issuer's word: {body}"
    assert body.get("access_granted") is False, body
    assert body.get("quote"), "the gate must expose the quote so the portal can poll"
    # And the issuer must show it was asked exactly once.
    state = requests.get(f"{ISSUER}/_state", timeout=5).json()
    assert len(state["callbacks"]) == 1, state


def test_issuer_refusal_grants_nothing_and_does_not_reach_the_mint():
    issuer_mode("spent")
    r = pay(note_for("refused-1"))
    assert r.status_code == 402, r.text
    body = r.json()
    assert body.get("granted") is False
    state = requests.get(f"{ISSUER}/_state", timeout=5).json()
    assert state["callbacks"] == [], "a refused note must not reach the callback"
    assert "quote" not in body or not body.get("quote"), (
        "no invoice may be requested for a note the issuer already refuses"
    )


def test_issuer_offline_is_a_refusal_not_a_retry():
    issuer_mode("offline")
    r = pay(note_for("offline-1"))
    assert r.status_code == 402, r.text
    assert r.json().get("granted") is False


def test_second_presentation_does_not_call_the_issuer_again():
    issuer_mode("ok")
    note = note_for("duplicate-1")
    first = pay(note)
    assert first.status_code in (200, 202), first.text
    after_first = requests.get(f"{ISSUER}/_state", timeout=5).json()
    n_first = len(after_first["callbacks"])

    # A broken issuer would now pay a second time. It must not be consulted.
    issuer_mode("forever", reset=False)
    second = pay(note)
    assert second.status_code in (200, 202, 409), second.text
    after_second = requests.get(f"{ISSUER}/_state", timeout=5).json()
    assert len(after_second["callbacks"]) == n_first, (
        "the note was presented to the issuer twice: the journal did not decide"
    )


def test_the_note_is_never_written_to_the_gate_log():
    """The note is a bearer secret. The gate runs with debug logging in this lane,
    so the secret must not appear in it.

    The log is read from the HOST side by the driver, not from inside this
    container: `podman logs` is not available here (this is the test runner, not
    the engine), and shelling out to it produced a FileNotFoundError that looked
    like a leak-check failure. The driver writes the gate's log to
    LNURLCASH_GATE_LOG; when that is unset the check is skipped loudly rather than
    passing vacuously.
    """
    log_path = os.environ.get("LNURLCASH_GATE_LOG", "")
    if not log_path:
        pytest.skip("LNURLCASH_GATE_LOG is not set: the log-leak check needs the host-side gate log")

    issuer_mode("ok")
    secret = "logcheck-secret-do-not-print-me"
    pay(note_for(secret))

    with open(log_path, "r", errors="replace") as fh:
        out = fh.read()
    assert out, "the gate log is empty: the assertions below would be vacuous"
    assert "LNURLcash" in out, "the gate log has no LNURLcash lines: wrong log, or the route never ran"
    assert secret not in out, "the note's k1 appears in the gate's log"
    assert "logcheck" not in out, "a fragment of the note's k1 appears in the gate's log"
