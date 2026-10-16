#!/usr/bin/env python3
"""
granularity.py -- the break-even table for "granularity, price, and the
relaxation lattice", computed from the LIVE router's own configuration.

Every number here comes from this script. Source constants are read from the
live device (tollgate-A04E, tollgate-wrt v0.6.0-rc4-gcf7cad3):

    metric        = "bytes"
    step_size     = 22020096          (= 21 MiB exactly, 21 * 2**20)
    price_per_step= 1 sat             (all 7 configured mints)
    min_balance   = 64 sat
    min_payout    = 128 sat

CORRECTION THIS SCRIPT EXISTS TO RECORD: the original spike's simulator read
`step_size` as MILLISECONDS (22020096 ms = 6.117 h) and reported "1 satoshi buys
6.12 hours of internet". The live router's metric is BYTES, so the shipped
meaning is "1 satoshi buys 21 MiB of data". Both readings are supported by the
module (`tollgate config set metric milliseconds` switches metric), but the
SHIPPED DEFAULT is bytes. The error did not change the overhead percentages
(which are a ratio of sats to sats) but it did change what a purchase BUYS, and
therefore how large a realistic purchase is in sats -- which is what the channel
cost amortises against. Output is deterministic; no network, no chain.
"""
import json
import os

# ---- constants, sourced from the live router ------------------------------
STEP_SIZE_BYTES = 22020096          # 21 MiB exactly
PRICE_PER_STEP_SAT = 1
STEP_SIZE_IF_MS = 22020096          # the ms reading, for the erratum table

CHANNEL_SETUP_CLOSE_SAT = 716       # measured in the rig at 2 sat/vB
SATS_PER_STEP = PRICE_PER_STEP_SAT

MIB = 1024 ** 2
GIB = 1024 ** 3
TIB = 1024 ** 4


def fmt_data(b):
    if b >= TIB:
        return f"{b/TIB:.2f} TiB"
    if b >= GIB:
        return f"{b/GIB:.1f} GiB"
    return f"{b/MIB:.0f} MiB"


def main():
    out = {"source": "live tollgate-A04E config.json", "metric": "bytes",
           "step_size_bytes": STEP_SIZE_BYTES, "price_per_step_sat": PRICE_PER_STEP_SAT,
           "channel_setup_close_sat": CHANNEL_SETUP_CLOSE_SAT}

    print("=" * 76)
    print("granularity.py -- break-even for an on-chain channel at TollGate prices")
    print("=" * 76)
    print(f"  live config      : metric=bytes, step_size={STEP_SIZE_BYTES} B "
          f"({STEP_SIZE_BYTES/MIB:.0f} MiB = 21 MiB exactly)")
    print(f"  price            : {PRICE_PER_STEP_SAT} sat per step")
    print(f"  => 1 satoshi buys: {fmt_data(STEP_SIZE_BYTES)} of data")
    print()

    # ---- THE ERRATUM -----------------------------------------------------
    print("T0  ERRATUM -- the two readings of step_size=22020096")
    print("-" * 76)
    h = STEP_SIZE_IF_MS / 1000 / 3600
    print(f"  as MILLISECONDS  : {STEP_SIZE_IF_MS} ms = {h:.3f} h  "
          f"(what the original sim assumed: '1 sat = 6.12 hours')")
    print(f"  as BYTES (LIVE)  : {STEP_SIZE_IF_MS} B = 21 MiB  "
          f"(what the shipped config means)")
    print(f"  the value {STEP_SIZE_IF_MS} = 21 * 2**20 exactly, which is a clean")
    print("  MiB quantity -- evidence the bytes reading is the intended default.")
    print()

    # ---- what a realistic purchase is, in sats ---------------------------
    print("T1  A realistic purchase, in sats (the denominator for amortisation)")
    print("-" * 76)
    print("      data bought |      = sats | (= fiat @ $100k/BTC)")
    for label, gib in [("1 month light (50 GB)", 50), ("1 month heavy (300 GB)", 300),
                       ("1 year heavy (3.6 TB)", 3600), ("1 year very heavy (12 TB)", 12000)]:
        byt = gib * 1e9
        sats = byt / STEP_SIZE_BYTES * SATS_PER_STEP
        fiat = sats * 1e-8 * 100_000
        print(f"  {label:22} | {sats:11,.0f} | (${fiat:9,.2f})")
    print()

    # ---- overhead table --------------------------------------------------
    print("T2  Channel overhead = 716 sat / purchase (ratio is metric-independent)")
    print("-" * 76)
    print("        purchase |      = data |         overhead")
    for sats in [100, 1_000, 5_000, 10_000, 50_000, 71_600, 716_000]:
        data = sats * STEP_SIZE_BYTES
        ov = CHANNEL_SETUP_CLOSE_SAT / sats * 100
        print(f"  {sats:11,} sat | {fmt_data(data):>10} | {ov:12,.2f}%")
    print()

    # ---- break-even in TIME, which is the operator's actual question -----
    print("T3  Break-even expressed as TIME, at a given usage rate")
    print("-" * 76)
    print("  (price is per BYTE, so 'time' depends on how hard you use the link)")
    print()
    print("     usage rate |  sats/month |  months to 5% | months to 1% | to 0.1%")
    for label, gb_per_month in [("light  50 GB/mo", 50), ("heavy 300 GB/mo", 300),
                                ("very heavy 1 TB/mo", 1000)]:
        sats_mo = gb_per_month * 1e9 / STEP_SIZE_BYTES * SATS_PER_STEP
        m5 = (CHANNEL_SETUP_CLOSE_SAT / 0.05) / sats_mo
        m1 = (CHANNEL_SETUP_CLOSE_SAT / 0.01) / sats_mo
        m01 = (CHANNEL_SETUP_CLOSE_SAT / 0.001) / sats_mo
        print(f"  {label:16} | {sats_mo:11,.0f} | {m5:13.2f} | {m1:12.2f} | {m01:6.2f}")
    print()

    # ---- the honest comparison to Cashu ---------------------------------
    print("T4  Cashu vs channel, at the CORRECTED units")
    print("-" * 76)
    print("  Cashu cost per purchase = 0.1% (melt). Channel = 716 sat fixed.")
    print()
    print("     purchase |  cashu 0.1% | channel 716 |  channel / cashu")
    for sats in [1_000, 10_000, 71_600, 716_000]:
        cashu = sats * 0.001
        ratio = CHANNEL_SETUP_CLOSE_SAT / cashu
        print(f"  {sats:11,} sat | {cashu:11,.1f} | {CHANNEL_SETUP_CLOSE_SAT:11,} | {ratio:13,.1f}x")
    print()
    print("  NOTE: at the corrected (bytes) units the channel is 1-3 orders of")
    print("  magnitude worse, NOT the 3-4 orders the ms-metric sim reported. The")
    print("  cost argument is therefore WEAKER than first published. The CUSTODY")
    print("  argument (V0/V2/V3 always carry a custodian) is unaffected at any price.")
    print()

    out["tables"] = {"erratum_hours_if_ms": h,
                     "sats_per_gib": GIB / STEP_SIZE_BYTES * SATS_PER_STEP}
    dest = os.path.join(os.path.dirname(os.path.abspath(__file__)), "out",
                        "granularity.json")
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    with open(dest, "w") as f:
        json.dump(out, f, indent=2)
    print(f"  wrote {dest}")


if __name__ == "__main__":
    main()
