- **Removed the `nucula` wallet backend, which never had an implementation.**
  `nucula` appeared in the policy vocabulary, the `WalletConfig.Backend`
  documentation, the `knownBackends` set and the `OpenWallet` switch — but no
  adapter for it ever existed. `OpenWallet` mapped it onto the generic sidecar
  client, so "nucula" silently meant "connect to the CDK sidecar socket". Its
  manifest also disqualified it for router use on three counts recorded in the
  repo's own measurement doc: **no upstream licence file**, a **whole-blob**
  storage model (`crash_consistent: false`), and no NUT-07/NUT-09 support.

  Removing it drops one manifest file, one `switch` arm, one `knownBackends`
  entry and the stale doc references. The generic `"sidecar"` value remains, so
  a future daemon speaking the sidecar protocol is still selectable without any
  code change.

- **Corrected the `gonuts` capability manifest, which claimed `v0.11.2`.**
  Every Go module and `packaging/build-inputs.json` pin **v0.13.0**; the
  manifest was two minors stale. `check-gonuts-pin.py` does not read the
  manifest, so nothing caught it. The manifest now records the pinned version
  and that upstream is archived.

- **Dropped `nucula` from the wallet measurement protocol's candidate list** so
  the measurement doc no longer names a candidate that cannot be measured.
