package utils

import (
	"testing"
)

func TestValidateMACAddress(t *testing.T) {
	tests := []struct {
		name     string
		mac      string
		expected bool
	}{
		// Valid MAC addresses
		{"Valid colon format", "00:1A:2B:3C:4D:5E", true},
		{"Valid hyphen format", "00-1A-2B-3C-4D-5E", true},
		{"Valid no separator format", "001A2B3C4D5E", true},
		{"Valid lowercase colon format", "aa:bb:cc:dd:ee:ff", true},
		{"Valid mixed case colon format", "Aa:bB:Cc:dD:Ee:Ff", true},

		// Invalid MAC addresses
		{"Empty string", "", false},
		{"Invalid character", "ZZ:1A:2B:3C:4D:5E", false},
		{"Too short colon format", "00:1A:2B:3C:4D", false},
		{"Too long colon format", "00:1A:2B:3C:4D:5E:6F", false},
		{"Invalid separator", "00*1A*2B*3C*4D*5E", false},
		{"Mixed separators", "00:1A-2B:3C-4D:5E", false},
		{"Missing separator", "001A:2B:3C:4D:5E", false},
		{"Invalid format (only numbers)", "123456789012", false},
		{"Whitespace in MAC", "00:1A:2B 3C:4D:5E", false},
		{"With whitespace around", "  00:1A:2B:3C:4D:5E  ", true}, // Should pass due to trim
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateMACAddress(tt.mac); got != tt.expected {
				t.Errorf("ValidateMACAddress(%q) = %v, want %v", tt.mac, got, tt.expected)
			}
		})
	}
}
