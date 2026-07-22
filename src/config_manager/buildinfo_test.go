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

	t.Run("main branch has only production mints", func(t *testing.T) {
		GitBranch = "main"
		cfg := NewDefaultConfig()

		if len(cfg.AcceptedMints) != 2 {
			t.Fatalf("expected 2 accepted mints on main, got %d", len(cfg.AcceptedMints))
		}
		for _, m := range cfg.AcceptedMints {
			if m.URL == "https://nofee.testnut.cashu.space" {
				t.Error("test mint should not be present on main branch")
			}
		}
	})

	t.Run("non-main branch includes test mint", func(t *testing.T) {
		GitBranch = "feature/test-branch"
		cfg := NewDefaultConfig()

		if len(cfg.AcceptedMints) != 3 {
			t.Fatalf("expected 3 accepted mints on feature branch, got %d", len(cfg.AcceptedMints))
		}

		found := false
		for _, m := range cfg.AcceptedMints {
			if m.URL == "https://nofee.testnut.cashu.space" {
				found = true
				if m.MinBalance != 0 {
					t.Errorf("expected test mint MinBalance=0, got %d", m.MinBalance)
				}
				if m.PayoutIntervalSeconds != 999999 {
					t.Errorf("expected test mint PayoutIntervalSeconds=999999, got %d", m.PayoutIntervalSeconds)
				}
				break
			}
		}
		if !found {
			t.Error("test mint not found in non-main branch config")
		}
	})

	t.Run("unknown branch (tests) includes test mint", func(t *testing.T) {
		GitBranch = "unknown"
		cfg := NewDefaultConfig()

		if len(cfg.AcceptedMints) != 3 {
			t.Fatalf("expected 3 accepted mints for unknown branch, got %d", len(cfg.AcceptedMints))
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

func TestDefaultTestMint(t *testing.T) {
	mint := defaultTestMint()
	if mint.URL != "https://nofee.testnut.cashu.space" {
		t.Errorf("expected test mint URL, got %s", mint.URL)
	}
	if mint.PricePerStep != 1 {
		t.Errorf("expected PricePerStep=1, got %d", mint.PricePerStep)
	}
	if mint.PriceUnit != "sat" {
		t.Errorf("expected PriceUnit=sats, got %s", mint.PriceUnit)
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
