package cli

import (
	"fmt"
	"runtime"
)

// Build information. These variables are set via -ldflags at build time.
var (
	// Version is the semantic version (e.g., "v0.0.4")
	Version = "v0.0.0"

	// GitCommit is the git commit hash
	GitCommit = "unknown"

	// BuildTime is the build timestamp
	BuildTime = "unknown"

	// GoVersion is the Go version used to build
	GoVersion = runtime.Version()
)

// GetVersionInfo returns a formatted version string
func GetVersionInfo() string {
	if Version == "dev" {
		return fmt.Sprintf("TollGate %s (commit: %s, built: %s, go: %s)",
			Version, GitCommit, BuildTime, GoVersion)
	}
	return fmt.Sprintf("TollGate %s", Version)
}

// GetFullVersionInfo returns detailed version information as a map
func GetFullVersionInfo() map[string]string {
	return map[string]string{
		"version":    Version,
		"commit":     GitCommit,
		"build_time": BuildTime,
		"go_version": GoVersion,
	}
}
