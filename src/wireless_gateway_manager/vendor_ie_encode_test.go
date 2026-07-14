package wireless_gateway_manager

import (
	"encoding/hex"
	"testing"
)

func TestEncodeDecodeRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		adv  TollGateAdvertisement
	}{
		{
			name: "minimal — version only",
			adv:  TollGateAdvertisement{Version: 1},
		},
		{
			name: "all flags set",
			adv: TollGateAdvertisement{
				Version:     2,
				IsReseller:  true,
				HasInternet: true,
				OpenNetwork: true,
			},
		},
		{
			name: "with mint URL",
			adv: TollGateAdvertisement{
				Version:  1,
				MintURL:  "https://testnut.cashu.exchange",
				HasInternet: true,
			},
		},
		{
			name: "with pubkey",
			adv: TollGateAdvertisement{
				Version:  1,
				Pubkey:   []byte{0x02, 0xab, 0xcd, 0xef},
				HasInternet: true,
			},
		},
		{
			name: "full — all fields",
			adv: TollGateAdvertisement{
				Version:     3,
				IsReseller:  true,
				HasInternet: true,
				OpenNetwork: false,
				MintURL:     "http://10.99.99.2:8385",
				Pubkey:      []byte{0x02, 0xaa, 0xbb, 0xcc, 0xdd, 0xee},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hexIE, err := EncodeTollGateVendorIE(tt.adv)
			if err != nil {
				t.Fatalf("EncodeTollGateVendorIE failed: %v", err)
			}

			raw, err := hex.DecodeString(hexIE)
			if err != nil {
				t.Fatalf("hex decode failed: %v", err)
			}

			parsed := ParseTollGateVendorIE(raw)
			if parsed == nil {
				t.Fatalf("ParseTollGateVendorIE returned nil for valid IE")
			}

			if parsed.Version != tt.adv.Version {
				t.Errorf("Version mismatch: got %d, want %d", parsed.Version, tt.adv.Version)
			}
			if parsed.IsReseller != tt.adv.IsReseller {
				t.Errorf("IsReseller mismatch: got %v, want %v", parsed.IsReseller, tt.adv.IsReseller)
			}
			if parsed.HasInternet != tt.adv.HasInternet {
				t.Errorf("HasInternet mismatch: got %v, want %v", parsed.HasInternet, tt.adv.HasInternet)
			}
			if parsed.OpenNetwork != tt.adv.OpenNetwork {
				t.Errorf("OpenNetwork mismatch: got %v, want %v", parsed.OpenNetwork, tt.adv.OpenNetwork)
			}
			if parsed.MintURL != tt.adv.MintURL {
				t.Errorf("MintURL mismatch: got %q, want %q", parsed.MintURL, tt.adv.MintURL)
			}
		})
	}
}

func TestEncodeRejectsOversizedBody(t *testing.T) {
	longURL := make([]byte, 260)
	for i := range longURL {
		longURL[i] = 'a'
	}
	_, err := EncodeTollGateVendorIE(TollGateAdvertisement{
		Version: 1,
		MintURL: string(longURL),
	})
	if err == nil {
		t.Error("expected error for body > 255 bytes, got nil")
	}
}

func TestParseRejectsInvalidOUI(t *testing.T) {
	// Valid IE structure but wrong OUI (00:00:00 instead of 21:21:21)
	raw := []byte{0xDD, 0x06, 0x00, 0x00, 0x00, 0x01, 0x01, 0x00}
	parsed := ParseTollGateVendorIE(raw)
	if parsed != nil {
		t.Error("expected nil for invalid OUI, got non-nil")
	}
}

func TestParseRejectsTooShort(t *testing.T) {
	raw := []byte{0xDD, 0x03, 0x21, 0x21}
	parsed := ParseTollGateVendorIE(raw)
	if parsed != nil {
		t.Error("expected nil for too-short IE, got non-nil")
	}
}

func TestParseRejectsWrongElementType(t *testing.T) {
	raw := []byte{0xDD, 0x06, 0x21, 0x21, 0x21, 0x02, 0x01, 0x00}
	parsed := ParseTollGateVendorIE(raw)
	if parsed != nil {
		t.Error("expected nil for wrong element type, got non-nil")
	}
}

func TestParseRespectsBodyLen(t *testing.T) {
	// Create a valid IE with body length that excludes trailing garbage
	adv := TollGateAdvertisement{Version: 1, HasInternet: true}
	hexIE, err := EncodeTollGateVendorIE(adv)
	if err != nil {
		t.Fatalf("encode failed: %v", err)
	}
	raw, _ := hex.DecodeString(hexIE)

	// Append garbage after the IE — parser must ignore it
	rawWithGarbage := append(raw, 0xFF, 0xFF, 0xFF, 0xFF)
	parsed := ParseTollGateVendorIE(rawWithGarbage)
	if parsed == nil {
		t.Fatal("expected non-nil for valid IE with trailing garbage")
	}
	if parsed.Version != 1 {
		t.Errorf("Version: got %d, want 1", parsed.Version)
	}
}
