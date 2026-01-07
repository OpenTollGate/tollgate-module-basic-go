package utils

import (
	"fmt"
	"regexp"
	"strings"
)

// BytesToHumanReadable converts a byte count to a human-readable string (KB, MB, GB).
func BytesToHumanReadable(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ValidateMACAddress checks if a string is a valid MAC address format.
// Supports formats: XX:XX:XX:XX:XX:XX, XX-XX-XX-XX-XX-XX, XXXXXXXXXXXX
// Returns true if the MAC address is valid, false otherwise.
func ValidateMACAddress(mac string) bool {
	// Normalize the MAC address to uppercase
	mac = strings.ToUpper(strings.TrimSpace(mac))

	// If empty, it's not valid
	if mac == "" {
		return false
	}

	// Check colon-separated format (XX:XX:XX:XX:XX:XX)
	colonPattern := regexp.MustCompile(`^([0-9A-F]{2}[:]){5}([0-9A-F]{2})$`)
	if colonPattern.MatchString(mac) {
		return true
	}

	// Check hyphen-separated format (XX-XX-XX-XX-XX-XX)
	hyphenPattern := regexp.MustCompile(`^([0-9A-F]{2}[-]){5}([0-9A-F]{2})$`)
	if hyphenPattern.MatchString(mac) {
		return true
	}

	// Check no separator format (XXXXXXXXXXXX)
	// A valid MAC address should have at least one hex digit (A-F)
	// to distinguish it from a purely numeric identifier
	noSeparatorPattern := regexp.MustCompile(`^[0-9A-F]{12}$`)
	if noSeparatorPattern.MatchString(mac) && regexp.MustCompile(`[A-F]`).MatchString(mac) {
		return true
	}

	// If none of the patterns match, it's not valid
	return false
}
