# rig — the reproducible IaC for "one-way channels for TollGate"

Everything in `RISK-AND-IAC.md` that is a number comes from a script here.
Nothing in the memo is typed from a REPL.

## Run it

```bash
bash rig/run-rig.sh          # everything; writes rig/out/
bash rig/check-env.sh        # B1: what this host has, from the live host
bash rig/check-go.sh         # does this host build the module, with which Go
```

Artefacts land in `rig/out/` and are committed:

| file | what it is | used by |
|---|---|---|
| `ENV.txt` | live host inventory (binaries, listeners, network) | B1 |
| `GO-TOOLCHAIN.txt` | Go version three-way finding | B1 |
| `BITCOIND-VERSION.txt` | the exact node the numbers came from | B1 |
| `SIM-OUTPUT.txt` | the economics tables (**a simulator**) | A4 |
| `RED.txt` | the payer-offline construction: non-custody **fails** | A1 |
| `GREEN.txt` | the Spilman construction: non-custody holds, payer is not blind | A1, A2 |
| `out/sim_economics.json` | machine-readable sim output | A4 |

## Network needs, declared

- `sim_economics.py` — **no network, no chain.** Pure arithmetic.
- `demo_channel.py` — **no network.** A local `bitcoind` on `127.0.0.1:18443`.
- `run-rig.sh` step 1 — needs the network **once**, only if `bitcoind` is
  absent. Supply `BITCOIND=/path/to/bitcoind` to avoid it entirely.

So the offline claim is checkable by the rig itself: with a toolchain in place,
unplug the network and every assertion still executes.

## Files

| file | role |
|---|---|
| `btclib.py` | secp256k1 + RFC6979 + BIP143 P2WSH + BIP68/BIP112. **Zero third-party deps** — a rig that needs PyPI to check a no-internet claim is not evidence |
| `demo_channel.py` | the channel construction, `--variant spilman\|naive`, all checks executed against Core consensus |
| `sim_economics.py` | the costing model (`--` see its own LIMITS section) |
| `run-rig.sh` | the one command |
| `check-env.sh` | host inventory |
| `check-go.sh` | Go toolchain finding |
| `ERRATA.md` | the six things running this rig caught — read this first |

## The two deliberate failures

`GREEN.txt` contains **two** `[FAIL]` lines. Both are the finding, asserted
false on purpose:

- *"named a construction that is BOTH payer-chain-blind AND non-custodial"*
- *"the ladder makes the construction non-custodial AND payer-offline"*

If a future contributor makes either pass, the memo is disproved and should be
revised. That is the falsification condition, made executable.

## What this rig is NOT

It is not a mainnet test, not a reorg test, not a fee-market model, and not a
LN implementation. `A3`'s reorg and watchtower risks are argued from
construction and flagged as **unquantified**; the rig does not measure them.
See B4 in the memo for the claim-by-claim boundary.
