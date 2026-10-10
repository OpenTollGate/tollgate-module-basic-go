- **`ca-bundle` is now a declared dependency on every build lane, instead of an
  assumption the installer used to paper over.** The gateway does money over
  TLS — it talks to Cashu mints and Lightning with Go's system root pool and no
  insecure escape hatch — so a box that cannot verify TLS cannot take a payment.
  The CA store was previously missing from the package metadata: the SDK recipe
  (packaging/Makefile) and the `.ipk` recipes (packaging/local-build-ipk.sh, the
  GitHub `.ipk` lane, the generated ngit shards) all declare `ca-bundle` now, so
  apk/opkg resolves the closure and pulls the CA store in with the package. On a
  box that can still reach the feed, that is the whole fix.
  ([#828](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/828)).

  The missing dependency surfaced on a GL-MT3000 (OpenWrt 25.12.5): installing
  the shipped package by hand failed at the first HTTPS fetch, where `wget` and
  `apk update` both died with "SSL verify error: unknown error" because the
  image carried no CA store.

  It does not, and cannot, bootstrap a box that has NO trust anchors at all:
  there the very first HTTPS fetch already fails verification, and no dependency
  can repair a fetch that must itself be verified. The README now states that
  honest offline path — fetch the package files to the box by other means
  (scp), then install them locally in one call under a single explicit trust
  override — and no longer implies the dependency alone can bootstrap an
  anchor-less box. The manual-install docs also name the file the verifier
  actually consumes, the allowed-signers entry at
  .github/release-keys/allowed_signers rather than the bare public key
  (release-signing.pub) beside it, pin it by commit SHA rather than the moving
  master branch, and note that a stock router ships dropbear (no ssh-keygen)
  and wget or uclient-fetch (no curl), so the manifest is verified on a trusted
  machine and the hash checked on the router. The dependency is now asserted
  from build metadata, not grepped text:
  tests/packaging/apk-install-resolution_test.sh expands packaging/Makefile
  with `make -pn` and requires ca-bundle in the package definition's DEPENDS,
  and builds a real `.ipk` with packaging/build-ipk.sh and asserts the
  `Depends:` field of the control file opkg consumes — both fail closed when
  the dependency is dropped or moved into a comment, where the previous
  literal-string greps could not tell the difference.
