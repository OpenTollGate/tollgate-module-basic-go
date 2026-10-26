package wireless_gateway_manager

import "testing"

// F1 — found by a cold cross-family review (2026-10-16). With the ORIGINAL
// ordering (pubkey before pricing) a 251-byte pubkey landed the body at 253 and
// the pricing was then silently dropped by the 4-byte guard while the element
// still reported success. Pricing must be emitted BEFORE the pubkey so an
// oversized pubkey fails loudly instead.
func TestEncode_PricingSurvivesWithMaxFitPubkey(t *testing.T) {
	adv := TollGateAdvertisement{
		Version:      1,
		Pubkey:       make([]byte, 249), // 6 + 2 + 249 = 257 -> too long; use 247
		PricePerStep: 7,
		StepSize:     1000,
	}
	adv.Pubkey = make([]byte, 247) // 6 + 4 + 4 + 2 + 247 = 263 -> must error
	_, _, err := EncodeTollGateVendorIE(adv)
	if err == nil {
		t.Errorf("expected a loud error for an oversized pubkey, got none")
	}
}

// The decisive case: a pubkey that fits ALONGSIDE pricing must keep the pricing.
func TestEncode_PricingNotDroppedByPubkey(t *testing.T) {
	// body before pubkey: 6 (ou i+type+ver+flags) + 4 price + 4 step = 14
	// pubkey TLV: 2 + n. Need 14 + 2 + n <= 255  =>  n <= 239.
	adv := TollGateAdvertisement{
		Version:      1,
		Pubkey:       make([]byte, 239),
		PricePerStep: 7,
		StepSize:     1000,
	}
	hexIE, truncated, err := EncodeTollGateVendorIE(adv)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if truncated {
		t.Errorf("truncated=%v, want false — pricing fits alongside a 239-byte pubkey", truncated)
	}
	raw, err := hexDecode(hexIE)
	if err != nil {
		t.Fatalf("hex: %v", err)
	}
	got := ParseTollGateVendorIE(raw)
	if got == nil {
		t.Fatal("nil advertisement")
	}
	if got.PricePerStep != 7 {
		t.Errorf("PricePerStep = %d, want 7 — pricing must not be dropped when it fits", got.PricePerStep)
	}
	if got.StepSize != 1000 {
		t.Errorf("StepSize = %d, want 1000", got.StepSize)
	}
	if len(got.Pubkey) != 239 {
		t.Errorf("pubkey length = %d, want 239 — identification must survive", len(got.Pubkey))
	}
}

// The length octet must never silently wrap.
func TestEncode_LengthOctetNeverWraps(t *testing.T) {
	adv := TollGateAdvertisement{
		Version:      1,
		MintURL:      string(make([]byte, 200)),
		Pubkey:       make([]byte, 100),
		PricePerStep: 7,
		StepSize:     1000,
	}
	hexIE, _, err := EncodeTollGateVendorIE(adv)
	if err != nil {
		// a loud error is an acceptable outcome here
		return
	}
	raw, _ := hexDecode(hexIE)
	if len(raw) < 2 {
		t.Fatalf("element too short: %d bytes", len(raw))
	}
	into := int(raw[1])
	body := len(raw) - 2
	if body > 255 {
		t.Errorf("body is %d bytes, over the 255 cap", body)
	}
	if into != body {
		t.Errorf("length octet %d != actual body %d — a wrap would misparse", into, body)
	}
}
