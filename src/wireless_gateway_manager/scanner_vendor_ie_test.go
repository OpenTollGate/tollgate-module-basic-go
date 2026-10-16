package wireless_gateway_manager

import (
	"strings"
	"testing"
)

// The vendor-IE producer wiring (issue: PricePerStep/StepSize were staged
// zero at the scan boundary — scanner.go's "STAGED (this PR)" block — and
// had no producer). These tests pin the producer: given raw scan bytes that
// carry a TollGate vendor IE, ParseIwinfoOutput must populate IsTollGate,
// RawIEs and — when the IE carries pricing TLVs — PricePerStep/StepSize.
//
// RED first: before the producer is wired, ParseIwinfoOutput hard-omits
// these fields, so every assertion below fails with a zero value.

// ieHex renders a raw IE byte slice as the hex blob iwinfo/iw emit.
func ieHex(b []byte) string {
	const hexdigits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, hexdigits[c>>4], hexdigits[c&0x0f])
	}
	return string(out)
}

// buildTollGateIE assembles a TollGate vendor IE with the same shape the
// emitter produces: OUI 212121 | elemType 01 | version | flags | TLVs.
func buildTollGateIE(version, flags byte, mintURL string, price, step uint16) []byte {
	body := []byte{0x21, 0x21, 0x21, 0x01, version, flags}

	// tlvTypeMintURL (0x01) — len-prefixed string.
	if mintURL != "" {
		body = append(body, 0x01, byte(len(mintURL)))
		body = append(body, []byte(mintURL)...)
	}
	// tlvTypePricePerStep (0x03) — 2-byte big-endian, per the emitter's
	// TLV extension for pricing.
	if price > 0 {
		body = append(body, 0x03, 0x02, byte(price>>8), byte(price))
	}
	// tlvTypeStepSize (0x04) — 2-byte big-endian.
	if step > 0 {
		body = append(body, 0x04, 0x02, byte(step>>8), byte(step))
	}

	return append([]byte{0xDD, byte(len(body))}, body...)
}

// iwinfoScanBlock renders one iwinfo scan entry. iwinfo's human output has
// no IE field, so the raw IE is surfaced the way `iwinfo <dev> scan` and
// iw do: a trailing "IE: <hex>" line summarised from the beacon.
func iwinfoScanBlock(bssid, ssid string, signal int, enc string, ie []byte) string {
	var b strings.Builder
	b.WriteString("Cell 01 - Address: " + bssid + "\n")
	b.WriteString("          ESSID: \"" + ssid + "\"\n")
	b.WriteString("          Mode: Master  Channel: 36\n")
	b.WriteString("          Signal: " + itoa(signal) + " dBm  Quality: 70/70\n")
	b.WriteString("          Encryption: " + enc + "\n")
	if len(ie) > 0 {
		b.WriteString("          IE: " + ieHex(ie) + "\n")
	}
	return b.String()
}

func itoa(i int) string {
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
	if neg {
		return "-" + string(d)
	}
	return string(d)
}

// TestParseIwinfoOutput_PopulatesVendorIEFields is the core RED test: a
// TollGate IE in the scan bytes must set IsTollGate, retain the raw IE, and
// populate the pricing fields from the IE's TLVs.
func TestParseIwinfoOutput_PopulatesVendorIEFields(t *testing.T) {
	ie := buildTollGateIE(1, flagHasInternet|flagOpenNetwork, "https://mint.example", 7, 1000)
	out := []byte(iwinfoScanBlock("aa:bb:cc:dd:ee:01", "!TollGate-AB12", -55, "none", ie))

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio1")

	if len(nets) != 1 {
		t.Fatalf("want 1 network, got %d", len(nets))
	}
	n := nets[0]

	if !n.IsTollGate {
		t.Errorf("IsTollGate = false, want true (IE with OUI %s present)", tollgateOUI)
	}
	if len(n.RawIEs) == 0 {
		t.Errorf("RawIEs empty, want the raw IE bytes retained")
	}
	if n.PricePerStep != 7 {
		t.Errorf("PricePerStep = %d, want 7 (from tlvTypePricePerStep)", n.PricePerStep)
	}
	if n.StepSize != 1000 {
		t.Errorf("StepSize = %d, want 1000 (from tlvTypeStepSize)", n.StepSize)
	}
}

// TestParseIwinfoOutput_NonTollGateIEIgnored — a foreign vendor IE (any
// other OUI) must NOT mark the network as a TollGate.
func TestParseIwinfoOutput_NonTollGateIEIgnored(t *testing.T) {
	foreign := []byte{0xDD, 0x05, 0x00, 0x11, 0x22, 0x33, 0x01}
	out := []byte(iwinfoScanBlock("aa:bb:cc:dd:ee:02", "SomeOtherAP", -60, "psk2", foreign))

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio1")
	if len(nets) != 1 {
		t.Fatalf("want 1 network, got %d", len(nets))
	}
	if nets[0].IsTollGate {
		t.Errorf("IsTollGate = true for a foreign OUI %s, want false", "001122")
	}
	if nets[0].PricePerStep != 0 || nets[0].StepSize != 0 {
		t.Errorf("pricing populated from a foreign IE: price=%d step=%d",
			nets[0].PricePerStep, nets[0].StepSize)
	}
}

// TestParseIwinfoOutput_TollGateIEWithoutPricingTLVs — identification and
// pricing are separable. An IE carrying flags but no price TLVs must still
// identify the TollGate, leaving pricing at zero rather than failing.
func TestParseIwinfoOutput_TollGateIEWithoutPricingTLVs(t *testing.T) {
	ie := buildTollGateIE(1, flagHasInternet, "", 0, 0)
	out := []byte(iwinfoScanBlock("aa:bb:cc:dd:ee:03", "!TollGate-CC33", -48, "none", ie))

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio1")
	if len(nets) != 1 {
		t.Fatalf("want 1 network, got %d", len(nets))
	}
	if !nets[0].IsTollGate {
		t.Errorf("IsTollGate = false, want true — identification does not require pricing TLVs")
	}
	if nets[0].PricePerStep != 0 || nets[0].StepSize != 0 {
		t.Errorf("pricing should stay zero without pricing TLVs, got price=%d step=%d",
			nets[0].PricePerStep, nets[0].StepSize)
	}
}

// TestParseIwinfoOutput_NoIEMeansNotTollGate — the absence of an IE must not
// set IsTollGate. This guards the scan boundary against over-claiming.
func TestParseIwinfoOutput_NoIEMeansNotTollGate(t *testing.T) {
	out := []byte(iwinfoScanBlock("aa:bb:cc:dd:ee:04", "!TollGate-DD44", -70, "none", nil))

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio1")
	if len(nets) != 1 {
		t.Fatalf("want 1 network, got %d", len(nets))
	}
	if nets[0].IsTollGate {
		t.Errorf("IsTollGate = true with no IE present — SSID prefix alone is a weak " +
			"signal and must not be promoted to the definitive identification")
	}
}

// TestParseIwinfoOutput_MultipleNetworksAndIEs — the producer must attach the
// IE to the right network, not leak it across entries.
func TestParseIwinfoOutput_MultipleNetworksAndIEs(t *testing.T) {
	tgIE := buildTollGateIE(1, flagHasInternet, "", 3, 500)
	plain := iwinfoScanBlock("aa:bb:cc:dd:ee:10", "Neighbour-5G", -65, "psk2", nil)
	tg := iwinfoScanBlock("aa:bb:cc:dd:ee:11", "!TollGate-EE55", -50, "none", tgIE)
	out := []byte(plain + tg)

	s := &Scanner{}
	nets := s.ParseIwinfoOutput(out, "radio1")
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
			if n.PricePerStep != 3 || n.StepSize != 500 {
				t.Errorf("pricing misplaced: price=%d step=%d, want 3/500",
					n.PricePerStep, n.StepSize)
			}
		}
	}
	if found != 1 {
		t.Errorf("TollGate network matched %d times, want 1", found)
	}
}
