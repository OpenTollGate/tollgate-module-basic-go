- **The SSID matcher recognises the `Archipelago-` captive prefix.** The shell
  writer already derived `!Archipelago-XXXX` from `/etc/tollgate/brand`; the Go
  reader now recognises it for reseller-mode upstream selection and the
  vendor-IE score heuristic.
  ([#1144](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/1144)).
