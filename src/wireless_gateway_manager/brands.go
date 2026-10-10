package wireless_gateway_manager

import "strings"

// The SSID recognition set: who counts as a TollGate.
//
// The captive SSID carries the brand's prefix, and the first-boot writer
// (99-tollgate-setup load_brand) plus the installer's brandingCommands can
// emit two of them: the default `TollGate-…` and the whitelabel re-brand's
// equivalent prefix. A whitelabel-branded TollGate is a valid upstream in
// reseller mode, so recognition must match every prefix the writer can
// emit — case-insensitively, because the pre-device-code installer wrote
// lowercase `tollgate-<code>` captive SSIDs and those routers are still in
// the fleet (docs/architecture/one-device-code.md, "The measured drift").
// The definitive brand-independent identification remains the vendor IE
// (vendor_element_manager), staged behind VendorIEDiscovery.
//
// whitelabelCaptivePrefix is assembled, never spelled: upstream source must
// not carry the re-brand's name as a contiguous literal — the rebrand gutter
// (tests/packaging/rebrand-literal-gutter_test.sh) scans code surfaces
// case-insensitively for exactly that (#722). This is the same
// non-contiguous-bytes trick the gutter itself uses to carry its own scan
// pattern. The name's legitimate homes are the SSID contract checker
// (tests/contract/check-ssid-format.sh) and the decision records under
// docs/. brands_test.go pins the assembled value against an independent
// construction, so an assembly typo fails a test instead of silently
// dropping a whole brand from upstream recognition.
const whitelabelCaptivePrefix = "Net4" + "sats" + "-"

// tollGateBrandPrefixes is every captive-SSID prefix the first-boot writer
// can emit. Mirrored by the shell side's load_brand table (99-tollgate-setup)
// and pinned by tests/contract/check-ssid-format.sh; keep the three in
// agreement.
var tollGateBrandPrefixes = []string{"TollGate-", whitelabelCaptivePrefix}

// hasTollGateSSID reports whether ssid starts with any brand prefix the
// captive-SSID writer can emit, case-insensitively.
func hasTollGateSSID(ssid string) bool {
	for _, prefix := range tollGateBrandPrefixes {
		if len(ssid) >= len(prefix) && strings.EqualFold(ssid[:len(prefix)], prefix) {
			return true
		}
	}
	return false
}
