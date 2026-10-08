- **Four drift fences now guard the contract estate (fragment identity,
  toolchain ceiling, SSID matcher table, clean-container suite).** Every
  `changelog.d/` fragment must name its own real PR or issue — the number in
  the filename, the same number in the body, no link to a foreign PR, no
  placeholder text (`check-changelog-fragments.py`; the numberless
  vm-campaign entry is repaired as #704's). Every `go.mod` `go` directive
  must stay at or below the manifest's pinned toolchain, and workflow
  `golang:` container images must match it (`check-toolchain-parity.py`).
  The SSID matcher's acceptance table — both brands, bare and
  single-`!`-decorated, case-insensitive, a double `!` rejected — is pinned
  by a committed fixture file shared by the Go test and
  `check-ssid-naming.sh` (the #618 and #706 break classes), which lands
  #706's reader-side tolerance ahead of its writer half. And a
  clean-container job runs the module suite in pristine
  `golang:1.26.8-bookworm`, where a failure means hidden host-state
  coupling — each failure gets its own issue, never a widened fence
  ([#767](https://github.com/OpenTollGate/tollgate-module-basic-go/issues/767)).
