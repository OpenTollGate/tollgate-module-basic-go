package wireless_gateway_manager

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBuildNetworkInfoCarriesPeerMetadata(t *testing.T) {
	ie, _, err := EncodeTollGateVendorIE(TollGateAdvertisement{
		Version:      1,
		IsReseller:   true,
		HasInternet:  true,
		OpenNetwork:  true,
		MintURL:      "https://mint.example.com",
		Pubkey:       []byte{0xaa, 0xbb, 0xcc},
		PricePerStep: 7,
		StepSize:     60000,
	})
	assert.NoError(t, err)
	raw, err := hex.DecodeString(ie)
	assert.NoError(t, err)

	s := &Scanner{}
	n := s.buildNetworkInfo("AA:BB:CC:DD:EE:FF", "!TollGate-AB12", -42, "none", "radio0", raw)
	assert.True(t, n.IsTollGate)
	assert.True(t, n.IsReseller)
	assert.True(t, n.HasInternet)
	assert.True(t, n.OpenNetwork)
	assert.Equal(t, "https://mint.example.com", n.MintURL)
	assert.Equal(t, []byte{0xaa, 0xbb, 0xcc}, n.Pubkey)
	assert.Equal(t, 7, n.AdvertisedPricePerStep)
	assert.Equal(t, 60000, n.AdvertisedStepSize)
}
