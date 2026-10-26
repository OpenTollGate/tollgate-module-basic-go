- **The release fold is a fence, not a memory.** Cutting a final
  release now requires the changelog fold to have happened: the section to
  exist, the fragments drained, and the notes to name the version
  (rc2-rc4 shipped by VERSION bump alone, leaving the fold to reconstruct
  at final). Pre-releases cut unfolded by design — warned, never blocked.
  Wired into `release-check` as the first gate; offline fixture suite with
  planted-drift verdicts included.
  ([#916](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/916)).
