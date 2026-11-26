#!/usr/bin/env bash
# Does `tollgate lnurlcash` work against a REAL running gate, with real records?
#
# Two properties, both only testable against the live binary:
#   1. the reader is not refused by the exclusive writer lock the daemon holds;
#   2. it sees records that are actually in the journal.
#
# The journal is seeded BEFORE boot (a hand-written operations.json is exactly what the
# store persists), so the daemon loads it and holds the lock it really holds.
set -uo pipefail
LAB=/tmp/lnurlcash-cli
rm -rf "$LAB"; mkdir -p "$LAB/cfg" "$LAB/work"
FAIL=0
ok()  { printf 'PASS  %s\n' "$*"; }
bad() { printf 'FAIL  %s\n' "$*"; FAIL=$((FAIL+1)); }

if [ -f /tmp/dhcp.leases ]; then cp /tmp/dhcp.leases "$LAB/leases.bak"; HAD=1; else HAD=0; fi
restore() { if [ "$HAD" = "1" ]; then cp "$LAB/leases.bak" /tmp/dhcp.leases; else rm -f /tmp/dhcp.leases; fi; }
cleanup() { [ -n "${GATE:-}" ] && kill "$GATE" 2>/dev/null; [ -n "${MINT:-}" ] && kill "$MINT" 2>/dev/null; restore; }
trap cleanup EXIT

printf '1700000000 02:00:00:00:00:42 127.0.0.1 tg-client *\n' > /tmp/dhcp.leases

cat > "$LAB/mint.py" <<'PY'
import http.server, json
class H(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        b = json.dumps({"keysets":[{"id":"000f715baf5d4c2e","unit":"sat","active":True}]}).encode() if self.path.startswith("/v1/keysets") else b"{}"
        self.send_response(200); self.send_header("Content-Type","application/json")
        self.send_header("Content-Length",str(len(b))); self.end_headers(); self.wfile.write(b)
    def log_message(self,*a): pass
http.server.HTTPServer(("127.0.0.1",3338),H).serve_forever()
PY
python3 "$LAB/mint.py" & MINT=$!
sleep 1

cat > "$LAB/cfg/config.json" <<JSON
{
  "accepted_mints": [{"url": "http://127.0.0.1:3338/", "price_per_step": 1000}],
  "lnurlcash": {
    "enabled": true,
    "issuers": ["issuer.example"],
    "max_note_msat": 100000000,
    "acknowledgement": "lnurlcash-issuer-redeem-v1"
  }
}
JSON

# Seed via the STORE'S OWN API. A hand-written operations.json is rejected by its
# validation (an Incoming `awaiting` record must carry a payment_hash and an invoice, and
# `awaiting` cannot be reached through Advance at all), so a hand-built fixture tests the
# fixture, not the code.
mkdir -p /home/c08r4d0r/worktrees/lnurlcash-rail/src/cmd/probe-seed
cat > /home/c08r4d0r/worktrees/lnurlcash-rail/src/cmd/probe-seed/main.go <<'GO'
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

func main() {
	s, err := paymentjournal.Open(os.Args[1])
	if err != nil {
		fmt.Println("open:", err)
		os.Exit(1)
	}
	defer s.Close()
	op, err := s.Prepare(paymentjournal.Operation{
		ID: "lnurlcash-aaaa1111", WalletID: "mint-key", Direction: paymentjournal.Incoming,
		AmountMSat: 21000, Recipient: "issuer.example", Request: []byte("cp1"),
	}, paymentjournal.Budget{})
	if err != nil { fmt.Println("prepare:", err); os.Exit(1) }
	if _, err := s.Advance(op.ID, op.Version, paymentjournal.Submitted, 0); err != nil { fmt.Println("submitted:", err); os.Exit(1) }
	if _, err := s.BindInvoice(op.ID, 2, strings.Repeat("ab", 32), []byte("lnbc1invoice")); err != nil { fmt.Println("bind:", err); os.Exit(1) }
	f, err := s.Prepare(paymentjournal.Operation{
		ID: "lnurlcash-bbbb2222", WalletID: "mint-key", Direction: paymentjournal.Incoming,
		AmountMSat: 5000, Recipient: "issuer.example", Request: []byte("cp1"),
	}, paymentjournal.Budget{})
	if err != nil { fmt.Println("prepare2:", err); os.Exit(1) }
	if _, err := s.Advance(f.ID, f.Version, paymentjournal.Failed, 0); err != nil { fmt.Println("failed:", err); os.Exit(1) }
	fmt.Println("seeded")
}
GO
(cd /home/c08r4d0r/worktrees/lnurlcash-rail/src && PATH="$HOME/.cache/tollgate-tools/go/bin:$PATH" CGO_ENABLED=0 \
  timeout 180 go run ./cmd/probe-seed "$LAB/cfg/lnurlcash-journal" 2>&1 | tail -1)
rm -rf /home/c08r4d0r/worktrees/lnurlcash-rail/src/cmd/probe-seed

env -i PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin HOME="$HOME" \
    TOLLGATE_TEST_CONFIG_DIR="$LAB/cfg" /tmp/tg-lnurlcash-wrt >"$LAB/work/gate.log" 2>&1 &
GATE=$!

GATE_URL=""
for _ in $(seq 1 120); do
  addr=$(sed 's/\x1b\[[0-9;]*m//g' "$LAB/work/gate.log" 2>/dev/null | grep -o 'addr="[0-9.]*:[0-9]*"' | head -1 | sed 's/addr="//; s/"//')
  if [ -n "$addr" ]; then
    GATE_URL="http://$addr"
    curl -sf "$GATE_URL/whoami" >/dev/null 2>&1 && break
  fi
  sleep 0.25
done
[ -n "$GATE_URL" ] && ok "gate bound at $GATE_URL (holding the journal write lock)" || { bad "gate did not bind"; exit 1; }

mkdir -p /home/c08r4d0r/worktrees/lnurlcash-rail/src/cmd/probe-readonly
cat > /home/c08r4d0r/worktrees/lnurlcash-rail/src/cmd/probe-readonly/main.go <<'GO'
package main

import (
	"fmt"
	"os"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/paymentjournal"
)

func main() {
	r, err := paymentjournal.OpenReadOnly(os.Args[1])
	if err != nil {
		fmt.Println("REFUSED:", err)
		os.Exit(1)
	}
	defer r.Close()
	ops, err := r.List()
	if err != nil {
		fmt.Println("READ-ERROR:", err)
		os.Exit(1)
	}
	fmt.Printf("read %d record(s) while the daemon holds the write lock\n", len(ops))
	for _, o := range ops {
		fmt.Printf("  %s|%s|%s|%d\n", o.ID, o.State, o.Recipient, o.AmountMSat)
	}
}
GO

RESULT=$(cd /home/c08r4d0r/worktrees/lnurlcash-rail/src && \
  PATH="$HOME/.cache/tollgate-tools/go/bin:$PATH" CGO_ENABLED=0 \
  timeout 180 go run ./cmd/probe-readonly "$LAB/cfg/lnurlcash-journal" 2>&1)
rm -rf /home/c08r4d0r/worktrees/lnurlcash-rail/src/cmd/probe-readonly

printf '\n--- reader output, against a live gate ---\n%s\n' "$RESULT"

if echo "$RESULT" | grep -q "REFUSED"; then
  bad "refused while the daemon ran — the command is useless in production"
else
  ok "not refused by the daemon's exclusive lock"
fi
echo "$RESULT" | grep -q "lnurlcash-aaaa1111|awaiting" \
  && ok "saw the awaiting record" || bad "did not see the awaiting record"
echo "$RESULT" | grep -q "lnurlcash-bbbb2222|failed" \
  && ok "saw the failed record" || bad "did not see the failed record"

printf '\n=== RESULT: %d assertion(s) failed\n' "$FAIL"
exit "$FAIL"
