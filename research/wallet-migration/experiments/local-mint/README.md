# Local deterministic mint (`cdk-mintd` + fakewallet)

**Status:** working 2026-09-14. A fully local Cashu mint with the **fakewallet**
backend (auto-settling payments), so the wallet-sidecar value flow and the T15
parity cases can run **deterministically and with no external network**.

## Why

The on-router value-flow tests need a reachable mint. The MT6000 lab router has no
upstream internet, and the router cannot reach the build host (LAN client
isolation), so a mint on the host is unreachable from the router. A local mint
removes both dependencies — and on the router it would be reachable at
`127.0.0.1:8085`.

## Build

```
cargo build -p cdk-mintd --no-default-features --features sqlite,fakewallet,info-page --release
```

`config.toml` (this directory) sets the fakewallet payment + onchain backends,
sqlite storage, and fast fake settlement (`min_delay_time=0`, `max_delay_time=1`).

```
export CDK_MINTD_MNEMONIC="abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon abandon about"
mkdir -p <workdir>
cdk-mintd --work-dir <workdir> config init --new-mint --file config.toml
cdk-mintd --work-dir <workdir>            # listens on 0.0.0.0:8085
```

## Verified: full wallet-sidecar value flow against the local mint (host)

Pointing `cdk-walletd` at `http://127.0.0.1:8085` and driving it through the
sidecar client:

```
info backend            : cdk
mintquote ok            : True  (quote_id present)
mint_quote_state        : PAID       # fakewallet auto-settles
mint_tokens             : 10
get_balance             : 10
send(10)                : token (2126 chars)
receive(token)          : 10
get_balance             : 10
```

So the daemon's **entire value flow is exercised deterministically**:
quote → paid → mint → balance → send → receive.

## On-router caveat (environment)

Running the same flow **on the MT6000** is blocked by two environment facts, not
by the daemon:

1. the lab router has **no upstream** (so no public mint), and
2. the router **cannot reach the build host** (LAN client isolation), so a
   host-side mint is unreachable.
3. Cross-building `cdk-mintd` for the router (to run it locally) is currently
   blocked: `cdk`'s `mint` feature hard-enables the gRPC signatory
   (`cdk-signatory` `grpc`), whose build script needs **`protoc`**, absent from
   the OpenWrt SDK container.

The off-router value flow above, plus the on-router offline tests
(`physical-router-test-automation` PR #117), cover the daemon; the on-router
value flow is a lab-networking task (give the router upstream, or run the mint
on-device once a protoc-equipped cross-build is set up).
