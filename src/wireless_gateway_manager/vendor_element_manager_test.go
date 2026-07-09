package wireless_gateway_manager

import (
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeParseRoundtrip_Basic(t *testing.T) {
	orig := TollGateAdvertisement{
		Version:     1,
		IsReseller:  true,
		HasInternet: true,
		MintURL:     "https://mint.coinos.io",
		Pubkey:      []byte{0x01, 0x02, 0x03},
	}

	ieHex, err := EncodeTollGateVendorIE(orig)
	require.NoError(t, err)

	raw, err := hex.DecodeString(ieHex)
	require.NoError(t, err)

	parsed := ParseTollGateVendorIE(raw)
	require.NotNil(t, parsed)

	assert.Equal(t, orig.Version, parsed.Version)
	assert.Equal(t, orig.IsReseller, parsed.IsReseller)
	assert.Equal(t, orig.HasInternet, parsed.HasInternet)
	assert.False(t, parsed.OpenNetwork)
	assert.Equal(t, orig.MintURL, parsed.MintURL)
	assert.Equal(t, orig.Pubkey, parsed.Pubkey)
}

func TestEncodeParseRoundtrip_AllFlags(t *testing.T) {
	flags := []TollGateAdvertisement{
		{Version: 1, IsReseller: false, HasInternet: false, OpenNetwork: false},
		{Version: 1, IsReseller: true, HasInternet: false, OpenNetwork: false},
		{Version: 1, IsReseller: false, HasInternet: true, OpenNetwork: false},
		{Version: 1, IsReseller: false, HasInternet: false, OpenNetwork: true},
		{Version: 1, IsReseller: true, HasInternet: true, OpenNetwork: true},
		{Version: 2, IsReseller: true, HasInternet: true, OpenNetwork: true,
			MintURL: "https://mint.example.com", Pubkey: []byte{0xAA, 0xBB, 0xCC, 0xDD}},
	}

	for i, orig := range flags {
		ieHex, err := EncodeTollGateVendorIE(orig)
		require.NoError(t, err, "flag combo %d", i)

		raw, err := hex.DecodeString(ieHex)
		require.NoError(t, err)

		parsed := ParseTollGateVendorIE(raw)
		require.NotNil(t, parsed, "flag combo %d", i)

		assert.Equal(t, orig.Version, parsed.Version, "version combo %d", i)
		assert.Equal(t, orig.IsReseller, parsed.IsReseller, "reseller combo %d", i)
		assert.Equal(t, orig.HasInternet, parsed.HasInternet, "internet combo %d", i)
		assert.Equal(t, orig.OpenNetwork, parsed.OpenNetwork, "open combo %d", i)
		if orig.MintURL != "" {
			assert.Equal(t, orig.MintURL, parsed.MintURL, "mint combo %d", i)
		}
		if len(orig.Pubkey) > 0 {
			assert.Equal(t, orig.Pubkey, parsed.Pubkey, "pubkey combo %d", i)
		}
	}
}

func TestEncode_NoMintNoPubkey(t *testing.T) {
	adv := TollGateAdvertisement{Version: 1}
	ieHex, err := EncodeTollGateVendorIE(adv)
	require.NoError(t, err)

	raw, _ := hex.DecodeString(ieHex)
	parsed := ParseTollGateVendorIE(raw)
	require.NotNil(t, parsed)
	assert.Equal(t, "", parsed.MintURL)
	assert.Nil(t, parsed.Pubkey)
}

func TestEncode_MintURLTooLong(t *testing.T) {
	adv := TollGateAdvertisement{
		Version: 1,
		MintURL: string(make([]byte, 256)),
	}
	_, err := EncodeTollGateVendorIE(adv)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "mint_url too long")
}

func TestEncode_PubkeyTooLong(t *testing.T) {
	adv := TollGateAdvertisement{
		Version: 1,
		Pubkey:  make([]byte, 256),
	}
	_, err := EncodeTollGateVendorIE(adv)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "pubkey too long")
}

func TestParse_WrongOUI(t *testing.T) {
	raw := []byte{0xDD, 0x06, 0xAA, 0xBB, 0xCC, 0x01, 0x01, 0x00}
	parsed := ParseTollGateVendorIE(raw)
	assert.Nil(t, parsed)
}

func TestParse_WrongElemType(t *testing.T) {
	raw := []byte{0xDD, 0x06, 0x21, 0x21, 0x21, 0x02, 0x01, 0x00}
	parsed := ParseTollGateVendorIE(raw)
	assert.Nil(t, parsed)
}

func TestParse_NotVendorIE(t *testing.T) {
	raw := []byte{0x00, 0x06, 0x21, 0x21, 0x21, 0x01, 0x01, 0x00}
	parsed := ParseTollGateVendorIE(raw)
	assert.Nil(t, parsed)
}

func TestParse_TooShort(t *testing.T) {
	cases := [][]byte{
		nil,
		{},
		{0xDD},
		{0xDD, 0x04},
		{0xDD, 0x05, 0x21, 0x21, 0x21, 0x01, 0x01},
	}
	for i, raw := range cases {
		parsed := ParseTollGateVendorIE(raw)
		assert.Nil(t, parsed, "case %d: length %d", i, len(raw))
	}
}

func TestParse_TruncatedTLV(t *testing.T) {
	raw := []byte{
		0xDD, 0x08,
		0x21, 0x21, 0x21, 0x01,
		0x01, 0x00,
		0x01, 0x05,
	}
	parsed := ParseTollGateVendorIE(raw)
	require.NotNil(t, parsed)
	assert.Equal(t, uint8(1), parsed.Version)
	assert.Equal(t, "", parsed.MintURL)
}

func TestParseVendorIEsFromScanData_MultipleIEs(t *testing.T) {
	adv1 := TollGateAdvertisement{Version: 1, IsReseller: true, MintURL: "https://a.test"}
	adv2 := TollGateAdvertisement{Version: 1, HasInternet: true, MintURL: "https://b.test"}

	hex1, err := EncodeTollGateVendorIE(adv1)
	require.NoError(t, err)
	hex2, err := EncodeTollGateVendorIE(adv2)
	require.NoError(t, err)

	raw1, _ := hex.DecodeString(hex1)
	raw2, _ := hex.DecodeString(hex2)

	nonVendorIE := []byte{0x00, 0x03, 0xAA, 0xBB, 0xCC}
	scanData := append(append(append(raw1, nonVendorIE...), raw2...), nonVendorIE...)

	results := ParseVendorIEsFromScanData(scanData)
	require.Len(t, results, 2)
	assert.Equal(t, "https://a.test", results[0].MintURL)
	assert.True(t, results[0].IsReseller)
	assert.Equal(t, "https://b.test", results[1].MintURL)
	assert.True(t, results[1].HasInternet)
}

func TestParseVendorIEsFromScanData_Empty(t *testing.T) {
	results := ParseVendorIEsFromScanData(nil)
	assert.Empty(t, results)
	results = ParseVendorIEsFromScanData([]byte{})
	assert.Empty(t, results)
}

func TestParseVendorIEsFromScanData_NoTollGateIEs(t *testing.T) {
	scanData := []byte{
		0x00, 0x03, 0xAA, 0xBB, 0xCC,
		0x30, 0x02, 0x01, 0x02,
	}
	results := ParseVendorIEsFromScanData(scanData)
	assert.Empty(t, results)
}

func TestParseVendorIEsFromScanData_TruncatedAtEnd(t *testing.T) {
	adv := TollGateAdvertisement{Version: 1}
	hex1, _ := EncodeTollGateVendorIE(adv)
	raw1, _ := hex.DecodeString(hex1)

	scanData := append(raw1, 0xDD, 0x10)
	results := ParseVendorIEsFromScanData(scanData)
	require.Len(t, results, 1)
	assert.Equal(t, uint8(1), results[0].Version)
}

func TestCalculateScore_TollGateSSID(t *testing.T) {
	v := &VendorElementProcessor{}
	score := v.calculateScore(NetworkInfo{SSID: "TollGate-ABCD", Signal: -50}, nil)
	assert.Equal(t, -50+100, score)
}

func TestCalculateScore_IsTollGateFlag(t *testing.T) {
	v := &VendorElementProcessor{}
	score := v.calculateScore(NetworkInfo{SSID: "RandomNet", Signal: -60, IsTollGate: true}, nil)
	assert.Equal(t, -60+200, score)
}

func TestCalculateScore_TollGateSSIDAndFlag(t *testing.T) {
	v := &VendorElementProcessor{}
	score := v.calculateScore(NetworkInfo{SSID: "TollGate-X", Signal: -40, IsTollGate: true}, nil)
	assert.Equal(t, -40+100+200, score)
}

func TestCalculateScore_RegularNetwork(t *testing.T) {
	v := &VendorElementProcessor{}
	score := v.calculateScore(NetworkInfo{SSID: "HomeWiFi", Signal: -30}, nil)
	assert.Equal(t, -30, score)
}
