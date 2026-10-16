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
