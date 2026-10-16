package wireless_gateway_manager

import (
	"strings"
	"testing"
)

// The vendor-IE producer wiring: PricePerStep/StepSize were staged zero at the
// scan boundary (scanner.go's "STAGED (this PR)" block) and had no producer.
// These tests pin the producer against the format the router ACTUALLY emits.
//
// MEASURED ON HARDWARE (tollgate-A04E, 2026-10-16): neither `iwinfo <dev> scan`
// nor `iw dev <dev> scan` prints a raw hex "IE:" blob. iwinfo prints
// signal/ENC/rate and NO elements at all; iw prints each element DECODED into
// named fields ("SSID:", "RSN:", "HT capabilities:", …). So a hex-IE parser
// would be dead code on this platform. The producer therefore reads the
// decoded named fields — including a vendor element printed as
// "Vendor specific:" with a colon-separated hex body.

// iwScanBlock renders one `iw dev <dev> scan` BSS entry in the real shape:
// tab-indented named fields, with information elements decoded into names.
func iwScanBlock(bssid, ssid string, signal int, vendorHex string) string {
	var b strings.Builder
	b.WriteString("BSS " + bssid + "(on phy0-ap0)\n")
	b.WriteString("\tlast seen: 1480 ms ago\n")
	b.WriteString("\tfreq: 2412.0\n")
	b.WriteString("\tsignal: " + ftoa(signal) + " dBm\n")
	b.WriteString("\tcapability: ESS (0x1431)\n")
	b.WriteString("\tInformation elements from Probe Response frame:\n")
	if ssid != "" {
		b.WriteString("\t\tSSID: " + ssid + "\n")
	}
	b.WriteString("\t\tRSN:\t * Version: 1\n")
	b.WriteString("\t\t\t * Group cipher: CCMP\n")
	b.WriteString("\t\tHT capabilities:\n")
	b.WriteString("\t\t\tCapabilities: 0x1ad\n")
	if vendorHex != "" {
		b.WriteString("\t\tVendor specific: " + vendorHex + "\n")
	}
	return b.String()
}

func ftoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var d []byte
	for i > 0 {
		d = append([]byte{byte('0' + i%10)}, d...)
		i /= 10
	}
	s := string(d)
	if neg {
		return "-" + s
	}
	return s
}

// vendorHex renders a TollGate vendor element body the way `iw` prints a
// "Vendor specific:" element: colon-separated hex of OUI | type | payload.
func vendorTollGateHex(version, flags byte, mintURL string, price, step uint16) string {
	body := []byte{0x21, 0x21, 0x21, 0x01, version, flags}
	if mintURL != "" {
		body = append(body, 0x01, byte(len(mintURL)))
		body = append(body, []byte(mintURL)...)
	}
	if price > 0 {
		body = append(body, 0x03, 0x02, byte(price>>8), byte(price))
	}
	if step > 0 {
		body = append(body, 0x04, 0x02, byte(step>>8), byte(step))
	}
	parts := make([]string, 0, len(body))
	for _, c := range body {
		parts = append(parts, hexByte(c))
	}
	return strings.Join(parts, ":")
}

func hexByte(c byte) string {
	const d = "0123456789abcdef"
	return string([]byte{d[c>>4], d[c&0x0f]})
}

// TestParseIWScanOutput_PopulatesVendorIEFields is the core test: a TollGate
// vendor element in real `iw` output must set IsTollGate and populate the
// pricing fields from its TLVs.
func TestParseIWScanOutput_PopulatesVendorIEFields(t *testing.T) {
	vh := vendorTollGateHex(1, flagHasInternet|flagOpenNetwork, "https://mint.example", 7, 1000)
	out := []byte(iwScanBlock("94:83:c4:d1:57:59", "!TollGate-A04E", -55, vh))

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio0")

	if len(nets) != 1 {
		t.Fatalf("want 1 network, got %d", len(nets))
	}
	n := nets[0]

	if !n.IsTollGate {
		t.Errorf("IsTollGate = false, want true (vendor element with OUI %s present)", tollgateOUI)
	}
	if n.AdvertisedPricePerStep != 7 {
		t.Errorf("AdvertisedPricePerStep = %d, want 7 (from tlvTypePricePerStep)", n.AdvertisedPricePerStep)
	}
	if n.AdvertisedStepSize != 1000 {
		t.Errorf("AdvertisedStepSize = %d, want 1000 (from tlvTypeStepSize)", n.AdvertisedStepSize)
	}
}

// TestParseIWScanOutput_NonTollGateVendorIgnored — a foreign OUI must NOT mark
// the network as a TollGate.
func TestParseIWScanOutput_NonTollGateVendorIgnored(t *testing.T) {
	out := []byte(iwScanBlock("aa:bb:cc:dd:ee:02", "SomeOtherAP", -60, "00:11:22:33:01"))

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio0")
	if len(nets) != 1 {
		t.Fatalf("want 1 network, got %d", len(nets))
	}
	if nets[0].IsTollGate {
		t.Errorf("IsTollGate = true for foreign OUI 001122, want false")
	}
	if nets[0].AdvertisedPricePerStep != 0 || nets[0].AdvertisedStepSize != 0 {
		t.Errorf("pricing populated from a foreign element: price=%d step=%d",
			nets[0].AdvertisedPricePerStep, nets[0].AdvertisedStepSize)
	}
}

// TestParseIWScanOutput_TollGateWithoutPricingTLVs — identification and pricing
// are separable; flags-only must still identify.
func TestParseIWScanOutput_TollGateWithoutPricingTLVs(t *testing.T) {
	vh := vendorTollGateHex(1, flagHasInternet, "", 0, 0)
	out := []byte(iwScanBlock("aa:bb:cc:dd:ee:03", "!TollGate-CC33", -48, vh))

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio0")
	if len(nets) != 1 {
		t.Fatalf("want 1 network, got %d", len(nets))
	}
	if !nets[0].IsTollGate {
		t.Errorf("IsTollGate = false, want true — identification needs no pricing TLV")
	}
	if nets[0].AdvertisedPricePerStep != 0 || nets[0].AdvertisedStepSize != 0 {
		t.Errorf("pricing should stay zero without pricing TLVs, got %d/%d",
			nets[0].AdvertisedPricePerStep, nets[0].AdvertisedStepSize)
	}
}

// TestParseIWScanOutput_NoVendorElementMeansNotTollGate — absence of a vendor
// element must not set IsTollGate, even for a TollGate-prefixed SSID.
func TestParseIWScanOutput_NoVendorElementMeansNotTollGate(t *testing.T) {
	out := []byte(iwScanBlock("aa:bb:cc:dd:ee:04", "!TollGate-DD44", -70, ""))

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio0")
	if len(nets) != 1 {
		t.Fatalf("want 1 network, got %d", len(nets))
	}
	if nets[0].IsTollGate {
		t.Errorf("IsTollGate = true with no vendor element — the SSID prefix is a weak " +
			"signal and must not be promoted to definitive identification")
	}
}

// TestParseIWScanOutput_RealHardwareHiddenSSID — measured on A04E: `iw` prints
// "SSID: unknown" for an AP whose SSID is not in the frame. That is a literal
// string, not a hidden-SSID marker, and must not become a network named
// "unknown" that then competes in upstream selection.
func TestParseIWScanOutput_RealHardwareHiddenSSID(t *testing.T) {
	out := []byte(iwScanBlock("34:19:4d:da:c7:6c", "unknown", -90, ""))

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio0")
	for _, n := range nets {
		if n.SSID == "unknown" {
			t.Errorf("network named %q was admitted; a parsed placeholder must not "+
				"become a selectable upstream", n.SSID)
		}
	}
}

// TestParseIWScanOutput_MultipleNetworksAndVendorElements — the element must
// attach to the right BSS, not leak across entries.
func TestParseIWScanOutput_MultipleNetworksAndVendorElements(t *testing.T) {
	tgVH := vendorTollGateHex(1, flagHasInternet, "", 3, 500)
	plain := iwScanBlock("aa:bb:cc:dd:ee:10", "Neighbour-2G", -65, "")
	tg := iwScanBlock("aa:bb:cc:dd:ee:11", "!TollGate-EE55", -50, tgVH)
	out := []byte(plain + tg)

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio0")
	if len(nets) != 2 {
		t.Fatalf("want 2 networks, got %d", len(nets))
	}

	var found int
	for _, n := range nets {
		if n.BSSID == "aa:bb:cc:dd:ee:10" && n.IsTollGate {
			t.Errorf("non-TollGate network marked as TollGate")
		}
		if n.BSSID == "aa:bb:cc:dd:ee:11" {
			found++
			if !n.IsTollGate {
				t.Errorf("TollGate network not identified")
			}
			if n.AdvertisedPricePerStep != 3 || n.AdvertisedStepSize != 500 {
				t.Errorf("pricing misplaced: %d/%d, want 3/500", n.AdvertisedPricePerStep, n.AdvertisedStepSize)
			}
		}
	}
	if found != 1 {
		t.Errorf("TollGate network matched %d times, want 1", found)
	}
}
