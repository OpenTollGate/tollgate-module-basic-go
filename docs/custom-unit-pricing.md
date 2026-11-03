# Unit-aware pricing and operator-issued ecash (#1040)

What this changes, what it costs, and what it still cannot do.

## The bug this closes

A gateway priced a token by a bare number. `Token` had no `Unit()`, and
`calculateAllotment` divided `amount` by a sat-denominated `price_per_step`. A
token minted in a custom unit was therefore **silently mispriced**: 64 `mb`
proofs priced as 64 sats. Misprice in that direction hands out access the
operator was never paid for; the reverse takes the customer's money for almost
nothing.

Measured during the 2026-10 integration: a mint configured with the default
bytes `step_size` (22020096) and unit `mb` sold **21× the access** for the same
money.

## What is enforced

- `MintConfig.Unit` names the unit a mint's config prices. Empty means `sat`, so
  every pre-existing config keeps its exact behaviour.
- `calculateAllotmentForUnit` compares the token's unit (case-folded, trimmed)
  with the config's. **A mismatch is a refusal**, and the refusal names both
  units and tells the operator which config key to set.
- `Token.Unit()` reads the unit off the decoded V4 `u` / V3 `unit` field. A token
  that cannot report a unit returns `""`, which the merchant treats as unknown
  and refuses, rather than assuming satoshis.
- `ValidateUnitPricingForConfig` warns (does not refuse) when a declared custom
  unit is priced with a `step_size` from another scale — the 21× case. A warning
  rather than a refusal because the router must keep serving customers who
  already paid; the operator has to see it in `logread`.

## Issuing

```
tollgate wallet issue <amount> <unit> [mint-url]
```

Issuance runs **over the existing daemon socket** (`/var/run/tollgate.sock`,
mode `0660`), not a new listener. The command asks the local mint for a quote,
waits for it to be paid, mints, splits, and returns the serialized token. Partial
failure is reported as such and never retried: the error says the value is in the
operator's wallet and that the quote must not be re-minted. Re-minting a paid
quote is how an operator pays twice for one issuance.

## The limitation that decides deployment

**Custom-unit tokens cannot be redeemed on a router built the shipping way.**

Redeeming an `mb` token requires the `cdk_wallet` path, which needs
`cdk-go`'s FFI bindings and therefore **CGO**. The module is built
`CGO_ENABLED=0` for OpenWrt — a hard constraint in `AGENTS.md`, because the
binary must cross-compile for mips/mipsel/arm/arm64/x86.

Verified on this branch:

```
$ CGO_ENABLED=0 go build -tags cdk_wallet .
package .../tollwallet
    imports github.com/cashubtc/cdk-go/bindings/cdkffi:
    build constraints exclude all Go files in .../cdk-go@v0.17.3/bindings/cdkffi
$ CGO_ENABLED=1 go build -tags cdk_wallet .
(clean)
```

So today:

| Path | Custom unit | Shipping build (`CGO_ENABLED=0`) |
|---|---|---|
| Issue (`wallet issue`) | yes | **yes** — issuance goes through the mint's own API |
| Redeem at the gate | yes **only** with `cdk_wallet` | **no** — `cdk_wallet` cannot compile |
| Redeem, default build | refused with a clear error (not mispriced) | yes, safely |

The safe default is the important property: a shipping router does not accept a
custom-unit token, and says so, rather than pricing it as satoshis.

Making redemption work on a shipping router needs one of:

1. the gonuts fork teaching `cashu.Unit` about custom units (keeps CGO off, and
   is the path that does not fight the packaging constraint), or
2. running the CDK wallet behind the existing **sidecar** seam
   (`tollwallet/sidecar.go`, AF_UNIX) on targets that can afford a second
   process — the architecture already anticipates this for FFI-on-MIPS.

Either is follow-up work; neither is in this branch.

## Verification

- `scripts/go-test.sh`: **19 modules green** (merchant 339 s, tollwallet 62 s).
- `gofmt` and `go vet` clean.
- Not run against a router. The router-visible acceptance for this class belongs
  to PRTA per the ownership map; nothing here is claimed as hardware-verified.
