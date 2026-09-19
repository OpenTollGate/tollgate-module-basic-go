//go:build !cdk_wallet

package tollwallet

import "testing"

func TestResolveKeysetID(t *testing.T) {
	const fullV2 = "01182e5f5e35aecd90da50a53c95667f745bc08ded556085c795c94adfa40b8591"
	fees := map[string]uint{
		fullV2:             100,
		"004f7adf2a04356c": 0,
	}

	cases := []struct {
		id   string
		want string
	}{
		{"01182e5f5e35aecd", fullV2},             // V4 short id (first 8 bytes) -> full v2
		{"01182E5F5E35AECD", fullV2},             // case-insensitive
		{"004f7adf2a04356c", "004f7adf2a04356c"}, // full v1 id
		{"ffffffffffffffff", ""},                 // unknown keyset contributes no fee
	}
	for _, c := range cases {
		if got := resolveKeysetID(c.id, fees); got != c.want {
			t.Errorf("resolveKeysetID(%q) = %q, want %q", c.id, got, c.want)
		}
	}
}
