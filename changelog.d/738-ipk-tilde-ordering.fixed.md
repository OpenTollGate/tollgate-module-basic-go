- **ipk package versions use Debian pre-release ordering.** The ipk lane
  shipped the raw tag (`v0.6.0-rc1`) as the package version: opkg's
  comparator sorts a `-rc1` suffix AFTER the bare release, so a device on
  rc1 would refuse v0.6.0-final as a downgrade, and the leading `v` has
  no defined ordering at all. `packaging/normalize-ipk-version.sh` (the
  ipk twin of the apk normalizer) strips the `v` and re-spells
  `-alpha/-beta/-rc/-pre` as `~alpha/~beta/~rc/~pre`, which sorts
  before the release — the upgrade topology alpha < beta < rc < final
  that the tester fleet needs. rc1's sealed artifacts keep the raw
  scheme; the rc1→final transition on opkg devices needs
  `opkg install --force-downgrade` once, which the bench upgrade lane
  should measure
  ([#738](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/738)).
