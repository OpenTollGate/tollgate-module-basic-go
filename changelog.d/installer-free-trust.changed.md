- **Installing the package alone now brings TLS trust with it, and the manual
  install has a stated verification chain.** Installing the shipped package by
  hand on a GL-MT3000 (OpenWrt 25.12.5) failed at the first HTTPS fetch: wget
  and apk update both died with "SSL verify error: unknown error" because the
  image carried no CA store. This module's job is money over TLS — it talks to
  Cashu mints and Lightning with Go's system root pool and no insecure escape
  hatch — so a gateway without a CA bundle cannot take a payment. ca-bundle is
  now a declared dependency rather than an assumption the installer used to
  paper over. The docs also now state the chain that makes an unsigned artifact
  safe to install by hand: the release's SHA256SUMS is signed (SSHSIG, ed25519,
  public half at .github/release-keys/release-signing.pub), so verify that
  signature first, then check the artifact hash, then install with
  --allow-untrusted — the artifact carries no apk-level signature, which is
  exactly why the manifest must be verified first. On a stock router ssh-keygen
  is absent (dropbear), so the manifest is verified on a trusted machine and
  the hash checked on the router. tests/packaging/apk-install-resolution_test.sh
  now asserts these declarations from source (before its docker gate), so the
  CA dependency and the documented chain cannot rot silently.
