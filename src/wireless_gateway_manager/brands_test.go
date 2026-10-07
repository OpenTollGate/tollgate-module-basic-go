package wireless_gateway_manager

import (
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
		"Net4sats-OQ3Q", // whitelabel brand
		"tollgate-0GLK", // old-installer lowercase SSID, still deployed
		"NET4SATS-AA11", // case-insensitive whitelabel
		"TollGate-",     // bare prefix, code length is not this helper's concern
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
	}
	for _, ssid := range rejected {
		assert.False(t, hasTollGateSSID(ssid), "expected rejected: %s", ssid)
	}
}
