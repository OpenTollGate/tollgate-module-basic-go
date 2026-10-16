# PROGRESS — vendor-IE price producer

- [x] Closed UNKNOWN: signed kind-10021 ALREADY carries price
      (`merchant.go` CreateAdvertisement: tags metric/step_size/tips +
      price_per_step{cashu,price,unit,mintURL,minSteps}, signed by merchant
      identity). Server side complete.
- [x] RED observed: 3/5 new tests fail on the staged zero-fields.
- [x] GREEN: pricing TLVs (0x03 price, 0x04 step) added to
      vendor_element_manager encode+parse; producer wired in scanner.go
      buildNetworkInfo(); "IE:" hex line captured.
- [x] Full wireless_gateway_manager suite green (incl. 5 pre-existing IE tests).
- [ ] Build for aarch64_cortex-a53
- [ ] Install + demo on MT6000 (A04E) under bench mutex
- [ ] Push branch + open PR

## CORRECTION after hardware verification (2026-10-16)
Measured on tollgate-A04E: `iwinfo <dev> scan` prints NO information elements
at all (grep -c 'IE:' == 0), and prints hidden APs as `ESSID: "unknown"`.
`iw dev <dev> scan` is the only tool that prints elements, and it prints them
DECODED into named fields (`SSID:`, `RSN:`, `HT capabilities:`), not as a hex
blob. A vendor element appears as `Vendor specific: <colon-hex>` and — critically
— iw strips the element ID (0xDD) and length byte, so the body must be
re-framed before ParseVendorIEsFromScanData will accept it.

So the first implementation (parsing a hex `IE:` line) was WRONG for real
hardware and is replaced: the parser now accepts BOTH real formats, handles
the "unknown" placeholder, and re-frames the iw vendor body. Tests rewritten
to the measured format; 3 failed with the raw-body bug and pass with the fix.
