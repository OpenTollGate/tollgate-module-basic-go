.PHONY: portal-build reproducibility-test reproducibility-variance go-battery

# Canonical pre-PR Go gate across ALL 16 modules (src/ is a multi-module
# tree: `go ... ./...` from src/ alone covers only the root module).
# Same set the go-test CI lane runs: gofmt, vet, build, race tests.
go-battery:
	@bash scripts/go-battery.sh

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
