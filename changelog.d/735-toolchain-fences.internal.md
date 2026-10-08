- **Go toolchain fences are now a checked contract.** The two-era SDK
  doctrine pins one host toolchain for both eras while `go get` and
  dependabot only see go.mod — `tests/contract/check-toolchain-fences.py`
  (run by `scripts/check-version-sync.sh`) fails on `toolchain`
  directives, on go directives disagreeing across the tree, and on any
  directive above the pinned toolchain. A first `.github/dependabot.yml`
  keeps version updates off and groups security-update PRs across every
  module
  ([#735](https://github.com/OpenTollGate/tollgate-module-basic-go/pull/735)).
