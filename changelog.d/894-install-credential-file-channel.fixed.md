- **Install-time credentials now survive `apk add`.** The operator-chosen
  admin password and private-WiFi key are delivered through root-only
  staged secret files (`/tmp/tollgate-admin-password`,
  `/tmp/tollgate-wifi-key`), consumed — unlinked on first read — by the
  setup script: apk-tools on 25.12 does not hand the installer's
  environment to package scripts, so the previously documented
  `TOLLGATE_ADMIN_PASSWORD=… apk add …` flow silently degraded to a random
  provisional credential the operator never chose (found end-to-end on the
  vlab installer lane). Mode-600 is enforced with a `find -perm` gate —
  the minimal busybox on real router images ships without `stat`, which
  the first revision used and which refused every staging — and the
  environment channel remains for manual re-runs. The installer-contract
  lane (#892) asserts the whole contract: staged secret becomes root's
  hash (sha256crypt-verified), nothing plaintext in the install output,
  files consumed
  ([#894](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/894)).
