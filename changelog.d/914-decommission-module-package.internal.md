- **The module package build is scheduled for decommission.** A new
  `packaging/README.md` records that `FreedomTechFeed/packages/net/tollgate-wrt/
  Makefile` is the single canonical shipping package and that this repository's
  own `packaging/Makefile` exists only for test/reproducibility lanes. The
  mechanical removal (the `Package/tollgate-wrt` + `Build/*` targets, the
  `build-ipk`/`local-build-ipk`/`build-env` helpers and the publish jobs in
  `build-package.yml`) lands after the `v0.6.0-rc4` release; `packaging/files/`
  stays — the feed consumes it.
