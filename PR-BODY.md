# PR — wire the vendor-IE price producer

## What this fixes

`PricePerStep` and `StepSize` were declared across **four** data models
(`NetworkInfo`, `Gateway`, `DiscoveryEntry`, `KnownTollGate`) and **had no
producer**. `scanner.go` hard-zeroed them:

```go
// STAGED (this PR): IsTollGate/RawIEs/PricePerStep/StepSize
// stay zero here — the vendor-IE parse that fills them is
// wired in the follow-up that gates on VendorIEDiscovery.
```

and `VendorIEDiscovery` was a config flag with **no consumer**. So the whole
scan-side identification path was plumbing with no source: a router could serve
a signed, priced advertisement on `:2121`, but nothing could read a neighbour's
price from the air.

This wires the producer, reads the flag, and adds the pricing TLVs the element
needed to carry a price at all.

## Two hardware findings that shaped the design

Both measured on a live GL-MT6000 (`tollgate-A04E`, OpenWrt 24.10.2,
`mediatek/filogic`, `aarch64_cortex-a53`), 2026-10-16.

**1. `iwinfo <dev> scan` prints no information elements at all.**

```
$ iwinfo phy1-ap0 scan | grep -c 'IE:'
0
```

So the scan source the daemon used could *never* identify a TollGate by its
vendor element. `iwinfo` is kept (it reports hidden APs, as `ESSID: "unknown"`)
but is now complemented by `iw`, which is the only element source.

**2. `iw` prints elements decoded, and strips the element framing.**

`iw dev <dev> scan` emits named fields (`SSID:`, `RSN:`, `HT capabilities:`),
never a hex blob. A vendor element appears as:

```
Vendor specific: 21:21:21:01:01:02:01:14:...
```

and **iw omits the element ID (`0xDD`) and length byte** that
`ParseVendorIEsFromScanData` expects. Feeding the body straight through
returned **36 good bytes in, 0 advertisements out** — measured, not inferred.
The body is therefore re-framed before parsing.

## Changes

- `scanner.go` — `ParseIwinfoOutput` now accepts **both** real formats
  (`iwinfo` `Cell NN - Address:` and `iw` `BSS <mac>(on …)`), per record.
  Element-derived fields are filled by a new `buildNetworkInfo`; `iw`'s
  `RSN:`/`freq:` supply encryption/channel; placeholder SSIDs (`""`,
  `"(hidden)"`, `"unknown"`) are refused admission so a literal `unknown`
  cannot become a selectable upstream.
- `scanner.go` — `scanRadio` gains an `iw` pass when vendor-IE discovery is on,
  and `mergeScanResults` folds the two views by BSSID (iw supplies the
  element-derived fields; iwinfo-only entries survive).
- `vendor_element_manager.go` — pricing TLVs `0x03` (price_per_step) and `0x04`
  (step_size), 2-byte big-endian, emitted and parsed. Dropped with
  `truncated=true` rather than overflowing the 255-byte element or displacing
  the pubkey: **identification outranks the pricing hint**.
- `types.go` — `TollGateAdvertisement` gains `PricePerStep`/`StepSize`;
  `Scanner` gains `vendorIEDiscovery` + `NewScanner`/`SetVendorIEDiscovery`.
- `main.go` — reads `config.UpstreamWifi.VendorIEDiscovery` (the field lives on
  the `UpstreamWifi` sub-struct) so the flag now has a consumer.
- `src/cmd/tg-vendor-ie-demo` — a small binary that runs the real producer
  against the router's own live `iw` output, so the parsing claim is verified
  against the machine rather than a fixture.

## Authority is unchanged

The element is an **unsigned hint**. OUI identification selects a candidate;
the **signed kind-10021 advertisement on `:2121` remains the authority for
price** — and that advertisement already carries it:

```
Kind 10021, tags: metric, step_size, tips,
                  price_per_step{ cashu, price, unit, mintURL, minSteps }
```

A zero pricing value in the element means **"not advertised here"**, never
"free". That is stated in-code at both the producer and the TLV constants.

## Tests

Observed **RED before** the change (3 of 5 failed on the staged zero fields),
GREEN after, and **rewritten once hardware falsified the first format
assumption** — three tests failed against the un-re-framed body and pass with
the fix.

- `TestParseIWScanOutput_{PopulatesVendorIEFields, NonTollGateVendorIgnored,
  TollGateWithoutPricingTLVs, NoVendorElementMeansNotTollGate,
  RealHardwareHiddenSSID, MultipleNetworksAndVendorElements}`
- `TestVendorIE_RoundTripPricing` (+4 subtests), `TestVendorIE_PricingDroppedRatherThanOverflow`
- `TestMergeScanResults_{EnrichesExistingByBSSID, KeepsEntriesOnlyOneViewSaw,
  FillsGapsFromSecondView, EmptySecondViewIsIdentity}`
- `TestScanRadioWithIW_FailureReturnsNil`, `TestNewScanner_WiresVendorIEDiscovery`

Coverage on the new surface: `mergeScanResults` 100%, `appendScanEntry` 100%,
`NewScanner` 100%, `SetVendorIEDiscovery` 100%, `ParseIwinfoOutput` 98.6%,
`buildNetworkInfo` 92.9%, `scanRadioWithIW` 70%, `parseColonHex` 75%. The
package suite is green including the five pre-existing vendor-IE tests (no
regressions).

**Honest note on the whole-module run:** `go test ./` at `src/` fails on this
dev box with `create temp config: open /etc/tollgate/...: no such file or
directory`. That is **pre-existing and environmental** — reproduced on a
pristine checkout with this change stashed — not a regression here. Per
`AGENTS.md`, router-visible behaviour that unit tests cannot cover is called out
rather than claimed.

## What is NOT claimed

- No router-visible end-to-end claim from unit tests alone. The demo binary
  exists to make that claim against live output.
- `scanRadioWithIW` and `parseColonHex` are not at 100% — the un-covered
  branches are the iw-failure and `p > 2 hex digits` paths.
- This does **not** add a router-to-router signalling plane, and deliberately
  so: price is a policy that must be told-and-signed, not exchanged
  peer-to-peer (see `docs/architecture/discovery-signaling-decision.md` for the
  signal-vs-authority split this preserves).
