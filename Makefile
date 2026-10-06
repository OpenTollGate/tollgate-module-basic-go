.PHONY: portal-build reproducibility-test reproducibility-variance go-battery release-check release-check-fast

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
