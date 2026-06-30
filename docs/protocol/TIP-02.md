# TIP-02 - Cashu payments

---

- A TollGate can accept multiple mints and multiple currencies.

## TollGate Discovery

A TollGate that accepts Cashu tokens as payment may advertise its pricing using the following tags.

```json
{
    "kind": 10021,
    // ...
    "tags": [
        // <TIP-01 tags>
        ["price_per_step", "<bearer_asset_type>", "<price>", "<unit>", "<mint_url>", "<min_steps>"],
        ["price_per_step", "...", "...", "...", "...", "..."],
        // Optional: only emitted for mints whose Lightning backend was verified working
        ["supports_ln", "<mint_url>", "true"],
    ]
}
```

Tags:
- `price_per_step`: (one or more)
	- `<bearer_asset_type>` Always `cashu`.
	- `<price>` price for purchasing 1 time the `step_size`.
	- `<unit>` unit or currency.
	- `<mint_url>` Accepted mint. Example: 210 sats per minute (60000ms)
	- `<min_steps>` Minimum amount of steps to purchase using this mint. Positive whole number, default 0 ⚠️ TENTATIVE: Strucuture of incorporation of fees/min purchases is not final

- `supports_ln`: (optional, zero or more)
	- Signals that a mint's Lightning backend was verified working by the
	  TollGate. The TollGate probes each reachable mint by requesting a minimal
	  1-sat mint quote (NUT-04); only mints whose backing Lightning node (e.g.
	  coinos.io) answered successfully emit this tag.
	- `<mint_url>` The mint this capability applies to (matches a `price_per_step` mint).
	- `<value>` Currently always `true`.
	- **Absence is meaningful**: customers / frontends SHOULD treat the lack of a
	  `supports_ln` tag for a given mint as "Lightning is NOT available" and
	  hide or grey-out the Lightning payment option for that mint. This prevents
	  silent invoice failures when a mint's LN backend is down.

- The value of `<price>` MUST be the same across all occurrences of the same `<unit>` value.

### Example
```json
{
    "kind": 10021,
    // ...
    "tags": [
        // <TIP-01 tags>
        ["price_per_step", "cashu", "210", "sat", "https://mint.domain.net", 1],
        ["price_per_step", "cashu", "210", "sat", "https://other.mint.net", 1],
        ["price_per_step", "cashu", "500", "eur", "https://mint.thirddomain.eu", 3],
        ["supports_ln", "https://mint.domain.net", "true"],
    ]
}
```

## Payment

The customer sends a Cashu token directly over the TollGate's supported transport(s). The allotment the customer receives is relative to the value of the token sent.

```
cashuB...
```
