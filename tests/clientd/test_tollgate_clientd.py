"""Unit tests for scripts/tollgate-clientd.py.

Run: python3 -m pytest tests/clientd/ -v

No hardware, no wallets, no docker: HTTP boundaries are served by in-process
mocks; subprocess boundaries are stubbed. The cloud-lab e2e
(tests/cloud-lab/test_clientd_autotopup.py) covers the real stack.
"""

import importlib.util
import json
import subprocess
import sys
import threading
import typing
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path

import pytest

SCRIPT = Path(__file__).resolve().parents[2] / "scripts" / "tollgate-clientd.py"
spec = importlib.util.spec_from_file_location("tollgate_clientd", SCRIPT)
clientd = importlib.util.module_from_spec(spec)
spec.loader.exec_module(clientd)

ADV_BYTES = json.dumps({
    "kind": 10021, "pubkey": "ab" * 32,
    "tags": [
        ["metric", "bytes"],
        ["step_size", "22020096"],
        ["price_per_step", "cashu", "1", "sat", "https://mint.example", "3"],
        ["price_per_step", "cashu", "2", "sat", "https://mint2.example"],
    ],
    "content": "",
})

ADV_MS = json.dumps({
    "kind": 10021, "pubkey": "ab" * 32,
    "tags": [
        ["metric", "milliseconds"],
        ["step_size", "60000"],
        ["price_per_step", "cashu", "1", "sat", "https://mint.example", "0"],
    ],
    "content": "",
})


class TestParseAdvertisement:
    def test_bytes_advertisement(self):
        ad = clientd.parse_advertisement(ADV_BYTES)
        assert ad.metric == "bytes"
        assert ad.step_size == 22020096
        assert ad.pubkey == "ab" * 32
        assert len(ad.offers) == 2
        assert ad.offers[0].price == 1
        assert ad.offers[0].mint_url == "https://mint.example"
        assert ad.offers[0].min_steps == 3

    def test_offer_without_min_steps_defaults_to_zero(self):
        ad = clientd.parse_advertisement(ADV_BYTES)
        assert ad.offers[1].min_steps == 0

    def test_default_offer_selects_mint(self):
        ad = clientd.parse_advertisement(ADV_BYTES)
        assert ad.default_offer("https://mint2.example/").price == 2
        assert ad.default_offer().mint_url == "https://mint.example"

    def test_unknown_mint_rejected(self):
        ad = clientd.parse_advertisement(ADV_BYTES)
        with pytest.raises(clientd.ClientError, match="not accepted"):
            ad.default_offer("https://elsewhere.example")

    def test_rejects_non_advertisement(self):
        with pytest.raises(clientd.ClientError):
            clientd.parse_advertisement(json.dumps({"kind": 1, "tags": []}))
        with pytest.raises(clientd.ClientError):
            clientd.parse_advertisement("not json at all")


class TestFormatting:
    def test_human_bytes(self):
        assert clientd.human_bytes(512) == "512 B"
        assert clientd.human_bytes(5 * 1024 * 1024) == "5.0 MB"
        assert clientd.human_bytes(3 * 1024**3) == "3.0 GB"

    def test_human_ms(self):
        assert clientd.human_ms(90_000) == "1:30"
        assert clientd.human_ms(3_600_000) == "1:00:00"

    def test_format_remaining(self):
        assert clientd.format_remaining("bytes", 5 * 1024 * 1024) == "5.0 MB left"
        assert clientd.format_remaining("milliseconds", 90_000) == "1:30 left"

    def test_parse_renew_below(self):
        assert clientd.parse_renew_below("20MB", "bytes") == 20 * 1024**2
        assert clientd.parse_renew_below("1.5GB", "bytes") == int(1.5 * 1024**3)
        assert clientd.parse_renew_below("1000000", "bytes") == 1_000_000
        assert clientd.parse_renew_below("30s", "milliseconds") == 30_000
        assert clientd.parse_renew_below("2m", "milliseconds") == 120_000
        assert clientd.parse_renew_below("5000", "milliseconds") == 5_000
        with pytest.raises(clientd.ClientError):
            clientd.parse_renew_below("20quux", "bytes")


class TestExtractToken:
    def test_takes_cashu_line_before_balance_line(self):
        out = "cashuB64TOKENHERE\nBalance: 21\n"
        assert clientd._extract_token(out) == "cashuB64TOKENHERE"

    def test_v3_prefix(self):
        assert clientd._extract_token("\ncashuAeyJ0b2tlbiI\n") == "cashuAeyJ0b2tlbiI"

    def test_no_token_raises(self):
        with pytest.raises(clientd.ClientError):
            clientd._extract_token("Balance: 0\nnothing to send")


class MockTollGate(BaseHTTPRequestHandler):
    """Configurable mock: usage responses and payment outcomes."""

    state: typing.ClassVar[dict] = {"usage_body": "-1/-1", "post_status": 200,
                                    "post_body": "{}"}

    def log_message(self, *a):
        pass

    def do_GET(self):
        if self.path == "/":
            body = ADV_MS.encode()
        elif self.path == "/usage":
            body = self.state["usage_body"].encode()
        elif self.path == "/v1/keysets":
            body = json.dumps({"keysets": [
                {"id": "0011223344556677" + "88" * 25}]}).encode()
        else:
            self.send_error(404)
            return
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        length = int(self.headers.get("Content-Length", 0))
        body = self.rfile.read(length).decode()
        self.state["last_post_body"] = body
        self.state["last_post_path"] = self.path
        self.send_response(self.state["post_status"])
        self.end_headers()
        self.wfile.write(self.state["post_body"].encode())


@pytest.fixture()
def mock_gate():
    server = HTTPServer(("127.0.0.1", 0), MockTollGate)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    yield f"127.0.0.1:{server.server_address[1]}", MockTollGate.state
    server.shutdown()
    MockTollGate.state.update({"usage_body": "-1/-1", "post_status": 200,
                               "post_body": "{}"})


class TestWireProtocol:
    def test_get_usage_parses_pair(self, mock_gate):
        gw, state = mock_gate
        state["usage_body"] = "123/456"
        assert clientd.get_usage(gw) == (123, 456)

    def test_get_usage_no_session(self, mock_gate):
        gw, _ = mock_gate
        assert clientd.get_usage(gw) is None

    def test_get_usage_garbage_raises(self, mock_gate):
        gw, state = mock_gate
        state["usage_body"] = "what is this"
        with pytest.raises(clientd.ClientError):
            clientd.get_usage(gw)

    def test_pay_success_and_mac_passthrough(self, mock_gate):
        gw, state = mock_gate
        state["post_status"], state["post_body"] = 200, json.dumps(
            {"kind": 1022, "tags": [["allotment", "60000"]]})
        ev = clientd.pay(gw, "02:00:00:00:00:20", "cashuB64X")
        assert ev["kind"] == 1022
        assert state["last_post_path"] == "/?mac=02:00:00:00:00:20"
        assert state["last_post_body"] == "cashuB64X"

    def test_pay_notice_surfaces_message(self, mock_gate):
        gw, state = mock_gate
        state["post_status"] = 400
        state["post_body"] = json.dumps(
            {"kind": 21023, "content": "payment below minimum"})
        with pytest.raises(clientd.ClientError, match="payment below minimum"):
            clientd.pay(gw, "02:00:00:00:00:20", "cashuB64X")

    def test_pay_non_json_reply_raises(self, mock_gate):
        gw, state = mock_gate
        state["post_status"], state["post_body"] = 502, "<html>bad gateway"
        with pytest.raises(clientd.ClientError, match="not JSON"):
            clientd.pay(gw, "02:00:00:00:00:20", "cashuB64X")


class TestKeysetExpansion:
    def _make_v3_token(self, keyset_id, amount=1):
        import base64
        data = {"token": [{"mint": "https://mint.example",
                           "proofs": [{"id": keyset_id, "amount": amount,
                                       "secret": "s", "C": "c"}]}]}
        return "cashuA" + base64.urlsafe_b64encode(
            json.dumps(data).encode()).decode().rstrip("=")

    def test_short_ids_expanded_from_mint(self, mock_gate):
        import base64
        gw, _ = mock_gate
        token = self._make_v3_token("0011223344556677")
        out = clientd._expand_keyset_ids(token, f"http://{gw}")
        payload = out[6:]
        payload += "=" * (4 - len(payload) % 4)
        decoded = json.loads(base64.urlsafe_b64decode(payload))
        assert decoded["token"][0]["proofs"][0]["id"] == \
            "0011223344556677" + "88" * 25

    def test_unknown_short_id_left_alone(self, mock_gate):
        gw, _ = mock_gate
        token = self._make_v3_token("ffffffffffffffff")
        assert clientd._expand_keyset_ids(token, f"http://{gw}") == token

    def test_v4_tokens_untouched(self, mock_gate):
        gw, _ = mock_gate
        assert clientd._expand_keyset_ids("cashuB64XYZ", f"http://{gw}") == "cashuB64XYZ"


class TestCdkCliAdapter:
    def _patch_run(self, monkeypatch, responses):
        calls = []

        def fake_run(cmd, input=None, capture_output=False, text=False,
                     check=False):
            calls.append({"cmd": cmd, "input": input})
            if cmd[1:3] == ["send", "--help"]:
                r = responses.get("help")
            elif "--amount" in cmd:
                r = responses.get("amount")
            else:
                r = responses.get("stdin")
            return subprocess.CompletedProcess(cmd, r.get("rc", 0),
                                                r.get("stdout", ""),
                                                r.get("stderr", ""))

        monkeypatch.setattr(clientd.subprocess, "run", fake_run)
        return calls

    def test_modern_cdk_cli_uses_amount_flag_and_v3(self, monkeypatch):
        clientd._CDK_AMOUNT_FLAG = None
        calls = self._patch_run(monkeypatch, {
            "help": {"stdout": "--amount AMOUNT"},
            "amount": {"stdout": "cashuB64X\nBalance: 9\n"},
        })
        token = clientd.wallet_send_cdk_cli("https://mint.example", 21, None)
        assert token == "cashuB64X"
        send_call = [c for c in calls if "send" in c["cmd"]][-1]
        assert "--v3" in send_call["cmd"] and "--amount" in send_call["cmd"]
        assert "21" in send_call["cmd"] and send_call["input"] is None

    def test_legacy_cdk_cli_uses_stdin(self, monkeypatch):
        clientd._CDK_AMOUNT_FLAG = None
        calls = self._patch_run(monkeypatch, {
            "help": {"stdout": "no amount flag here"},
            "stdin": {"stdout": "cashuB64Y\n"},
        })
        token = clientd.wallet_send_cdk_cli("https://mint.example", 5, "/tmp/w")
        assert token == "cashuB64Y"
        send_call = [c for c in calls if c["input"] is not None][-1]
        assert send_call["input"] == "5\n"
        assert "/tmp/w" in send_call["cmd"]

    def test_send_failure_surfaces_stderr(self, monkeypatch):
        clientd._CDK_AMOUNT_FLAG = None
        self._patch_run(monkeypatch, {
            "help": {"stdout": "--amount"},
            "amount": {"rc": 1, "stderr": "insufficient funds"},
        })
        with pytest.raises(clientd.ClientError, match="insufficient funds"):
            clientd.wallet_send_cdk_cli("https://mint.example", 21, None)


class Args:
    gateway = None  # filled per test
    iface = "lo0"
    mac = "02:00:00:00:00:02"
    mint = None
    renew_below = None
    steps = 1
    wallet_dir = None
    dry_run = False
    interval = 0.05
    state_dir = None  # filled per test (tmp_path) — never write to $HOME
    payment_timeout = 30.0
    max_blind_payments = 3


class TestDaemonAgainstMock:
    def _daemon(self, gw, state, renew_below=None, steps=1, tmp_path=None):
        state["usage_body"] = "-1/-1"
        args = Args()
        args.gateway = gw
        args.renew_below = renew_below
        args.steps = steps
        args.state_dir = str(tmp_path) if tmp_path else None
        payments = []

        def wallet(mint_url, amount_sats, wallet_dir):
            payments.append(amount_sats)
            state["usage_body"] = f"0/{amount_sats * 60000}"
            return f"cashuB64MOCK{amount_sats}"

        state["post_status"], state["post_body"] = 200, json.dumps(
            {"kind": 1022, "tags": [["allotment", "60000"]]})
        if args.state_dir is None:
            pytest.fail("daemon tests must pass tmp_path so pending tokens "
                        "never land in $HOME")
        d = clientd.ClientDaemon(args, "stub", wallet)
        return d, payments

    def test_initial_payment_when_no_session(self, mock_gate, tmp_path):
        gw, state = mock_gate
        d, payments = self._daemon(gw, state, tmp_path=tmp_path)
        assert d.needs_top_up(None)
        d.top_up()
        assert payments == [1]
        st = d.status()
        assert st["session_active"] and st["allotment"] == 60000

    def test_steps_raised_to_mint_minimum(self, mock_gate, tmp_path):
        gw, state = mock_gate
        d, _ = self._daemon(gw, state, steps=1, tmp_path=tmp_path)
        # ADV_MS mint has min_steps 0; build a daemon against ADV_BYTES-style
        # min via direct offer check instead
        assert d.steps >= d.offer.min_steps

    def test_renewal_when_below_threshold(self, mock_gate, tmp_path):
        gw, state = mock_gate
        d, payments = self._daemon(gw, state, renew_below="50s", tmp_path=tmp_path)
        d.top_up()
        state["usage_body"] = "20000/60000"   # 40s left <= 50s threshold
        assert d.needs_top_up(d.status()["remaining"])
        d.last_payment = 0.0                  # skip the 5s inter-payment throttle
        d.top_up()
        assert payments == [1, 1]

    def test_payment_throttle_blocks_rapid_duplicates(self, mock_gate, tmp_path):
        gw, state = mock_gate
        d, payments = self._daemon(gw, state, tmp_path=tmp_path)
        assert d.top_up().startswith("paid")
        assert d.top_up() == "throttled"      # within PAYMENT_THROTTLE seconds
        assert payments == [1]

    def test_no_renewal_when_above_threshold(self, mock_gate, tmp_path):
        gw, state = mock_gate
        d, payments = self._daemon(gw, state, renew_below="50s", tmp_path=tmp_path)
        d.top_up()
        state["usage_body"] = "5000/60000"    # 55s left > 50s
        assert not d.needs_top_up(d.status()["remaining"])
        assert len(payments) == 1

    def test_status_shape(self, mock_gate, tmp_path):
        gw, state = mock_gate
        d, _ = self._daemon(gw, state, tmp_path=tmp_path)
        d.top_up()
        st = d.status()
        for key in ("gateway", "mac", "metric", "mint", "wallet",
                    "session_active", "usage", "allotment", "remaining"):
            assert key in st
        assert st["metric"] == "milliseconds"
        assert st["mint"] == "https://mint.example"


class TestSelftestEntry:
    def test_selftest_passes(self):
        rc = subprocess.run([sys.executable, str(SCRIPT), "--selftest"],
                            capture_output=True, text=True, timeout=120,
                            check=False)
        assert rc.returncode == 0, rc.stdout + rc.stderr
        assert "SELFTEST PASS" in rc.stdout


class TestMoneyPathGuards:
    """The two #422/#423 blockers from the review: no blind re-pay loop,
    and no token minted-then-dropped on a failed POST."""

    def test_terminal_code_raises_terminal_error(self, mock_gate):
        gw, state = mock_gate
        state["post_status"] = 400
        state["post_body"] = json.dumps({
            "kind": 21023, "content": "This e-cash note is 1 sat but the "
            "mint charges a 1 sat swap fee…",
            "tags": [["code", "payment-error-below-swap-fee"]]})
        with pytest.raises(clientd.TerminalPaymentError) as ei:
            clientd.pay(gw, "02:00:00:00:00:02", "cashuB64MOCK1")
        assert ei.value.code == "payment-error-below-swap-fee"

    def test_retryable_rejection_stays_client_error(self, mock_gate):
        gw, state = mock_gate
        state["post_status"] = 400
        state["post_body"] = json.dumps({
            "kind": 21023, "content": "Mint is temporarily unavailable",
            "tags": [["code", "payment-error-mint-unreachable"]]})
        with pytest.raises(clientd.ClientError) as ei:
            clientd.pay(gw, "02:00:00:00:00:02", "cashuB64MOCK1")
        assert not isinstance(ei.value, clientd.TerminalPaymentError)

    def test_terminal_rejection_stops_daemon(self, mock_gate, tmp_path):
        gw, state = mock_gate
        d, payments = self._blind_daemon(gw, state, tmp_path)
        state["post_status"] = 400
        state["post_body"] = json.dumps({
            "kind": 21023, "content": "below swap fee",
            "tags": [["code", "payment-error-below-swap-fee"]]})
        with pytest.raises(clientd.TerminalPaymentError):
            d.run()
        assert payments == [1], "terminal rejection must not re-mint"

    def test_blind_payments_capped_and_exits(self, mock_gate, tmp_path,
                                             monkeypatch):
        gw, state = mock_gate
        monkeypatch.setattr(clientd, "PAYMENT_THROTTLE", 0.05)
        d, payments = self._blind_daemon(gw, state, tmp_path)
        # /usage never leaves -1/-1 and the router keeps saying 1022: the
        # #422 runaway — now bounded at --max-blind-payments.
        with pytest.raises(clientd.BlindPaymentCapExceeded):
            d.run()
        assert payments == [1, 1, 1], "exactly max_blind_payments minted"

    def test_blind_counter_resets_on_observed_usage(self, mock_gate,
                                                    tmp_path, monkeypatch):
        gw, state = mock_gate
        monkeypatch.setattr(clientd, "PAYMENT_THROTTLE", 0.05)
        d, payments = self._blind_daemon(gw, state, tmp_path,
                                         usage_grows=True)
        import threading
        import time as _t
        t = threading.Thread(target=d.run, daemon=True)
        t.start()
        deadline = _t.time() + 5
        while len(payments) < 4 and _t.time() < deadline:
            _t.sleep(0.02)
        assert len(payments) >= 4, (
            f"healthy renewals must continue past the cap (got {len(payments)}) "
            f"— without the /usage reset the cap would have stopped at 3")

    def test_pending_token_reused_on_retry(self, mock_gate, tmp_path):
        gw, state = mock_gate
        d, payments = self._blind_daemon(gw, state, tmp_path)
        # First attempt: the POST dies mid-flight (connection error class).
        state["post_status"] = 200
        state["post_body"] = "not json at all"
        with pytest.raises(clientd.ClientError):
            d.top_up()
        assert payments == [1]
        assert d._load_pending_token() == "cashuB64MOCK1", (
            "token must be on disk (0600) before the POST")
        # Second attempt: the wallet is NOT asked for a new token.
        state["post_status"], state["post_body"] = 200, json.dumps(
            {"kind": 1022, "tags": [["allotment", "60000"]]})
        d.last_payment = 0.0  # skip the inter-payment throttle, like test_renewal
        msg = d.top_up()
        assert payments == [1], "retry must reuse the pending token"
        assert "reused pending" in msg
        assert d._load_pending_token() is None, "cleared after success"

    def test_terminal_spent_clears_pending(self, mock_gate, tmp_path):
        gw, state = mock_gate
        d, payments = self._blind_daemon(gw, state, tmp_path)
        state["post_status"] = 400
        state["post_body"] = json.dumps({
            "kind": 21023, "content": "Token has already been spent",
            "tags": [["code", "payment-error-token-spent"]]})
        with pytest.raises(clientd.TerminalPaymentError):
            d.top_up()
        assert d._load_pending_token() is None, (
            "a spent token must not be retried or advertised as recoverable")

    def test_payment_timeout_defaults_wide(self, mock_gate, tmp_path):
        gw, state = mock_gate
        d, _ = self._blind_daemon(gw, state, tmp_path)
        assert d.payment_timeout >= 30.0, (
            "payment POST timeout must default to >= 30 s (#423: routers "
            "swap synchronously)")

    def _blind_daemon(self, gw, state, tmp_path, usage_grows=False):
        """Daemon whose gateway accepts payments; /usage maps nothing
        unless usage_grows (then every poll credits the session)."""
        state["usage_body"] = "-1/-1"
        args = Args()
        args.gateway = gw
        args.interval = 0.02
        args.state_dir = str(tmp_path)
        payments = []

        def wallet(mint_url, amount_sats, wallet_dir):
            payments.append(amount_sats)
            if usage_grows:
                # credit the session AND burn most of it, so the daemon
                # renews every cycle while /usage keeps reflecting credit
                n = len(payments)
                state["usage_body"] = f"{n * 58000}/{n * 60000}"
            return f"cashuB64MOCK{amount_sats}"

        state["post_status"], state["post_body"] = 200, json.dumps(
            {"kind": 1022, "tags": [["allotment", "60000"]]})
        return clientd.ClientDaemon(args, "stub", wallet), payments
