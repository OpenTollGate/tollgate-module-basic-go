package config_manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// operatorConfig returns a complete, valid v0.0.10 config document with the
// given accepted_mints JSON spliced in, so each case varies only the field
// under test.
func operatorConfig(t *testing.T, acceptedMints string) []byte {
	t.Helper()
	doc := `{
  "config_version": "v0.0.10",
  "log_level": "info",
  "metric": "bytes",
  "step_size": 22020096,
  "show_setup": true,
  "reseller_mode": false,
  "private_encryption": "psk2+ccmp",
  "admin_access": "both",
  "entry_ui": "board",
  "accepted_mints": ` + acceptedMints + `,
  "profit_share": [
    {"factor": 1.0, "identity": "owner"}
  ],
  "upstream_detector": {},
  "upstream_session_manager": {},
  "upstream_wifi": {}
}`
	return []byte(doc)
}

const issue929ReproMints = `["http://10.0.0.1:8383"]`

const oneOperatorMint = `[
    {
      "url": "https://mint.operator.example",
      "min_balance": 64,
      "balance_tolerance_percent": 10,
      "payout_interval_seconds": 60,
      "min_payout_amount": 128,
      "price_per_step": 1,
      "price_unit": "sat",
      "purchase_min_steps": 1
    }
  ]`

// TestEnsureDefaultConfigRefusesStringAcceptedMints is the #929 repro: a
// plain string array is the intuitive-but-wrong spelling of accepted_mints.
// Before the fix the loader treated this as wholesale corruption, backed the
// operator's file up, rewrote factory defaults, and booted on the seven
// public production mints. The only acceptable outcomes now are a loud
// refusal with the file untouched on disk.
func TestEnsureDefaultConfigRefusesStringAcceptedMints(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	before := operatorConfig(t, issue929ReproMints)
	if err := os.WriteFile(cfgPath, before, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := EnsureDefaultConfig(cfgPath)
	if err == nil {
		t.Fatalf("expected a loud refusal for accepted_mints as a plain string array, got config with %d mints", len(cfg.AcceptedMints))
	}
	if !strings.Contains(err.Error(), "accepted_mints") {
		t.Errorf("error should name the offending field, got: %v", err)
	}
	if cfg != nil {
		t.Errorf("no config must be returned on refusal, got %+v", cfg)
	}

	after, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("config file must be left untouched on refusal:\nbefore: %s\nafter:  %s", before, after)
	}
}

// TestEnsureDefaultConfigRefusesMalformedAcceptedMintsVariants covers the
// other mis-shapes an operator can write: one bad entry among good ones, a
// bare object instead of an array, and per-field type errors inside an entry.
func TestEnsureDefaultConfigRefusesMalformedAcceptedMintsVariants(t *testing.T) {
	cases := []struct {
		name        string
		mints       string
		wantInError string
	}{
		{
			name:        "string entry after a valid mint",
			mints:       `[{"url": "https://mint.operator.example"}, "http://10.0.0.1:8383"]`,
			wantInError: "accepted_mints[1]",
		},
		{
			name:        "bare object instead of array",
			mints:       `{"url": "https://mint.operator.example"}`,
			wantInError: "accepted_mints must be an array",
		},
		{
			name:        "number entry",
			mints:       `[42]`,
			wantInError: "accepted_mints[0]",
		},
		{
			name:        "wrong type inside an entry",
			mints:       `[{"url": 5}]`,
			wantInError: "accepted_mints[0]",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.json")
			before := operatorConfig(t, tc.mints)
			if err := os.WriteFile(cfgPath, before, 0600); err != nil {
				t.Fatal(err)
			}

			cfg, err := EnsureDefaultConfig(cfgPath)
			if err == nil {
				t.Fatalf("expected refusal, got config with %d mints", len(cfg.AcceptedMints))
			}
			if !strings.Contains(err.Error(), tc.wantInError) {
				t.Errorf("error should contain %q, got: %v", tc.wantInError, err)
			}

			after, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Errorf("config file must be left untouched on refusal")
			}
		})
	}
}

// TestEnsureDefaultConfigKeepsOperatorMints pins the load path the refusal
// protects: a well-formed mint list survives boot exactly as written, and a
// legacy min_purchase_steps key still parses (#402-adjacent legacy spelling
// handled by MintConfig.UnmarshalJSON).
func TestEnsureDefaultConfigKeepsOperatorMints(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, operatorConfig(t, oneOperatorMint), 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := EnsureDefaultConfig(cfgPath)
	if err != nil {
		t.Fatalf("valid operator config must load: %v", err)
	}
	if len(cfg.AcceptedMints) != 1 || cfg.AcceptedMints[0].URL != "https://mint.operator.example" {
		t.Fatalf("operator mint must be preserved, got %+v", cfg.AcceptedMints)
	}
}

// TestAcceptedMintsShapeErrorDiscriminates pins the discrimination itself:
// only a parseable document carrying a mis-shaped accepted_mints is refused.
// Everything else — truncated JSON, a type error in another field, a missing
// key — must stay nil so those documents keep their existing recovery paths.
func TestAcceptedMintsShapeErrorDiscriminates(t *testing.T) {
	cases := []struct {
		name    string
		doc     string
		wantErr bool
	}{
		{name: "truncated json stays on the recovery path", doc: `{"accepted_mints": [{"url": "https://`, wantErr: false},
		{name: "type error in another field is not a mint refusal", doc: `{"step_size": "big", "accepted_mints": ` + oneOperatorMint + `}`, wantErr: false},
		{name: "missing accepted_mints", doc: `{"step_size": 1}`, wantErr: false},
		{name: "string array is refused", doc: `{"accepted_mints": ` + issue929ReproMints + `}`, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := acceptedMintsShapeError([]byte(tc.doc))
			if !tc.wantErr && got != nil {
				t.Fatalf("expected nil (existing recovery path), got: %v", got)
			}
			if tc.wantErr && got == nil {
				t.Fatalf("expected a refusal error, got nil")
			}
		})
	}
}
