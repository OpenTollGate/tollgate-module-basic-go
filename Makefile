.PHONY: portal-build reproducibility-test reproducibility-variance go-battery release-check release-check-fast cold-battery manual-check package-ipk package-apk

# Package target defaults. Override on the command line:
#   make package-ipk ARCH=aarch64_cortex-a53
#   make package-apk SDK_TAG=mediatek-filogic-25.12.0
ARCH ?= x86_64
SDK_TAG ?= x86-64-25.12.0
ARTIFACT_DIR ?= /tmp/tollgate-artifacts

# Canonical pre-PR Go gate across ALL 16 modules (src/ is a multi-module
# tree: `go ... ./...` from src/ alone covers only the root module).
# Same set the go-test CI lane runs: gofmt, vet, build, race tests.
go-battery:
	@bash scripts/go-battery.sh

# One-command pre-release gate (docs/release-process.md): orchestrates the
# existing gates — go-battery, deps/imports, contract, packaging shell
# suites, the three fund-safety invariant tests, the conformance fast
# subset, the release-matrix cross-check, reproducibility and version
# consistency — and prints one unambiguous READY FOR HARDWARE verdict.
# TOLLGATE_RELEASE_CHECK_CONFORMANCE=1 makes the conformance subset
# mandatory; TOLLGATE_RELEASE_CHECK_REPRO=none skips the reproducibility
# leg (default: binaries/x86_64).
release-check:
	@bash scripts/release-check.sh "$(VERSION)"

# The per-push shape: every cheap leg of release-check (battery, deps,
# contract, packaging, the three invariant groups, version consistency)
# with reproducibility and conformance skipped — what CI runs on main.
release-check-fast:
	@TOLLGATE_RELEASE_CHECK_REPRO=none TOLLGATE_RELEASE_CHECK_SKIP_CONFORMANCE=1 \
	  bash scripts/release-check.sh "$(VERSION)"

portal-build:
	@bash packaging/portal-build.sh

# The user-facing build manual's executable check (docs/building-from-source.md):
# portal build + the all-module Go battery in a FRESH module cache. The cold
# cache is the gate that catches missing go.sum entries (#1036); a warm cache
# hides them.
manual-check:
	@bash scripts/manual-build-check.sh

# The cold-cache half of manual-check, for contributors who already have a
# portal build (or do not have the pinned Node installed).
cold-battery:
	@bash scripts/manual-build-check.sh --battery-only

# Full .ipk package (compiles binaries, stages the payload, packs it).
# portal-build is a prerequisite: a clean checkout has no built portal assets.
# The low-level packer (packaging/build-ipk.sh) expects a prepared payload and
# is deliberately NOT wrapped here.
package-ipk: portal-build
	@ARCH=$(ARCH) bash packaging/local-build-ipk.sh
	@ls -lh packaging/tollgate-wrt_*_$(ARCH).ipk | tail -1

# Full .apk package through the pinned OpenWrt SDK. SDK_TAG selects the target.
package-apk: portal-build
	@SDK_TAG=$(SDK_TAG) PACKAGE_FORMAT=apk ARTIFACT_DIR=$(ARTIFACT_DIR) \
	  bash scripts/build-sdk-package.sh

# Reproducibility check: build an artifact twice in independent clean roots
# and require byte-identical output. T = binaries | portal | ipk | ipk-upx | apk
# ARCH = x86_64 (default) | aarch64_cortex-a53 | arm_cortex-a7 | mips_24kc |
#        mipsel_24kc | aarch64_cortex-a72
reproducibility-test:
	@bash scripts/repro-test.sh "$(T)" "$(ARCH)"

reproducibility-test-default:
	@bash scripts/repro-test.sh binaries x86_64

# Variance check (reprotest): rebuild under hostile environment variations
# (umask, timezone, locales, file ordering) and require identical output —
# the complement to the clean-roots check above. Requires reprotest (pip).
reproducibility-variance:
	@bash scripts/repro-variance.sh "$(T)" "$(ARCH)"

# #947: strict pre/post-run hygiene for the happy-path suite (shared hosts).
# Fixture-proves the identification logic (own-tree kills, foreign refusals).
happy-path-hygiene-test:
	@bash tests/happy-path/hygiene_test.sh
