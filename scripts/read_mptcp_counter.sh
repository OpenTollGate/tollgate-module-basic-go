#!/bin/sh
# read_mptcp_counter.sh -- print one MPTCP kernel counter by name.
#
# Why this exists: the useful MPTCP counters live in /proc/net/netstat as two
# positional lines (a header line and a values line under "MPTcpExt:"). Reading
# them by name with the obvious pipeline fails on OpenWrt because BusyBox has no
# `paste`:
#
#   awk '/MPTcpExt:/{print}' /proc/net/netstat | tr ' ' '\n' | paste - -   # NO
#
# This walks the two lines with POSIX shell only, so it works on BusyBox.
#
#   MPJoinSynRx   subflow JOINs RECEIVED (0 = no subflow ever formed)
#   MPJoinAckRx   JOIN handshakes COMPLETED
#   MPCurrEstab   currently established MPTCP connections
#
# Usage: read_mptcp_counter.sh [COUNTER]
#   With no argument, prints all MPTcpExt counters as "name value" pairs.

set -e

NETSTAT=/proc/net/netstat
WANT="$1"

if [ ! -r "$NETSTAT" ]; then
    echo "read_mptcp_counter: cannot read $NETSTAT" >&2
    exit 1
fi

# Pull the two MPTcpExt lines: first is the names, second is the values.
HEADER=$(grep '^MPTcpExt:' "$NETSTAT" | head -1)
VALUES=$(grep '^MPTcpExt:' "$NETSTAT" | sed -n '2p')

if [ -z "$HEADER" ] || [ -z "$VALUES" ]; then
    echo "read_mptcp_counter: no MPTcpExt counters (MPTCP not compiled in?)" >&2
    exit 1
fi

# Strip the "MPTcpExt:" tag; the remaining fields line up positionally.
set -- $HEADER
shift
NAMES="$*"
set -- $VALUES
shift
VALS="$*"

# Walk both lists in lockstep using a here-doc-free pairing: count fields, then
# index with `cut`. `cut` is present in BusyBox.
i=1
for name in $NAMES; do
    val=$(echo "$VALS" | cut -d' ' -f"$i")
    if [ -z "$WANT" ]; then
        echo "$name $val"
    elif [ "$name" = "$WANT" ]; then
        echo "$val"
        exit 0
    fi
    i=$((i + 1))
done

if [ -n "$WANT" ]; then
    echo "read_mptcp_counter: unknown counter '$WANT'" >&2
    exit 1
fi
