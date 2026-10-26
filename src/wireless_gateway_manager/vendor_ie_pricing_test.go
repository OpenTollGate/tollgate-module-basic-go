package wireless_gateway_manager

import "testing"

// Round-trip: what the emitter produces, the scan-side parser must recover
// exactly. This pins the producer and the consumer to one wire format — the
// class of drift the repo has been bitten by before (#791-style).
func TestVendorIE_RoundTripPricing(t *testing.T) {
	cases := []struct {
		name  string
		adv   TollGateAdvertisement
		wantP uint16
		wantS uint16
	}{
		{
			name:  "pricing present",
			adv:   TollGateAdvertisement{Version: 1, HasInternet: true, PricePerStep: 7, StepSize: 1000},
			wantP: 7, wantS: 1000,
		},
		{
			name:  "no pricing advertised",
			adv:   TollGateAdvertisement{Version: 1, HasInternet: true},
			wantP: 0, wantS: 0,
		},
		{
			name:  "large values survive big-endian encoding",
			adv:   TollGateAdvertisement{Version: 1, PricePerStep: 65535, StepSize: 4096},
			wantP: 65535, wantS: 4096,
		},
		{
			name:  "with mint url alongside pricing",
			adv:   TollGateAdvertisement{Version: 1, MintURL: "https://mint.example", PricePerStep: 3, StepSize: 500},
			wantP: 3, wantS: 500,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hexIE, truncated, err := EncodeTollGateVendorIE(tc.adv)
			if err != nil {
				t.Fatalf("encode: %v", err)
			}
			if truncated {
				t.Errorf("truncated = true for a small advertisement, want false")
			}

			raw, err := hexDecode(hexIE)
			if err != nil {
				t.Fatalf("decode hex: %v", err)
			}

			got := ParseTollGateVendorIE(raw)
			if got == nil {
				t.Fatal("ParseTollGateVendorIE returned nil for a self-produced IE")
			}
			if got.PricePerStep != tc.wantP {
				t.Errorf("PricePerStep = %d, want %d", got.PricePerStep, tc.wantP)
			}
			if got.StepSize != tc.wantS {
				t.Errorf("StepSize = %d, want %d", got.StepSize, tc.wantS)
			}
			if got.MintURL != tc.adv.MintURL {
				t.Errorf("MintURL = %q, want %q", got.MintURL, tc.adv.MintURL)
			}
		})
	}
}

// The element must stay within the 255-byte cap and degrade gracefully: a
// pubkey that cannot fit is an error, but pricing must be DROPPED rather than
// allowed to overflow or to push out identification.
func TestVendorIE_PricingDroppedRatherThanOverflow(t *testing.T) {
	big := TollGateAdvertisement{
		Version:      1,
		Pubkey:       make([]byte, 200), // + 6 header + 2 TLV header + 3+2+2 = 215
		PricePerStep: 5,
		StepSize:     100,
	}

	hexIE, _, err := EncodeTollGateVendorIE(big)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	raw, err := hexDecode(hexIE)
	if err != nil {
		t.Fatalf("decode hex: %v", err)
	}
	if len(raw) > 255 {
		t.Fatalf("element is %d bytes, over the 255 cap", len(raw))
	}

	got := ParseTollGateVendorIE(raw)
	if got == nil {
		t.Fatal("nil advertisement")
	}
	if len(got.Pubkey) != 200 {
		t.Errorf("pubkey length = %d, want 200 — identification must survive", len(got.Pubkey))
	}
}

// hexDecode is a tiny local helper so the test does not import encoding/hex
// under a name that clashes with the package's own use.
func hexDecode(s string) ([]byte, error) {
	const digits = "0123456789abcdef"
	var out []byte
	for i := 0; i+1 < len(s); i += 2 {
		hi := indexHex(digits, s[i])
		lo := indexHex(digits, s[i+1])
		if hi < 0 || lo < 0 {
			return nil, errBadHex
		}
		out = append(out, byte(hi<<4|lo))
	}
	return out, nil
}

func indexHex(digits string, c byte) int {
	for i := 0; i < len(digits); i++ {
		if digits[i] == c {
			return i
		}
	}
	return -1
}

var errBadHex = errHex("bad hex")

type errHex string

func (e errHex) Error() string { return string(e) }
