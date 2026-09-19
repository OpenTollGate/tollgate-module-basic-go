#!/bin/sh
# run.sh — gonuts baseline measurement (WALLET-BASELINE / research T1a).
#
#   ./run.sh env        reproducibility facts -> raw/env.txt (+ raw/router-*)
#   ./run.sh build      F1/F2/F3/F4/F10/E1 on the build host -> raw/build-*
#   ./run.sh idle       F5/F6/F7/F8 at idle (5 min) -> raw/idle-*
#   ./run.sh startup    P1 startup-to-wallet-ready (restarts the service)
#   ./run.sh payments   S1/S2/S3, P2/P3/P4, peak F5/F7. INJECTS A TEST MINT
#                       into accepted_mints and restores it at the end.
#                       Read phases/04-payments.sh before running it.
#   ./run.sh faults     INJ-1..INJ-3 against the test mint
#   ./run.sh analyze    recompute every number quoted in 03-baseline/gonuts.md
#
# Env knobs: ROUTER_IP (default 192.168.1.1), ROUTER_PW (default the lab
# default "password"), OUT (default ./raw), CYCLES, IDLE_SECS.
set -eu
HERE=$(cd "$(dirname "$0")" && pwd)

PHASE="${1:-}"
case "$PHASE" in
    env)      SCRIPT=phases/00-env.sh ;;
    build)    SCRIPT=phases/01-build.sh ;;
    idle)     SCRIPT=phases/02-idle.sh ;;
    startup)  SCRIPT=phases/03-startup.sh ;;
    payments) SCRIPT=phases/04-payments.sh ;;
    faults)   SCRIPT=phases/05-faults.sh ;;
    analyze)  SCRIPT=phases/06-analyze.sh ;;
    *)
        sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'
        exit 2
        ;;
esac

exec sh "$HERE/$SCRIPT"
