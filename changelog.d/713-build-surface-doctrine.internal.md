- **The build surface is now a checked contract.** `tests/contract/check-build-surface.py`
  pins the two-path doctrine (#708/#710's two pinned SDK eras, #711's rails): it fails when
  a script looks like a third build path but is not on the sanctioned list, when the
  manifest stops carrying both eras, or when `build-sdk-package.sh` stops flowing
  `TG_PACKAGE_FORMAT` into the era-aware sdk image ref. The one-pass jq matrix split in
  `.github/workflows/build-package.yml` (`ipk_matrix`/`apk_matrix` from a single
  `define-package-matrix` job) replaces the duplicated per-era matrix blocks, and
  `hooks/pre-commit` runs the fence locally alongside the version-sync check. Landing after
  #738, the fence sanctions `packaging/normalize-ipk-version.sh` (the ipk ordering twin of
  the apk normalizer) so the list reflects current main, not the pre-#738 surface
  ([#713](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/713)).
