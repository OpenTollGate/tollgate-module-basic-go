package wireless_gateway_manager

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConsentAllowsNetworkPostures(t *testing.T) {
	manifest := []ConsentEntry{
		{Match: "CafeGuest", Consent: ConsentPreAccepted, TOSURL: "https://example.test/tos"},
		{Match: "Manchester Airport*", Consent: ConsentPreAccepted},
		{Match: "PayPerVisit", Consent: ConsentPerSession},
	}

	cases := []struct {
		name       string
		ssid       string
		encryption string
		allowAny   bool
		want       bool
		reason     string
	}{
		{"tollgate peer is exempt", "!TollGate-AB12", "none", false, true, "tollgate-peer"},
		{"private ssid is exempt", "Private-c08r4d0r-AB12", "none", false, true, "private-ssid"},
		{"saved encrypted network is exempt", "HomeWiFi", "WPA2 PSK (CCMP)", false, true, "encrypted-saved"},
		{"legacy scanner value is left alone", "HomeWiFi", "", false, true, "unknown-encryption"},
		{"unknown open network is blocked", "FreeAirportWiFi", "none", false, false, "no-consent-entry"},
		{"pre-accepted exact match", "CafeGuest", "none", false, true, "pre-accepted"},
		{"pre-accepted prefix match", "Manchester Airport Free", "open", false, true, "pre-accepted"},
		{"per-session never self-consents", "PayPerVisit", "none", false, false, "per-session"},
		{"allow_any_open is an explicit override", "FreeAirportWiFi", "none", true, true, "allow-any-open"},
		{"ssid matching is case-sensitive", "cafeguest", "none", false, false, "no-consent-entry"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := consentAllowsNetwork(tc.ssid, tc.encryption, manifest, tc.allowAny)
			assert.Equal(t, tc.want, got, "reason: %s", reason)
			assert.Equal(t, tc.reason, reason)
		})
	}
}

func TestConsentEntryForExactAndPrefix(t *testing.T) {
	manifest := []ConsentEntry{
		{Match: "CafeGuest", Consent: ConsentPreAccepted},
		{Match: "Airport*", Consent: ConsentPerSession},
	}
	assert.Equal(t, "CafeGuest", consentEntryFor("CafeGuest", manifest).Match)
	assert.Equal(t, "Airport*", consentEntryFor("Airport Free", manifest).Match)
	assert.Equal(t, "Airport*", consentEntryFor("Airport", manifest).Match)
	assert.Nil(t, consentEntryFor("Airpor", manifest))
	assert.Nil(t, consentEntryFor("", manifest))
}

func TestFindKnownCandidatesHonoursConsent(t *testing.T) {
	baseSections := []STASection{
		{Name: "upstream_cafe", SSID: "CafeGuest", Device: "radio0", Disabled: true},
	}
	openCafe := []NetworkInfo{{SSID: "CafeGuest", Signal: -45, Radio: "radio0", Encryption: "none"}}

	t.Run("unknown open is not selected", func(t *testing.T) {
		um := newConsentManager(t, baseSections, nil, false)
		candidate, err := um.findCandidates(openCafe, false, false)
		assert.NoError(t, err)
		assert.Nil(t, candidate)
	})

	t.Run("pre-accepted is selected", func(t *testing.T) {
		um := newConsentManager(t, baseSections, []ConsentEntry{{Match: "CafeGuest", Consent: ConsentPreAccepted}}, false)
		candidate, err := um.findCandidates(openCafe, false, false)
		assert.NoError(t, err)
		assert.NotNil(t, candidate)
		assert.Equal(t, "CafeGuest", candidate.SSID)
	})

	t.Run("per-session is not selected", func(t *testing.T) {
		um := newConsentManager(t, baseSections, []ConsentEntry{{Match: "CafeGuest", Consent: ConsentPerSession}}, false)
		candidate, err := um.findCandidates(openCafe, false, false)
		assert.NoError(t, err)
		assert.Nil(t, candidate)
	})

	t.Run("allow_any_open override", func(t *testing.T) {
		um := newConsentManager(t, baseSections, nil, true)
		candidate, err := um.findCandidates(openCafe, false, false)
		assert.NoError(t, err)
		assert.NotNil(t, candidate)
	})
}

func TestFindResellerCandidatesExemptsTollGatePeers(t *testing.T) {
	um := newConsentManager(t, []STASection{
		{Name: "upstream_tg", SSID: "!TollGate-ABC", Device: "radio0", Disabled: true},
	}, nil, false)

	candidate, err := um.findCandidates([]NetworkInfo{
		{SSID: "!TollGate-ABC", Signal: -40, Radio: "radio0", Encryption: "none"},
	}, true, false)
	assert.NoError(t, err)
	assert.NotNil(t, candidate)
	assert.Equal(t, "!TollGate-ABC", candidate.SSID)
}

func newConsentManager(t *testing.T, sections []STASection, manifest []ConsentEntry, allowAny bool) *UpstreamManager {
	t.Helper()
	connector := &MockConnector{}
	scanner := &MockScanner{}
	reseller := &MockResellerChecker{}
	config := DefaultUpstreamManagerConfig()
	config.ConsentManifest = manifest
	config.AllowAnyOpen = allowAny
	connector.On("GetSTASections").Return(sections, nil)
	return NewUpstreamManager(connector, scanner, reseller, config)
}
