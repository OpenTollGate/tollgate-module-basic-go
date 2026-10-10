- **The Go suite no longer depends on an `ndsctl` being on the host's PATH.**
  The duplicate-guard harness's final row drives a grant through the real
  valve, and on a host with no `ndsctl` binary every gate open fails — the
  owed path answers grant-pending and the row goes red, so the battery's
  colour depended on the machine it ran on. The harness now stages the
  repository's one shipped fake (`tests/cloud-lab/fake-ndsctl.sh` — the same
  seam the cloud lab and happy-path harness use, deliberately not a second
  one) as `ndsctl` on the test's own PATH, with its auth/deauth log kept in
  the test's TempDir. No workflow in either CI system installs an `ndsctl`,
  and the GitHub Actions lane predates the test entirely — the suite is now
  host-independent and that question is closed
  ([#776](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/776)).
