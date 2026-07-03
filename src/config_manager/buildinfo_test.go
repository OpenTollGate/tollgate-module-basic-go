package config_manager

import (
	"testing"
)

func TestIsDevBuild_DefaultBranch(t *testing.T) {
	original := GitBranch
	defer func() { GitBranch = original }()

	t.Run("unknown branch is dev build", func(t *testing.T) {
		GitBranch = "unknown"
		if !IsDevBuild() {
			t.Error("expected IsDevBuild()=true for unknown branch")
		}
	})

	t.Run("main branch is not dev build", func(t *testing.T) {
		GitBranch = "main"
		if IsDevBuild() {
			t.Error("expected IsDevBuild()=false for main branch")
		}
	})

	t.Run("feature branch is dev build", func(t *testing.T) {
		GitBranch = "94-mint-health-rebase"
		if !IsDevBuild() {
			t.Error("expected IsDevBuild()=true for feature branch")
		}
	})

	t.Run("empty string is dev build", func(t *testing.T) {
		GitBranch = ""
		if !IsDevBuild() {
			t.Error("expected IsDevBuild()=true for empty string")
		}
	})
}

func TestNewDefaultConfig_MintsForBranch(t *testing.T) {
	original := GitBranch
	defer func() { GitBranch = original }()

	// Map of test mint URLs that must only appear in dev builds
	testMintURLs := map[string]bool{
		"https://testnut.cashu.exchange":     true,
		"https://testnut.cashu.space":        true,
		"https://nofee.testnut.cashu.space":  true,
	}
	nTestMints := len(testMintURLs)

	t.Run("main branch has only production mints", func(t *testing.T) {
		GitBranch = "main"
		cfg := NewDefaultConfig()

		if len(cfg.AcceptedMints) != 2 {
			t.Fatalf("expected 2 accepted mints on main, got %d", len(cfg.AcceptedMints))
		}
		for _, m := range cfg.AcceptedMints {
			if testMintURLs[m.URL] {
				t.Errorf("test mint %s should not be present on main branch", m.URL)
			}
		}
	})

	t.Run("non-main branch includes test mints", func(t *testing.T) {
		GitBranch = "feature/test-branch"
		cfg := NewDefaultConfig()

		expectedCount := 2 + nTestMints // 2 prod + N test
		if len(cfg.AcceptedMints) != expectedCount {
			t.Fatalf("expected %d accepted mints on feature branch, got %d", expectedCount, len(cfg.AcceptedMints))
		}

		found := 0
		for _, m := range cfg.AcceptedMints {
			if testMintURLs[m.URL] {
				found++
				if m.MinBalance != 0 {
					t.Errorf("expected test mint %s MinBalance=0, got %d", m.URL, m.MinBalance)
				}
				if m.PayoutIntervalSeconds != 999999 {
					t.Errorf("expected test mint %s PayoutIntervalSeconds=999999, got %d", m.URL, m.PayoutIntervalSeconds)
				}
			}
		}
		if found != nTestMints {
			t.Errorf("expected %d test mints, found %d", nTestMints, found)
		}
	})

	t.Run("unknown branch (tests) includes test mints", func(t *testing.T) {
		GitBranch = "unknown"
		cfg := NewDefaultConfig()

		expectedCount := 2 + nTestMints
		if len(cfg.AcceptedMints) != expectedCount {
			t.Fatalf("expected %d accepted mints for unknown branch, got %d", expectedCount, len(cfg.AcceptedMints))
		}
	})

	t.Run("production mints present regardless of branch", func(t *testing.T) {
		GitBranch = "main"
		cfgMain := NewDefaultConfig()

		GitBranch = "feature/test"
		cfgFeature := NewDefaultConfig()

		for _, mainMint := range cfgMain.AcceptedMints {
			found := false
			for _, featMint := range cfgFeature.AcceptedMints {
				if featMint.URL == mainMint.URL {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("production mint %s missing from feature branch config", mainMint.URL)
			}
		}
	})
}

func TestDefaultTestMints(t *testing.T) {
	mints := defaultTestMints()
	if len(mints) < 2 {
		t.Fatalf("expected at least 2 test mints for redundancy, got %d", len(mints))
	}

	// Verify all test mints have sane config
	for _, m := range mints {
		if m.PricePerStep != 1 {
			t.Errorf("expected PricePerStep=1 for %s, got %d", m.URL, m.PricePerStep)
		}
		if m.PriceUnit != "sats" {
			t.Errorf("expected PriceUnit=sats for %s, got %s", m.URL, m.PriceUnit)
		}
		if m.MinBalance != 0 {
			t.Errorf("expected MinBalance=0 for %s, got %d", m.URL, m.MinBalance)
		}
	}

	// Verify testnut.cashu.exchange is present (primary Spilman channel test mint)
	foundExchange := false
	for _, m := range mints {
		if m.URL == "https://testnut.cashu.exchange" {
			foundExchange = true
		}
	}
	if !foundExchange {
		t.Error("testnut.cashu.exchange should be in test mints (used for Spilman channels)")
	}
}

func TestDefaultProductionMints(t *testing.T) {
	mints := defaultProductionMints()
	if len(mints) != 2 {
		t.Fatalf("expected 2 production mints, got %d", len(mints))
	}
	if mints[0].URL != "https://mint.coinos.io" {
		t.Errorf("expected first mint coinos.io, got %s", mints[0].URL)
	}
	if mints[1].URL != "https://mint.minibits.cash/Bitcoin" {
		t.Errorf("expected second mint minibits, got %s", mints[1].URL)
	}
}
