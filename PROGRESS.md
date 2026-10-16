# PROGRESS — vendor-IE price producer

## Status: build fixed, tests green, demo pending

- [x] Closed UNKNOWN: signed kind-10021 ALREADY carries price (merchant.go
      CreateAdvertisement: metric/step_size/tips + price_per_step{cashu,
      price,unit,mintURL,minSteps}, signed by merchant identity). Server side
      complete -> the work is extend, not add.
- [x] RED observed, then GREEN, then HARDWARE-FALSIFIED and corrected.
- [x] Build error fixed: VendorIEDiscovery lives on mainConfig.UpstreamWifi,
      not mainConfig. arm64 cross-build OK (17.4 MB).
- [x] main-module test failure is PRE-EXISTING and environmental
      (/etc/tollgate absent on dev box) — proven by stashing to pristine.
- [ ] Install on MT6000 (A04E) under bench mutex + live demo
- [ ] Push branch + open PR

## Hardware format facts (tollgate-A04E, 2026-10-16) — the load-bearing part
- `iwinfo <dev> scan` prints NO information elements. It can never identify a
  TollGate by vendor element. Still parsed (carries hidden APs as "unknown").
- `iw dev <dev> scan` is the ONLY element source; prints elements DECODED as
  named fields, not a hex blob.
- Vendor element appears as `Vendor specific: <colon-hex>` and iw STRIPS the
  element ID 0xDD + length byte -> must be re-framed or the parser returns 0.
- iw prints `SSID: unknown` for hidden APs (literal, must not become a name).
