"""test_clientd_autotopup.py — tollgate-clientd e2e against the real stack.

Runs scripts/tollgate-clientd.py (bind-mounted at /clientd) inside the
client container against the live TollGate backend and cdk-mintd:

  1. --list-offers parses the real advertisement
  2. --status --json / --waybar one-shot output
  3. --dry-run watches but never pays
  4. the daemon buys a 1-minute step, then renews at the threshold

Sessions live in the upstream container's memory and are keyed by the
client MAC, so a leftover session from another suite blocks renewal
observation. Run standalone or restart first:

    docker compose up -d mint upstream
    docker compose run --rm client -sv test_clientd_autotopup.py
    # or: docker compose restart upstream
"""

import json
import subprocess
import time

import pytest
import requests
from conftest import UPSTREAM_URL

CLIENTD = "/clientd/tollgate-clientd.py"
CLIENT_IP = "172.28.0.20"
LOG = "/tmp/clientd-daemon.log"


def run_clientd(*args, timeout=60):
    return subprocess.run(
        ["python3", CLIENTD, *args],
        capture_output=True, text=True, timeout=timeout, check=False,
    )


def usage():
    r = requests.get(f"{UPSTREAM_URL}/usage",
                     headers={"X-Real-Ip": CLIENT_IP}, timeout=10)
    return r.text.strip()


def current_allotment():
    u = usage()
    return 0 if u == "-1/-1" else int(u.split("/")[1])


def wait_allotment(predicate, timeout, what):
    deadline = time.time() + timeout
    last = -1
    while time.time() < deadline:
        last = current_allotment()
        if predicate(last):
            return last
        time.sleep(1)
    pytest.fail(f"timed out waiting for {what} (last allotment={last})")


@pytest.fixture(scope="module")
def clean_session(upstream_health):
    """Require no active session for the client MAC."""
    deadline = time.time() + 30
    while time.time() < deadline:
        if usage() == "-1/-1":
            return
        time.sleep(2)
    pytest.skip("client MAC already has an active session (leftover from "
                "another suite); run `docker compose restart upstream` first")


class TestReadOnlyModes:
    def test_list_offers_parses_real_advertisement(self, upstream_health, client_mac):
        r = run_clientd("--gateway", "upstream", "--mac", client_mac,
                        "--list-offers")
        assert r.returncode == 0, r.stderr
        assert "metric=milliseconds" in r.stdout
        assert "1 sats/step" in r.stdout
        assert "http://mint:8085" in r.stdout

    def test_status_json_no_session(self, upstream_health, clean_session, client_mac):
        r = run_clientd("--gateway", "upstream", "--mac", client_mac,
                        "--status", "--json")
        assert r.returncode == 0, r.stderr
        st = json.loads(r.stdout)
        assert st["session_active"] is False
        assert st["metric"] == "milliseconds"
        assert st["mint"] == "http://mint:8085"
        assert st["gateway"] == "upstream"

    def test_waybar_json_shape(self, upstream_health, clean_session, client_mac):
        r = run_clientd("--gateway", "upstream", "--mac", client_mac,
                        "--waybar")
        assert r.returncode == 0, r.stderr
        w = json.loads(r.stdout)
        assert {"text", "tooltip", "class", "percentage"} <= set(w)
        assert w["class"] == "critical"
        assert "no session" in w["text"]


def test_dry_run_never_pays(upstream_health, clean_session, client_mac):
    proc = subprocess.Popen(
        ["python3", CLIENTD, "--gateway", "upstream", "--mac", client_mac,
         "--dry-run", "--interval", "0.5"],
        stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    try:
        time.sleep(6)
    finally:
        proc.terminate()
        out, err = proc.communicate(timeout=10)
    assert usage() == "-1/-1", "dry-run must not create a session"
    assert "would top up" in out + err


def test_daemon_pays_and_renews(upstream_health, ecash_wallet, clean_session, client_mac):
    with open(LOG, "w") as log:
        proc = subprocess.Popen(
            ["python3", CLIENTD, "--gateway", "upstream", "--mac", client_mac,
             "--wallet", "cdk-cli", "--wallet-dir", ecash_wallet,
             "--steps", "1", "--renew-below", "45s", "--interval", "1"],
            stdout=log, stderr=log)
        try:
            first = wait_allotment(lambda a: a > 0, 60, "initial payment")
            second = wait_allotment(lambda a: a > first, 90,
                                    "threshold renewal")
        finally:
            proc.terminate()
            proc.wait(timeout=10)

    with open(LOG) as f:
        daemon_log = f.read()
    assert daemon_log.count("-> paid") >= 2, daemon_log
    assert second > first
    # the bought time persists after the daemon exits
    assert usage() != "-1/-1"
    print(f"allotment after initial payment: {first} ms; after renewal: {second} ms")
    print(daemon_log)
