package wireless_gateway_manager

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The pinned acceptance table for hasTollGateSSID: ONE file, read by this
// test (the matcher's behaviour) and by tests/contract/check-ssid-naming.sh
// (the table's structure and wiring). The classes it pins are the two
// measured SSID recognition breaks: the brand-blind matcher (#618, re-landed
// as #682) and the single-'!' writer decoration (#706).
const ssidFixturePath = "../../tests/contract/ssid-naming-fixtures.txt"

func TestHasTollGateSSIDRecognitionSet(t *testing.T) {
	data, err := os.ReadFile(ssidFixturePath)
	if err != nil {
		t.Fatalf("SSID fixture table unreadable: %v — the pinned recognition contract is missing", err)
	}
	matches, rejects := 0, 0
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		verdict, ssid, _ := strings.Cut(line, " ")
		switch verdict {
		case "match":
			matches++
			assert.True(t, hasTollGateSSID(ssid), "expected recognized: %q", ssid)
		case "nomatch":
			rejects++
			assert.False(t, hasTollGateSSID(ssid), "expected rejected: %q", ssid)
		case "banner", "banner-suffix", "nomatch-banner":
			// The portal-banner directives share this table but not this
			// matcher's subject; TestCanonicalPortalBannerIsPinned reads them.
		default:
			t.Fatalf("fixture line %q is neither 'match' nor 'nomatch' — the table is malformed", line)
		}
	}
	if matches < 10 || rejects < 8 {
		t.Fatalf("fixture table too thin (%d match, %d reject) — required classes are missing", matches, rejects)
	}
}

// The whitelabel entry's value, pinned against an assembly independent of
// brands.go's: the gutter keeps the name out of source, so this pin is what
// turns an assembly typo into a test failure instead of a silently
// unrecognized upstream brand.
func TestWhitelabelCaptivePrefixValue(t *testing.T) {
	assert.Equal(t, "Net4"+"sats-", whitelabelCaptivePrefix)
	assert.Contains(t, tollGateBrandPrefixes, whitelabelCaptivePrefix)
}

// The decoration contract from #706, independent of the table: at most one
// '!' is stripped, and stripping is idempotent per spelling — the decorated
// and bare forms of one router's SSID recognise identically, a double does
// not recognise at all.
func TestSSIDDecorationIsSingleOptional(t *testing.T) {
	for _, prefix := range tollGateBrandPrefixes {
		assert.True(t, hasTollGateSSID(prefix+"A1B2"), "bare form must recognise: %s", prefix)
		assert.True(t, hasTollGateSSID(ssidDecoration+prefix+"A1B2"), "decorated form must recognise: %s", prefix)
		assert.False(t, hasTollGateSSID(ssidDecoration+ssidDecoration+prefix+"A1B2"), "double decoration must not recognise: %s", prefix)
	}
}

// The CROSS-REPO portal-banner pin.
//
// `nodogsplash.@nodogsplash[0].gatewayname` has TWO writers in two DIFFERENT
// repositories: this module's 99-tollgate-setup (both setup paths) and the
// installer's brandingCommands (deploy.go), which runs LAST on a deployed
// router and therefore wins. The module's value is `<captive SSID> Portal`;
// the installer's is the captive SSID with NO suffix — a fourth spelling.
//
// The invariant used to be asserted only INSIDE the module's own shell suite,
// so the installer's divergent spelling was invisible. It is now declared once
// in the shared pinned table that both a shell fence and this test read, and
// this test closes the loop onto the shipped writer source, so a drift in
// either the table or 99-tollgate-setup is a Go test failure.
//
// The brand literal never appears here: names are composed from the table.
func TestCanonicalPortalBannerIsPinned(t *testing.T) {
	data, err := os.ReadFile(ssidFixturePath)
	if err != nil {
		t.Fatalf("SSID fixture table unreadable: %v — the pinned banner contract is missing", err)
	}

	var suffix string
	var suffixCount int
	var bannerSSIDs []string
	var divergent []string
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		switch {
		case strings.HasPrefix(line, "banner-suffix "):
			suffix = strings.TrimSpace(strings.TrimPrefix(line, "banner-suffix "))
			suffixCount++
		case strings.HasPrefix(line, "banner "):
			bannerSSIDs = append(bannerSSIDs, strings.TrimSpace(strings.TrimPrefix(line, "banner ")))
		case strings.HasPrefix(line, "nomatch-banner "):
			divergent = append(divergent, strings.TrimSpace(strings.TrimPrefix(line, "nomatch-banner ")))
		}
	}

	assert.Equal(t, 1, suffixCount, "the table must declare exactly one 'banner-suffix' directive")
	if !assert.NotEmpty(t, bannerSSIDs, "the table must declare canonical 'banner' directives") {
		t.Fatal("cannot check the banner shape without a declared banner")
	}
	assert.NotEmpty(t, suffix, "the banner suffix must not be empty")

	// ONE shape, declared once: the banner is '<declared SSID> <suffix>', and
	// that SSID is a name the matcher recognizes — the banner and the
	// guest-visible network must be the same name.
	for _, ssid := range bannerSSIDs {
		assert.True(t, hasTollGateSSID(ssid),
			"the canonical banner's SSID must be a recognized TollGate SSID: %q", ssid)
	}
	// ...and every brand the matcher knows has its canonical banner SSID
	// pinned, so neither brand can drift off the banner shape unnoticed. The
	// #706 decoration is stripped before the compare — the banner naming rule is
	// about the BRAND, not about the decoration the SSID happens to carry.
	for _, prefix := range tollGateBrandPrefixes {
		found := false
		for _, ssid := range bannerSSIDs {
			bare := strings.TrimPrefix(ssid, ssidDecoration)
			if strings.HasPrefix(strings.ToLower(bare), strings.ToLower(prefix)) {
				found = true
			}
		}
		assert.True(t, found, "no canonical banner pinned for brand prefix %q", prefix)
	}

	// The divergent spelling (what the installer writes) is on record as a
	// DIFFERENT string from every canonical banner, so the divergence is named
	// rather than implied. It is a canonical banner SSID with the suffix
	// dropped — that is the recorded failure mode.
	if assert.Len(t, divergent, 1, "the table must record exactly one 'nomatch-banner' (the installer's spelling)") {
		assert.True(t, hasTollGateSSID(divergent[0]),
			"the divergent spelling is still a TollGate NAME, just not the banner: %q", divergent[0])
		suffixDropped := false
		for _, ssid := range bannerSSIDs {
			assert.NotEqual(t, ssid+" "+suffix, divergent[0],
				"the installer's spelling must differ from every canonical banner or the pin is vacuous")
			if ssid == divergent[0] {
				suffixDropped = true
			}
		}
		assert.True(t, suffixDropped,
			"the installer writes a canonical banner SSID with the suffix dropped — that is the recorded divergence: %q", divergent[0])
	}

	// Layer B: the shipped writer must actually produce the declared shape.
	// Without this the table would merely describe a banner nobody writes.
	script, err := os.ReadFile("../../packaging/files/etc/uci-defaults/99-tollgate-setup")
	if err != nil {
		t.Fatalf("the setup script is unreadable: %v — the banner pin cannot be closed onto the writer", err)
	}
	re := regexp.MustCompile(`gatewayname="([^"]*)"`)
	vals := re.FindAllStringSubmatch(string(script), -1)
	if !assert.NotEmpty(t, vals, "no gatewayname writer found in 99-tollgate-setup — this pin is observing nothing") {
		return
	}
	seen := map[string]bool{}
	for _, m := range vals {
		seen[m[1]] = true
	}
	assert.Len(t, seen, 1, "every gatewayname writer in the module must use ONE value expression")
	for v := range seen {
		assert.Equal(t, "${GATEWAY_NAME} "+suffix, v,
			"the shipped writer must use the declared suffix (%q), not a second spelling", suffix)
	}
	// ...and GATEWAY_NAME is the captive SSID, so the chain is closed:
	// canonical SSID -> GATEWAY_NAME -> banner.
	if !assert.Contains(t, string(script), "GATEWAY_NAME=$(captive_ssid_for_code",
		"GATEWAY_NAME must be derived from the captive SSID helper (the banner's base name)") {
		return
	}
}
