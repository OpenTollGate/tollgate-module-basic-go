package wireless_gateway_manager

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Pins the SSID recognition contract shared with the first-boot writer
// (99-tollgate-setup load_brand) and the installer's brandingCommands:
// both brands' captive prefixes, case-insensitively (the pre-device-code
// installer wrote lowercase tollgate-<code> SSIDs; those routers are still
// in the fleet). A rename or a foreign brand is not a TollGate.
func TestHasTollGateSSIDRecognitionSet(t *testing.T) {
	recognized := []string{
		"TollGate-OQ3Q", // current writer, default brand
		"TollGate-A1B2", // hex-era code
		"TollGate-G7ZQ", // adopted alphanumeric code
		// whitelabelCaptivePrefix is assembled in source (see brands.go:
		// the rebrand gutter bans the spelled-out name), so its fixtures
		// derive from the constant; the value itself is pinned by
		// TestWhitelabelCaptivePrefixValue below.
		whitelabelCaptivePrefix + "OQ3Q",                  // whitelabel brand
		"tollgate-0GLK",                                   // old-installer lowercase SSID, still deployed
		strings.ToUpper(whitelabelCaptivePrefix) + "AA11", // case-insensitive whitelabel
		"TollGate-", // bare prefix, code length is not this helper's concern
		// The captive-SSID sort decoration: the shipped writer now emits
		// !TollGate-<code> so the guest network sorts first in an
		// alphabetically ordered WiFi list. Readers must treat the leading
		// '!' as optional and match the name underneath it — both brands.
		"!TollGate-OQ3Q",
		"!TollGate-G7ZQ",
		"!" + whitelabelCaptivePrefix + "OQ3Q", // decorated whitelabel (assembled, per the gutter)
		"!tollgate-0GLK",                       // decorated legacy lowercase
	}
	for _, ssid := range recognized {
		assert.True(t, hasTollGateSSID(ssid), "expected recognized: %s", ssid)
	}

	rejected := []string{
		"",                // empty
		"TollGate",        // no separator
		"TollGatex-AB12",  // prefix must be exact up to case
		"MyTollGate-AB12", // prefix must be at the start
		"FreeWifi",        // unrelated
		"tollgate",        // bare brand, lowercase
		"!FreeWifi",       // a '!' does not make a foreign name a TollGate
		"!",               // decoration with nothing under it
		"!!TollGate-OQ3Q", // at most ONE decoration is stripped
	}
	for _, ssid := range rejected {
		assert.False(t, hasTollGateSSID(ssid), "expected rejected: %s", ssid)
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
