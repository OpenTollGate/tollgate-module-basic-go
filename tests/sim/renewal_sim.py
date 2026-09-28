#!/usr/bin/env python3
"""renewal_sim.py — discrete-event simulation of the upstream renewal policy.

Model of the mechanics documented in #460 (from the code, not assumed):

  - usage poll every 1 s; renewal triggers when remaining <= effective_offset,
    effective_offset = min(configured_offset, current_allotment / 2)  (#442)
  - renewal is non-blocking: traffic continues on remaining runway while the
    payment is in flight for payment_rtt seconds (failures retry >= 5 s apart)
  - tracker triggers throttle at >= 10 s apart -> the sustained-throughput
    ceiling is allotment gained per 10 s
  - allotment is CUMULATIVE across renewals (remaining bytes carry over);
    capital strands only on gateway-side session loss
  - purchase quantizes to whole 22,020,096-byte steps (floor)

Outputs the five #460 metrics per (link, increment, offset, price, rtt) cell.
Deterministic: faults are seeded.
"""

from __future__ import annotations

import argparse
import json
import math
import random
from dataclasses import dataclass, field

STEP = 22_020_096  # upstream advertisement step, bytes


@dataclass
class Cell:
    link_mbps: float
    rtt_s: float            # one-way payment latency (mint+gateway round trip)
    increment_mb: float     # preferred_session_increments_bytes
    offset_frac: float      # bytes_renewal_offset as fraction of increment
    price_sats_per_gb: float
    mint_outage_s: float = 0.0
    session_loss_at_s: float | None = None   # gateway-side loss -> strand
    duration_s: float = 7200.0
    initial_allotment_mb: float = 50.0

    # results
    stall_s: float = 0.0
    transferred_b: float = 0.0
    renewals: int = 0
    inter_renewal: list = field(default_factory=list)
    sats_spent: float = 0.0
    sats_stranded: float = 0.0
    bootstrap_sats: float = 0.0


def run(c: Cell) -> Cell:
    rng = random.Random(hash((c.link_mbps, c.increment_mb, c.offset_frac,
                               c.price_sats_per_gb, c.rtt_s)) & 0xFFFFFFFF)
    T = c.duration_s
    loss_at = c.session_loss_at_s
    outage_until = c.mint_outage_s if c.mint_outage_s else 0.0
    # randomized outage start within first quarter
    outage_start = rng.uniform(0, T / 4) if c.mint_outage_s else None

    throughput = c.link_mbps * 1e6 / 8.0
    increment = c.increment_mb * 1e6
    offset = c.offset_frac * increment
    allotment = c.initial_allotment_mb * 1e6
    used = 0.0
    sats_per_step = c.price_sats_per_gb * STEP / 1e9
    steps_per_buy = max(1, math.floor(increment / STEP))
    c.bootstrap_sats = steps_per_buy * sats_per_step

    t = 0.0
    last_trigger = -math.inf
    payment_done_at: float | None = None
    payment_started_at = 0.0
    last_renewal_t = 0.0
    stalled_prev = False

    while t < T:
        remaining = allotment - used
        eff_offset = min(offset, allotment / 2.0)

        # consume
        chunk = min(throughput, remaining)
        if chunk <= 0:
            c.stall_s += 1.0
        used += chunk
        c.transferred_b += chunk

        # session loss strands remaining + in-flight payment
        if loss_at is not None and t >= loss_at:
            rem = max(0.0, allotment - used)
            c.sats_stranded = rem / 1e9 * c.price_sats_per_gb
            if payment_done_at is not None:
                c.sats_stranded += steps_per_buy * sats_per_step
            return c

        # renewal trigger
        if payment_done_at is None and remaining <= eff_offset and (t - last_trigger) >= 10.0:
            last_trigger = t
            payment_started_at = t
            payment_done_at = t + c.rtt_s
            if outage_start is not None and outage_start <= t < outage_start + c.mint_outage_s:
                # retry loop: retry every 5 s while the mint is unreachable
                backoff = t + 5.0
                while backoff < outage_start + c.mint_outage_s:
                    backoff += 5.0
                payment_done_at = backoff + c.rtt_s
        # completion (non-blocking)
        if payment_done_at is not None and t >= payment_done_at:
            allotment += steps_per_buy * STEP
            c.renewals += 1
            c.sats_spent += steps_per_buy * sats_per_step
            c.inter_renewal.append(t - last_renewal_t)
            last_renewal_t = t
            payment_done_at = None

        t += 1.0
    return c


def summarize(c: Cell) -> dict:
    gb = c.transferred_b / 1e9
    return {
        "link_mbps": c.link_mbps, "rtt_s": c.rtt_s,
        "increment_mb": c.increment_mb, "offset_frac": c.offset_frac,
        "price_sats_per_gb": c.price_sats_per_gb,
        "gb_transferred": round(gb, 2),
        "stall_s_per_gb": round(c.stall_s / gb, 3) if gb else None,
        "renewals_per_h": round(c.renewals / (c.duration_s / 3600), 1),
        "min_inter_renewal_s": min(c.inter_renewal) if c.inter_renewal else None,
        "achieved_pct_of_link": round(100 * (c.transferred_b / c.duration_s) / (c.link_mbps * 1e6 / 8), 1),
        "sats_spent": round(c.sats_spent, 0),
        "bootstrap_sats": round(c.bootstrap_sats, 0),
        "sats_stranded_on_loss": round(c.sats_stranded, 0),
    }


def main() -> None:
    p = argparse.ArgumentParser()
    p.add_argument("--json", action="store_true")
    args = p.parse_args()

    links = [(3, 0.3), (25, 0.1), (100, 0.02), (20, 0.6), (1000, 0.005)]  # (mbps, payment rtt)
    increments = [50, 125, 250, 500, 1000, 2500]
    offsets = [0.10, 0.25, 0.40]
    prices = [1, 20, 1000]  # sats/GB buckets ($10/GB ~ 1000 sats/GB)

    rows = []
    for mbps, rtt in links:
        for inc in increments:
            for off in offsets:
                c = Cell(link_mbps=mbps, rtt_s=rtt, increment_mb=inc,
                         offset_frac=off, price_sats_per_gb=20.0)
                rows.append(summarize(run(c)))
    # capital metrics vs price at representative link
    for price in prices:
        for inc in increments:
            c = Cell(link_mbps=25, rtt_s=0.1, increment_mb=inc, offset_frac=0.25,
                     price_sats_per_gb=price, session_loss_at_s=1800.0)
            rows.append(summarize(run(c)))

    if args.json:
        print(json.dumps(rows, indent=1))
        return
    # digest: per (link, increment) best offset by stall_s_per_gb
    print(f"{'link':>6} {'inc_MB':>7} {'off':>4} | {'GB':>7} {'stall/GB':>8} {'ren/h':>6} {'minΔren':>7} {'ach%':>5} | bootstrap(sats@20/G)")
    seen = {}
    for r in rows:
        if r["price_sats_per_gb"] != 20.0:
            continue
        key = (r["link_mbps"], r["increment_mb"])
        seen.setdefault(key, []).append(r)
    for (mbps, inc), rs in sorted(seen.items()):
        rs.sort(key=lambda r: (r["stall_s_per_gb"] if r["stall_s_per_gb"] is not None else 9e9, -r["achieved_pct_of_link"]))
        for r in rs:
            print(f"{mbps:>6} {inc:>7} {int(r['offset_frac']*100):>3}% | {r['gb_transferred']:>7} "
                  f"{str(r['stall_s_per_gb']):>8} {r['renewals_per_h']:>6} {str(r['min_inter_renewal_s']):>7} "
                  f"{r['achieved_pct_of_link']:>5} | {r['bootstrap_sats']:>0}")


if __name__ == "__main__":
    main()
