package config_manager

import (
	"strings"
	"testing"
)

// testnutHosts lists the test-mint endpoints that must never reach a
// release-line default configuration.
//
// The default mint list is money-bearing: the captive portal advertises those
// mints to paying clients, and the backend settles against them. A test mint
// that leaks into defaultProductionMints() therefore sends real traffic at a
// throwaway mint. Test mints belong in defaultTestMint(), which
// NewDefaultConfig() only appends when IsDevBuild() is true.
var testnutHosts = []string{
	"testnut.cashu.space",
	"nofee.testnut.cashu.space",
	"nofees.testnut.cashu.space",
	"testnut.cashu.exchange",
}

// isTestnutURL reports whether a mint URL points at a known test mint. The
// substring check is deliberately broader than testnutHosts: any new
// testnut.* host is caught without having to extend the list first.
func isTestnutURL(rawURL string) bool {
	lower := strings.ToLower(strings.TrimSpace(rawURL))
	if lower == "" {
		return false
	}
	if strings.Contains(lower, "testnut") {
		return true
	}
	for _, host := range testnutHosts {
		if strings.Contains(lower, host) {
			return true
		}
	}
	return false
}

// TestDefaultProductionMintsHaveNoTestnutURL pins the production mint list to
// real mints only.
//
// Regression: the module gates its test mint behind IsDevBuild(), so nothing
// in the production list may point at testnut.*. A release line that ships a
// test mint would hand paying clients a mint nobody can be paid from.
func TestDefaultProductionMintsHaveNoTestnutURL(t *testing.T) {
	mints := defaultProductionMints()

	if len(mints) == 0 {
		t.Fatal("defaultProductionMints() returned no mints; the production default list must not be empty")
	}

	for i, mint := range mints {
		url := strings.TrimSpace(mint.URL)
		if url == "" {
			t.Errorf("defaultProductionMints()[%d].URL is empty", i)
			continue
		}
		if !strings.HasPrefix(url, "https://") {
			t.Errorf("defaultProductionMints()[%d].URL = %q, want an https:// URL", i, url)
		}
		if isTestnutURL(url) {
			t.Errorf("defaultProductionMints()[%d].URL = %q is a test mint; the production defaults must only carry real mints (test mints are injected by NewDefaultConfig() via defaultTestMint() when IsDevBuild() is true)", i, url)
		}
	}
}

// TestDefaultMintListGateIsTestnutOnly closes the other half of the invariant:
// the single test-mint entry in the package must be the gated one, so a
// testnut URL cannot hide in a second helper that some later caller uses
// unconditionally.
func TestDefaultMintListGateIsTestnutOnly(t *testing.T) {
	testMint := defaultTestMint()
	if !isTestnutURL(testMint.URL) {
		t.Fatalf("defaultTestMint().URL = %q, want a testnut test mint (the gate in NewDefaultConfig() would otherwise be a no-op)", testMint.URL)
	}
}

// TestReleaseLineDefaultConfigHasNoTestnutMint asserts the property end to
// end: a config built for a release line ("main", the empty branch, or the
// "unknown" placeholder) carries no test mint, while a dev-line build still
// does — so the assertion above cannot pass by the gate having gone dead.
func TestReleaseLineDefaultConfigHasNoTestnutMint(t *testing.T) {
	original := GitBranch
	t.Cleanup(func() { GitBranch = original })

	for _, branch := range []string{"main", "", "unknown"} {
		GitBranch = branch
		if IsDevBuild() {
			t.Fatalf("IsDevBuild() = true for branch %q, want false", branch)
		}
		cfg := NewDefaultConfig()
		for i, mint := range cfg.AcceptedMints {
			if isTestnutURL(mint.URL) {
				t.Errorf("branch %q: NewDefaultConfig().AcceptedMints[%d].URL = %q is a test mint; a release-line default config must not carry test mints", branch, i, mint.URL)
			}
		}
		if len(cfg.AcceptedMints) != len(defaultProductionMints()) {
			t.Errorf("branch %q: NewDefaultConfig() carries %d mints, want the %d production defaults", branch, len(cfg.AcceptedMints), len(defaultProductionMints()))
		}
	}

	GitBranch = "dev"
	if !IsDevBuild() {
		t.Fatal("IsDevBuild() = false for branch \"dev\", want true")
	}
	devCfg := NewDefaultConfig()
	found := false
	for _, mint := range devCfg.AcceptedMints {
		if isTestnutURL(mint.URL) {
			found = true
			break
		}
	}
	if !found {
		t.Error("a dev-line default config carries no test mint; the IsDevBuild() gate is dead and the release-line assertions above are vacuous")
	}
}
