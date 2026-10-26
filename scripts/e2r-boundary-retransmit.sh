#!/bin/bash
# E2-R — the retransmission/double-count experiment at the tunnel-side boundary.
#
# Answers EXPERIMENT-CONSULTANT.md §1 "Q2 (LOAD-BEARING)":
#   H2a (immunity):  bytes at the tunnel-side boundary == bytes the application wrote, within [0,+R]
#                    where R is the retransmit fraction; a naive per-path sum diverges by the same R.
#   H2b (magnitude): R = (boundary count - plaintext payload) / plaintext payload
#                    is <= 0.02 (2%) healthy, and <= 0.10 (10%) with 5% loss on one leg.
#
# PASS (advance, per EXP-CONSULTANT.md):
#   H2a: D > 0 in the loss cells and D ~ 0 in the no-loss cells, and R bounded as H2b.
#   H2b: no-loss R <= 0.02 ; 5%-loss R <= 0.10.
# FAIL (advance):
#   R > 0.02 with NO loss injection (the meter counts something that is not the datagram stream), OR
#   R > 0.10 with 5% injected loss (retransmit over-charge is material), OR
#   D ~ 0 under loss WHILE R > 0 (asymmetric: per-leg counters miss what the boundary sees).
#
# Variables (all overridable — config-as-code, no hardcoded thresholds in the logic):
: "${E2R_NS_CLI:=e2r_cli}"
: "${E2R_NS_SRV:=e2r_srv}"
: "${E2R_PORT:=9999}"
: "${E2R_NET_A:=10.10.1.0/24}"   # leg A subnet
: "${E2R_NET_B:=10.20.2.0/24}"   # leg B subnet
: "${E2R_LOSS_PCT:=5}"           # injected loss in the "loss" cell
: "${E2R_LOSS_LEG:=a}"           # WHICH leg gets the loss: a = the low-RTT (carrying) leg.
                                 # MEASURED: with 2ms vs 60ms the scheduler puts ~99.9% of payload on
                                 # the low-RTT leg, so injecting loss on the slow leg exercises NOTHING
                                 # and yields an uninformative R. Default is therefore the carrying leg;
                                 # the harness gates on the lossy leg's measured share and reports
                                 # INVALID rather than PASS when it carried a negligible share.
: "${E2R_BYTES_MB:=900}"         # payload target per run, MiB
: "${E2R_RUNS:=5}"               # n per cell, per the preregistration
: "${E2R_PACE_MBIT:=300}"        # leg pacing so mid-flight sampling is possible
: "${E2R_FAST_DELAY_MS:=2}"      # leg A netem delay
: "${E2R_SLOW_DELAY_MS:=60}"     # leg B netem delay
: "${E2R_OUT:=/home/c08r4d0r/reports/mptcp-loop/e2r-raw.txt}"
set -u
mkdir -p "$(dirname "$E2R_OUT")"
: > "$E2R_OUT"     # create early; under `sudo` $HOME is /root, so this path must be absolute

CLI=$E2R_NS_CLI; SRV=$E2R_NS_SRV; PORT=$E2R_PORT
A_CLI=$(echo "$E2R_NET_A" | cut -d. -f1-3).1 ; A_SRV=$(echo "$E2R_NET_A" | cut -d. -f1-3).2
B_CLI=$(echo "$E2R_NET_B" | cut -d. -f1-3).1 ; B_SRV=$(echo "$E2R_NET_B" | cut -d. -f1-3).2

cleanup(){ ip netns del $CLI 2>/dev/null; ip netns del $SRV 2>/dev/null; }
cleanup
ip netns add $CLI; ip netns add $SRV
for ns in $CLI $SRV; do
  ip netns exec $ns ip link set lo up
  ip netns exec $ns sysctl -qw net.mptcp.enabled=1
  ip netns exec $ns sysctl -qw net.ipv4.conf.all.rp_filter=0
  ip netns exec $ns sysctl -qw net.ipv4.conf.default.rp_filter=0
  ip netns exec $ns ip mptcp limits set subflow 2 add_addr_accepted 2
done
ip link add a0 type veth peer name a1; ip link add b0 type veth peer name b1
ip link set a0 netns $CLI; ip link set a1 netns $SRV
ip link set b0 netns $CLI; ip link set b1 netns $SRV
ip netns exec $CLI ip addr add "$A_CLI/24" dev a0
ip netns exec $SRV ip addr add "$A_SRV/24" dev a1
ip netns exec $CLI ip addr add "$B_CLI/24" dev b0
ip netns exec $SRV ip addr add "$B_SRV/24" dev b1
for i in a0 b0; do ip netns exec $CLI ip link set $i up; done
for i in a1 b1; do ip netns exec $SRV ip link set $i up; done
ip netns exec $CLI tc qdisc add dev a0 root handle 1: tbf rate ${E2R_PACE_MBIT}mbit burst 1m latency 40ms
ip netns exec $CLI tc qdisc add dev b0 root handle 1: tbf rate ${E2R_PACE_MBIT}mbit burst 1m latency 40ms
ip netns exec $CLI tc qdisc add dev a0 parent 1:1 handle 10: netem delay ${E2R_FAST_DELAY_MS}ms
ip netns exec $CLI tc qdisc add dev b0 parent 1:1 handle 10: netem delay ${E2R_SLOW_DELAY_MS}ms
# `signal subflow` on BOTH ends — a `subflow`-only endpoint never sends ADD_ADDR and no
# second subflow can ever form. This is the trap the independence protocol exists to catch.
ip netns exec $CLI ip mptcp endpoint add $A_CLI dev a0 signal subflow
ip netns exec $CLI ip mptcp endpoint add $B_CLI dev b0 signal subflow
ip netns exec $SRV ip mptcp endpoint add $A_SRV dev a1 signal subflow
ip netns exec $SRV ip mptcp endpoint add $B_SRV dev b1 signal subflow

# Measurement-only counters at the tunnel-side boundary. NOTE: `policy accept` here is deliberate
# and safe ONLY because nothing in this probe enforces — it exists to MEASURE. Any enforcing chain
# must be `policy drop` with a terminal drop (see the ADR, D3); an accept-policy metered chain is a
# free tunnel. Do not copy this shape into anything that bills.
ip netns exec $SRV nft add table inet m
ip netns exec $SRV nft add chain inet m tin  '{ type filter hook input  priority 0; policy accept; }'
ip netns exec $SRV nft add chain inet m tout '{ type filter hook output priority 0; policy accept; }'

cat > /tmp/e2r_srv.py <<'PYEOF'
import socket, sys
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM, 262)
s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("0.0.0.0", int(sys.argv[1]))); s.listen(8)
sys.stderr.write("srv listening\n"); sys.stderr.flush()
c, a = s.accept(); tot = 0
while True:
    d = c.recv(1 << 16)
    if not d: break
    tot += len(d)
sys.stderr.write("srv recv %d bytes\n" % tot); sys.stderr.flush()
PYEOF
cat > /tmp/e2r_cli.py <<'PYEOF'
import socket, sys, time
port = int(sys.argv[1]); dst = sys.argv[2]; bind = sys.argv[3]; target = int(sys.argv[4]) << 20
s = socket.socket(socket.AF_INET, socket.SOCK_STREAM, 262)
s.bind((bind, 0)); s.connect((dst, port))
buf = b"x" * (1 << 16); sent = 0; t0 = time.time()
# A plain TCP echo of exactly N bytes with no loss is the GOLDEN calibration:
# there C_tunnel == N exactly, which is the only place an exact equality is available.
while sent < target:
    s.sendall(buf); sent += len(buf)
s.shutdown(socket.SHUT_WR)
# drain so the server's final ACK/close is accounted
try:
    while s.recv(1 << 16): pass
except Exception: pass
sys.stderr.write("cli sent %d bytes\n" % sent); sys.stderr.flush()
PYEOF

run_cell() {   # $1 = label, $2 = loss pct on leg B (0 = none), $3 = run index
  local label=$1 loss=$2 idx=$3
  # reset counters
  ip netns exec $SRV nft flush chain inet m tin
  ip netns exec $SRV nft flush chain inet m tout
  ip netns exec $SRV nft add rule inet m tin counter
  ip netns exec $SRV nft add rule inet m tout counter
  ip netns exec $SRV nft add rule inet m tin iifname a1 counter
  ip netns exec $SRV nft add rule inet m tin iifname b1 counter
  # Inject the impairment on the leg that actually CARRIES payload (E2R_LOSS_LEG), not on the
  # idle one. Getting this backwards produces a run that looks like a clean no-loss result.
  if [ "$loss" != "0" ]; then
    if [ "$E2R_LOSS_LEG" = "a" ]; then
      ip netns exec $CLI tc qdisc change dev a0 parent 1:1 handle 10: netem delay ${E2R_FAST_DELAY_MS}ms loss ${loss}%
    else
      ip netns exec $CLI tc qdisc change dev b0 parent 1:1 handle 10: netem delay ${E2R_SLOW_DELAY_MS}ms loss ${loss}%
    fi
  else
    ip netns exec $CLI tc qdisc change dev a0 parent 1:1 handle 10: netem delay ${E2R_FAST_DELAY_MS}ms
    ip netns exec $CLI tc qdisc change dev b0 parent 1:1 handle 10: netem delay ${E2R_SLOW_DELAY_MS}ms
  fi
  ip netns exec $SRV python3 /tmp/e2r_srv.py $PORT 2>/tmp/e2r_srv.log &
  local SPID=$!; sleep 1
  ip netns exec $CLI python3 /tmp/e2r_cli.py $PORT $A_SRV $A_CLI $E2R_BYTES_MB 2>/tmp/e2r_cli.log &
  local CPID=$!
  wait $CPID 2>/dev/null; wait $SPID 2>/dev/null
  local cli_sent srv_recv
  cli_sent=$(sed -n 's/^cli sent \([0-9]*\) bytes$/\1/p' /tmp/e2r_cli.log)
  srv_recv=$(sed -n 's/^srv recv \([0-9]*\) bytes$/\1/p' /tmp/e2r_srv.log)
  local total legA legB
  total=$(ip netns exec $SRV nft list chain inet m tin | awk '/counter packets/ && !/iifname/ {print $NF; exit}')
  legA=$(ip netns exec $SRV nft list chain inet m tin | awk '/iifname "a1"/ {print $NF; exit}')
  legB=$(ip netns exec $SRV nft list chain inet m tin | awk '/iifname "b1"/ {print $NF; exit}')
  local rmp; rmp=$(awk -v t="${total:-0}" -v p="${cli_sent:-1}" 'BEGIN{ if(p>0) printf "%.6f",(t-p)/p; else print "NA"}')
  local dmp; dmp=$(awk -v a="${legA:-0}" -v b="${legB:-0}" -v t="${total:-1}" 'BEGIN{ if(t>0) printf "%.6f",(a+b-t)/t; else print "NA"}')
  # VALIDITY GATE for the loss cell: if the lossy leg carried a negligible share, the run did not
  # exercise the impairment, so R is uninformative for H2b. Report INVALID, never PASS.
  local share_b valid; share_b=$(awk -v b="${legB:-0}" -v t="${total:-1}" 'BEGIN{ if(t>0) printf "%.4f", b/t; else print "NA"}')
  if [ "$loss" != "0" ] && awk -v s="${share_b:-0}" 'BEGIN{ exit !(s < 0.01) }'; then valid="INVALID_no_traffic_on_lossy_leg"; else valid="ok"; fi
  echo "CELL=$label RUN=$idx loss=${loss}% cli_sent=${cli_sent:-NA} srv_recv=${srv_recv:-NA} boundary_total=${total:-NA} legA=${legA:-NA} legB=${legB:-NA} shareB=$share_b R=$rmp D=$dmp validity=$valid"
  echo "CELL=$label RUN=$idx loss=${loss}% cli_sent=${cli_sent:-NA} srv_recv=${srv_recv:-NA} boundary_total=${total:-NA} legA=${legA:-NA} legB=${legB:-NA} shareB=$share_b R=$rmp D=$dmp validity=$valid" >> "$E2R_OUT"
}

echo "=== P0-P6 INDEPENDENCE PRECONDITION ===" | tee "$E2R_OUT"
echo "-- ip route get over ALL peer addresses (must differ in BOTH egress iface AND gateway):" | tee -a "$E2R_OUT"
ip netns exec $CLI ip route get $A_SRV | tee -a "$E2R_OUT"
ip netns exec $CLI ip route get $B_SRV | tee -a "$E2R_OUT"
echo "-- ARBITER: MPJoinSynRx non-zero at the responder (a subflow can only form if this moves):" | tee -a "$E2R_OUT"

echo | tee -a "$E2R_OUT"
echo "=== GOLDEN calibration + cells (n=$E2R_RUNS) ===" | tee -a "$E2R_OUT"
for i in $(seq 1 "$E2R_RUNS"); do run_cell golden 0 "$i"; done
for i in $(seq 1 "$E2R_RUNS"); do run_cell noloss 0 "$i"; done
for i in $(seq 1 "$E2R_RUNS"); do run_cell loss "$E2R_LOSS_PCT" "$i"; done

echo | tee -a "$E2R_OUT"
echo "=== subflow arbiter + DSS consistency (zeros on the DSS counters are the proof the map stayed consistent) ===" | tee -a "$E2R_OUT"
ip netns exec $SRV nstat -az 2>/dev/null | grep -E 'MPJoinSynRx|MPJoinAckRx|MPJoinNoTokenFound|DSSNotMatching|DSSCorruption|InfiniteMap|MPTCPRetrans|DuplicateData' | tee -a "$E2R_OUT"
echo "-- per-leg split of the last transfer:" | tee -a "$E2R_OUT"
ip netns exec $SRV nft list chain inet m tin | tee -a "$E2R_OUT"
cleanup
echo | tee -a "$E2R_OUT"
echo "RAW RECORD: $E2R_OUT" | tee -a "$E2R_OUT"
