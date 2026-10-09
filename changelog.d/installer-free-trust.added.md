- **Installing the package alone now brings TLS trust with it on a box that can
  still reach the feed, and the manual install has a stated verification
  chain.** Installing the shipped package by hand on a GL-MT3000 (OpenWrt
  25.12.5) failed at the first HTTPS fetch: wget and apk update both died with
  "SSL verify error: unknown error" because the image carried no CA store. This
  module does money over TLS — it talks to Cashu mints and Lightning with Go's
  system root pool and no insecure escape hatch — so a gateway that cannot
  verify TLS cannot take a payment. ca-bundle is now a declared dependency on
  every build lane instead of an assumption the installer used to paper over:
  the SDK recipe (packaging/Makefile) and the `.ipk` recipes
  (packaging/local-build-ipk.sh, the GitHub `.ipk` lane, the generated ngit
  shards) all declare it. That fixes an install on a box that can still reach
  the feed — apk/opkg resolves the closure and pulls ca-bundle with it. It does
  not, and cannot, bootstrap a box that has NO trust anchors at all: there the
  very first HTTPS fetch already fails verification, and no dependency can
  repair a fetch that must itself be verified. The README now states that
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
  ([#828](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/828)).
