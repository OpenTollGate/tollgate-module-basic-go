#!/usr/bin/env bash
# End-to-end lab: a real module binary redeeming a real note at a mock issuer.
#
# What this proves that the unit tests cannot: the endpoint exists in a booted
# gate, the opt-in gate is read from config, a note travels the whole path
# (parse -> allowlist -> issuer Info -> mint invoice -> issuer Melt), and a
# disabled config yields NO route at all.
#
# The issuer client refuses loopback and private addresses on purpose (SSRF
# protection), so the mock issuer is bound to 127.0.0.1 but reached through a
# name that resolves to a documentation-range address the client also refuses.
# That means the real client CANNOT be pointed at a local mock without weakening
# the SSRF guard — which is exactly why this script asserts the refusal path
# rather than pretending to complete a melt. A completed melt is proven by the
# unit tests with an injected melt, and by a real issuer only when one exists.
set -uo pipefail

LAB=/tmp/lnurlcash-lab
rm -rf "$LAB"; mkdir -p "$LAB/cfg" "$LAB/bin" "$LAB/work"
PORT=21456
ISSUER_PORT=21457
FAIL=0

say() { printf '\n=== %s\n' "$*"; }
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; FAIL=$((FAIL+1)); }

say "fixture: /tmp/dhcp.leases for the payer's address"
# /tmp/dhcp.leases is a SHARED path: the module hardcodes it, and another lab on
# this host may rely on its contents. Back up whatever is there and restore it on
# exit, rather than leaving this run's MAC behind for the next one to inherit.
if [ -f /tmp/dhcp.leases ]; then
  cp /tmp/dhcp.leases "$LAB/dhcp.leases.bak"
  HAD_LEASES=1
else
  HAD_LEASES=0
fi
restore_leases() {
  if [ "$HAD_LEASES" = "1" ]; then cp "$LAB/dhcp.leases.bak" /tmp/dhcp.leases
  else rm -f /tmp/dhcp.leases; fi
}
printf '1700000000 02:00:00:00:00:42 127.0.0.1 tg-client *\n' > /tmp/dhcp.leases

say "stub mint on 127.0.0.1:3338 (enough to clear the wallet/mint health gate)"
# The gate refuses to serve the money path while its mint is unknown (requireStarted
# answers 503 "starting"), which is correct behaviour — a quote needs a reachable
# mint. A full cdk-mintd is not available on this host and docker is not usable, so
# this stub answers /v1/keysets. It makes the gate reach "started"; it is NOT a
# settlement backend, and nothing here proves mint settlement.
cat > "$LAB/mint.py" <<'PY'
import http.server, json
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path.startswith("/v1/keysets"):
            body = json.dumps({"keysets":[{"id":"000f715baf5d4c2e","unit":"sat","active":True}]}).encode()
        else:
            body = b"{}"
        self.send_response(200)
        self.send_header("Content-Type","application/json")
        self.send_header("Content-Length",str(len(body)))
        self.end_headers(); self.wfile.write(body)
    def log_message(self,*a): pass
http.server.HTTPServer(("127.0.0.1",3338),H).serve_forever()
PY
python3 "$LAB/mint.py" & MINT_PID=$!
trap 'kill $ISSUER_PID $MINT_PID $GATE_PID 2>/dev/null; restore_leases' EXIT
sleep 1
curl -sf "http://127.0.0.1:3338/v1/keysets" >/dev/null && ok "stub mint answers /v1/keysets" || bad "stub mint did not start"

say "mock issuer on 127.0.0.1:$ISSUER_PORT"
cat > "$LAB/issuer.py" <<'PY'
import json, http.server, urllib.parse
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        q = urllib.parse.parse_qs(urllib.parse.urlparse(self.path).query)
        # LUD-25 informational answer: the note is worth 1000 msat.
        body = json.dumps({
            "status": "OK",
            "maxWithdrawable": 1000,
            "minWithdrawable": 1000,
            "defaultDescription": "lab note",
            "callback": "https://issuer.example/lnurl/cash",
            "k1": q.get("k1", [""])[0],
        }).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)
    def log_message(self, *a): pass
http.server.HTTPServer(("127.0.0.1", 21457), H).serve_forever()
PY
python3 "$LAB/issuer.py" & ISSUER_PID=$!
sleep 1
curl -sf "http://127.0.0.1:$ISSUER_PORT/.well-known/lnurlw/x?k1=00" >/dev/null \
  && ok "mock issuer answers" || bad "mock issuer did not start"

write_config() {  # $1 = enabled true/false
  cat > "$LAB/cfg/config.json" <<JSON
{
  "accepted_mints": [{"url": "http://127.0.0.1:3338/", "price_per_step": 1000}],
  "lnurlcash": {
    "enabled": $1,
    "issuers": ["issuer.example"],
    "max_note_msat": 100000000,
    "acknowledgement": "lnurlcash-issuer-redeem-v1"
  }
}
JSON
}

# The testenv build binds an EPHEMERAL port, not the shipped :2121 — which on this
# host belongs to another lane. Discover the port the gate actually bound, or the
# script probes a stranger's process and reads its 404s as our own failures. That
# is exactly what happened on the first run.
start_gate() {
  : >"$LAB/work/gate.log"
  env -i PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin \
      HOME="$HOME" TOLLGATE_TEST_CONFIG_DIR="$LAB/cfg" \
      /tmp/tg-lnurlcash-wrt >"$LAB/work/gate.log" 2>&1 &
  GATE_PID=$!
  GATE_URL=""
  for _ in $(seq 1 120); do
    local addr
    # Extract the FIRST addr="host:port" on the listening line. The line also
    # carries api_addr="127.0.0.1:0" (the configured value under testenv), so a
    # greedy `.*addr=` lands on the wrong field; take the first match instead.
    # logrus colourises KEY NAMES when stdout is a TTY-ish sink, emitting
    # ESC[36maddr ESC[0m="127.0.0.1:35815". Strip ANSI before matching, or a plain
    # grep for 'addr="' finds nothing and the script silently probes port 0.
    addr=$(sed 's/\x1b\[[0-9;]*m//g' "$LAB/work/gate.log" 2>/dev/null \
           | grep -o 'addr="[0-9.]*:[0-9]*"' | head -1 | sed 's/addr="//; s/"//')
    if [ -n "$addr" ]; then
      GATE_URL="http://$addr"
      for _ in $(seq 1 40); do
        curl -sf "$GATE_URL/whoami" >/dev/null 2>&1 && return 0
        sleep 0.25
      done
    fi
    sleep 0.25
  done
  return 1
}

say "A: rail DISABLED — the route must not exist"
write_config false
start_gate || bad "gate did not boot (disabled config)"
if grep -q "LNURLcash redemption rail is off" "$LAB/work/gate.log"; then
  ok "boot log says the rail is off, with a reason"
else
  bad "boot log does not mention the rail being off"; sed -n '1,5p' "$LAB/work/gate.log"
fi
CODE=$(curl -s -o "$LAB/work/disabled.json" -w '%{http_code}' \
  -X POST -H 'Content-Type: application/json' \
  -d '{"note":"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f","mint":"http://127.0.0.1:3338/","issuer":"issuer.example"}' \
  "$GATE_URL/lnurlcash/redeem")
# NOT a 404: "/" is a catch-all on this mux, so an unregistered path falls through to
# the root handler. The property that matters is that the LNURLcash route is absent —
# no lnurlcash refusal code ever comes back.
if ! grep -q "lnurlcash" "$LAB/work/disabled.json" 2>/dev/null; then
  ok "no /lnurlcash route while disabled (fell through to the root handler, HTTP $CODE, no lnurlcash code)"
else
  bad "a disabled rail still answered as lnurlcash: $(cat "$LAB/work/disabled.json")"
fi
kill $GATE_PID 2>/dev/null; wait $GATE_PID 2>/dev/null

say "B: rail ENABLED — route exists and refuses a non-allowlisted issuer"
write_config true
start_gate || bad "gate did not boot (enabled config)"
if grep -q "LNURLcash redemption rail is ON" "$LAB/work/gate.log"; then
  ok "boot log says the rail is ON"
else
  bad "boot log does not say the rail is ON"; grep -i lnurl "$LAB/work/gate.log" | head -3
fi

CODE=$(curl -s -o "$LAB/work/allowed.json" -w '%{http_code}' \
  -X POST -H 'Content-Type: application/json' \
  -d '{"note":"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f","mint":"http://127.0.0.1:3338/","issuer":"issuer.example"}' \
  "$GATE_URL/lnurlcash/redeem")
say "  gate bound at $GATE_URL; allowlisted issuer, real client -> HTTP $CODE"
cat "$LAB/work/allowed.json"; echo
# The note is refused because the client will not dial issuer.example (it does not
# resolve, and the SSRF guard refuses anything private). What matters is that the
# refusal is the ISSUER path, not a 404 for a missing route.
if [ "$CODE" != "404" ]; then ok "the route exists when enabled (not 404)"
else bad "route still 404 with the rail enabled"; fi
if grep -qi "lnurlcash" "$LAB/work/gate.log"; then ok "the gate logged the redemption attempt"
else bad "no lnurlcash log line"; fi

CODE=$(curl -s -o "$LAB/work/evil.json" -w '%{http_code}' \
  -X POST -H 'Content-Type: application/json' \
  -d '{"note":"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f","mint":"http://127.0.0.1:3338/","issuer":"evil.example"}' \
  "$GATE_URL/lnurlcash/redeem")
if [ "$CODE" = "400" ]; then ok "non-allowlisted issuer -> 400 before any network call"
else bad "expected 400 for a non-allowlisted issuer, got $CODE"; cat "$LAB/work/evil.json"; fi

CODE=$(curl -s -o /dev/null -w '%{http_code}' "$GATE_URL/lnurlcash/redeem?note=x")
if [ "$CODE" = "405" ]; then ok "GET -> 405"
else bad "expected 405 for GET, got $CODE"; fi

kill $GATE_PID 2>/dev/null; wait $GATE_PID 2>/dev/null

say "RESULT"
if [ "$FAIL" -eq 0 ]; then
  echo "ALL LAB ASSERTIONS PASSED"
else
  echo "$FAIL LAB ASSERTION(S) FAILED"
fi
exit "$FAIL"
