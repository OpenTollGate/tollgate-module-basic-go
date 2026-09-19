package cli

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

// Build information. All of these are injected with -ldflags at build time.
var (
	// Version is the release version (e.g. "v0.6.0-alpha2"). The single
	// source of truth is the VERSION file at the repository root: every
	// packaging path injects it here with -X, and the git tag must equal
	// it (CI enforces that on tag builds). The literal below is only the
	// fallback for a plain `go build` outside those paths — it is
	// deliberately not a release string, so a mis-built binary can never
	// pass itself off as a release. See CONTRIBUTING.md, "Version single source
	// of truth"; scripts/check-version-sync.sh fails the tree if a
	// version literal reappears here.
	Version = "dev"

	// GitCommit is the git commit hash
	GitCommit = "unknown"

	// BuildTime is the build timestamp
	BuildTime = "unknown"

	// GoVersion is the Go version used to build
	GoVersion = runtime.Version()
)

// getOpenWrtVersion reads the OpenWrt version from /etc/openwrt_release
func getOpenWrtVersion() string {
	data, err := os.ReadFile("/etc/openwrt_release")
	if err != nil {
		return "unknown"
	}

	// Parse the file to extract DISTRIB_DESCRIPTION
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "DISTRIB_DESCRIPTION=") {
			// Extract value between quotes
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				value := strings.Trim(parts[1], "'\"")
				return value
			}
		}
	}

	return "unknown"
}

// GetVersionInfo returns a formatted version string
func GetVersionInfo() string {
	return fmt.Sprintf("TollGate %s", Version)
}

// GetFullVersionInfo returns detailed version information as a map
func GetFullVersionInfo() map[string]string {
	return map[string]string{
		"version":         Version,
		"commit":          GitCommit,
		"build_time":      BuildTime,
		"go_version":      GoVersion,
		"openwrt_version": getOpenWrtVersion(),
	}
}

// GetFormattedVersionInfo returns a formatted multi-line version string
func GetFormattedVersionInfo() string {
	return fmt.Sprintf(`TollGate Version
version: %s
commit: %s
build_time: %s
go_version: %s
openwrt_version: %s`,
		Version, GitCommit, BuildTime, GoVersion, getOpenWrtVersion())
}
