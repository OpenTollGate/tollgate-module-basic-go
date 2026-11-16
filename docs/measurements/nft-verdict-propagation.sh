#!/bin/bash
# nft-verdict-propagation.sh — does an `accept` in one base chain stop a
# packet from reaching a later base chain on the same hook?
#
# This is the rig behind docs/measurements/nft-verdict-propagation-2026-10-28.md.
# It exists because the header of /etc/nftables.d/20-nds-enforce.nft asserted
# (from #283 onward) that fw4's inet fw4 forward at priority 0 "accepts
# forwarded traffic before the ip filter FORWARD chain (same priority) ever
# sees it". That claim decided a shipped firewall fragment, so it gets measured
# rather than argued.
#
# Topology: three network namespaces, one forwarding router between them.
#
#     ns_c (10.9.0.1) --- r1 [ nftvp_r ] r2 --- ns_s (10.9.1.1)
#                            ip_forward=1
#
# Every echo request AND every echo reply crosses nftvp_r's `forward` hook, so
# `ping -c 3` puts exactly 6 packets through every chain in these trials.
# Counters are read from `nft list ruleset` inside the router namespace, after
# the flow.
#
# Requirements: nftables, iproute2, and root. Run it as yourself — the script
# elevates each command it needs through `sudo -n`, so it never needs an
# interactive root shell. Nothing outside the three namespaces and the output
# directory is touched; the namespaces are deleted on entry and on exit.
#
# Usage: bash nft-verdict-propagation.sh [outdir]
#   Default outdir /tmp/nftvp-test. Prints one block per trial and keeps a
#   per-trial `nft list ruleset` dump (the counters) under <outdir>/rulesets/.

set -u
NS_C=nftvp_c; NS_R=nftvp_r; NS_S=nftvp_s
OUT="${1:-/tmp/nftvp-test}"
D="$OUT/rulesets"
mkdir -p "$D" || exit 1

# Elevate per command; never take an interactive root shell.
as_root() {
    if [ "$(id -u)" = 0 ]; then "$@"; else sudo -n "$@"; fi
}
R() { as_root ip netns exec "$NS_R" "$@"; }

cleanup() {
    local ns
    for ns in $NS_C $NS_R $NS_S; do as_root ip netns del "$ns" 2>/dev/null; done
}
trap cleanup EXIT Int Term

setup() {
    cleanup
    as_root ip netns add "$NS_C" || exit 1
    as_root ip netns add "$NS_R" || exit 1
    as_root ip netns add "$NS_S" || exit 1
    as_root ip link add c0 netns "$NS_C" type veth peer name r1 netns "$NS_R"
    as_root ip link add s0 netns "$NS_S" type veth peer name r2 netns "$NS_R"
    as_root ip -n "$NS_C" addr add 10.9.0.1/24 dev c0
    as_root ip -n "$NS_C" link set c0 up
    as_root ip -n "$NS_S" addr add 10.9.1.1/24 dev s0
    as_root ip -n "$NS_S" link set s0 up
    as_root ip -n "$NS_R" addr add 10.9.0.2/24 dev r1
    as_root ip -n "$NS_R" link set r1 up
    as_root ip -n "$NS_R" addr add 10.9.1.2/24 dev r2
    as_root ip -n "$NS_R" link set r2 up
    as_root ip -n "$NS_R" link set lo up
    # Forwarding, and rp_filter off: the point of the rig is the nft verdict
    # path, not the kernel's reverse-path policy.
    R sysctl -qw net.ipv4.ip_forward=1
    local k
    for k in all r1 r2; do R sysctl -qw "net.ipv4.conf.$k.rp_filter=0"; done
    as_root ip -n "$NS_C" route add default via 10.9.0.2
    as_root ip -n "$NS_S" route add default via 10.9.1.2
    R nft flush ruleset
}

# trial <letter> <title> <ruleset-file>
trial() {
    local letter="$1" title="$2" file="$3"
    setup
    if ! R nft -f "$file" 2>"$OUT/err-$letter.txt"; then
        printf '===== TRIAL %s: %s\n      RULESET FAILED TO LOAD — see %s\n\n' \
            "$letter" "$title" "$OUT/err-$letter.txt"
        return
    fi
    as_root ip netns exec "$NS_C" ping -c 3 -q -W 1 10.9.1.1 >/dev/null 2>&1
    R nft -nn list ruleset > "$D/trial-$letter.txt" 2>&1
    printf '===== TRIAL %s: %s\n' "$letter" "$title"
    grep -E '^\s*(table|chain)|counter packets' "$D/trial-$letter.txt" \
        | sed 's/^[[:space:]]*/      /'
    printf '\n'
}

w() { cat > "$D/$1"; }

# --- A/B: two base chains at the SAME priority, accept in one ----------------
# The interesting variable is registration order: which chain the kernel
# happens to run first is UNSPECIFIED, so both orders are measured. Either way
# 6/6 means the accept did not starve the chain behind it.
w A.nft <<'EOF'
table inet ta {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter accept
  }
}
table ip tb {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter
  }
}
EOF

w B.nft <<'EOF'
table ip tb {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter
  }
}
table inet ta {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter accept
  }
}
EOF

# --- C/D: earlier priority, accept vs drop ----------------------------------
# Same shape, only the verdict differs. This pair separates "an earlier chain
# accepted" from "an earlier chain starved the later one".
w C.nft <<'EOF'
table ip tc {
  chain fw {
    type filter hook forward priority -1; policy accept;
    counter accept
  }
}
table ip td {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter
  }
}
EOF

w D.nft <<'EOF'
table ip tc {
  chain fw {
    type filter hook forward priority -1; policy accept;
    counter drop
  }
}
table ip td {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter
  }
}
EOF

# --- E/F: within ONE chain, does accept end the chain? ----------------------
# Bare `accept`/`reject` (no match) so the first rule always decides.
w E.nft <<'EOF'
table ip te {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter accept
    counter
  }
}
EOF

w F.nft <<'EOF'
table ip tf {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter reject with icmp type port-unreachable
    counter
  }
}
EOF

# --- G: the shipped fragment's shape, unmarked flow -------------------------
# A priority -1 chain ending in the same terminal reject 20-nds-enforce.nft
# uses, ahead of a priority-0 counter chain. This is the shape that produces
# "the later chain reads 0 packets" — and it is the terminal verdict doing it.
w G.nft <<'EOF'
table ip tg {
  chain fw {
    type filter hook forward priority -1; policy accept;
    counter reject with icmp type port-unreachable
  }
}
table ip tz {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter
  }
}
EOF

# --- H: same shape, nothing marked ------------------------------------------
# The -1 chain's mark-accept cannot match (no earlier chain sets the mark), so
# the reject takes the flow. Contrast with I.
w H.nft <<'EOF'
table ip ti {
  chain fw {
    type filter hook forward priority -1; policy accept;
    meta mark 0x20000 counter accept
    counter reject with icmp type port-unreachable
  }
}
table ip tj {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter
  }
}
EOF

# --- I: the full NDS analogue ------------------------------------------------
# -150 sets the mark on the client's packets (NDS mangle PREROUTING), -1
# accepts them, a priority-0 chain counts behind. If an accept were terminal
# across base chains, the -1 accept rule would show 3 packets and tl would
# show 0. Requests are marked and accepted; replies are unmarked and hit the
# reject, so the -1 chain's reject also reads 3.
w I.nft <<'EOF'
table ip tk {
  chain pre {
    type filter hook prerouting priority -150; policy accept;
    ip saddr 10.9.0.1 counter meta mark set 0x20000
  }
  chain fw {
    type filter hook forward priority -1; policy accept;
    meta mark 0x20000 counter accept
    counter reject with icmp type port-unreachable
  }
}
table ip tl {
  chain fw {
    type filter hook forward priority 0; policy accept;
    counter
  }
}
EOF

echo "nft: $(nft --version 2>/dev/null)"
echo "kernel: $(uname -r)"
echo "3 echo requests per trial = 6 packets through every forward chain"
echo

trial A "inet table accept @prio 0 registered FIRST; ip counter @prio 0 behind" "$D/A.nft"
trial B "ip counter @prio 0 registered FIRST; inet accept @prio 0 behind"      "$D/B.nft"
trial C "accept @prio -1; counter-only chain @prio 0 behind"                    "$D/C.nft"
trial D "drop   @prio -1; counter-only chain @prio 0 behind"                    "$D/D.nft"
trial E "ONE chain: 'accept' rule, then a counting rule"                        "$D/E.nft"
trial F "ONE chain: 'reject' rule, then a counting rule"                        "$D/F.nft"
trial G "shipped shape: reject @-1 ahead of counter @0, UNMARKED flow"          "$D/G.nft"
trial H "shipped shape: nothing marked, mark-accept cannot match"               "$D/H.nft"
trial I "NDS analogue: mark set @-150, accept @-1, counter @0 behind"           "$D/I.nft"

echo "per-trial ruleset dumps with counters: $D/"
