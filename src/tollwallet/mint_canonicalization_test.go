package tollwallet

import "testing"

// equivalentMintURLs are URL pairs that MUST resolve to the same logical
// mint. They mirror the duplicate-registry trigger from issue #375: a
// config-side URL correction that differed by a trailing slash left two
// registry entries for one physical mint.
var equivalentMintURLs = [][2]string{
	{"https://mint.example/Bitcoin", "https://mint.example/Bitcoin/"},
	{"https://mint.example/Bitcoin", "https://mint.example/Bitcoin//"},
	{"https://mint.example", "https://mint.example/"},
	{"https://MINT.example/Bitcoin", "https://mint.example/Bitcoin"},
	{"https://Mint.Example/Bitcoin/", "https://mint.example/Bitcoin"},
	{"HTTPS://mint.example/Bitcoin", "https://mint.example/Bitcoin"},
	// Default ports are identity-irrelevant.
	{"https://mint.example/Bitcoin", "https://mint.example:443/Bitcoin"},
	{"http://mint.example/Bitcoin", "http://mint.example:80/Bitcoin"},
	// Userinfo, query and fragment never select a different mint.
	{"https://user:pass@mint.example/Bitcoin", "https://mint.example/Bitcoin"},
	{"https://mint.example/Bitcoin?v=1", "https://mint.example/Bitcoin"},
	{"https://mint.example/Bitcoin#frag", "https://mint.example/Bitcoin"},
}

// distinctMintURLs are URL pairs that MUST NOT be conflated.
var distinctMintURLs = [][2]string{
	{"https://mint.example/Bitcoin", "https://mint.example/Liquid"},
	{"https://mint1.example/Bitcoin", "https://mint2.example/Bitcoin"},
	{"https://mint.example/Bitcoin", "http://mint.example/Bitcoin"},
	{"https://mint.example", "https://mint.example/Bitcoin"},
	// Non-default ports are different mints.
	{"https://mint.example/Bitcoin", "https://mint.example:8443/Bitcoin"},
	// An escaped slash is path text, not a separator: %2F must not
	// over-merge onto a real trailing slash (over-merging loses funds).
	{"https://mint.example/Bitcoin%2F", "https://mint.example/Bitcoin"},
}

// TestNormalizeMintURL_CanonicalEquivalence pins the registry-key
// canonicalization: URLs that MintURLMatches considers equal must
// normalize to the identical registry key, otherwise the registeredMints
// map can hold two entries for one logical mint — the trigger condition
// for the phantom-duplicate-balance drain failure in issue #375.
func TestNormalizeMintURL_CanonicalEquivalence(t *testing.T) {
	for _, pair := range equivalentMintURLs {
		a, b := pair[0], pair[1]
		if got, want := normalizeMintURL(a), normalizeMintURL(b); got != want {
			t.Errorf("normalizeMintURL(%q) = %q, normalizeMintURL(%q) = %q: same logical mint must canonicalize identically", a, got, b, want)
		}
	}
	for _, pair := range distinctMintURLs {
		a, b := pair[0], pair[1]
		if got, want := normalizeMintURL(a), normalizeMintURL(b); got == want {
			t.Errorf("normalizeMintURL(%q) == normalizeMintURL(%q) == %q: distinct mints must not collide", a, b, got)
		}
	}
}

// TestMintURLMatches_AgreesWithNormalizeMintURL enforces a single mint
// identity semantics: registeredMints keys (normalizeMintURL) and URL
// comparison (MintURLMatches) must agree on every pair, so the two
// functions can never disagree about whether two URLs are the same mint.
func TestMintURLMatches_AgreesWithNormalizeMintURL(t *testing.T) {
	all := append(append([][2]string{}, equivalentMintURLs...), distinctMintURLs...)
	for _, pair := range all {
		a, b := pair[0], pair[1]
		matches := MintURLMatches(a, b)
		sameKey := normalizeMintURL(a) == normalizeMintURL(b)
		if matches != sameKey {
			t.Errorf("MintURLMatches(%q, %q) = %v but canonical keys %q/%q differ: match semantics and registry-key semantics must agree", a, b, matches, normalizeMintURL(a), normalizeMintURL(b))
		}
	}
}
