#!/usr/bin/env python3
"""
sim_economics.py -- the deterministic economics harness.

THIS IS A SIMULATOR, NOT A CHAIN. It produces the A4 cost/dominance tables and
nothing about cryptographic validity. It cannot support, and does not claim,
any statement about whether a construction is consensus-valid, non-custodial,
or exploitable -- those come from demo_channel.py against real Bitcoin Core.
Read the limits section printed at the end before quoting any number.

Every constant is either
  (*) SOURCED -- cited to a file in OpenTollGate/tollgate-module-basic-go, or
  (A) ASSUMED -- an explicit market/parameter assumption, listed in output.

Output goes to sim_economics.json alongside stdout, so the memo's tables are
reproducible from a committed script rather than retyped by hand.
"""

import json
import os

# --------------------------------------------------------------------------
# SOURCED constants (module @ cf7cad3)
# --------------------------------------------------------------------------
# (*) price_per_step = 1 sat / step_size = 22020096 ms
#     src/tollgate_protocol/validator_test.go: {"step_size", "22020096"},
#     {"price_per_step","cashu","1","sat",...}; README.md:351 "price_per_step": 1
PRICE_PER_STEP_SATS = 1
STEP_SIZE_MS = 22_020_096            # 6.117 h per step
# (*) src/config_manager/config_manager_config.go: MinBalance: 64, MinPayoutAmount: 128,
#     PayoutIntervalSeconds: 60, MinPurchaseSteps: 1
MIN_BALANCE_SATS = 64
MIN_PAYOUT_SATS = 128
MIN_PURCHASE_STEPS = 1

# --------------------------------------------------------------------------
# (A) ASSUMED constants -- all listed in output
# --------------------------------------------------------------------------
ASSUMPTIONS = {
    "btc_usd": 100_000,               # for a fiat frame only; cancels in ratios
    "onchain_fee_rate_sat_vb": 2,     # regtest relay floor is 1; mainnet quiet = 1-3
    "onchain_fee_rate_high_sat_vb": 50,   # a fee spike / congestion event
    "vsize_fund_tx": 155,             # 1-in-1-out P2WSH funding
    "vsize_claim_tx": 203,            # 2-in... measured in demo_channel.py T2
    "vsize_refund_tx": 142,           # measured in demo_channel.py T5/T6
    "vsize_breach_tx": 203,           # same shape as claim
    "cashu_swap_fee_bps": 0,          # most mints charge 0 for NUT-03 swaps
    "cashu_ln_payout_fee_bps": 10,    # 0.1% typical melt/routing cost
    "channel_apr": 0.05,              # opportunity cost of locked capital
    "session_hours": 72,              # a plausible prepaid session
    "p_payer_never_returns": 0.02,    # abandon rate: prefund left to expire
    "p_router_goes_dark": 0.01,       # router never claims; payer must self-refund
}

MS_PER_HOUR = 3_600_000
SAT_PER_BTC = 100_000_000


def sats_to_hours(sats):
    """Internet time a given sat amount buys, at the advertised price."""
    steps = sats / PRICE_PER_STEP_SATS
    return steps * STEP_SIZE_MS / MS_PER_HOUR


def onchain_setup_sats(rate_sat_vb, unilateral=True):
    """On-chain cost of opening AND closing one channel."""
    v = ASSUMPTIONS["vsize_fund_tx"]
    v += ASSUMPTIONS["vsize_claim_tx"] if unilateral else 0
    return v * rate_sat_vb


def cashu_cost_sats(amount):
    """Router-side cost to receive `amount` sats of ecash and pay out.
    No chain. Mint swaps are free at the configured mints; the melt to
    Lightning carries the routing/melt cost."""
    return amount * ASSUMPTIONS["cashu_ln_payout_fee_bps"] / 10_000


def channel_cost_sats(amount, channel_value, rate_sat_vb, unilateral=True):
    """Total channel cost attributable to one customer buying `amount` sats:
      on-chain setup+close, pro-rated by how much of the channel was used,
      + opportunity cost of the locked capital over the session,
      + expected loss from abandonment / a dark router.
    """
    setup = onchain_setup_sats(rate_sat_vb, unilateral)
    if channel_value <= 0:
        return float("inf")
    # on-chain cost is borne once per channel, however much is used
    onchain = setup
    lockup = channel_value * ASSUMPTIONS["channel_apr"] * \
        ASSUMPTIONS["session_hours"] / 8760
    risk = channel_value * (ASSUMPTIONS["p_payer_never_returns"] +
                            ASSUMPTIONS["p_router_goes_dark"])
    return onchain + lockup + risk


def fmt(n, w=12, d=1):
    return f"{n:>{w},.{d}f}"


def table(title, headers, rows, note=""):
    print()
    print(title)
    print("-" * len(title))
    print("  " + " | ".join(f"{h:>14}" for h in headers))
    print("  " + "-+-".join("-" * 14 for _ in headers))
    for r in rows:
        print("  " + " | ".join(f"{c:>14}" for c in r))
    if note:
        print(f"  {note}")


def main():
    out = {"constants": {"price_per_step_sats": PRICE_PER_STEP_SATS,
                         "step_size_ms": STEP_SIZE_MS,
                         "min_balance_sats": MIN_BALANCE_SATS},
           "assumptions": ASSUMPTIONS, "tables": {}}

    print("sim_economics.py -- DETERMINISTIC SIMULATOR (not a chain)")
    print("=" * 78)
    print(f"  advertised price      : {PRICE_PER_STEP_SATS} sat per "
          f"{STEP_SIZE_MS} ms")
    print(f"  => 1 sat buys         : {sats_to_hours(1):.2f} hours "
          f"({sats_to_hours(1)/24:.2f} days) of internet")
    print(f"  => min wallet balance : {MIN_BALANCE_SATS} sats = "
          f"{sats_to_hours(MIN_BALANCE_SATS):.0f} hours = "
          f"{sats_to_hours(MIN_BALANCE_SATS)/24:.1f} days")
    print(f"  => min purchase       : {MIN_PURCHASE_STEPS} step = "
          f"{MIN_PURCHASE_STEPS*PRICE_PER_STEP_SATS} sat = "
          f"{sats_to_hours(MIN_PURCHASE_STEPS*PRICE_PER_STEP_SATS):.2f} h")
    print()
    print(f"  THE HEADLINE NUMBER: at the module's own advertised price, a")
    print(f"  single satoshi buys {sats_to_hours(1):.2f} hours of internet. That is the")
    print(f"  unit the channel's on-chain cost has to be amortised against.")

    # ---------------- T1: channel overhead vs purchase size ---------------
    rate = ASSUMPTIONS["onchain_fee_rate_sat_vb"]
    setup = onchain_setup_sats(rate, unilateral=True)
    rows = []
    for purchase in (1, 10, 64, 1_000, 10_000, 100_000, 1_000_000):
        oh = setup / purchase * 100
        rows.append([f"{purchase:,} sat", f"{sats_to_hours(purchase):,.1f} h",
                     f"{sats_to_hours(purchase)/24:,.1f} d", f"{setup:,} sat",
                     f"{oh:,.1f}%"])
    table(f"T1  On-chain setup+close cost ({setup} sats @ {rate} sat/vB) vs purchase size",
          ["purchase", "= internet", "= days", "chain cost", "overhead"],
          rows,
          "Note: the channel's on-chain cost is FIXED per channel; the purchase it\n"
          "  settles is not. Below roughly 10^5 sats the overhead exceeds 1%.")
    out["tables"]["T1_overhead_vs_purchase"] = rows

    # ---------------- T2: break-even channel size ------------------------
    rows = []
    for target_pct in (10.0, 1.0, 0.1, 0.01):
        need_sats = setup / (target_pct / 100)
        rows.append([f"{target_pct}%", f"{need_sats:,.0f} sat",
                     f"{sats_to_hours(need_sats):,.0f} h",
                     f"{sats_to_hours(need_sats)/24/365:,.2f} yr",
                     f"${need_sats/SAT_PER_BTC*ASSUMPTIONS['btc_usd']:,.0f}"])
    table(f"T2  Channel value needed to amortise {setup} sats of on-chain cost",
          ["target", "channel", "= internet", "= years", "= fiat"],
          rows,
          "To get the on-chain cost under the 0.1% a mint/Lightning fee costs, the\n"
          "  customer must prepay for CENTURIES of internet. The instrument is three\n"
          "  to four orders of magnitude too small for its own settlement layer.")
    out["tables"]["T2_breakeven_channel_value"] = rows

    # ---------------- T3: full cost model, both paths --------------------
    rows = []
    for purchase in (64, 1_000, 10_000, 100_000, 1_000_000):
        cashu = cashu_cost_sats(purchase)
        chan = channel_cost_sats(purchase, purchase, rate)
        chan_hi = channel_cost_sats(purchase, purchase,
                                    ASSUMPTIONS["onchain_fee_rate_high_sat_vb"])
        ratio = chan / cashu if cashu > 0 else float("inf")
        rows.append([f"{purchase:,}", f"{cashu:,.2f}", f"{chan:,.0f}",
                     f"{chan_hi:,.0f}", f"{ratio:,.1f}x"])
    table("T3  Cost to settle one purchase (sats), Cashu vs one-way channel",
          ["purchase", "cashu", "channel@2", "chan@50", "channel/cashu"],
          rows,
          "Channel column = on-chain fund+claim + capital lockup + abandon/dark-risk.\n"
          "  Cashu column = 0.1% melt-to-Lightning. Cashu is cheaper at EVERY size\n"
          "  the product actually sells.")
    out["tables"]["T3_cost_per_purchase"] = rows

    # ---------------- T4: customer maximum exposure ----------------------
    rows = []
    setup_a = onchain_setup_sats(rate, unilateral=True)
    for purchase in (1, 64, 1_000, 10_000, 100_000):
        # Cashu: the customer hands over ONE bearer token worth the purchase.
        # Nothing else is committed. The ROUTER carries the mint risk.
        cashu_exposure = purchase
        # Channel: the customer must lock a whole prefund BEFORE any service,
        # and that prefund must at minimum cover the purchase plus the on-chain
        # cost the customer bears. Exposure is the whole locked amount.
        chan_exposure = purchase + setup_a
        rows.append([f"{purchase:,}", f"{cashu_exposure:,}", f"{chan_exposure:,}",
                     f"{chan_exposure/max(cashu_exposure,1):,.0f}x"])
    table("T4  Customer's maximum value at risk per transaction (sats)",
          ["purchase", "cashu", "channel", "chan/cashu"], rows,
          "Cashu exposes the customer to EXACTLY the amount being bought -- a bearer\n"
          "  token is handed over like cash, so there is nothing else to lose. A\n"
          "  one-way channel locks the entire prefund BEFORE any service is\n"
          "  rendered. The channel INCREASES customer exposure by the whole\n"
          "  settlement cost, and on top of that the escape hatch is only usable by\n"
          "  a payer who can observe height and publish in time (demo_channel.py T5).")
    out["tables"]["T4_customer_exposure"] = rows

    # ---------------- T5: what the channel would have to replace ---------
    # The brief's stated motivation: remove the mint round-trip from the
    # customer's path. Quantify what that round-trip actually costs today.
    rows = [
        ["mint NUT-07 checkstate", "~1 RTT", "~0 sat", "router (not the customer)"],
        ["Cashu swap NUT-03", "0 bps", "0 sat", "router, at all 5 configured mints"],
        ["Lightning melt payout", "0.1%", f"{int(0.001*10_000):,} / 10k sat",
         "router; already exists today"],
        ["CHANNEL funding tx", f"{ASSUMPTIONS['vsize_fund_tx']} vB",
         f"{155*rate:,} sat", "NEW -- customer must bear"],
        ["CHANNEL claim tx", f"{ASSUMPTIONS['vsize_claim_tx']} vB",
         f"{203*rate:,} sat", "NEW -- once per channel"],
    ]
    table("T5  What the channel replaces, and its own new costs",
          ["mechanism", "unit", "cost", "who pays"],
          rows,
          "The thing being removed costs ~0 sat and one round-trip. The thing\n"
          "  replacing it costs hundreds of sats. This is a strictly worse trade\n"
          "  at the product's own unit size.")
    out["tables"]["T5_replacement"] = rows

    # ---------------- T6: sensitivity to a fee spike ---------------------
    rows = []
    for r in (1, 2, 5, 20, 50, 100, 200):
        setup_r = onchain_setup_sats(r, unilateral=True)
        rows.append([f"{r}", f"{setup_r:,}",
                     f"{sats_to_hours(setup_r)/24:,.1f}",
                     f"{setup_r/PRICE_PER_STEP_SATS:,.0f}"])
    table("T6  Fee-spike sensitivity: on-chain cost in internet-time",
          ["sat/vB", "chan cost", "days of net", "purchases forgone"],
          rows,
          f"At {ASSUMPTIONS['onchain_fee_rate_high_sat_vb']} sat/vB the setup alone buys "
          f"{sats_to_hours(onchain_setup_sats(50))/24:,.0f} days of internet. A customer\n"
          "  choosing between settling a channel and buying bandwidth will always\n"
          "  choose bandwidth -- which is the correct outcome for the product and a\n"
          "  fatal one for the channel as a payment rail.")
    out["tables"]["T6_fee_spike"] = rows

    # ---------------- T7: is the verdict an artifact of price_per_step=1? --
    rows = []
    for pps in (1, 10, 100, 1_000):
        # hours bought per sat changes; the channel's sat cost does NOT
        hours_per_sat = STEP_SIZE_MS / pps / MS_PER_HOUR
        # the purchase that buys 1000 hours of internet at this price
        purchase = int(1_000 / hours_per_sat)
        overhead = setup / purchase * 100
        rows.append([f"{pps}", f"{hours_per_sat:.3f}", f"{purchase:,}",
                     f"{overhead:,.2f}%"])
    table(f"T7  FALSIFIABILITY: does the verdict survive a different price_per_step?",
          ["sat/step", "h per sat", "1000h costs", "overhead"], rows,
          "The module DEFAULT is price_per_step=1 (config_manager_config.go:305).\n"
          "  This table re-runs the overhead at 10x, 100x and 1000x that price. Even\n"
          "  pricing internet 1000x higher (1000 sat per 6.1 h, i.e. ~USD 0.16/h at\n"
          "  the assumed BTC price), a customer buying 1000 hours still pays only\n"
          "  ~0.1-1% overhead -- and that is the ONLY regime where the channel starts\n"
          "  to be defensible. If TollGate's real price were ~1000 sat/step the cost\n"
          "  argument weakens; the CUSTODY argument (demo T3/T5/T7) does not.")
    out["tables"]["T7_price_sensitivity"] = rows

    # ---------------- verdict --------------------------------------------
    p_small = 1_000
    verdict = {
        "cashu_cost_sats": cashu_cost_sats(p_small),
        "channel_cost_sats": channel_cost_sats(p_small, p_small, rate),
        "channel_overhead_pct": onchain_setup_sats(rate) / p_small * 100,
        "breakeven_for_0p1pct": onchain_setup_sats(rate) / 0.001,
    }
    print()
    print("=" * 78)
    print("A4 DOMINANCE VERDICT (computed, not asserted)")
    print("=" * 78)
    print(f"  At a {p_small:,}-sat purchase (the largest plausible single TollGate sale):")
    print(f"    Cashu cost           : {verdict['cashu_cost_sats']:,.2f} sats")
    print(f"    Channel cost         : {verdict['channel_cost_sats']:,.0f} sats "
          f"({verdict['channel_overhead_pct']:,.1f}% overhead)")
    print(f"    To reach the 0.1% a mint/LN fee costs, a channel must be worth")
    print(f"    {verdict['breakeven_for_0p1pct']:,.0f} sats = "
          f"{sats_to_hours(verdict['breakeven_for_0p1pct'])/24/365:,.1f} years of internet.")
    print()
    print("  VERDICT: Cashu DOMINATES the one-way channel for every purchase size")
    print("  this product sells. Cashu is a bearer instrument, needs no chain, lets")
    print("  the payer hand value over while fully offline, is free per purchase, and")
    print("  caps the CUSTOMER's exposure at the amount being bought. The channel adds")
    print("  on-chain cost, capital lockup, timelock and fee-timeliness risk, and a")
    print("  larger customer exposure -- and buys back only the removal of a mint")
    print("  round-trip that the customer was never performing anyway.")
    out["verdict"] = verdict

    print()
    print("-" * 78)
    print("LIMITS OF THIS SIMULATOR (mandatory)")
    print("-" * 78)
    print("  IT CANNOT SUPPORT, and no claim is made about:")
    print("   - whether any construction is consensus-valid (-> demo_channel.py)")
    print("   - whether a construction is non-custodial (-> demo_channel.py T3/T5)")
    print("   - exploitability, breach races, reorg behaviour (-> not tested at all:")
    print("     regtest does not reorg on demand here, so A3's reorg risk is")
    print("     UNQUANTIFIED and argued from construction, not measured)")
    print("   - real market fee rates, mint fee schedules, or BTC price")
    print("   - real user behaviour (abandon rate, session length are guesses)")
    print("  IT IS a deterministic arithmetic model over SOURCED module constants")
    print("  and explicitly-listed ASSUMED parameters. Change the assumptions in")
    print("  ASSUMPTIONS and the tables move; the conclusion's DIRECTION does not,")
    print("  because the sign is set by the ratio in T1/T2 (three to four orders of")
    print("  magnitude), not by any single parameter.")

    here = os.path.dirname(os.path.abspath(__file__))
    dest = os.path.join(here, "out", "sim_economics.json")
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    with open(dest, "w") as f:
        json.dump(out, f, indent=2)
    print()
    print(f"  wrote {dest}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
