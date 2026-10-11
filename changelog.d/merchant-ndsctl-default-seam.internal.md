- **The merchant battery no longer depends on the host's `ndsctl`.** A
  merchant test whose assertion walks a successful grant
  (`PurchaseSession` → `grantSessionAccess` → `valve.OpenGate` → `exec
  ndsctl`) needed an answering binary that nothing defaulted: on bare
  hosts the grant died as `payment-received-grant-pending` noise (one
  instance fixed in #835; the class is #853), while a host carrying an
  answerless `exit 0` stub let the auth pass with `ndsctl json` reads
  degrading silently — green by accident of the host. A `TestMain` in
  `src/merchant` now stages the seam the harnesses already share —
  `tests/cloud-lab/fake-ndsctl.sh`, the drop-in `tests/happy-path/run.sh`
  reuses on purpose — into a package temp bin dir prepended to PATH, with
  `NDSCTL_LOG` pointed inside that dir per the fake's logging contract.
  The staging is unconditional on purpose: a present-but-answerless host
  binary is exactly the masquerade, and tests that script their own ndsctl
  behavior keep shadowing the seam via their per-test `t.Setenv` prepend.
  A canary (`TestDefaultNdsctlSeamAnswersGrants`) fails the day the seam
  disappears or stops answering grants, distinguishing all three shapes —
  bare host, masquerade stub, working seam.
  ([#853](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/853))
