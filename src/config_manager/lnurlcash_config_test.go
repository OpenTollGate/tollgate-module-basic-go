package config_manager

import (
	"encoding/json"
	"strings"
	"testing"
)

// The LNURLcash rail is opt-in, and "opt-in" has to mean something stronger
// than a boolean: a router that upgraded cannot be serving notes because a
// struct default flipped, and an operator who enabled the rail without the
// disclosure must be refused rather than trusted. These tests pin every
// partial state to OFF, because the shipped failure mode of an accidental
// payment rail is money.

func lnurlCashEnabledConfig() *Config {
	c := NewDefaultConfig()
	c.LNURLCash = LNURLCashConfig{
		Enabled:         true,
		Issuers:         []string{"mint.example"},
		MaxNoteMSat:     21_000_000,
		Acknowledgement: LNURLCashDisclosureVersion,
	}
	return c
}

func TestLNURLCashIsOffInTheShippedDefaults(t *testing.T) {
	c := NewDefaultConfig()
	if c.LNURLCash.Enabled {
		t.Fatal("NewDefaultConfig enables the LNURLcash rail; an opt-in rail must ship off")
	}
	if ok, _ := c.LNURLCashActivation(); ok {
		t.Fatal("the shipped defaults activate the LNURLcash rail")
	}
}

// An upgraded config file has no lnurlcash key at all: the zero value must stay
// off. This is the "no silent activation on upgrade" property from
// docs/architecture/payment-backend-disclosure-decision.md, applied to this rail.
func TestLNURLCashStaysOffForAPreExistingConfigFile(t *testing.T) {
	// A v0.0.10-era config: no lnurlcash key.
	raw := `{
		"config_version": "v0.0.10",
		"log_level": "info",
		"metric": "bytes",
		"step_size": 22020096,
		"show_setup": true,
		"reseller_mode": false,
		"entry_ui": "board",
		"accepted_mints": [
			{"url": "https://mint.example", "min_balance": 64, "balance_tolerance_percent": 10,
			 "payout_interval_seconds": 60, "min_payout_amount": 128, "price_per_step": 1,
			 "price_unit": "sat", "purchase_min_steps": 1}
		],
		"profit_share": [{"factor": 1.0, "identity": "owner"}]
	}`
	var c Config
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal pre-existing config: %v", err)
	}
	if ok, reason := c.LNURLCashActivation(); ok {
		t.Fatalf("a config file with no lnurlcash key activated the rail (%s)", reason)
	}
}

func TestLNURLCashActivationRequiresEveryOptInGate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Config)
		wantSub string
	}{
		{
			name:    "enabled without acknowledgement",
			mutate:  func(c *Config) { c.LNURLCash.Acknowledgement = "" },
			wantSub: "acknowledgement is empty",
		},
		{
			name:    "acknowledgement for different words",
			mutate:  func(c *Config) { c.LNURLCash.Acknowledgement = "lnurlcash-issuer-redeem-v0" },
			wantSub: "this build presents",
		},
		{
			name:    "acknowledged but no issuer allowlist",
			mutate:  func(c *Config) { c.LNURLCash.Issuers = nil },
			wantSub: "issuers is empty",
		},
		{
			name:    "allowlist entry is a URL, not a host",
			mutate:  func(c *Config) { c.LNURLCash.Issuers = []string{"https://mint.example/w"} },
			wantSub: "bare host",
		},
		{
			name:    "allowlist entry carries userinfo",
			mutate:  func(c *Config) { c.LNURLCash.Issuers = []string{"user@mint.example"} },
			wantSub: "bare host",
		},
		{
			name:    "allowlist entry carries a port",
			mutate:  func(c *Config) { c.LNURLCash.Issuers = []string{"mint.example:8443"} },
			wantSub: "port",
		},
		{
			name:    "no value ceiling",
			mutate:  func(c *Config) { c.LNURLCash.MaxNoteMSat = 0 },
			wantSub: "max_note_msat is 0",
		},
		{
			name:    "disabled outright",
			mutate:  func(c *Config) { c.LNURLCash.Enabled = false },
			wantSub: "opt-in",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := lnurlCashEnabledConfig()
			tc.mutate(c)
			ok, reason := c.LNURLCashActivation()
			if ok {
				t.Fatalf("rail activated in the %q state; every partial opt-in must stay off", tc.name)
			}
			if !strings.Contains(reason, tc.wantSub) {
				t.Fatalf("reason %q does not explain the refusal (want it to mention %q)", reason, tc.wantSub)
			}
		})
	}
}

func TestLNURLCashActivationWithCompleteOptIn(t *testing.T) {
	c := lnurlCashEnabledConfig()
	if ok, reason := c.LNURLCashActivation(); !ok {
		t.Fatalf("complete opt-in refused: %s", reason)
	}
}

// The allowlist comparison must be canonical on BOTH sides. A note names a
// domain, and the same domain spelled with a trailing dot, in uppercase, or
// with a default port is one issuer — but an allowlist that matched textually
// would let "mint.example." pass a check written for "mint.example" only by
// accident, in either direction.
func TestLNURLCashIssuerAllowlistIsCanonical(t *testing.T) {
	c := lnurlCashEnabledConfig()
	c.LNURLCash.Issuers = []string{"Mint.Example."}

	for _, spelling := range []string{"mint.example", "MINT.EXAMPLE", "mint.example.", "Mint.Example"} {
		host, ok := c.LNURLCashIssuerAllowed(spelling)
		if !ok {
			t.Fatalf("issuer %q was refused but is the allowlisted domain", spelling)
		}
		if host != "mint.example" {
			t.Fatalf("issuer %q canonicalised to %q, want %q", spelling, host, "mint.example")
		}
	}

	for _, other := range []string{"evil.example", "mint.example.evil.example", "sub.mint.example", ""} {
		if _, ok := c.LNURLCashIssuerAllowed(other); ok {
			t.Fatalf("issuer %q was allowed but is not the allowlisted domain", other)
		}
	}
}

func TestCanonicalLNURLCashIssuerRejectsNonHosts(t *testing.T) {
	bad := []string{
		"",
		"   ",
		"https://mint.example",
		"mint.example/w",
		"mint.example?x=1",
		"mint.example#frag",
		"user@mint.example",
		"mint.example:443",
		"localhost",
		"127.0.0.1",
		"bücher.example",
	}
	for _, raw := range bad {
		if got, err := CanonicalLNURLCashIssuer(raw); err == nil {
			t.Errorf("CanonicalLNURLCashIssuer(%q) = %q, want an error", raw, got)
		}
	}

	good := map[string]string{
		"mint.example":      "mint.example",
		"Mint.Example.":     "mint.example",
		" mint.example ":    "mint.example",
		"mint-2.example.co": "mint-2.example.co",
	}
	for raw, want := range good {
		got, err := CanonicalLNURLCashIssuer(raw)
		if err != nil {
			t.Errorf("CanonicalLNURLCashIssuer(%q) errored: %v", raw, err)
			continue
		}
		if got != want {
			t.Errorf("CanonicalLNURLCashIssuer(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The rail must never be reachable from a degraded/uninitialised config value:
// LNURLCashActivation is defined on *Config and a nil receiver must refuse
// rather than panic on a boot path that has no config yet.
func TestLNURLCashActivationRefusesNilConfig(t *testing.T) {
	var c *Config
	if ok, _ := c.LNURLCashActivation(); ok {
		t.Fatal("a nil config activated the rail")
	}
}
