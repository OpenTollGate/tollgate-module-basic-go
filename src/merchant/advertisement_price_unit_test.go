package merchant

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/OpenTollGate/tollgate-module-basic-go/src/config_manager"
	"github.com/nbd-wtf/go-nostr"
)

// The advertisement must carry the protocol's unit spelling even when the
// config file does not.
//
// Measured failure this pins: a router whose `/etc/tollgate/config.json` held
// `"price_unit": "sats"` advertised
//
//	["price_per_step","cashu","10","sats","https://testnut.cashu.space","1"]
//
// The captive portal compares the advertised unit against the unit inside the
// customer's token, which is always "sat" for the satoshi unit. "sats" != "sat"
// rejected a token minted seconds earlier, and the portal reported the
// rejection as "this e-cash note uses an outdated mint key — the mint has
// rotated its security keys", so the customer was told to fetch a fresh token
// that would fail identically. Nothing on the router's logs said "unit".
func TestCreateAdvertisement_SpeaksTheProtocolUnit(t *testing.T) {
	srv := reachableServer(t)

	cm, _ := setupTestConfigManager(t)
	cfg := cm.GetConfig()
	cfg.AcceptedMints = []config_manager.MintConfig{
		// Deliberately the spelling a real provisioning tool wrote.
		{URL: srv.URL, PricePerStep: 10, PriceUnit: "sats", MinPurchaseSteps: 1},
	}

	tracker := newTestTracker(cfg, nil)
	tracker.RunInitialProbe()

	ad, err := CreateAdvertisement(cm, tracker)
	if err != nil {
		t.Fatalf("CreateAdvertisement: %v", err)
	}

	var event nostr.Event
	if err := json.Unmarshal([]byte(ad), &event); err != nil {
		t.Fatalf("advertisement is not valid JSON: %v", err)
	}

	var tag nostr.Tag
	for _, candidate := range event.Tags {
		if len(candidate) > 0 && candidate[0] == "price_per_step" {
			tag = candidate
			break
		}
	}
	if tag == nil {
		t.Fatalf("advertisement carries no price_per_step tag: %s", ad)
	}
	if len(tag) < 6 {
		t.Fatalf("price_per_step tag is malformed (want 6 fields): %v", tag)
	}

	if got := tag[3]; got != config_manager.CanonicalSatoshiUnit {
		t.Errorf("advertised unit = %q, want %q — a consumer compares this against "+
			"the unit inside the customer's token and rejects valid e-cash on a "+
			"literal mismatch (tag: %v)", got, config_manager.CanonicalSatoshiUnit, tag)
	}

	// The rest of the tag must survive the normalisation untouched.
	if tag[1] != "cashu" {
		t.Errorf("asset type = %q, want cashu", tag[1])
	}
	if tag[2] != "10" {
		t.Errorf("price = %q, want 10", tag[2])
	}
	if tag[4] != srv.URL {
		t.Errorf("mint url = %q, want %q", tag[4], srv.URL)
	}

	// A guard that cannot fail is decoration: confirm the raw spelling really
	// would have leaked had the advertisement not normalised it.
	if strings.Contains(ad, `"sats"`) {
		t.Errorf("advertisement still carries the non-canonical spelling: %s", ad)
	}
}
