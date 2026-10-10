package wireless_gateway_manager

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The pinned acceptance table for hasTollGateSSID: ONE file, read by this
// test (the matcher's behaviour) and by tests/contract/check-ssid-naming.sh
// (the table's structure and wiring). The classes it pins are the two
// measured SSID recognition breaks: the brand-blind matcher (#618, re-landed
// as #682) and the single-'!' writer decoration (#706).
const ssidFixturePath = "../../tests/contract/ssid-naming-fixtures.txt"

func TestHasTollGateSSIDRecognitionSet(t *testing.T) {
	data, err := os.ReadFile(ssidFixturePath)
	if err != nil {
		t.Fatalf("SSID fixture table unreadable: %v — the pinned recognition contract is missing", err)
	}
	matches, rejects := 0, 0
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		verdict, ssid, _ := strings.Cut(line, " ")
		switch verdict {
		case "match":
			matches++
			assert.True(t, hasTollGateSSID(ssid), "expected recognized: %q", ssid)
		case "nomatch":
			rejects++
			assert.False(t, hasTollGateSSID(ssid), "expected rejected: %q", ssid)
		default:
			t.Fatalf("fixture line %q is neither 'match' nor 'nomatch' — the table is malformed", line)
		}
	}
	if matches < 8 || rejects < 8 {
		t.Fatalf("fixture table too thin (%d match, %d reject) — required classes are missing", matches, rejects)
	}
}

// The whitelabel entry's value, pinned against an assembly independent of
// brands.go's: the gutter keeps the name out of source, so this pin is what
// turns an assembly typo into a test failure instead of a silently
// unrecognized upstream brand.
func TestWhitelabelCaptivePrefixValue(t *testing.T) {
	assert.Equal(t, "Net4"+"sats-", whitelabelCaptivePrefix)
	assert.Contains(t, tollGateBrandPrefixes, whitelabelCaptivePrefix)
}
